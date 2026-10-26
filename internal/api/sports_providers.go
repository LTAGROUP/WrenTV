package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/livetv"
	"github.com/LTAGROUP/wrentv/internal/sports"
	"github.com/LTAGROUP/wrentv/internal/sports/match"
)

// SportsHub searches the same Stremio providers Live TV uses. A provider is a
// configured Live TV source whose manifest declares playable catalogs; its
// items are matched to the event with the shared sports matcher (live) or a
// replay-tolerant title/team match (finished events). The provider's stream
// URL never leaves the server: a match is an address for the ordinary Live TV
// addon endpoint, which ranks, checks and falls back between feeds.
const (
	// sportsProvidersWait bounds one discovery request across every provider.
	sportsProvidersWait = 20 * time.Second
	// sportsProviderSourceWait bounds one provider, so a slow manifest can't
	// consume the whole request and hide the providers that do answer.
	sportsProviderSourceWait = 8 * time.Second
	sportsProviderMaxSources = 8
	// sportsProviderMaxCatalogs bounds the shelves read from one provider.
	// HighFly declares one shelf per sport; the event's own sport is ranked
	// first, so the bound never hides the relevant shelf.
	sportsProviderMaxCatalogs = 6
	sportsProviderMaxItems    = 300
	sportsProviderMaxMatches  = 12
	// sportsProviderMaxVerify bounds how many replay items are asked for their
	// feeds. A replay is only reported when the provider really offers one.
	sportsProviderMaxVerify = 4
)

// sportsProviderMatch is one provider item that plausibly carries the event.
// It carries no stream URL; playback goes through
// /api/livetv/addons/{source}/item/{type}/{id}/play, which selects the feed.
type sportsProviderMatch struct {
	ID             string  `json:"id"`
	Source         string  `json:"source"`
	SourceName     string  `json:"sourceName"`
	Type           string  `json:"type"`
	ItemID         string  `json:"itemId"`
	Name           string  `json:"name"`
	Description    string  `json:"description,omitempty"`
	Group          string  `json:"group,omitempty"`
	Confidence     float64 `json:"confidence"`
	ConfidenceTier string  `json:"confidenceTier"`
	MatchReason    string  `json:"matchReason,omitempty"`
	MatchedTeam    string  `json:"matchedTeam,omitempty"`
	// Feeds is how many playable feeds the provider offered for a replay. It
	// is proven at discovery time for replays and left 0 for live matches,
	// whose feeds are ranked and proven by automatic playback.
	Feeds int    `json:"feeds,omitempty"`
	Mode  string `json:"mode"`
}

// sportsProviderStatus says what one provider contributed. A provider that
// answered with nothing useful is reported by state instead of being dropped
// silently.
type sportsProviderStatus struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind,omitempty"`
	State  string `json:"state"`
	Items  int    `json:"items,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// handleSportsEventProviders searches configured Live TV Stremio providers for
// an event. mode=replay searches replay/VOD shelves as well and only reports a
// match whose feeds exist. Everything else about the event (teams, title,
// sport, kind and parent id) is read from the server's own event record.
func (s *Server) handleSportsEventProviders(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(pathParam(r, "id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "event required")
		return
	}
	game, ok := s.sportsWatchEvent(r, id)
	if !ok {
		if s.sportsDataPending() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "Sports data is still loading.", "code": "sports_pending",
			})
			return
		}
		writeErr(w, http.StatusNotFound, "event not found")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode != "replay" && mode != "live" {
		mode = "live"
		if game.Status == sports.SportsGameFinal {
			mode = "replay"
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), sportsProvidersWait)
	defer cancel()

	matches, statuses := s.discoverSportsProviders(ctx, game, mode)
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Confidence != matches[j].Confidence {
			return matches[i].Confidence > matches[j].Confidence
		}
		return matches[i].SourceName < matches[j].SourceName
	})
	if len(matches) > sportsProviderMaxMatches {
		matches = matches[:sportsProviderMaxMatches]
	}
	stale := false
	for _, status := range statuses {
		if status.State == "unavailable" {
			stale = true
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"eventId": game.ID, "mode": mode, "matches": matches, "sources": statuses, "stale": stale,
	})
}

// discoverSportsProviders queries every enabled addon source concurrently.
// One provider's failure is one status line, never a failed request.
func (s *Server) discoverSportsProviders(ctx context.Context, game sports.SportsGame, mode string) ([]sportsProviderMatch, []sportsProviderStatus) {
	if s.Cfg == nil || !s.Cfg.LiveTV.Enabled || s.LiveTV == nil {
		return []sportsProviderMatch{}, []sportsProviderStatus{}
	}
	sources := make([]config.LiveTVSource, 0, sportsProviderMaxSources)
	for _, src := range s.Cfg.LiveTV.Sources {
		if !src.Enabled || !livetv.IsAddonKind(src.Kind) || len(sources) >= sportsProviderMaxSources {
			continue
		}
		sources = append(sources, src)
	}
	if len(sources) == 0 {
		return []sportsProviderMatch{}, []sportsProviderStatus{}
	}

	matches := make([][]sportsProviderMatch, len(sources))
	statuses := make([]sportsProviderStatus, len(sources))
	var wg sync.WaitGroup
	for index, src := range sources {
		wg.Add(1)
		go func(index int, src config.LiveTVSource) {
			defer wg.Done()
			sourceCtx, cancel := context.WithTimeout(ctx, sportsProviderSourceWait)
			defer cancel()
			matches[index], statuses[index] = s.discoverSportsProvider(sourceCtx, src, game, mode)
		}(index, src)
	}
	wg.Wait()

	flat := make([]sportsProviderMatch, 0, len(sources))
	outStatuses := make([]sportsProviderStatus, 0, len(sources))
	for index := range sources {
		flat = append(flat, matches[index]...)
		outStatuses = append(outStatuses, statuses[index])
	}
	return flat, outStatuses
}

// discoverSportsProvider reads one provider's playable catalogs and matches
// their items to the event. The provider's own metadata is used as evidence,
// never as a promise: live matches are proven at play time, replays here.
func (s *Server) discoverSportsProvider(ctx context.Context, src config.LiveTVSource, game sports.SportsGame, mode string) ([]sportsProviderMatch, sportsProviderStatus) {
	name := strings.TrimSpace(src.Name)
	if name == "" {
		name = src.ID
	}
	status := sportsProviderStatus{ID: src.ID, Name: name, Kind: "addon", State: "complete"}
	caps, err := s.LiveTV.AddonCapabilities(ctx, src)
	if err != nil {
		status.State = "unavailable"
		status.Reason = "The provider could not be reached."
		return nil, status
	}
	catalogs := s.sportsProviderCatalogs(caps, game, mode)
	if len(catalogs) == 0 {
		status.State = "unsupported"
		status.Reason = "The manifest has no playable event catalogs."
		return nil, status
	}

	items := s.sportsProviderItems(ctx, src, caps, catalogs, game, mode)
	if len(items) == 0 {
		status.State = "empty"
		status.Reason = noProviderItemsReason(mode)
		return nil, status
	}
	status.Items = len(items)

	if mode == "replay" {
		found, scored := s.replayMatches(ctx, src, name, game, items)
		if len(found) == 0 {
			if scored > 0 {
				// The provider knows the event but has nothing to play for it.
				// That is a miss, not an available replay.
				status.State = "no-feed"
				status.Reason = "The provider lists this event but offers no playable replay feed."
				return nil, status
			}
			status.State = "no-match"
			status.Reason = "No listing matched this event."
			return nil, status
		}
		return found, status
	}

	found := liveProviderMatches(src, name, game, items)
	if len(found) == 0 {
		status.State = "no-match"
		status.Reason = "No listing matched this event."
		return nil, status
	}
	return found, status
}

func noProviderItemsReason(mode string) string {
	if mode == "replay" {
		return "The provider lists no replay or VOD items for this event right now."
	}
	return "The provider listed nothing for this event right now."
}

// sportsProviderCatalogs selects and ranks the catalogs worth reading. In live
// mode an archive shelf is skipped: an old replay is not the live event. In
// replay mode archive shelves are included and ranked first.
func (s *Server) sportsProviderCatalogs(caps *config.AddonCapabilities, game sports.SportsGame, mode string) []livetv.AddonCatalogView {
	if caps == nil {
		return nil
	}
	streaming := map[string]bool{}
	for _, model := range caps.Models {
		if model.Stream {
			streaming[model.Type] = true
		}
	}
	type ranked struct {
		cat  livetv.AddonCatalogView
		rank int
		at   int
	}
	catalogs := livetv.AddonCatalogViews(caps, "")
	// If the provider declares archive shelves, a replay search reads those. A
	// provider with none gets its normal sport shelves searched: it may still
	// publish the finished event there, and that listing is its own evidence.
	hasArchive := false
	for _, cat := range catalogs {
		if cat.Type != "tv" && streaming[cat.Type] && len(cat.Unsupported) == 0 &&
			(cat.Browsable || cat.Searchable) && isReplayShelf(cat) {
			hasArchive = true
			break
		}
	}
	rankedList := make([]ranked, 0, len(catalogs))
	for index, cat := range catalogs {
		if cat.Type == "tv" || !streaming[cat.Type] || len(cat.Unsupported) > 0 {
			continue
		}
		if !cat.Browsable && !cat.Searchable {
			continue
		}
		replayShelf := isReplayShelf(cat)
		if mode != "replay" && replayShelf {
			continue
		}
		if mode == "replay" && hasArchive && !replayShelf {
			continue
		}
		if mode == "replay" && !hasArchive && cat.Type != "sport" {
			// Without archive shelves, only a sport shelf can hold the event.
			continue
		}
		rankedList = append(rankedList, ranked{cat: cat, rank: sportsCatalogRank(cat, game, replayShelf), at: index})
	}
	sort.SliceStable(rankedList, func(i, j int) bool {
		if rankedList[i].rank != rankedList[j].rank {
			return rankedList[i].rank < rankedList[j].rank
		}
		return rankedList[i].at < rankedList[j].at
	})
	out := make([]livetv.AddonCatalogView, 0, len(rankedList))
	for _, entry := range rankedList {
		if len(out) >= sportsProviderMaxCatalogs {
			break
		}
		out = append(out, entry.cat)
	}
	return out
}

func isReplayShelf(cat livetv.AddonCatalogView) bool {
	return isReplayShelfLabel(cat.ID + " " + cat.Name)
}

// isReplayShelfLabel reports whether a catalog or an event entry's own group
// names an archive shelf.
func isReplayShelfLabel(label string) bool {
	label = strings.ToLower(label)
	for _, marker := range []string{"replay", "replays", "archive", "vod", "ondemand", "on demand", "catchup", "catch-up"} {
		if strings.Contains(label, marker) {
			return true
		}
	}
	return false
}

// sportsCatalogRank puts the event's own sport first, then generic live/today
// shelves, then everything else. The original order breaks ties.
func sportsCatalogRank(cat livetv.AddonCatalogView, game sports.SportsGame, replayShelf bool) int {
	label := strings.ToLower(cat.ID + " " + cat.Name)
	rank := 6
	haystack := label + " " + strings.ToLower(cat.Type)
	for _, word := range strings.FieldsFunc(strings.ToLower(game.Sport+" "+game.League+" "+game.EventContext), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(word) >= 3 && !genericSportsWords[word] && strings.Contains(haystack, word) {
			rank = min(rank, 1)
		}
	}
	for _, marker := range []string{"live", "today", "event", "match", "sport"} {
		if strings.Contains(haystack, marker) {
			rank = min(rank, 2)
		}
	}
	if cat.Searchable {
		rank--
	}
	if replayShelf {
		// In replay mode archives are the most likely source; in live mode they
		// were filtered out already.
		rank = min(rank, 0)
	}
	return rank
}

var genericSportsWords = map[string]bool{
	"sport": true, "sports": true, "live": true, "today": true, "event": true,
	"events": true, "match": true, "matches": true, "stream": true, "streams": true,
}

// sportsProviderItems reads the selected catalogs with a query built from the
// event's teams (or its title for named events). If the first query lists
// nothing, the title is tried before the provider is called empty-handed.
func (s *Server) sportsProviderItems(ctx context.Context, src config.LiveTVSource, caps *config.AddonCapabilities, catalogs []livetv.AddonCatalogView, game sports.SportsGame, mode string) []livetv.AddonItem {
	queries := sportsProviderQueries(game)
	seen := map[string]bool{}
	out := make([]livetv.AddonItem, 0, 40)
	for queryIndex, query := range queries {
		before := len(out)
		for _, cat := range catalogs {
			if len(out) >= sportsProviderMaxItems || ctx.Err() != nil {
				return out
			}
			items, err := s.LiveTV.AddonCatalogItems(ctx, src, caps, cat.Type, cat.ID, query, 0)
			if err != nil {
				continue
			}
			for _, item := range items {
				if len(out) >= sportsProviderMaxItems {
					return out
				}
				key := item.Type + "\x00" + item.ID
				if item.ID == "" || item.Name == "" || seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, item)
			}
		}
		// A provider that answered the first query already has a shelf worth
		// matching. Only fall back to the event title when it had nothing.
		if len(out) > before || queryIndex+1 >= len(queries) {
			break
		}
	}
	return out
}

// sportsProviderQueries preserves the event's teams, title and context. A
// matchup is searched by team name first (the way providers name their
// listings), then by its full title; a named event is searched by its title.
func sportsProviderQueries(game sports.SportsGame) []string {
	var queries []string
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			return
		}
		seen[key] = true
		queries = append(queries, value)
	}
	away := strings.TrimSpace(game.AwayTeam.Name)
	home := strings.TrimSpace(game.HomeTeam.Name)
	if away != "" && home != "" {
		add(away)
		add(home)
	}
	add(game.Title)
	if game.EventKind != "" && game.EventKind != "matchup" {
		add(game.EventContext)
	}
	return queries
}

// liveProviderMatches uses the shared sports matcher on the provider's listing
// names. A listing does not need the exact title: both teams in any supported
// form, or the named event's title, is what the matcher accepts.
func liveProviderMatches(src config.LiveTVSource, name string, game sports.SportsGame, items []livetv.AddonItem) []sportsProviderMatch {
	channels := make([]match.Channel, 0, len(items))
	byID := make(map[string]livetv.AddonItem, len(items))
	for _, item := range items {
		channelID := providerChannelID(src.ID, item)
		byID[channelID] = item
		channels = append(channels, match.Channel{
			ID: channelID, Name: item.Name, TvgName: item.Name, Group: item.Group,
			SourceID: src.ID, SourceName: name, Metadata: sanitizeProviderText(item.Description, 240),
		})
	}
	event := match.Event{
		ID: game.ID, League: game.League, Sport: game.Sport, EventKind: game.EventKind,
		Title: game.Title, Context: game.EventContext, Home: game.HomeTeam, Away: game.AwayTeam,
	}
	if !game.StartTime.IsZero() {
		event.StartTime = game.StartTime.Unix()
	}
	streams, _ := match.Resolve(event, channels, "")
	out := make([]sportsProviderMatch, 0, len(streams))
	seen := map[string]bool{}
	for _, stream := range streams {
		item, ok := byID[stream.ChannelID]
		if !ok || seen[stream.ChannelID] {
			continue
		}
		if stream.ConfidenceTier == "" || stream.ConfidenceTier == "weak" {
			continue
		}
		seen[stream.ChannelID] = true
		out = append(out, providerMatchFromItem(src, name, item, "live", stream.Confidence, stream.ConfidenceTier, stream.MatchReason, stream.MatchedTeam))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	if len(out) > sportsProviderMaxMatches {
		out = out[:sportsProviderMaxMatches]
	}
	return out
}

func providerChannelID(source string, item livetv.AddonItem) string {
	return source + ":" + item.Type + ":" + item.ID
}

// sportsAddonEventMatch is one addon catalog item that plausibly carries the
// event, in the shape the Live TV list renders. It carries no stream URL and no
// addon token: playback goes through the ordinary Live TV addon endpoint, which
// ranks the feeds, proves one starts and falls back.
type sportsAddonEventMatch struct {
	ID             string  `json:"id"`
	Source         string  `json:"source"`
	SourceName     string  `json:"sourceName,omitempty"`
	Type           string  `json:"type"`
	ItemID         string  `json:"itemId"`
	Name           string  `json:"name"`
	Group          string  `json:"group,omitempty"`
	Logo           string  `json:"logo,omitempty"`
	Description    string  `json:"description,omitempty"`
	ReleaseInfo    string  `json:"releaseInfo,omitempty"`
	Confidence     float64 `json:"confidence"`
	ConfidenceTier string  `json:"confidenceTier"`
	MatchReason    string  `json:"matchReason,omitempty"`
	MatchedTeam    string  `json:"matchedTeam,omitempty"`
}

// sportsAddonEventMatches ranks the Live TV event entries against one event, so
// the first Browse Live TV click lists a Stremio provider's events instead of
// waiting for a second manual search. The entries are exactly the ones the
// provider published into Live TV: nothing is guessed, and the provider is not
// asked for a stream until the viewer presses Watch.
//
// A live event uses the shared channel matcher, so its evidence is the same
// evidence a playlist channel is judged on. A finished event uses the replay
// scorer the Watch panel already uses: the matcher deliberately rejects labels
// like "Replay", which is exactly what a replay search is looking for.
//
// A live event is not its replay, so archive shelves are searched only for a
// finished event; when a provider publishes one, the archive is searched on its
// own, the same rule provider discovery uses.
func sportsAddonEventMatches(game sports.SportsGame, event match.Event, entries []livetv.EventEntry, query string, replay bool) []sportsAddonEventMatch {
	shelves := eventEntriesForMode(entries, replay)
	usable := make([]livetv.EventEntry, 0, len(shelves))
	for _, entry := range shelves {
		if entry.ID != "" && entry.ItemID != "" && entry.Name != "" {
			usable = append(usable, entry)
		}
	}
	if query != "" {
		// The manual-search contract is the same for both kinds: a listing is
		// returned when its own name matches the query, not by score.
		out := make([]sportsAddonEventMatch, 0, len(usable))
		for _, entry := range usable {
			channel := match.Channel{Name: entry.Name, TvgName: entry.Name}
			if !match.ChannelMatchesText(channel, query) {
				continue
			}
			out = append(out, addonEventMatchFromEntry(entry, 1, "manual", "Manual event search", ""))
		}
		return rankAddonEventMatches(out)
	}
	if replay {
		type scored struct {
			entry  livetv.EventEntry
			score  float64
			reason string
			team   string
		}
		candidates := make([]scored, 0, len(usable))
		for _, entry := range usable {
			score, reason, team := replayScore(game, addonItemFromEntry(entry))
			if score <= 0 {
				continue
			}
			candidates = append(candidates, scored{entry: entry, score: score, reason: reason, team: team})
		}
		out := make([]sportsAddonEventMatch, 0, len(candidates))
		for _, candidate := range candidates {
			out = append(out, addonEventMatchFromEntry(candidate.entry, candidate.score,
				sportsProviderTier(candidate.score), candidate.reason, candidate.team))
		}
		return rankAddonEventMatches(out)
	}
	channels := make([]match.Channel, 0, len(usable))
	byID := make(map[string]livetv.EventEntry, len(usable))
	for _, entry := range usable {
		channels = append(channels, match.Channel{
			ID: entry.ID, Name: entry.Name, TvgName: entry.Name, Group: entry.Group,
			SourceID: entry.Source, SourceName: entry.SourceName, Metadata: entry.Description,
		})
		byID[entry.ID] = entry
	}
	streams, _ := match.Resolve(event, channels, "")
	out := make([]sportsAddonEventMatch, 0, len(streams))
	seen := make(map[string]bool, len(streams))
	for _, stream := range streams {
		entry, ok := byID[stream.ChannelID]
		if !ok || seen[stream.ChannelID] {
			continue
		}
		// A weak listing is not evidence: it is not offered as a source.
		if stream.ConfidenceTier == "" || stream.ConfidenceTier == "weak" {
			continue
		}
		seen[stream.ChannelID] = true
		out = append(out, addonEventMatchFromEntry(entry, stream.Confidence, stream.ConfidenceTier,
			stream.MatchReason, stream.MatchedTeam))
	}
	return rankAddonEventMatches(out)
}

func addonItemFromEntry(entry livetv.EventEntry) livetv.AddonItem {
	return livetv.AddonItem{
		Type: entry.Type, ID: entry.ItemID, Name: entry.Name, Description: entry.Description,
		Group: entry.Group, Poster: entry.Logo, ReleaseInfo: entry.ReleaseInfo,
	}
}

func addonEventMatchFromEntry(entry livetv.EventEntry, confidence float64, tier, reason, team string) sportsAddonEventMatch {
	return sportsAddonEventMatch{
		ID: entry.ID, Source: entry.Source, SourceName: entry.SourceName,
		Type: entry.Type, ItemID: entry.ItemID,
		Name: sanitizeProviderText(entry.Name, 160), Group: sanitizeProviderText(entry.Group, 80),
		Logo: entry.Logo, Description: sanitizeProviderText(entry.Description, 240),
		ReleaseInfo: sanitizeProviderText(entry.ReleaseInfo, 80),
		Confidence:  confidence, ConfidenceTier: tier,
		MatchReason: sanitizeProviderText(reason, 160), MatchedTeam: sanitizeProviderText(team, 80),
	}
}

func rankAddonEventMatches(out []sportsAddonEventMatch) []sportsAddonEventMatch {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > sportsProviderMaxMatches {
		out = out[:sportsProviderMaxMatches]
	}
	return out
}

// eventEntriesForMode keeps the shelves that belong to this mode: a live event
// skips archive shelves, and a finished event reads an archive when the
// provider publishes one.
func eventEntriesForMode(entries []livetv.EventEntry, replay bool) []livetv.EventEntry {
	hasArchive := false
	for _, entry := range entries {
		if isReplayShelfLabel(entry.Group) {
			hasArchive = true
			break
		}
	}
	out := make([]livetv.EventEntry, 0, len(entries))
	for _, entry := range entries {
		archive := isReplayShelfLabel(entry.Group)
		if replay {
			if hasArchive && !archive {
				continue
			}
		} else if archive {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func providerMatchFromItem(src config.LiveTVSource, name string, item livetv.AddonItem, mode string, confidence float64, tier, reason, team string) sportsProviderMatch {
	return sportsProviderMatch{
		ID:     providerChannelID(src.ID, item),
		Source: src.ID, SourceName: name, Type: item.Type, ItemID: item.ID,
		Name: sanitizeProviderText(item.Name, 160), Description: sanitizeProviderText(item.Description, 240),
		Group: sanitizeProviderText(item.Group, 80), Confidence: confidence, ConfidenceTier: tier,
		MatchReason: sanitizeProviderText(reason, 160), MatchedTeam: sanitizeProviderText(team, 80), Mode: mode,
	}
}

// replayMatches accepts a finished event's listing by both teams or by the
// named event's title. Unlike the live matcher it does not reject "Replay" or
// "Full match" labels; those are exactly what it is looking for. A match is
// only reported after the provider actually hands over a playable feed.
func (s *Server) replayMatches(ctx context.Context, src config.LiveTVSource, name string, game sports.SportsGame, items []livetv.AddonItem) ([]sportsProviderMatch, int) {
	type scored struct {
		item        livetv.AddonItem
		score       float64
		reason      string
		matchedTeam string
	}
	candidates := make([]scored, 0, len(items))
	for _, item := range items {
		score, reason, team := replayScore(game, item)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, scored{item: item, score: score, reason: reason, matchedTeam: team})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	out := make([]sportsProviderMatch, 0, sportsProviderMaxMatches)
	verified := 0
	for _, candidate := range candidates {
		if len(out) >= sportsProviderMaxMatches || verified >= sportsProviderMaxVerify || ctx.Err() != nil {
			break
		}
		verified++
		streams, err := s.LiveTV.AddonStreams(ctx, src, candidate.item.Type, candidate.item.ID)
		if err != nil || len(streams) == 0 {
			// The provider names the event but has nothing to play. Reporting it
			// would promise a replay that does not exist.
			continue
		}
		entry := providerMatchFromItem(src, name, candidate.item, "replay", candidate.score,
			sportsProviderTier(candidate.score), candidate.reason, candidate.matchedTeam)
		entry.Feeds = len(streams)
		out = append(out, entry)
	}
	return out, len(candidates)
}

// replayScore matches a finished event's listing without demanding the exact
// title. Both teams (full name or nickname) are required for a matchup; a
// named event needs its title words, extra provider words are fine.
func replayScore(game sports.SportsGame, item livetv.AddonItem) (float64, string, string) {
	hay := sportsTokenSet(item.Name + " " + item.Description + " " + item.Group)
	home := replayTeamAliases(game.HomeTeam)
	away := replayTeamAliases(game.AwayTeam)
	if len(home.full) > 0 && len(away.full) > 0 {
		homeFull, awayFull := aliasHit(hay, home.full), aliasHit(hay, away.full)
		homeNick, awayNick := aliasHit(hay, home.nickname), aliasHit(hay, away.nickname)
		switch {
		case homeFull && awayFull:
			return 0.96, "Both team names in the replay listing", game.HomeTeam.Name
		case (homeFull && awayNick) || (homeNick && awayFull):
			return 0.88, "Both teams in the replay listing", game.HomeTeam.Name
		case homeNick && awayNick:
			return 0.84, "Both team nicknames in the replay listing", game.HomeTeam.Name
		}
		return 0, "", ""
	}
	title := replayTitleTokens(game.Title)
	if len(title) > 0 && allTokensIn(hay, title) {
		return 0.86, "Event title in the replay listing", ""
	}
	context := replayTitleTokens(game.EventContext)
	if len(context) > 0 && allTokensIn(hay, context) {
		return 0.78, "Event session in the replay listing", ""
	}
	return 0, "", ""
}

type replayAliases struct {
	full     []string
	nickname []string
}

func replayTeamAliases(team sports.SportsTeam) replayAliases {
	aliases := replayAliases{}
	if strings.TrimSpace(team.Name) != "" {
		aliases.full = replayTitleTokens(team.Name)
	}
	if strings.TrimSpace(team.Nickname) != "" {
		aliases.nickname = replayTitleTokens(team.Nickname)
	}
	return aliases
}

func aliasHit(hay map[string]bool, words []string) bool {
	return len(words) > 0 && allTokensIn(hay, words)
}

func allTokensIn(hay map[string]bool, words []string) bool {
	for _, word := range words {
		if !hay[word] {
			return false
		}
	}
	return true
}

var replayStopWords = map[string]bool{
	"the": true, "and": true, "at": true, "vs": true, "v": true, "versus": true,
	"live": true, "replay": true, "replays": true, "full": true, "match": true,
	"game": true, "event": true, "show": true, "stream": true, "hd": true,
}

// replayTitleTokens keeps the distinctive words of a title. Single letters and
// generic words ("vs", "replay", "live") are not evidence of identity.
func replayTitleTokens(value string) []string {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) < 2 || replayStopWords[field] {
			continue
		}
		out = append(out, field)
	}
	return out
}

func sportsTokenSet(value string) map[string]bool {
	out := map[string]bool{}
	for _, token := range replayTitleTokens(value) {
		out[token] = true
	}
	return out
}

func sportsProviderTier(score float64) string {
	switch {
	case score >= 0.85:
		return "strong"
	case score >= 0.6:
		return "possible"
	default:
		return "weak"
	}
}
