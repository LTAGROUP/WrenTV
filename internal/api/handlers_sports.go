package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/livetv"
	"github.com/LTAGROUP/wrentv/internal/sports"
	"github.com/LTAGROUP/wrentv/internal/sports/match"
)

// pathParam returns a route parameter decoded once. chi matches on the escaped
// path (URL.RawPath), so an id the web client percent-encodes —
// espn%3Agolf%3Achampions-tour%3A401 — would arrive here still escaped and
// never match the stored id. Sports ids are single path segments and never
// contain a literal '%', so decoding an already-plain parameter is a no-op.
func pathParam(r *http.Request, key string) string {
	value := chi.URLParam(r, key)
	if decoded, err := url.PathUnescape(value); err == nil {
		return decoded
	}
	return value
}

// ---------- read API ----------

func (s *Server) handleSportsStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Sports.GetStatus())
}

func (s *Server) handleSportsLeagues(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"leagues": s.Sports.Leagues()})
}

// handleSportsScoreboard mirrors MediaStorm: a date query returns the dated
// board with per-league freshness; no date returns the polled snapshot.
func (s *Server) handleSportsScoreboard(w http.ResponseWriter, r *http.Request) {
	league := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("league")))
	if date := strings.TrimSpace(r.URL.Query().Get("date")); date != "" {
		if err := sports.ValidateScoreboardDate(date, time.Now()); err != nil {
			writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD within the current schedule window")
			return
		}
		board, err := s.Sports.GetDatedScoreboard(r.Context(), date, league)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "dated scoreboard unavailable: "+err.Error())
			return
		}
		writeJSON(w, 200, board)
		return
	}
	games := s.Sports.GetScoreboard(league)
	if games == nil {
		games = []sports.SportsGame{}
	}
	writeJSON(w, 200, map[string]any{
		"games":     games,
		"updatedAt": s.Sports.GetStatus().LastRefresh,
		"stale":     s.Sports.GetStatus().LastError != "",
	})
}

// handleSportsHub exposes generalized events plus special tournaments for
// large-screen clients. The date query selects a provider date, matching the
// hub's yesterday/today/tomorrow rail.
func (s *Server) handleSportsHub(w http.ResponseWriter, r *http.Request) {
	league := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("league")))
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	var games []sports.SportsGame
	availability := []sports.LeagueAvailability{}
	updatedAt := time.Time{}
	if date != "" {
		if err := sports.ValidateScoreboardDate(date, time.Now()); err != nil {
			writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD within the current schedule window")
			return
		}
		board, err := s.Sports.GetDatedScoreboard(r.Context(), date, league)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "dated scoreboard unavailable: "+err.Error())
			return
		}
		games, availability, updatedAt = board.Games, board.Leagues, board.UpdatedAt
	} else {
		games = s.Sports.GetScoreboard(league)
		if t := s.Sports.GetStatus().LastRefresh; t != nil {
			updatedAt = *t
		}
	}
	events := make([]sports.SportsEvent, 0, len(games))
	for _, game := range games {
		participants := game.Participants
		if len(participants) == 0 {
			participants = []sports.SportsParticipant{
				{ID: game.AwayTeam.ID, Name: game.AwayTeam.Name, Abbreviation: game.AwayTeam.Abbreviation, LogoURL: game.AwayTeam.LogoURL, Score: game.AwayTeam.Score, Winner: game.AwayTeam.Winner},
				{ID: game.HomeTeam.ID, Name: game.HomeTeam.Name, Abbreviation: game.HomeTeam.Abbreviation, LogoURL: game.HomeTeam.LogoURL, Score: game.HomeTeam.Score, Winner: game.HomeTeam.Winner},
			}
		}
		title := game.Title
		if title == "" {
			title = strings.TrimSpace(game.AwayTeam.Name + " vs " + game.HomeTeam.Name)
		}
		kind := game.EventKind
		if kind == "" {
			kind = "matchup"
		}
		events = append(events, sports.SportsEvent{
			ID: game.ID, Title: title, League: game.League, Sport: game.Sport, EventKind: kind,
			StartTime: game.StartTime, Status: game.Status, StatusDetail: game.StatusDetail,
			Participants: participants, Broadcasts: game.Broadcasts, VenueName: game.VenueName,
		})
	}
	writeJSON(w, 200, map[string]any{
		"events": events, "specialTournaments": []any{}, "leagues": availability,
		"updatedAt": updatedAt, "date": date,
	})
}

// handleSportsEvent returns one game with its enriched ESPN summary. Detail
// failures keep the last good panel and mark it stale; capabilities stay false
// until a module actually has verified data, so clients never open an empty tab.
func (s *Server) handleSportsEvent(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	game, ok := s.Sports.GetGame(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "event not found")
		return
	}
	game = s.Sports.EnrichGame(r.Context(), game)
	if game.Detail == nil {
		game.Detail = &sports.SportsGameDetail{
			Source: "espn", UpdatedAt: time.Now(),
			Periods: []sports.SportsPeriodScore{}, Plays: []sports.SportsDetailPlay{},
			Comparisons: []sports.SportsComparison{},
		}
	}
	writeJSON(w, 200, game)
}

// handleSportsEventStreams ports MediaStorm's "List Streams Here": one event
// is matched against Live TV channels and EPG now/next using the shared
// token-aware matcher. Results are heuristic and only plausible candidates are
// returned; a "possible" match is never auto-selected by the client.
func (s *Server) handleSportsEventStreams(w http.ResponseWriter, r *http.Request) {
	s.serveEventStreams(w, r, pathParam(r, "id"))
}

// handleSportsEventStreamsByQuery is the native-client shape: the event ID is
// a query parameter because the path is shared by several event kinds.
func (s *Server) handleSportsEventStreamsByQuery(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("event"))
	if id == "" {
		id = strings.TrimSpace(r.URL.Query().Get("eventId"))
	}
	if id == "" {
		writeErr(w, http.StatusBadRequest, "event required")
		return
	}
	s.serveEventStreams(w, r, id)
}

// streamMatchCache keeps a short-lived match result per event/scope, mirroring
// MediaStorm's 15 second, 32-entry stream cache. Channel and guide state can
// change at any time, so nothing is kept longer than that.
type streamMatchCache struct {
	mu      sync.Mutex
	entries map[string]streamMatchEntry
}

type streamMatchEntry struct {
	expires time.Time
	streams []sports.SportsStreamMatch
	groups  []sports.SportsStreamGroup
	// events are addon catalog items matched to the same event. They are rows
	// in the same Live TV list as the channels above; a channel and an event
	// differ only in how Live TV starts them.
	events []sportsAddonEventMatch
	liveTV bool
	// pending marks an answer computed before Live TV finished its first
	// refresh. It is never cached: the same request must be able to succeed as
	// soon as the providers are ready.
	pending    bool
	refreshing bool
}

const (
	streamMatchTTL     = 15 * time.Second
	streamMatchEntries = 32
)

func (c *streamMatchCache) get(key string) (streamMatchEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expires) {
		delete(c.entries, key)
		return streamMatchEntry{}, false
	}
	return entry, true
}

func (c *streamMatchCache) put(key string, entry streamMatchEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]streamMatchEntry{}
	}
	if len(c.entries) >= streamMatchEntries {
		// Drop expired answers first, then the oldest one. Resetting the whole
		// map would turn one more request into a burst on the provider.
		now := time.Now()
		var oldestKey string
		var oldest time.Time
		for existingKey, existing := range c.entries {
			if now.After(existing.expires) {
				delete(c.entries, existingKey)
				continue
			}
			if oldestKey == "" || existing.expires.Before(oldest) {
				oldestKey, oldest = existingKey, existing.expires
			}
		}
		if len(c.entries) >= streamMatchEntries && oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	entry.expires = time.Now().Add(streamMatchTTL)
	c.entries[key] = entry
}

func (s *Server) serveEventStreams(w http.ResponseWriter, r *http.Request, id string) {
	game, ok := s.sportsWatchEvent(r, id)
	if !ok {
		if s.sportsDataPending() {
			// The scoreboard is fetched in the background at startup. Until it
			// lands, an unknown id is a request that is too early, not an event
			// that doesn't exist.
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "Sports data is still loading.", "code": "sports_pending",
			})
			return
		}
		writeErr(w, http.StatusNotFound, "event not found")
		return
	}
	broadcast := strings.TrimSpace(r.URL.Query().Get("broadcast"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	status := s.LiveTV.Status()
	// The channel set is part of the key: a match computed against an earlier
	// refresh must not outlive it, even for the remainder of its TTL.
	cacheKey := fmt.Sprintf("%s|%s|%s|%d", id, broadcast, query, s.LiveTV.Revision())
	if entry, ok := s.sportsStreams.get(cacheKey); ok {
		s.writeStreamMatch(w, game.ID, entry, query)
		return
	}
	// An answer computed before the providers finished a refresh that covers
	// them is a transient state, not a result. Caching it is what makes the
	// first Browse Live TV visit look empty until something else warms the
	// providers, which is the bug this guard exists for.
	pending := s.liveTVStarting(status)
	if !s.LiveTV.Active() || pending {
		entry := streamMatchEntry{
			streams: []sports.SportsStreamMatch{}, groups: []sports.SportsStreamGroup{},
			events: []sportsAddonEventMatch{},
			liveTV: s.LiveTV.Active(), pending: pending, refreshing: status.Refreshing,
		}
		if !entry.pending {
			s.sportsStreams.put(cacheKey, entry)
		}
		s.writeStreamMatch(w, game.ID, entry, query)
		return
	}
	channels := s.LiveTV.MatchChannels()
	mapped := make([]match.Channel, 0, len(channels))
	for _, channel := range channels {
		mapped = append(mapped, match.Channel{
			ID: channel.ID, Name: channel.Name, TvgName: channel.TvgName, TvgID: channel.TvgID,
			Group: channel.Group, SourceID: channel.SourceID, SourceName: channel.SourceName,
			Logo: channel.Logo, Metadata: channel.Metadata,
			Now: matchProgram(channel.Now), Next: matchProgram(channel.Next),
		})
	}
	event := match.Event{
		ID: game.ID, League: game.League, Sport: game.Sport, EventKind: game.EventKind,
		StartTime: game.StartTime.Unix(), Title: game.Title,
		Context: game.EventContext, Home: game.HomeTeam, Away: game.AwayTeam,
	}
	if game.StartTime.IsZero() {
		event.StartTime = 0
	}
	// Addon events are searched in the same request. A Stremio provider lists
	// its events into Live TV, so they are matched here exactly like channels:
	// the viewer's first click on Browse Live TV must not need a second manual
	// search before the provider is consulted.
	events := sportsAddonEventMatches(game, event, s.sportsScopeEvents(game.League, s.LiveTV.EventsFor("")),
		query, game.Status == sports.SportsGameFinal)
	if query != "" {
		// Explicit search bypasses scoring and the discovery scope: it returns
		// every channel whose name or tvg identity contains all query tokens, so
		// an admin can deliberately pick a feed outside the default scope.
		filtered := make([]match.Channel, 0)
		for _, channel := range mapped {
			if match.ChannelMatchesText(channel, query) {
				filtered = append(filtered, channel)
			}
		}
		streams, groups := match.BuildStreamGroups(match.SearchChannels(filtered, query))
		entry := streamMatchEntry{streams: streams, groups: groups, events: events, liveTV: true, refreshing: status.Refreshing}
		s.sportsStreams.put(cacheKey, entry)
		s.writeStreamMatch(w, game.ID, entry, query)
		return
	}
	// Automatic discovery honours the configured sports source/group scope and
	// its per-league override, exactly like team auto-linking. Without a scope
	// every visible channel is searched.
	streams, groups := match.Resolve(event, s.sportsLinkScope(game.League, mapped), broadcast)
	entry := streamMatchEntry{streams: streams, groups: groups, events: events, liveTV: true, refreshing: status.Refreshing}
	s.sportsStreams.put(cacheKey, entry)
	s.writeStreamMatch(w, game.ID, entry, query)
}

// liveTVStarting reports whether Live TV has no completed refresh that covers
// the current provider configuration. Only then can an empty list mean "still
// loading"; a completed refresh with nothing is a real empty answer.
func (s *Server) liveTVStarting(status livetv.Status) bool {
	if s.Cfg == nil || !s.Cfg.LiveTV.Enabled || len(s.Cfg.LiveTV.Sources) == 0 {
		return false
	}
	if !status.Prepared {
		return true
	}
	if !status.Refreshing {
		return false
	}
	// A provider enabled after the last completed refresh has contributed
	// nothing yet. While the refresh that will include it is running, an empty
	// answer is pending, not a provider with nothing to offer.
	refreshed := make(map[string]bool, len(status.Sources))
	for _, source := range status.Sources {
		if source.Enabled {
			refreshed[source.ID] = true
		}
	}
	for _, source := range s.Cfg.LiveTV.Sources {
		if source.Enabled && !refreshed[source.ID] {
			return true
		}
	}
	return false
}

// sportsDataPending reports whether the sports scoreboard has never loaded and
// its first refresh is in flight.
func (s *Server) sportsDataPending() bool {
	if s.Sports == nil {
		return false
	}
	status := s.Sports.GetStatus()
	return status.Enabled && status.LastRefresh == nil && status.Refreshing
}

func (s *Server) writeStreamMatch(w http.ResponseWriter, eventID string, entry streamMatchEntry, query string) {
	entryEvents := entry.events
	if entryEvents == nil {
		entryEvents = []sportsAddonEventMatch{}
	}
	payload := map[string]any{
		"streams": entry.streams, "groups": entry.groups, "events": entryEvents,
		"liveTV": entry.liveTV, "eventId": eventID,
		"ready": !entry.pending, "refreshing": entry.refreshing,
	}
	if entry.pending {
		// Clients retry once provider discovery finishes rather than painting an
		// empty result they cannot tell apart from a real one.
		payload["code"] = "livetv_pending"
	}
	if query != "" {
		payload["query"] = query
	}
	writeJSON(w, 200, payload)
}

// sportsSearchTerms splits a query into lowercase words, dropping single
// letters that would match almost every channel.
func sportsSearchTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) >= 2 {
			terms = append(terms, field)
		}
	}
	return terms
}

// sportsMatchesAnyTerm reports whether any term is a substring of target,
// matching MediaStorm's OR-of-terms search.
func sportsMatchesAnyTerm(target string, terms []string) bool {
	lower := strings.ToLower(target)
	for _, term := range terms {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

// handleSportsSearch ports MediaStorm's live hub search: channel metadata,
// the retained EPG window and cached sports events/teams, OR-matched per term
// and capped per source. It never invents a channel for an event.
// sportsTeamHit keeps the competition beside a searched team: the same provider
// team id means different teams in different leagues.
type sportsTeamHit struct {
	sports.SportsTeam
	League string `json:"league"`
}

func (s *Server) handleSportsSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 2 {
		writeErr(w, http.StatusBadRequest, "query must contain at least two characters")
		return
	}
	terms := sportsSearchTerms(query)
	channels := make([]livetv.ChannelView, 0)
	if s.LiveTV.Active() {
		for _, channel := range s.LiveTV.Channels() {
			if !sportsMatchesAnyTerm(channel.Name+" "+channel.Group+" "+channel.TvgName, terms) {
				continue
			}
			if len(channels) >= 100 {
				break
			}
			channels = append(channels, channel)
		}
	}
	livePrograms, upcomingPrograms := s.LiveTV.SearchPrograms(func(title, description string) bool {
		return sportsMatchesAnyTerm(title+" "+description, terms)
	}, 100)
	if livePrograms == nil {
		livePrograms = []livetv.ProgramHit{}
	}
	if upcomingPrograms == nil {
		upcomingPrograms = []livetv.ProgramHit{}
	}
	events := make([]sports.SportsGame, 0)
	teams := make([]sportsTeamHit, 0)
	seen := map[string]struct{}{}
	for _, game := range s.Sports.GetScoreboard("") {
		if sportsMatchesAnyTerm(game.Title+" "+game.HomeTeam.Name+" "+game.AwayTeam.Name+" "+game.League, terms) && len(events) < 100 {
			events = append(events, game)
		}
		for _, team := range []sports.SportsTeam{game.HomeTeam, game.AwayTeam} {
			if !sportsMatchesAnyTerm(team.Name+" "+team.Location+" "+team.Nickname+" "+team.Abbreviation, terms) {
				continue
			}
			key := game.League + ":" + team.ID
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			if len(teams) < 100 {
				teams = append(teams, sportsTeamHit{SportsTeam: team, League: game.League})
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"channels": channels, "livePrograms": livePrograms, "upcomingPrograms": upcomingPrograms,
		"teams": teams, "sportsEvents": events,
	})
}

// handleSportsStreamQuality measures a channel without starting playback. The
// response is a measurement, never a claim that a labelled resolution is real;
// provider URLs and ffprobe errors stay on the server.
func (s *Server) handleSportsStreamQuality(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelID string `json:"channelId"`
	}
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		writeErr(w, http.StatusBadRequest, "channelId required")
		return
	}
	quality, err := s.LiveTV.ProbeChannel(r.Context(), strings.TrimSpace(req.ChannelID))
	if err != nil {
		switch {
		case errors.Is(err, livetv.ErrNoChannel):
			writeErr(w, http.StatusNotFound, "Live channel not found")
		case errors.Is(err, livetv.ErrProbeBusy):
			writeErr(w, http.StatusTooManyRequests, "Quality checker busy. Try again shortly.")
		case errors.Is(err, livetv.ErrProbeLimit):
			writeErr(w, http.StatusConflict, "Provider connection limit reached. Stop playback before checking quality.")
		case errors.Is(err, livetv.ErrProbeUnavailable):
			writeErr(w, http.StatusNotFound, "Quality check unavailable")
		case errors.Is(err, context.DeadlineExceeded):
			writeErr(w, http.StatusGatewayTimeout, "Quality check timed out")
		case errors.Is(err, context.Canceled):
			return
		default:
			// ffprobe errors may contain provider URLs and credentials.
			writeErr(w, http.StatusBadGateway, "Stream quality could not be detected")
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, quality)
}

func matchProgram(p *livetv.Program) *match.Program {
	if p == nil {
		return nil
	}
	return &match.Program{Title: p.Title, Description: p.Description, Start: p.Start, End: p.End}
}

func (s *Server) handleSportsLogo(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if raw == "" {
		writeErr(w, http.StatusBadRequest, "url required")
		return
	}
	data, contentType, err := s.Sports.GetCachedLogo(r.Context(), raw)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

// ---------- admin API ----------

type sportsSettingsView struct {
	config.SportsConfig
	Status sports.SportsStatus `json:"status"`
}

func (s *Server) handleAdminSports(w http.ResponseWriter, r *http.Request) {
	s.Cfg.Sports.Normalize()
	writeJSON(w, 200, sportsSettingsView{SportsConfig: s.Cfg.Sports, Status: s.Sports.GetStatus()})
}

// handleAdminSportsUpdate validates and saves server-wide sports settings, then
// re-registers the polled leagues and refreshes immediately. The same rules as
// MediaStorm apply: strict decoding, unknown leagues dropped, a non-empty
// selection that resolves to nothing rejected.
func (s *Server) handleAdminSportsUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled        *bool                                `json:"enabled"`
		EnabledLeagues *[]string                            `json:"enabledLeagues"`
		FavoriteTeams  *[]string                            `json:"favoriteTeamIds"`
		Sources        *[]string                            `json:"defaultSourceIds"`
		Groups         *[]string                            `json:"defaultCategoryIds"`
		Overrides      *map[string]config.SportsSearchScope `json:"leagueSearchOverrides"`
	}
	dec := decodeStrict(r, &req)
	if err := dec; err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	next := s.Cfg.Sports
	next.Normalize()
	if req.Enabled != nil {
		next.Enabled = req.Enabled
	}
	if req.EnabledLeagues != nil {
		known := map[string]struct{}{"*": {}}
		for _, league := range s.Sports.Leagues() {
			known[league.ID] = struct{}{}
		}
		clean := make([]string, 0, len(*req.EnabledLeagues))
		seen := map[string]struct{}{}
		for _, raw := range *req.EnabledLeagues {
			id := strings.ToLower(strings.TrimSpace(raw))
			if _, ok := known[id]; !ok {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			clean = append(clean, id)
		}
		if len(clean) == 0 && len(*req.EnabledLeagues) > 0 {
			writeErr(w, http.StatusBadRequest, "enable at least one supported league")
			return
		}
		next.Leagues = clean
	}
	if req.FavoriteTeams != nil {
		next.FavoriteTeams = dedupeStrings(*req.FavoriteTeams)
	}
	if req.Sources != nil {
		next.Sources = dedupeStrings(*req.Sources)
	}
	if req.Groups != nil {
		next.Groups = dedupeStrings(*req.Groups)
	}
	if req.Overrides != nil {
		next.Overrides = *req.Overrides
	}
	next.Normalize()
	s.Cfg.Sports = next
	if err := s.Cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to save sports settings")
		return
	}
	s.Sports.SetEnabledLeagueIDs(next.PollingLeagueIDs())
	if next.IsEnabled() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := s.Sports.Refresh(ctx); err != nil {
				slog.Warn("sports settings refresh", "err", err)
			}
		}()
	}
	writeJSON(w, 200, sportsSettingsView{SportsConfig: next, Status: s.Sports.GetStatus()})
}

func (s *Server) handleAdminSportsRefresh(w http.ResponseWriter, r *http.Request) {
	s.Sports.SetEnabledLeagueIDs(s.Cfg.Sports.PollingLeagueIDs())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := s.Sports.Refresh(ctx); err != nil {
			slog.Warn("sports manual refresh", "err", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleAdminSportsLogoCacheClear(w http.ResponseWriter, r *http.Request) {
	if err := s.Sports.ClearLogoCache(); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to clear sports logo cache")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "cleared"})
}

// handleAdminSportsTeams syncs the persisted team catalog on demand. The
// background cycle keeps it fresh; this is the admin's explicit retry. Leagues
// that loaded are persisted even when another league failed, and the response
// names what failed instead of hiding a partial result behind a blanket error.
func (s *Server) handleAdminSportsTeams(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	report, err := s.Sports.EnsureTeamCatalog(ctx)
	changed := 0
	if len(report.Teams) > 0 {
		var syncErr error
		changed, syncErr = s.syncSportsTeams(ctx, report.Teams)
		if syncErr != nil {
			writeErr(w, http.StatusInternalServerError, syncErr.Error())
			return
		}
	}
	if err != nil {
		// Every league that needed fetching failed; there is no partial success
		// to report, so the admin retry fails as a whole.
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	rows, err := s.DB.ListSportsTeams(r.Context(), strings.ToLower(r.URL.Query().Get("league")))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"teams": rows, "changed": changed,
		"failed": report.Failed, "unsupported": report.Unsupported,
	})
}

func dedupeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// errTrailingJSON reports a body with more than one JSON value.
var errTrailingJSON = errors.New("invalid JSON body: trailing data")
