package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/livetv"
	"github.com/LTAGROUP/wrentv/internal/sports"
	"github.com/LTAGROUP/wrentv/internal/sports/match"
)

// Sports addon expansion ports MediaStorm's event-scoped Stremio discovery:
// configured streaming addons are queried for the event, matching items become
// candidate sources, and only an opaque server-side identity is ever sent to a
// client. Provider URLs, credentials and stream links stay here.
const (
	addonMaxSources      = 4
	addonMaxCatalogs     = 2
	addonMaxMetas        = 40
	addonMaxCandidates   = 12
	addonBodyLimit       = 4 << 20
	addonRequestTimeout  = 6 * time.Second
	addonDiscoverTimeout = 15 * time.Second
	addonDiscoveryTTL    = 3 * time.Minute
	addonSourceTTL       = 10 * time.Minute
	addonSourceEntries   = 128
	addonUserAgent       = "WrenTV/1.0"
)

type addonManifest struct {
	Catalogs []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Extra []struct {
			Name string `json:"name"`
		} `json:"extra"`
	} `json:"catalogs"`
}

type addonCatalogue struct {
	Metas []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Poster      string   `json:"poster"`
		Genres      []string `json:"genres"`
	} `json:"metas"`
}

type addonStreams struct {
	Streams []struct {
		URL           string            `json:"url"`
		Name          string            `json:"name"`
		Title         string            `json:"title"`
		Headers       map[string]string `json:"headers"`
		BehaviorHints struct {
			ProxyHeaders struct {
				Request map[string]string `json:"request"`
			} `json:"proxyHeaders"`
		} `json:"behaviorHints"`
	} `json:"streams"`
}

// addonCandidate is safe to serialize: it carries identity and match evidence,
// never a provider URL.
type addonCandidate struct {
	ID             string    `json:"id"`
	Addon          string    `json:"addon"`
	Title          string    `json:"title,omitempty"`
	Description    string    `json:"description,omitempty"`
	Confidence     float64   `json:"confidence"`
	ConfidenceTier string    `json:"confidenceTier"`
	MatchReason    string    `json:"matchReason,omitempty"`
	MatchedTeam    string    `json:"matchedTeam,omitempty"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type addonStatus struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type addonDiscoveryEntry struct {
	expires    time.Time
	candidates []addonCandidate
	statuses   []addonStatus
	stale      bool
}

type addonDiscoveryCache struct {
	mu       sync.Mutex
	entries  map[string]addonDiscoveryEntry
	inflight map[string]chan struct{}
}

// addonSource is the server-only half of a candidate. The token is random and
// scoped to the event it was discovered for.
type addonSource struct {
	token       string
	eventID     string
	addon       string
	base        string
	catalogType string
	metaID      string
	expires     time.Time
}

type addonRegistry struct {
	mu      sync.Mutex
	byToken map[string]addonSource
}

func addonBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "stremio://") {
		raw = "https://" + strings.TrimPrefix(raw, "stremio://")
	}
	raw = strings.TrimSuffix(raw, "/")
	return strings.TrimSuffix(raw, "/manifest.json")
}

func randomToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// sanitizeProviderText removes anything that could be a credential or URL and
// bounds what a provider can inject into the response.
func sanitizeProviderText(value string, limit int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	for _, prefix := range []string{"http://", "https://"} {
		for {
			index := strings.Index(strings.ToLower(value), prefix)
			if index < 0 {
				break
			}
			end := index
			for end < len(value) && !strings.ContainsRune(" \t\n", rune(value[end])) {
				end++
			}
			value = strings.TrimSpace(value[:index] + value[end:])
		}
	}
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func (s *Server) addonHTTPClient() *http.Client {
	return &http.Client{Timeout: addonRequestTimeout}
}

func (s *Server) fetchAddonJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", addonUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("addon HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, addonBodyLimit)).Decode(out)
}

// discoverAddonCandidates queries enabled addons within the provider budget:
// four addons, two catalogues each and a bounded per-request timeout. A
// catalogue item must pass the ordinary sports matcher before it becomes a
// candidate, so a broad "live TV" shelf cannot leak an unrelated stream.
func (s *Server) discoverAddonCandidates(ctx context.Context, game sports.SportsGame) ([]addonCandidate, []addonStatus, bool) {
	addons := []string{}
	names := map[string]string{}
	for _, addon := range s.Cfg.Streaming.Addons {
		if !addon.Enabled || strings.TrimSpace(addon.URL) == "" || len(addons) >= addonMaxSources {
			continue
		}
		base := addonBase(addon.URL)
		if base == "" {
			continue
		}
		addons = append(addons, base)
		name := strings.TrimSpace(addon.Name)
		if name == "" {
			name = base
		}
		names[base] = name
	}
	if len(addons) == 0 {
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, addonDiscoverTimeout)
	defer cancel()
	client := s.addonHTTPClient()
	query := strings.TrimSpace(game.AwayTeam.Name)
	if game.EventKind == "tournament" || game.EventKind == "race" {
		query = strings.TrimSpace(game.Title)
	}
	if query == "" {
		query = strings.TrimSpace(game.Title)
	}
	event := match.Event{
		ID: game.ID, League: game.League, Sport: game.Sport, EventKind: game.EventKind,
		StartTime: game.StartTime.Unix(), Title: game.Title,
		Context: game.EventContext, Home: game.HomeTeam, Away: game.AwayTeam,
	}

	candidates := []addonCandidate{}
	statuses := []addonStatus{}
	failed := 0
	for _, base := range addons {
		name := names[base]
		var manifest addonManifest
		if err := s.fetchAddonJSON(ctx, client, base+"/manifest.json", &manifest); err != nil {
			statuses = append(statuses, addonStatus{Name: name, State: "unavailable"})
			failed++
			continue
		}
		channels := []match.Channel{}
		tokens := map[string]addonSource{}
		descriptions := map[string]string{}
		catalogs := 0
		for _, catalog := range manifest.Catalogs {
			if catalogs >= addonMaxCatalogs {
				break
			}
			label := strings.ToLower(catalog.ID + " " + catalog.Name)
			if strings.Contains(label, "replay") || strings.TrimSpace(catalog.ID) == "" {
				continue
			}
			searchable := false
			for _, extra := range catalog.Extra {
				if extra.Name == "search" {
					searchable = true
				}
			}
			endpoint := base + "/catalog/" + catalog.Type + "/" + catalog.ID + ".json"
			if searchable && query != "" {
				endpoint = base + "/catalog/" + catalog.Type + "/" + catalog.ID +
					"/search=" + strings.ReplaceAll(strings.TrimSpace(query), " ", "%20") + ".json"
			}
			var catalogue addonCatalogue
			if err := s.fetchAddonJSON(ctx, client, endpoint, &catalogue); err != nil {
				continue
			}
			catalogs++
			for _, meta := range catalogue.Metas {
				if len(meta.ID) == 0 || len(meta.Name) == 0 || len(channels) >= addonMaxMetas {
					continue
				}
				channelID := name + ":" + meta.ID
				channels = append(channels, match.Channel{
					ID: channelID, Name: meta.Name, TvgName: meta.Name,
					Group: catalog.Name, SourceID: base, SourceName: name,
					// The catalogue description rejects a contradictory sport (a
					// snooker listing for a golf event); it never scores evidence.
					Metadata: sanitizeProviderText(meta.Description, 240),
				})
				tokens[channelID] = addonSource{
					token: randomToken(), eventID: game.ID, addon: name, base: base,
					catalogType: catalog.Type, metaID: meta.ID,
				}
				descriptions[channelID] = sanitizeProviderText(meta.Description, 240)
			}
		}
		if len(channels) == 0 {
			statuses = append(statuses, addonStatus{Name: name, State: "empty"})
			continue
		}
		streams, _ := match.Resolve(event, channels, "")
		matched := 0
		for _, stream := range streams {
			src, ok := tokens[stream.ChannelID]
			if !ok || stream.ConfidenceTier == "" || stream.ConfidenceTier == "weak" {
				continue
			}
			if len(candidates) >= addonMaxCandidates {
				break
			}
			src.expires = time.Now().Add(addonSourceTTL)
			s.registerAddonSource(src)
			title := ""
			for _, channel := range channels {
				if channel.ID == stream.ChannelID {
					title = sanitizeProviderText(channel.Name, 160)
					break
				}
			}
			candidates = append(candidates, addonCandidate{
				ID: src.token, Addon: name, Title: title, Description: descriptions[stream.ChannelID],
				Confidence: stream.Confidence, ConfidenceTier: stream.ConfidenceTier,
				MatchReason: sanitizeProviderText(stream.MatchReason, 160),
				MatchedTeam: sanitizeProviderText(stream.MatchedTeam, 80),
				ExpiresAt:   src.expires,
			})
			matched++
		}
		state := "complete"
		if matched == 0 {
			state = "no-match"
		}
		statuses = append(statuses, addonStatus{Name: name, State: state})
	}
	return candidates, statuses, failed > 0
}

func (s *Server) registerAddonSource(src addonSource) {
	s.addonRegistry.mu.Lock()
	defer s.addonRegistry.mu.Unlock()
	if s.addonRegistry.byToken == nil {
		s.addonRegistry.byToken = map[string]addonSource{}
	}
	now := time.Now()
	for token, entry := range s.addonRegistry.byToken {
		if now.After(entry.expires) {
			delete(s.addonRegistry.byToken, token)
		}
	}
	if len(s.addonRegistry.byToken) >= addonSourceEntries {
		var oldestToken string
		var oldest time.Time
		for token, entry := range s.addonRegistry.byToken {
			if oldestToken == "" || entry.expires.Before(oldest) {
				oldestToken, oldest = token, entry.expires
			}
		}
		delete(s.addonRegistry.byToken, oldestToken)
	}
	s.addonRegistry.byToken[src.token] = src
}

var errAddonSourceExpired = fmt.Errorf("addon source expired")

func (s *Server) addonSource(token, eventID string) (addonSource, error) {
	s.addonRegistry.mu.Lock()
	defer s.addonRegistry.mu.Unlock()
	src, ok := s.addonRegistry.byToken[token]
	if !ok || (eventID != "" && src.eventID != eventID) {
		return addonSource{}, fmt.Errorf("addon source not found")
	}
	if time.Now().After(src.expires) {
		delete(s.addonRegistry.byToken, token)
		return addonSource{}, errAddonSourceExpired
	}
	return src, nil
}

// resolveAddonStream asks the addon for the current play link. Links expire
// quickly, so this runs at probe and play time rather than at discovery. The
// returned headers are the addon's browser identity (referer/origin/agent),
// already narrowed to the allowlist: a provider can ask for those, never for a
// cookie or a token.
func (s *Server) resolveAddonStream(ctx context.Context, src addonSource) (string, string, map[string]string, error) {
	endpoint := src.base + "/stream/" + src.catalogType + "/" + src.metaID + ".json"
	var payload addonStreams
	if err := s.fetchAddonJSON(ctx, s.addonHTTPClient(), endpoint, &payload); err != nil {
		return "", "", nil, err
	}
	for _, stream := range payload.Streams {
		url := strings.TrimSpace(stream.URL)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			continue
		}
		// behaviourHints.proxyHeaders.request is the current Stremio field;
		// headers is the older shape. The newer one wins on a conflict.
		supplied := map[string]string{}
		for name, value := range stream.BehaviorHints.ProxyHeaders.Request {
			supplied[name] = value
		}
		for name, value := range stream.Headers {
			supplied[name] = value
		}
		headers := livetv.SanitizeRequestHeaders(supplied)
		agent := addonUserAgent
		if value := headers["User-Agent"]; value != "" {
			agent = value
		}
		return url, agent, headers, nil
	}
	return "", "", nil, fmt.Errorf("addon has no playable stream")
}

func (s *Server) discoverAddons(ctx context.Context, game sports.SportsGame) addonDiscoveryEntry {
	s.addonCache.mu.Lock()
	if entry, ok := s.addonCache.entries[game.ID]; ok && time.Now().Before(entry.expires) {
		s.addonCache.mu.Unlock()
		return entry
	}
	if s.addonCache.inflight == nil {
		s.addonCache.inflight = map[string]chan struct{}{}
	}
	if wait, ok := s.addonCache.inflight[game.ID]; ok {
		s.addonCache.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return addonDiscoveryEntry{stale: true}
		}
		s.addonCache.mu.Lock()
		entry := s.addonCache.entries[game.ID]
		s.addonCache.mu.Unlock()
		return entry
	}
	done := make(chan struct{})
	s.addonCache.inflight[game.ID] = done
	previous := s.addonCache.entries[game.ID]
	s.addonCache.mu.Unlock()

	candidates, statuses, stale := s.discoverAddonCandidates(ctx, game)
	entry := addonDiscoveryEntry{
		expires: time.Now().Add(addonDiscoveryTTL), candidates: candidates, statuses: statuses, stale: stale,
	}
	s.addonCache.mu.Lock()
	if s.addonCache.entries == nil {
		s.addonCache.entries = map[string]addonDiscoveryEntry{}
	}
	// A total failure keeps the previous answer, marked stale, instead of
	// silently claiming the event has no addon sources.
	if len(statuses) == 0 && len(previous.candidates) > 0 {
		entry = previous
		entry.stale = true
		entry.expires = time.Now().Add(addonDiscoveryTTL)
	}
	s.addonCache.entries[game.ID] = entry
	delete(s.addonCache.inflight, game.ID)
	close(done)
	s.addonCache.mu.Unlock()
	return entry
}

// handleSportsAddons exposes event-scoped addon candidates. It is additive to
// the Live TV streams response: the client can show both, and only opaque ids
// leave the server.
func (s *Server) handleSportsAddons(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(pathParam(r, "id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "event required")
		return
	}
	game, ok := s.sportsWatchEvent(r, id)
	if !ok {
		writeErr(w, http.StatusNotFound, "event not found")
		return
	}
	entry := s.discoverAddons(r.Context(), game)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"eventId": game.ID, "candidates": entry.candidates, "sources": entry.statuses, "stale": entry.stale,
	})
}

// addonSourceOrError resolves a token and reports expiry honestly: an expired
// identity is gone, not an unknown route.
func (s *Server) addonSourceOrError(w http.ResponseWriter, srcID string) (addonSource, bool) {
	src, err := s.addonSource(strings.TrimSpace(srcID), "")
	switch {
	case errors.Is(err, errAddonSourceExpired):
		writeErr(w, http.StatusGone, "Addon source expired. Search again.")
		return addonSource{}, false
	case err != nil:
		writeErr(w, http.StatusNotFound, "Addon source not found")
		return addonSource{}, false
	}
	return src, true
}

// handleSportsAddonQuality measures an addon stream without creating a
// playback session. It preserves the Live TV quality contract exactly.
func (s *Server) handleSportsAddonQuality(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AddonID string `json:"addonId"`
	}
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.AddonID) == "" {
		writeErr(w, http.StatusBadRequest, "addonId required")
		return
	}
	src, ok := s.addonSourceOrError(w, req.AddonID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), addonRequestTimeout*2)
	defer cancel()
	streamURL, agent, headers, err := s.resolveAddonStream(ctx, src)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		writeErr(w, http.StatusBadGateway, "Addon stream could not be resolved")
		return
	}
	quality, err := s.LiveTV.ProbeStreamWithHeaders(ctx, streamURL, agent, headers)
	if err != nil {
		switch {
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
			// ffprobe errors may carry provider URLs and credentials.
			writeErr(w, http.StatusBadGateway, "Stream quality could not be detected")
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, quality)
}

// handleSportsAddonPlay starts the ordinary Live TV session on a server-held
// addon URL. The response shape matches /api/livetv/channels/{id}/play, so the
// player and the docked preview need no new protocol.
func (s *Server) handleSportsAddonPlay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HLS bool `json:"hls"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	src, ok := s.addonSourceOrError(w, pathParam(r, "id"))
	if !ok {
		return
	}
	user := principal(r)
	if user == nil || user.User == nil {
		writeErr(w, http.StatusUnauthorized, "sign in required")
		return
	}
	if msg, stopped := s.act().refused(user.User.ID, "sports-addon:"+src.token); stopped {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), addonRequestTimeout*2)
	defer cancel()
	streamURL, agent, headers, err := s.resolveAddonStream(ctx, src)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Addon stream could not be resolved")
		return
	}
	sess, err := s.LiveTV.PlayURLWithHeaders(ctx, user.User.ID, "addon:"+src.token, streamURL, agent, headers, req.HLS)
	switch {
	case errors.Is(err, livetv.ErrBusy):
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "Addon stream is not playable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": sess.URL(), "type": sess.Type(), "sessionId": sess.ID})
}
