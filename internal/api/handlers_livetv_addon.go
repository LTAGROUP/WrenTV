package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/livetv"
)

// Addon browsing inside regular Live TV. An addon's catalogs are content, not
// television channels, so they are browsed through these routes and played
// with the ordinary Live TV session. Television-channel catalogs are imported
// into the channel list by the refresh instead; sports events and any other
// catalog type share the generic catalog/item path. Nothing here creates a
// channel row or an XMLTV guide, and no channel an addon advertises is
// relabelled as an event.
const (
	addonFeedTTL     = 45 * time.Minute
	addonFeedEntries = 256
	addonMaxStreams  = 20
	addonRequestWait = 20 * time.Second
	addonCheckWait   = 45 * time.Second
)

// addonFeed is the server-side identity behind a stream token. The provider URL
// is not stored: addons hand out short-lived links, so it is resolved again at
// play time and matched to the feed the viewer picked. The content type and
// item id keep two addons with overlapping ids apart.
type addonFeed struct {
	sourceID    string
	contentType string
	itemID      string
	key         string
	index       int
	expires     time.Time
}

type addonFeedRegistry struct {
	mu   sync.Mutex
	byID map[string]addonFeed
}

// addonSourceView is one configured addon as the viewer's source picker sees
// it. Capabilities come from the last check or refresh, never from the client.
type addonSourceView struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Enabled      bool                      `json:"enabled"`
	Capabilities *config.AddonCapabilities `json:"capabilities,omitempty"`
	Note         string                    `json:"note,omitempty"`
}

type addonStreamView struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Quality int    `json:"quality,omitempty"`
}

// liveAddonsAvailable keeps the addon routes behind the same switch as the
// rest of Live TV: a disabled section has no viewer-facing endpoints.
func (s *Server) liveAddonsAvailable(w http.ResponseWriter) bool {
	if s.Cfg != nil && s.Cfg.LiveTV.Enabled {
		return true
	}
	writeErr(w, http.StatusConflict, "Live TV is off. Enable it in Manage → Settings.")
	return false
}

// addonSource finds a configured addon by its source id. Only sources the
// admin saved as an addon are eligible, so a URL from the request can never
// choose which addon is queried.
func (s *Server) liveAddonSource(id string) (config.LiveTVSource, bool) {
	id = strings.TrimSpace(id)
	if id == "" || s.Cfg == nil {
		return config.LiveTVSource{}, false
	}
	for _, src := range s.Cfg.LiveTV.Sources {
		if src.ID == id && livetv.IsAddonKind(src.Kind) {
			return src, true
		}
	}
	return config.LiveTVSource{}, false
}

// addonStatusCapabilities is what the last refresh discovered, for addons
// whose capabilities have not been saved to the configuration yet.
func (s *Server) addonStatusCapabilities() map[string]*config.AddonCapabilities {
	out := map[string]*config.AddonCapabilities{}
	if s.LiveTV == nil {
		return out
	}
	for _, st := range s.LiveTV.Status().Sources {
		if st.Capabilities != nil {
			out[st.ID] = st.Capabilities
		}
	}
	return out
}

// addonSourceViews lists addon sources with the newest capabilities available,
// optionally filtered by what they can do.
func (s *Server) addonSourceViews(keep func(*config.AddonCapabilities) bool) []addonSourceView {
	out := []addonSourceView{}
	if s.Cfg == nil {
		return out
	}
	live := s.addonStatusCapabilities()
	for _, src := range s.Cfg.LiveTV.Sources {
		if !livetv.IsAddonKind(src.Kind) {
			continue
		}
		// The last refresh is newer than the saved copy, so it wins when it
		// exists; the saved copy describes a disabled or not-yet-refreshed addon.
		caps := live[src.ID]
		if caps == nil {
			caps = src.Capabilities
		}
		if keep != nil && !keep(caps) {
			continue
		}
		name := strings.TrimSpace(src.Name)
		if name == "" {
			name = src.ID
		}
		view := addonSourceView{ID: src.ID, Name: name, Enabled: src.Enabled, Capabilities: caps}
		if caps != nil && !caps.HasTVChannels() {
			view.Note = addonNote(caps)
		}
		out = append(out, view)
	}
	return out
}

func addonNote(caps *config.AddonCapabilities) string {
	if caps == nil {
		return ""
	}
	var types []string
	for _, model := range caps.Models {
		if model.Catalogs > 0 && model.Model != "channels" {
			types = append(types, model.Type)
		}
	}
	if len(types) == 0 {
		return ""
	}
	return "Browses " + strings.Join(types, ", ") + " catalogs"
}

// checkAddon is the admin Check button for a Stremio addon. It validates the
// manifest and reports what the addon can do. A valid addon with no TV
// channels, no catalogs or no streams is a successful check with limitations,
// not an invalid manifest.
func (s *Server) checkAddon(w http.ResponseWriter, r *http.Request, src config.LiveTVSource) {
	ctx, cancel := context.WithTimeout(r.Context(), addonCheckWait)
	defer cancel()
	caps, err := s.LiveTV.AddonCapabilities(ctx, src)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	channels := 0
	if caps.HasTVChannels() {
		// A channel count is a convenience for the admin page; a failing
		// channel catalog must not turn a valid manifest into a failed check.
		if list, err := s.LiveTV.StremioChannels(ctx, src); err == nil {
			channels = len(list)
		}
	}
	samples := s.addonSamples(ctx, src, caps)
	// Events are the items that appear in the ordinary Live TV list, so the
	// check reports how many there would be rather than leaving the admin to
	// discover it after saving.
	events := 0
	if entries, err := s.LiveTV.AddonEventEntries(ctx, src); err == nil {
		events = len(entries)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":         "addon",
		"capabilities": caps,
		"channels":     channels,
		"events":       events,
		"catalogs":     len(caps.Catalogs),
		"samples":      samples,
		"browsable":    caps.Browsable,
		"playable":     caps.Playable,
		"limitations":  caps.Limitations,
		// Kept for older settings pages: addons never supply XMLTV.
		"guide":      false,
		"guideCount": 0,
	})
}

// addonSamples counts the first items of up to three non-channel catalogs, so
// the admin page can show that browsing works without reading every shelf.
func (s *Server) addonSamples(ctx context.Context, src config.LiveTVSource, caps *config.AddonCapabilities) []map[string]any {
	out := []map[string]any{}
	for _, cat := range caps.Catalogs {
		if len(out) >= 3 {
			break
		}
		if cat.Type == "tv" || !cat.Browsable || len(cat.Unsupported) > 0 {
			continue
		}
		sample := map[string]any{"type": cat.Type, "catalog": cat.ID, "name": cat.Name}
		items, err := s.LiveTV.AddonItems(ctx, src, cat.Type, cat.ID, "", 0)
		if err != nil {
			sample["error"] = err.Error()
		} else {
			sample["items"] = len(items)
		}
		out = append(out, sample)
	}
	return out
}

// handleLiveAddons lists configured addons. It is a configuration read: no
// manifest or catalog is fetched, so the Live TV page stays fast even when an
// addon is offline.
func (s *Server) handleLiveAddons(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"sources": s.addonSourceViews(nil)})
}

// handleLiveAddonSource returns what one addon can do and the catalogs it
// declares. A valid addon with no browsable catalogs is reported with its
// limitations, not as an invalid addon.
func (s *Server) handleLiveAddonSource(w http.ResponseWriter, r *http.Request) {
	if !s.liveAddonsAvailable(w) {
		return
	}
	src, ok := s.requireAddonSource(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), addonRequestWait)
	defer cancel()
	caps, err := s.LiveTV.AddonCapabilities(ctx, src)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	name := strings.TrimSpace(src.Name)
	if name == "" {
		name = src.ID
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"source":       addonSourceView{ID: src.ID, Name: name, Enabled: src.Enabled, Capabilities: caps},
		"capabilities": caps,
		"catalogs":     livetv.AddonCatalogViews(caps, ""),
	})
}

// requireAddonSource resolves the {source} path parameter, answering 404/409
// when the addon is unknown or disabled.
func (s *Server) requireAddonSource(w http.ResponseWriter, r *http.Request) (config.LiveTVSource, bool) {
	src, ok := s.liveAddonSource(pathParam(r, "source"))
	if !ok {
		writeErr(w, http.StatusNotFound, "That addon is not configured.")
		return config.LiveTVSource{}, false
	}
	if !src.Enabled {
		writeErr(w, http.StatusConflict, "That addon is disabled. Enable it in Manage → Settings.")
		return config.LiveTVSource{}, false
	}
	return src, true
}

// handleLiveAddonCatalog browses one catalog of any content type. An empty
// catalog is a normal answer: nothing on right now is not an error.
func (s *Server) handleLiveAddonCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.liveAddonsAvailable(w) {
		return
	}
	src, ok := s.requireAddonSource(w, r)
	if !ok {
		return
	}
	contentType := pathParam(r, "type")
	catalog := pathParam(r, "catalog")
	query := strings.TrimSpace(r.URL.Query().Get("search"))
	skip := min(max(queryInt(r, "skip", 0), 0), 5000)
	ctx, cancel := context.WithTimeout(r.Context(), addonRequestWait)
	defer cancel()
	items, err := s.LiveTV.AddonItems(ctx, src, contentType, catalog, query, skip)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"source": src.ID, "type": contentType, "catalog": catalog, "items": items,
	})
}

// handleLiveAddonItem returns the addon's metadata for one item and the feeds
// it can play, as opaque ids. Stream URLs, request headers and the addon's own
// configuration stay on the server.
func (s *Server) handleLiveAddonItem(w http.ResponseWriter, r *http.Request) {
	if !s.liveAddonsAvailable(w) {
		return
	}
	src, ok := s.requireAddonSource(w, r)
	if !ok {
		return
	}
	contentType := pathParam(r, "type")
	itemID := pathParam(r, "id")
	if strings.TrimSpace(itemID) == "" {
		writeErr(w, http.StatusBadRequest, "item id required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), addonRequestWait)
	defer cancel()
	item, views, err := s.resolveAddonItem(ctx, src, contentType, itemID)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"source": src.ID, "type": contentType, "id": itemID,
		"item": item, "event": item, "streams": views,
	})
}

// resolveAddonItem fetches optional metadata and the playable feeds, then
// registers opaque tokens for the feeds. Metadata is optional: an addon may
// list items in a catalog and answer streams without implementing meta.
func (s *Server) resolveAddonItem(ctx context.Context, src config.LiveTVSource, contentType, itemID string) (*livetv.AddonEvent, []addonStreamView, error) {
	item, metaErr := s.LiveTV.AddonMeta(ctx, src, contentType, itemID)
	streams, err := s.LiveTV.AddonStreams(ctx, src, contentType, itemID)
	if err != nil {
		// A network failure is the more useful message when both requests fail.
		if metaErr != nil && errors.Is(err, livetv.ErrAddonUnreachable) {
			return nil, nil, metaErr
		}
		return nil, nil, err
	}
	views := make([]addonStreamView, 0, len(streams))
	// Best feeds first: the list is what a viewer picks from when they don't
	// take the automatic stream, so its first entry should be the best one.
	streams = livetv.RankAddonStreams(streams)
	keys := addonFeedKeys(streams)
	for index, stream := range streams {
		if len(views) >= addonMaxStreams {
			break
		}
		key := keys[index]
		token := s.registerAddonFeed(addonFeed{
			sourceID: src.ID, contentType: contentType, itemID: itemID, key: key, index: index,
			expires: time.Now().Add(addonFeedTTL),
		})
		views = append(views, addonStreamView{
			ID: token, Name: stream.Name, Title: stream.Title, Quality: livetv.AddonStreamQuality(stream),
		})
	}
	if item == nil {
		item = &livetv.AddonEvent{Type: contentType, ID: itemID}
	}
	return item, views, nil
}

// handleLiveAddonPlay starts the ordinary Live TV session on a feed the viewer
// picked. The response shape matches /api/livetv/channels/{id}/play, so the
// player needs no new protocol.
func (s *Server) handleLiveAddonPlay(w http.ResponseWriter, r *http.Request) {
	feed, err := s.addonFeed(pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "That addon feed expired. Open the item again.")
		return
	}
	src, ok := s.liveAddonSource(feed.sourceID)
	if !ok || !src.Enabled {
		writeErr(w, http.StatusNotFound, "That addon is no longer configured.")
		return
	}
	user := principal(r)
	if user == nil || user.User == nil {
		writeErr(w, http.StatusUnauthorized, "sign in required")
		return
	}
	if msg, stopped := s.act().refused(user.User.ID, "addons:"+feed.key); stopped {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	var req struct {
		HLS bool `json:"hls"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), addonRequestWait)
	defer cancel()
	// The link handed out when the item was opened is short-lived, so the
	// feed is resolved again and matched by its own identity.
	streams, err := s.LiveTV.AddonStreams(ctx, src, feed.contentType, feed.itemID)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	// The feed list is ranked, so the manual path reads the same order the
	// viewer was shown.
	streams = livetv.RankAddonStreams(streams)
	stream, ok := pickAddonFeed(streams, feed)
	if !ok {
		writeErr(w, http.StatusConflict, "That feed is no longer offered by the addon. Open the item again.")
		return
	}
	sessionKey := "addons:" + feed.key
	sess, err := s.LiveTV.PlayURLWithHeaders(ctx, user.User.ID, sessionKey, stream.URL, stream.UserAgent, stream.Headers, req.HLS)
	switch {
	case errors.Is(err, livetv.ErrBusy):
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "That addon feed is not playable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": sess.URL(), "type": sess.Type(), "sessionId": sess.ID})
}

// ---------- automatic playback ----------

const (
	// addonAutoWait bounds one automatic-playback request: every feed it tries
	// and every addon lookup it makes happen inside it.
	addonAutoWait = 30 * time.Second
	// addonAutoTries bounds how many feeds one request attempts. The rest are
	// reported as remaining, so the player can continue in another request
	// instead of holding one connection open indefinitely.
	addonAutoTries = 4
	// addonAutoSkip bounds the client's "already tried" list.
	addonAutoSkip = 40
)

// addonAutoAttempt is one feed automatic playback tried, for the viewer's
// error state. It names no URL: addon links carry the viewer's tokens.
type addonAutoAttempt struct {
	Key   string `json:"key"`
	Name  string `json:"name,omitempty"`
	Title string `json:"title,omitempty"`
	Error string `json:"error"`
}

// handleLiveAddonAutoPlay is one-click playback for addon content. It resolves
// the item's feeds again (addon URLs are short-lived), ranks them, proves each
// one starts and returns a Live TV session for the first that does. Feeds the
// player already tried are skipped, so a failed stream is never attempted twice
// in one playback attempt.
func (s *Server) handleLiveAddonAutoPlay(w http.ResponseWriter, r *http.Request) {
	if !s.liveAddonsAvailable(w) {
		return
	}
	src, ok := s.requireAddonSource(w, r)
	if !ok {
		return
	}
	user := principal(r)
	if user == nil || user.User == nil {
		writeErr(w, http.StatusUnauthorized, "sign in required")
		return
	}
	contentType := pathParam(r, "type")
	itemID := pathParam(r, "id")
	if strings.TrimSpace(itemID) == "" {
		writeErr(w, http.StatusBadRequest, "item id required")
		return
	}
	var req struct {
		HLS  bool     `json:"hls"`
		Skip []string `json:"skip"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	skip := map[string]bool{}
	for _, key := range req.Skip {
		if len(skip) >= addonAutoSkip {
			break
		}
		if key = strings.TrimSpace(key); key != "" {
			skip[key] = true
		}
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), addonAutoWait)
	defer cancel()
	streams, err := s.LiveTV.AddonStreams(ctx, src, contentType, itemID)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	feeds := livetv.RankAddonStreams(streams)
	keys := addonFeedKeys(feeds)
	attempts := []addonAutoAttempt{}
	tried := []string{}
	triedSet := map[string]bool{}
	// open counts the feeds from `from` on that this playback attempt hasn't
	// tried yet, so the viewer knows whether another round is worth it.
	open := func(from int) int {
		n := 0
		for i := from; i < len(feeds); i++ {
			key := keys[i]
			if !skip[key] && !triedSet[key] {
				n++
			}
		}
		return n
	}
	for i, feed := range feeds {
		key := keys[i]
		if skip[key] {
			continue
		}
		if len(tried) >= addonAutoTries {
			// More feeds are on offer: answer with what is left so the player
			// can continue in another request instead of one long wait.
			writeAddonAutoError(w, http.StatusBadGateway, "no_working_stream",
				fmt.Sprintf("No stream started yet; %d more to try.", open(i)), tried, attempts, open(i))
			return
		}
		if ctx.Err() != nil {
			writeAddonAutoError(w, http.StatusGatewayTimeout, "timeout",
				"Finding a stream that starts is taking too long. Try again, or choose a feed yourself.",
				tried, attempts, open(i))
			return
		}
		if msg, stopped := s.act().refused(user.User.ID, "addons:"+key); stopped {
			writeErr(w, http.StatusForbidden, msg)
			return
		}
		tried = append(tried, key)
		triedSet[key] = true
		if err := s.LiveTV.CheckAddonStream(ctx, feed); err != nil {
			// A provider that is refusing this server is not a dead feed: asking
			// it for the remaining feeds would deepen the limit, so this attempt
			// stops at the first refusal and says when to come back.
			if limit, limited := livetv.RateLimited(err); limited {
				slog.Warn("addon auto play rate limited", "type", contentType, "status", limit.Status,
					"retryAfter", limit.RetryAfter.String(), "ms", time.Since(started).Milliseconds(),
					"feeds", len(tried), "sessions", s.LiveTV.Playing())
				attempts = append(attempts, addonAutoAttempt{Key: key, Name: feed.Name, Title: feed.Title, Error: err.Error()})
				if limit.RetryAfter > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(int(limit.RetryAfter.Seconds())+1))
				}
				writeAddonAutoError(w, http.StatusTooManyRequests, "rate_limited", err.Error(), tried, attempts, open(i+1))
				return
			}
			attempts = append(attempts, addonAutoAttempt{Key: key, Name: feed.Name, Title: feed.Title, Error: err.Error()})
			continue
		}
		sess, err := s.LiveTV.PlayURLWithHeaders(ctx, user.User.ID, "addons:"+key, feed.URL, feed.UserAgent, feed.Headers, req.HLS)
		switch {
		case errors.Is(err, livetv.ErrBusy):
			writeErr(w, http.StatusTooManyRequests, err.Error())
			return
		case err != nil:
			attempts = append(attempts, addonAutoAttempt{Key: key, Name: feed.Name, Title: feed.Title, Error: "it started, but the server couldn't open it"})
			continue
		}
		// Diagnostics for one automatic attempt: the endpoint category and content
		// type, how long it took, how many feeds were tried and how many sessions
		// are open. No link, token or item id is logged.
		slog.Info("addon auto play started", "type", contentType, "status", http.StatusOK,
			"ms", time.Since(started).Milliseconds(), "feeds", len(tried),
			"failed", len(attempts), "sessions", s.LiveTV.Playing())
		writeJSON(w, http.StatusOK, map[string]any{
			"url": sess.URL(), "type": sess.Type(), "sessionId": sess.ID,
			"feed": addonAutoFeed(key, feed), "attempts": attempts, "tried": tried,
			"remaining": open(i + 1),
		})
		return
	}
	message := "Every stream this item has was already tried."
	if len(attempts) > 0 {
		plural := "s"
		if len(attempts) == 1 {
			plural = ""
		}
		message = fmt.Sprintf("Tried %d stream%s, but none would start.", len(attempts), plural)
	}
	slog.Info("addon auto play found no stream", "type", contentType, "status", http.StatusBadGateway,
		"ms", time.Since(started).Milliseconds(), "feeds", len(tried),
		"failed", len(attempts), "sessions", s.LiveTV.Playing())
	writeAddonAutoError(w, http.StatusBadGateway, "no_working_stream", message, tried, attempts, open(0))
}

// addonAutoFeed is the feed a viewer ended up watching, without its URL.
func addonAutoFeed(key string, feed livetv.AddonStream) map[string]any {
	view := map[string]any{"key": key}
	if feed.Name != "" {
		view["name"] = feed.Name
	}
	if feed.Title != "" {
		view["title"] = feed.Title
	}
	if quality := livetv.AddonStreamQuality(feed); quality > 0 {
		view["quality"] = quality
	}
	return view
}

// writeAddonAutoError answers with the feeds that were tried and how many are
// left, so the player can continue or stop with an honest message.
func writeAddonAutoError(w http.ResponseWriter, status int, code, message string, tried []string, attempts []addonAutoAttempt, remaining int) {
	writeJSON(w, status, map[string]any{
		"error": message, "code": code, "tried": tried, "attempts": attempts, "remaining": remaining,
	})
}

// ---------- sports-event aliases ----------

// handleLiveSports lists the addons that have sport catalogs. Choosing one
// browses its events with the generic addon routes.
func (s *Server) handleLiveSports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": s.addonSourceViews(func(caps *config.AddonCapabilities) bool {
			// Without discovered capabilities the addon is listed so the
			// viewer can find out what it offers; the sports model is a
			// legacy hint, not a gate.
			return caps == nil || caps.SportCatalogs() > 0
		}),
	})
}

// handleLiveSportSource returns only the sport catalogs of an addon. A valid
// addon with no sport catalogs is reported as incompatible with this model.
func (s *Server) handleLiveSportSource(w http.ResponseWriter, r *http.Request) {
	if !s.liveAddonsAvailable(w) {
		return
	}
	src, ok := s.requireAddonSource(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), addonRequestWait)
	defer cancel()
	caps, err := s.LiveTV.AddonCapabilities(ctx, src)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	catalogs := livetv.AddonCatalogViews(caps, "sport")
	if len(catalogs) == 0 {
		writeAddonError(w, fmt.Errorf("%w (it lists %s catalogs instead)", livetv.ErrNotSportAddon, strings.Join(caps.Types, ", ")))
		return
	}
	name := strings.TrimSpace(src.Name)
	if name == "" {
		name = src.ID
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"source":   addonSourceView{ID: src.ID, Name: name, Enabled: src.Enabled, Capabilities: caps},
		"catalogs": catalogs, "capabilities": caps,
	})
}

// handleLiveSportCatalog browses one event shelf.
func (s *Server) handleLiveSportCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.liveAddonsAvailable(w) {
		return
	}
	src, ok := s.requireAddonSource(w, r)
	if !ok {
		return
	}
	catalog := pathParam(r, "catalog")
	query := strings.TrimSpace(r.URL.Query().Get("search"))
	skip := min(max(queryInt(r, "skip", 0), 0), 5000)
	ctx, cancel := context.WithTimeout(r.Context(), addonRequestWait)
	defer cancel()
	items, err := s.LiveTV.AddonItems(ctx, src, "sport", catalog, query, skip)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"source": src.ID, "type": "sport", "catalog": catalog, "items": items})
}

// handleLiveSportEvent returns an event's metadata and opaque feed ids.
func (s *Server) handleLiveSportEvent(w http.ResponseWriter, r *http.Request) {
	if !s.liveAddonsAvailable(w) {
		return
	}
	src, ok := s.requireAddonSource(w, r)
	if !ok {
		return
	}
	eventID := pathParam(r, "event")
	if strings.TrimSpace(eventID) == "" {
		writeErr(w, http.StatusBadRequest, "event required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), addonRequestWait)
	defer cancel()
	event, views, err := s.resolveAddonItem(ctx, src, "sport", eventID)
	if err != nil {
		writeAddonError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"source": src.ID, "type": "sport", "event": event, "streams": views})
}

// handleLiveSportPlay keeps the sports route's play path.
func (s *Server) handleLiveSportPlay(w http.ResponseWriter, r *http.Request) {
	s.handleLiveAddonPlay(w, r)
}

// ---------- feed registry ----------

// addonStreamKey is the stable part of a feed's identity. Bitrate and codec
// details live in the title, so identical feeds offered twice stay distinct
// through the ordinal suffix the caller appends.
func addonStreamKey(stream livetv.AddonStream) string {
	return strings.ToLower(strings.TrimSpace(stream.Name)) + "\x00" + strings.ToLower(strings.TrimSpace(stream.Title))
}

// addonFeedKeys names feeds in a stable order: their base identity plus an
// ordinal for feeds the addon publishes twice. The URL is never part of a key,
// because addons rotate their tokens between two requests.
func addonFeedKeys(streams []livetv.AddonStream) []string {
	keys := make([]string, len(streams))
	counts := map[string]int{}
	for i, stream := range streams {
		base := addonStreamKey(stream)
		keys[i] = fmt.Sprintf("%s#%d", base, counts[base])
		counts[base]++
	}
	return keys
}

// pickAddonFeed finds the same feed in a freshly resolved list. Providers
// occasionally reorder their streams, so an exact title match wins and the
// original position is the fallback.
func pickAddonFeed(streams []livetv.AddonStream, feed addonFeed) (livetv.AddonStream, bool) {
	counts := map[string]int{}
	for _, stream := range streams {
		base := addonStreamKey(stream)
		key := fmt.Sprintf("%s#%d", base, counts[base])
		counts[base]++
		if key == feed.key {
			return stream, true
		}
	}
	if feed.index >= 0 && feed.index < len(streams) {
		return streams[feed.index], true
	}
	return livetv.AddonStream{}, false
}

func (s *Server) registerAddonFeed(feed addonFeed) string {
	token := randomToken()
	s.addonFeeds.mu.Lock()
	defer s.addonFeeds.mu.Unlock()
	if s.addonFeeds.byID == nil {
		s.addonFeeds.byID = map[string]addonFeed{}
	}
	now := time.Now()
	for id, entry := range s.addonFeeds.byID {
		if now.After(entry.expires) {
			delete(s.addonFeeds.byID, id)
		}
	}
	if len(s.addonFeeds.byID) >= addonFeedEntries {
		var oldestID string
		var oldest time.Time
		for id, entry := range s.addonFeeds.byID {
			if oldestID == "" || entry.expires.Before(oldest) {
				oldestID, oldest = id, entry.expires
			}
		}
		delete(s.addonFeeds.byID, oldestID)
	}
	s.addonFeeds.byID[token] = feed
	return token
}

func (s *Server) addonFeed(token string) (addonFeed, error) {
	s.addonFeeds.mu.Lock()
	defer s.addonFeeds.mu.Unlock()
	feed, ok := s.addonFeeds.byID[strings.TrimSpace(token)]
	if !ok {
		return addonFeed{}, errors.New("addon feed not found")
	}
	if time.Now().After(feed.expires) {
		delete(s.addonFeeds.byID, token)
		return addonFeed{}, errors.New("addon feed expired")
	}
	return feed, nil
}

// ---------- errors ----------

// writeAddonError separates a manifest the addon cannot supply from a provider
// that is merely unreachable, so the UI can say which one happened.
func writeAddonError(w http.ResponseWriter, err error) {
	code := "addon_unavailable"
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, livetv.ErrNotSportAddon):
		status, code = http.StatusUnprocessableEntity, "incompatible_addon"
	case errors.Is(err, livetv.ErrAddonInvalidResponse):
		status, code = http.StatusBadGateway, "invalid_manifest"
	case errors.Is(err, livetv.ErrUnknownCatalog):
		status, code = http.StatusNotFound, "unknown_catalog"
	case errors.Is(err, livetv.ErrCatalogUnsupported):
		status, code = http.StatusUnprocessableEntity, "catalog_unsupported"
	case errors.Is(err, livetv.ErrUnsupportedResource):
		status, code = http.StatusUnprocessableEntity, "unsupported_resource"
	case errors.Is(err, livetv.ErrNoStreams):
		status, code = http.StatusNotFound, "no_streams"
	case errors.Is(err, livetv.ErrAddonRateLimited):
		// The provider is refusing this server for now. That is not a manifest
		// fact or an empty item: the answer says how long it asked to wait, so
		// the client can tell the viewer instead of retrying into the limit.
		status, code = http.StatusTooManyRequests, "rate_limited"
		if limit, ok := livetv.RateLimited(err); ok && limit.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(limit.RetryAfter.Seconds())+1))
		}
	case errors.Is(err, livetv.ErrAddonUnreachable):
		status, code = http.StatusBadGateway, "addon_unreachable"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "timeout"
	case errors.Is(err, context.Canceled):
		return
	}
	writeJSON(w, status, map[string]any{"error": err.Error(), "code": code})
}
