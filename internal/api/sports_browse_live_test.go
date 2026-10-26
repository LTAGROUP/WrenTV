package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/livetv"
	"github.com/LTAGROUP/wrentv/internal/sports"
	"github.com/LTAGROUP/wrentv/internal/sports/match"
)

// browseLiveEvents is the addon-event half of the Browse Live TV answer.
func browseLiveEvents(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["events"].([]any)
	if !ok {
		t.Fatalf("the answer has no events list: %+v", body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("event is not an object: %+v", item)
		}
		out = append(out, entry)
	}
	return out
}

// TestBrowseLiveTVMatchesStremioEventsOnFirstClick is the regression for the
// Browse Live TV first click: a SportsHub deep link used to rank only playlist
// channels, so a Stremio provider's events appeared only after the viewer ran a
// manual search. One request must answer with both kinds.
func TestBrowseLiveTVMatchesStremioEventsOnFirstClick(t *testing.T) {
	f := newSportsFixture(t)
	provider := newSportsProviderFixture(t)
	configuredSportsLiveTV(f, config.LiveTVSource{
		ID: "highfly", Name: "HighFly", Kind: "stremio",
		URL: provider.addon.URL + "/manifest.json", Enabled: true,
	})
	startSportsLiveTV(t, f)
	sportsProviderRoutes(f)

	// The provider's event shelf is part of Live TV, exactly like a channel.
	rec, listing := f.do(t, "GET", "/api/livetv/channels", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("channels: %d %s", rec.Code, rec.Body.String())
	}
	events, _ := listing["events"].([]any)
	if len(events) == 0 {
		t.Fatalf("the provider's events never reached Live TV: %+v", listing)
	}

	// The first request, with no prior search, must already list the event.
	rec, body := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("streams: %d %s", rec.Code, rec.Body.String())
	}
	if body["ready"] != true || body["liveTV"] != true {
		t.Fatalf("a settled answer was not reported as settled: %+v", body)
	}
	if body["code"] != nil {
		t.Fatalf("a settled answer still carries a pending code: %+v", body)
	}
	matches := browseLiveEvents(t, body)
	if len(matches) != 1 {
		t.Fatalf("addon events on the first click = %+v", matches)
	}
	match := matches[0]
	if match["source"] != "highfly" || match["sourceName"] != "HighFly" ||
		match["type"] != "sport" || match["itemId"] != "ev:live" {
		t.Fatalf("event identity = %+v", match)
	}
	if !strings.Contains(strings.ToLower(match["name"].(string)), "celtics") {
		t.Fatalf("wrong event matched: %+v", match)
	}
	if tier, _ := match["confidenceTier"].(string); tier != "strong" && tier != "possible" {
		t.Fatalf("a weak listing was offered: %+v", match)
	}
	if match["id"] == "" || match["id"] == "ev:live" {
		t.Fatalf("the row needs the Live TV entry id: %+v", match)
	}
	// A live event is not its replay: the archive shelf must stay out of it.
	for _, entry := range matches {
		if strings.Contains(strings.ToLower(entry["name"].(string)), "replay") {
			t.Fatalf("a replay listing was offered for a live event: %+v", entry)
		}
	}
	// The provider's stream URL and its direct item id never leave the server.
	for _, secret := range []string{provider.addon.URL, provider.stream.URL, provider.dead.URL, "/live.m3u8"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("the answer leaked %q: %s", secret, rec.Body.String())
		}
	}

	// A second request inside the TTL is answered from the same cached match,
	// with the events still attached: they are part of the answer, not a detail
	// of the refresh that produced it.
	_, again := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if got := len(browseLiveEvents(t, again)); got != len(matches) {
		t.Fatalf("the cached answer lost its events: %+v", again)
	}
}

// TestBrowseLiveTVReportsPendingBeforeProvidersFinish keeps the contract that
// an unfinished refresh is a pending state, never a completed empty result.
// The first click must be retried by the client, not painted as "nothing".
func TestBrowseLiveTVReportsPendingBeforeProvidersFinish(t *testing.T) {
	f := newSportsFixture(t)
	provider := newSportsProviderFixture(t)
	configuredSportsLiveTV(f, config.LiveTVSource{
		ID: "highfly", Name: "HighFly", Kind: "stremio",
		URL: provider.addon.URL + "/manifest.json", Enabled: true,
	})
	sportsProviderRoutes(f)

	rec, early := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("early streams: %d %s", rec.Code, rec.Body.String())
	}
	if early["ready"] != false || early["code"] != "livetv_pending" {
		t.Fatalf("an unfinished refresh was reported as settled: %+v", early)
	}
	if got := browseLiveEvents(t, early); len(got) != 0 {
		t.Fatalf("a pending answer listed events: %+v", got)
	}
	if groups, _ := early["groups"].([]any); len(groups) != 0 {
		t.Fatalf("a pending answer listed channels: %+v", groups)
	}

	startSportsLiveTV(t, f)
	// The pending answer was never cached: the very next request finds the
	// provider instead of repeating the empty answer for its TTL.
	rec, settled := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != http.StatusOK || settled["ready"] != true {
		t.Fatalf("settled streams: %d %+v", rec.Code, settled)
	}
	if got := browseLiveEvents(t, settled); len(got) != 1 {
		t.Fatalf("the first settled answer has no provider event: %+v", settled)
	}
}

// TestBrowseLiveTVEmptyAnswerSettles is the other half of the rule: a provider
// that really lists nothing yields a final, cacheable empty answer, so the page
// stops waiting instead of polling forever.
func TestBrowseLiveTVEmptyAnswerSettles(t *testing.T) {
	f := newSportsFixture(t)
	empty := emptySportsAddon(t)
	configuredSportsLiveTV(f, config.LiveTVSource{
		ID: "empty", Name: "Empty Sports", Kind: "stremio",
		URL: empty.URL + "/manifest.json", Enabled: true,
	})
	startSportsLiveTV(t, f)
	sportsProviderRoutes(f)

	rec, body := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("streams: %d %s", rec.Code, rec.Body.String())
	}
	if body["ready"] != true || body["code"] != nil {
		t.Fatalf("an empty provider was reported as loading: %+v", body)
	}
	if got := browseLiveEvents(t, body); len(got) != 0 {
		t.Fatalf("an empty provider listed events: %+v", got)
	}
	if groups, _ := body["groups"].([]any); len(groups) != 0 {
		t.Fatalf("an empty provider listed channels: %+v", groups)
	}
	if f.s.LiveTV.Status().Refreshing {
		t.Fatalf("the refresh never finished: %+v", f.s.LiveTV.Status())
	}
}

// TestSportsAddonEventMatchesUsesTheModeShelf pins the shelf rule directly: a
// live event reads the live shelf, a finished event reads the archive when the
// provider publishes one. A "Full Match Replay" is never offered as live.
func TestSportsAddonEventMatchesUsesTheModeShelf(t *testing.T) {
	event := match.Event{
		ID: "402", League: "nba", Sport: "basketball", EventKind: "matchup",
		Title: "Boston Celtics at Miami Heat", StartTime: 1761422400,
		Home: sportsTeam("14", "Miami Heat"), Away: sportsTeam("2", "Boston Celtics"),
	}
	entries := []livetv.EventEntry{
		{
			ID: "live-1", ItemID: "ev:live", Type: "sport", Source: "highfly", SourceName: "HighFly",
			Name: "Boston Celtics vs Miami Heat", Group: "Live Now", ReleaseInfo: "LIVE",
		},
		{
			ID: "replay-1", ItemID: "ev:replay", Type: "sport", Source: "highfly", SourceName: "HighFly",
			Name: "Boston Celtics vs Miami Heat Full Match Replay", Group: "Replay archive",
		},
	}

	game := sports.SportsGame{
		ID: "402", League: "nba", Sport: "basketball", EventKind: "matchup",
		Title: "Boston Celtics at Miami Heat", StartTime: time.Unix(1761422400, 0),
		HomeTeam: sportsTeam("14", "Miami Heat"), AwayTeam: sportsTeam("2", "Boston Celtics"),
	}
	live := sportsAddonEventMatches(game, event, entries, "", false)
	if len(live) != 1 || live[0].ItemID != "ev:live" {
		t.Fatalf("live matches = %+v", live)
	}

	// A finished event searches the archive instead, with the replay scorer
	// (the channel matcher rejects every replay label by design).
	final := game
	final.Status = sports.SportsGameFinal
	replay := sportsAddonEventMatches(final, event, entries, "", true)
	if len(replay) != 1 || replay[0].ItemID != "ev:replay" {
		t.Fatalf("replay matches = %+v", replay)
	}

	// A provider with no archive shelf still has its live shelf searched for a
	// finished event: it may be the only listing it published.
	noArchive := entries[:1]
	if got := sportsAddonEventMatches(final, event, noArchive, "", true); len(got) != 1 || got[0].ItemID != "ev:live" {
		t.Fatalf("finished event without an archive shelf = %+v", got)
	}

	// An explicit search keeps the text contract for events too.
	if got := sportsAddonEventMatches(game, event, entries, "Celtics", false); len(got) != 1 || got[0].ItemID != "ev:live" {
		t.Fatalf("explicit event search = %+v", got)
	}
	if got := sportsAddonEventMatches(game, event, entries, "Knicks", false); len(got) != 0 {
		t.Fatalf("an unrelated search listed an event: %+v", got)
	}
}

func sportsTeam(id, name string) sports.SportsTeam {
	return sports.SportsTeam{ID: id, Name: name}
}
