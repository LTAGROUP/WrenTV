// Package sports fetches live scores, schedules and event detail from ESPN's
// public site API and caches them in memory and on disk, mirroring MediaStorm's
// Sports Hub behaviour on Wren TV's Go/SQLite architecture.
package sports

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPTimeout    = 15 * time.Second
	sportsCacheDir        = "sports"
	sportsCacheFile       = "scoreboard.json"
	espnScoreboardURLFmt  = "https://site.api.espn.com/apis/site/v2/sports/%s/%s/scoreboard"
	espnTeamsURLFmt       = "https://site.api.espn.com/apis/site/v2/sports/%s/%s/teams?limit=1000"
	sportsScoreboardLimit = 200
	// teamCatalogTTL is how long a completed team-catalog check is trusted,
	// including a check that found the provider publishes no catalog at all.
	teamCatalogTTL = 24 * time.Hour
)

// errTeamsUnsupported reports a provider that answered 404 for its teams
// endpoint: the league has no team catalog to fetch, so the check is finished
// rather than failed. Scoreboard participants remain the only team source.
var errTeamsUnsupported = errors.New("provider publishes no team catalog")

// Service fetches and caches scoreboard data.
type Service struct {
	dateMu sync.Mutex
	dated  map[string]datedEntry

	// Racing providers (ESPN racing, MotoGP) and cycling organizers have their
	// own caches because they publish weekends, sessions and stage races rather
	// than two-team scoreboards. Locks never cover provider requests.
	raceMu       sync.Mutex
	raceBoards   map[string]raceBoardEntry
	raceDetailMu sync.Mutex
	raceDetails  map[string]raceSessionEntry
	f1Archive    f1ArchiveCache
	motoGPExtra  motoGPEnrichmentCache
	cycling      cyclingCache
	standings    standingsCache

	storageDir string
	client     *http.Client

	mu                 sync.RWMutex
	leagues            []League
	games              map[string][]SportsGame // league ID -> games
	teamCatalog        map[string][]SportsTeamRecord
	teamCatalogUpdated map[string]time.Time
	// teamCatalogUnsupported marks leagues whose provider confirmed it has no
	// teams endpoint. With teamCatalogUpdated it separates "checked; no catalog"
	// from "never checked", which teamCatalog alone cannot express.
	teamCatalogUnsupported map[string]bool
	// providerBlocks remembers a league the provider refused (401/403/429),
	// so a blocked provider is not asked again on every cycle. A refusal is a
	// policy answer about this client, not a transient request failure.
	providerBlocks map[string]providerBlock
	lastUpdated    time.Time
	refreshing     bool
	lastError      string

	// details caches enriched summaries; guarded by detailCacheInit.
	details map[string]detailCacheEntry
}

// New creates a Service and loads the last scoreboard snapshot from disk so a
// restart shows the previous scores until the first refresh completes.
func New(storageDir string) *Service {
	return NewWithClient(storageDir, nil)
}

// NewWithClient is New with an explicit HTTP client. A nil client uses the
// shared bounded provider transport; tests inject a fixture transport.
func NewWithClient(storageDir string, client *http.Client) *Service {
	if client == nil {
		client = boundedSportsClient(&http.Client{Timeout: defaultHTTPTimeout})
	}
	s := &Service{
		storageDir:             storageDir,
		client:                 client,
		leagues:                selectLeagues(defaultLeagueIDs),
		games:                  make(map[string][]SportsGame),
		teamCatalog:            make(map[string][]SportsTeamRecord),
		teamCatalogUpdated:     make(map[string]time.Time),
		teamCatalogUnsupported: make(map[string]bool),
		providerBlocks:         make(map[string]providerBlock),
		dated:                  map[string]datedEntry{},
	}
	if err := os.MkdirAll(filepath.Join(storageDir, sportsCacheDir), 0o755); err != nil {
		slog.Warn("sports cache directory", "err", err)
	}
	if err := s.loadFromDisk(); err != nil && !os.IsNotExist(err) {
		slog.Warn("sports cached scoreboard", "err", err)
	}
	return s
}

func selectLeagues(ids []string) []League {
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	out := make([]League, 0, len(ids))
	for _, league := range LeagueCatalog {
		if _, ok := wanted[league.ID]; (ok || wantedAll(wanted)) && league.active() {
			out = append(out, league)
		}
	}
	return out
}

// SetEnabledLeagueIDs replaces the polled leagues. It is safe to call while a
// refresh is running; the change applies from the next refresh.
func (s *Service) SetEnabledLeagueIDs(ids []string) {
	s.mu.Lock()
	s.leagues = selectLeagues(ids)
	s.mu.Unlock()
}

// Leagues returns every catalog league with its enabled flag.
func (s *Service) Leagues() []SportsLeague {
	s.mu.RLock()
	defer s.mu.RUnlock()
	enabled := make(map[string]struct{}, len(s.leagues))
	for _, l := range s.leagues {
		enabled[l.ID] = struct{}{}
	}
	out := make([]SportsLeague, 0, len(LeagueCatalog))
	for _, l := range LeagueCatalog {
		if !l.active() {
			continue
		}
		_, isEnabled := enabled[l.ID]
		out = append(out, l.descriptor(isEnabled))
	}
	return out
}

// League returns catalog metadata even when the league is currently disabled.
func (s *Service) League(id string) (League, bool) {
	for _, league := range LeagueCatalog {
		if league.ID == id {
			return league, true
		}
	}
	return League{}, false
}

// GetStatus reports the health of the scoreboard cache.
func (s *Service) GetStatus() SportsStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, games := range s.games {
		count += len(games)
	}
	status := SportsStatus{
		Enabled:     len(s.leagues) > 0,
		DateQueries: true,
		GameCount:   count,
		Refreshing:  s.refreshing,
		LastError:   s.lastError,
	}
	if !s.lastUpdated.IsZero() {
		t := s.lastUpdated
		status.LastRefresh = &t
	}
	return status
}

// GetScoreboard returns cached games for a league ID, or all leagues combined.
func (s *Service) GetScoreboard(league string) []SportsGame {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if league != "" {
		games := s.games[league]
		out := make([]SportsGame, len(games))
		copy(out, games)
		return out
	}
	var all []SportsGame
	for _, l := range s.leagues {
		all = append(all, s.games[l.ID]...)
	}
	return all
}

// GetGame returns a single cached game by ID, if present. Dated scoreboards are
// searched too so a deep link works for yesterday/tomorrow.
func (s *Service) GetGame(id string) (SportsGame, bool) {
	s.mu.RLock()
	for _, games := range s.games {
		for _, g := range games {
			if g.ID == id {
				s.mu.RUnlock()
				return g, true
			}
		}
	}
	s.mu.RUnlock()
	s.dateMu.Lock()
	defer s.dateMu.Unlock()
	for _, entry := range s.dated {
		for _, g := range entry.board.Games {
			if g.ID == id {
				return g, true
			}
		}
	}
	return SportsGame{}, false
}

// Refresh fetches the current scoreboard for every tracked league.
func (s *Service) Refresh(ctx context.Context) error {
	s.mu.Lock()
	if s.refreshing {
		s.mu.Unlock()
		return nil
	}
	s.refreshing = true
	s.lastError = ""
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.refreshing = false
		s.mu.Unlock()
	}()

	s.mu.RLock()
	leagues := append([]League(nil), s.leagues...)
	// Start from the last known data for every league. A refresh can exhaust its
	// shared context while workers still wait for a slot; those workers never
	// reach the fetch error path that restores cached games.
	nextGames := make(map[string][]SportsGame, len(leagues))
	for _, league := range leagues {
		nextGames[league.ID] = append([]SportsGame(nil), s.games[league.ID]...)
	}
	s.mu.RUnlock()

	var firstErr error
	var resultMu sync.Mutex
	var wg sync.WaitGroup
	blocked := 0
	semaphore := make(chan struct{}, 4)
	for _, league := range leagues {
		league := league
		// The provider refused this league recently. Keep the last good data
		// and stay quiet instead of repeating a request that cannot succeed.
		if s.leagueBlocked(league.ID) {
			blocked++
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				resultMu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				resultMu.Unlock()
				return
			}
			defer func() { <-semaphore }()
			games, err := s.fetchLeagueScoreboard(ctx, league)
			if err != nil {
				s.noteLeagueRefusal(league.ID, err)
				// A cycle that runs out of time reports that once; every waiting
				// league would otherwise repeat the same deadline.
				if !providerRefusal(err) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					slog.Warn("sports scoreboard fetch", "league", league.ID, "err", err)
				}
				s.mu.RLock()
				if errors.Is(err, errPartialScoreboard) {
					games = mergeCoverageGames(s.games[league.ID], games)
				} else {
					games = s.games[league.ID]
				}
				s.mu.RUnlock()
				resultMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				nextGames[league.ID] = games
				resultMu.Unlock()
				return
			}
			s.clearLeagueRefusal(league.ID)
			resultMu.Lock()
			nextGames[league.ID] = games
			resultMu.Unlock()
		}()
	}
	wg.Wait()

	s.mu.Lock()
	s.games = nextGames
	s.lastUpdated = time.Now()
	if firstErr != nil {
		s.lastError = firstErr.Error()
	} else if blocked > 0 {
		// The state stays visible without a warning per league per cycle.
		s.lastError = fmt.Sprintf("sports provider refused %d league(s); retrying after backoff", blocked)
	}
	s.mu.Unlock()

	if err := s.saveToDisk(); err != nil {
		slog.Warn("sports save scoreboard cache", "err", err)
	}
	return firstErr
}

func (s *Service) fetchLeagueScoreboard(ctx context.Context, league League) ([]SportsGame, error) {
	return s.fetchLeagueScoreboardDate(ctx, league, "")
}

// fetchLeagueScoreboardDate fetches one league, optionally for a provider date.
// Secondary providers keep their own contracts: CFL fixtures, TheSportsDB
// boxing cards and organizer races. A race is never flattened into a
// fabricated two-team matchup.
func (s *Service) fetchLeagueScoreboardDate(ctx context.Context, league League, date string) ([]SportsGame, error) {
	switch league.Provider {
	case "cfl":
		return s.fetchCFLScoreboardDate(ctx, date)
	case "thesportsdb":
		return s.fetchBoxingDate(ctx, date)
	case "organizer":
		// Racing weekends and cycling stages use the event contract, not a
		// scoreboard lane.
		return []SportsGame{}, nil
	}
	if league.Slug == "" || league.Provider != "espn" {
		return []SportsGame{}, nil
	}
	if league.EventKind == "race" {
		// Racing has sessions and drivers; never synthesize a two-team matchup.
		return []SportsGame{}, nil
	}
	endpoint := fmt.Sprintf(espnScoreboardURLFmt, league.Sport, league.Slug)
	query := url.Values{"limit": {fmt.Sprint(sportsScoreboardLimit)}}
	if date != "" {
		query.Set("dates", strings.ReplaceAll(date, "-", ""))
	}
	// ESPN group IDs are sport-specific: 90 is Division I football (FBS+FCS),
	// 50 is Division I basketball.
	switch league.ID {
	case "college-football":
		query.Set("groups", "90")
	case "mens-college-basketball", "womens-college-basketball":
		query.Set("groups", "50")
	}
	endpoint += "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, providerStatus(resp.StatusCode, "espn scoreboard %s: status %d", league.ID, resp.StatusCode)
	}

	var payload espnScoreboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode espn scoreboard %s: %w", league.ID, err)
	}

	games := make([]SportsGame, 0, len(payload.Events))
	for _, event := range payload.Events {
		games = append(games, scoreboardEventGames(event, league)...)
	}
	if len(payload.Events) >= sportsScoreboardLimit || payload.Count > len(payload.Events) || payload.PageCount > max(1, payload.PageIndex) {
		return games, errPartialScoreboard
	}
	return games, nil
}

// TeamCatalogSyncResult reports one team-catalog pass so callers can persist the
// leagues that succeeded, name the leagues that failed, and stay quiet about a
// provider that publishes no team catalog at all.
type TeamCatalogSyncResult struct {
	// Teams is every cached team identity after the pass, including leagues that
	// were already fresh and so were not fetched again.
	Teams []SportsTeamRecord
	// Refreshed counts leagues fetched successfully in this pass.
	Refreshed int
	// Failed lists leagues whose fetch failed for a reason that may clear up.
	Failed []LeagueFetchError
	// Unsupported lists leagues whose provider confirmed it has no team catalog.
	Unsupported []string
}

// LeagueFetchError names a league whose team catalog could not be refreshed.
type LeagueFetchError struct {
	League string `json:"league"`
	Err    string `json:"error"`
}

// EnsureTeamCatalog fetches the complete team list for every enabled league that
// has not been checked in 24 hours. Scoreboards only contain teams playing in
// the current window, so they cannot populate team management by themselves.
// The error reports a pass in which every league that needed fetching failed;
// partial failures are listed in the result instead.
func (s *Service) EnsureTeamCatalog(ctx context.Context) (TeamCatalogSyncResult, error) {
	s.mu.RLock()
	leagues := append([]League(nil), s.leagues...)
	s.mu.RUnlock()
	return s.ensureTeamCatalog(ctx, leagues)
}

// EnsureLeagueTeamCatalog includes a disabled league without enabling polling.
func (s *Service) EnsureLeagueTeamCatalog(ctx context.Context, id string) (TeamCatalogSyncResult, error) {
	league, ok := s.League(id)
	if !ok {
		return TeamCatalogSyncResult{}, fmt.Errorf("unknown league %q", id)
	}
	return s.ensureTeamCatalog(ctx, []League{league})
}

func (s *Service) ensureTeamCatalog(ctx context.Context, leagues []League) (TeamCatalogSyncResult, error) {
	var result TeamCatalogSyncResult
	var resultMu sync.Mutex
	var wg sync.WaitGroup
	attempted := 0
	semaphore := make(chan struct{}, 4)
	for _, league := range leagues {
		if !league.SupportsTeams || league.Slug == "" {
			continue
		}
		if league.Provider != "espn" && league.Provider != "cfl" {
			continue
		}
		if s.teamCatalogFresh(league.ID) || s.leagueBlocked(league.ID) {
			continue
		}
		attempted++
		league := league
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				resultMu.Lock()
				result.Failed = append(result.Failed, LeagueFetchError{League: league.ID, Err: ctx.Err().Error()})
				resultMu.Unlock()
				return
			}
			defer func() { <-semaphore }()
			teams, err := s.fetchLeagueTeams(ctx, league)
			switch {
			case errors.Is(err, errTeamsUnsupported):
				// The provider says this league has no team endpoint. Record the
				// completed check so the freshness window covers it and the next cycle
				// does not probe a known-absent endpoint again.
				s.clearLeagueRefusal(league.ID)
				s.mu.Lock()
				firstNotice := !s.teamCatalogUnsupported[league.ID]
				s.teamCatalogUnsupported[league.ID] = true
				s.teamCatalogUpdated[league.ID] = time.Now()
				s.mu.Unlock()
				if firstNotice {
					slog.Info("sports team catalog unsupported", "league", league.ID, "err", err)
				}
				resultMu.Lock()
				result.Unsupported = append(result.Unsupported, league.ID)
				resultMu.Unlock()
			case err != nil:
				// A refusal backs the league off; a transient failure is retried on
				// the next pass, as the team-catalog contract already expects.
				s.noteLeagueRefusal(league.ID, err)
				if !providerRefusal(err) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					slog.Warn("sports team catalog fetch", "league", league.ID, "err", err)
				}
				resultMu.Lock()
				result.Failed = append(result.Failed, LeagueFetchError{League: league.ID, Err: err.Error()})
				resultMu.Unlock()
			default:
				s.clearLeagueRefusal(league.ID)
				s.mu.Lock()
				s.teamCatalog[league.ID] = teams
				s.teamCatalogUpdated[league.ID] = time.Now()
				delete(s.teamCatalogUnsupported, league.ID)
				s.mu.Unlock()
				resultMu.Lock()
				result.Refreshed++
				resultMu.Unlock()
			}
		}()
	}
	wg.Wait()

	s.mu.RLock()
	for _, league := range leagues {
		result.Teams = append(result.Teams, s.teamCatalog[league.ID]...)
	}
	s.mu.RUnlock()
	sort.Slice(result.Failed, func(i, j int) bool { return result.Failed[i].League < result.Failed[j].League })
	sort.Strings(result.Unsupported)

	if len(result.Failed) > 0 && result.Refreshed == 0 && len(result.Failed) == attempted {
		return result, fmt.Errorf("sports team catalog: %s", result.Failed[0].Err)
	}
	return result, nil
}

// teamCatalogFresh reports whether a league's team catalog was checked inside
// the TTL. A league the provider has no catalog for is fresh with no teams,
// which is how a completed check differs from one that never ran.
func (s *Service) teamCatalogFresh(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	updated, checked := s.teamCatalogUpdated[id]
	if !checked || time.Since(updated) >= teamCatalogTTL {
		return false
	}
	return len(s.teamCatalog[id]) > 0 || s.teamCatalogUnsupported[id]
}

// TeamCatalog returns the currently cached team identities.
func (s *Service) TeamCatalog(league string) []SportsTeamRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []SportsTeamRecord
	if league != "" {
		return append(out, s.teamCatalog[league]...)
	}
	for _, league := range s.leagues {
		out = append(out, s.teamCatalog[league.ID]...)
	}
	return out
}

func (s *Service) fetchLeagueTeams(ctx context.Context, league League) ([]SportsTeamRecord, error) {
	if league.Provider == "cfl" {
		return s.fetchCFLTeams(ctx)
	}
	endpoint := fmt.Sprintf(espnTeamsURLFmt, league.Sport, league.Slug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("espn teams %s: status %d: %w", league.ID, resp.StatusCode, errTeamsUnsupported)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, providerStatus(resp.StatusCode, "espn teams %s: status %d", league.ID, resp.StatusCode)
	}
	var payload espnTeamsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode espn teams %s: %w", league.ID, err)
	}
	teams := espnTeamsToRecords(payload, league)
	if len(teams) == 0 {
		return nil, fmt.Errorf("espn teams %s: empty team catalog", league.ID)
	}
	return teams, nil
}

func (s *Service) saveToDisk() error {
	s.mu.RLock()
	data, err := json.Marshal(s.games)
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal scoreboard: %w", err)
	}
	cacheDir := filepath.Join(s.storageDir, sportsCacheDir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	cachePath := filepath.Join(cacheDir, sportsCacheFile)
	tmpPath := cachePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

func (s *Service) loadFromDisk() error {
	cachePath := filepath.Join(s.storageDir, sportsCacheDir, sportsCacheFile)
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return err
	}
	var games map[string][]SportsGame
	if err := json.Unmarshal(data, &games); err != nil {
		return fmt.Errorf("unmarshal scoreboard: %w", err)
	}
	s.mu.Lock()
	s.games = games
	s.mu.Unlock()
	return nil
}

// ClearLogoCache removes cached provider artwork.
func (s *Service) ClearLogoCache() error {
	dir := filepath.Join(s.storageDir, sportsCacheDir, "logos")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

// GetCachedLogo fetches and caches ESPN artwork. Only known CDN hosts are
// allowed, avoiding an open image proxy/SSRF surface.
func (s *Service) GetCachedLogo(ctx context.Context, rawURL string) ([]byte, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" {
		return nil, "", fmt.Errorf("invalid sports logo URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "a.espncdn.com" && host != "a1.espncdn.com" && host != "secure.espncdn.com" {
		return nil, "", fmt.Errorf("sports logo host not allowed")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(rawURL)))
	dir := filepath.Join(s.storageDir, sportsCacheDir, "logos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, hash+".img")
	if data, readErr := os.ReadFile(path); readErr == nil {
		return data, http.DetectContentType(data), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("sports logo status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (5<<20)+1))
	if err != nil || len(data) > 5<<20 {
		return nil, "", fmt.Errorf("sports logo exceeds size limit")
	}
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("sports logo is not an image")
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}
