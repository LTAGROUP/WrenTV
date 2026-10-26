package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// sportsProviderFixture is a Stremio provider with a live shelf, a replay
// shelf, and real media servers so "the first feed failed" is an observed
// request rather than a fabricated error.
type sportsProviderFixture struct {
	addon  *httptest.Server
	stream *httptest.Server
	dead   *httptest.Server
	mu     sync.Mutex
	hits   []string
	// emptyReplay makes the replay shelf answer with no items.
	emptyReplay atomic.Bool
	// noFeedReplay makes the replay shelf list the event with no feed, so the
	// "listed but unplayable" state is exercised without inventing a match.
	noFeedReplay atomic.Bool
}

func (f *sportsProviderFixture) record(path string) {
	f.mu.Lock()
	f.hits = append(f.hits, path)
	f.mu.Unlock()
}

func (f *sportsProviderFixture) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hits...)
}

func (f *sportsProviderFixture) count(path string) int {
	n := 0
	for _, hit := range f.requests() {
		if hit == path {
			n++
		}
	}
	return n
}

func newSportsProviderFixture(t *testing.T) *sportsProviderFixture {
	t.Helper()
	f := &sportsProviderFixture{}
	f.stream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r.URL.Path)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
	}))
	t.Cleanup(f.stream.Close)
	f.dead = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r.URL.Path)
		http.Error(w, "gone", http.StatusForbidden)
	}))
	t.Cleanup(f.dead.Close)

	f.addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/manifest.json"):
			_, _ = io.WriteString(w, `{"id":"fixture.sports","name":"Fixture Sports","types":["sport"],
				"resources":[
					{"name":"catalog","types":["sport"]},
					{"name":"meta","types":["sport"]},
					{"name":"stream","types":["sport"]}
				],
				"catalogs":[
					{"type":"sport","id":"live","name":"Live Now","extra":[{"name":"search"}]},
					{"type":"sport","id":"replays","name":"Replay archive","extra":[{"name":"search"}]}
				]}`)
		case strings.HasPrefix(path, "/catalog/sport/live"):
			_, _ = io.WriteString(w, `{"metas":[
				{"id":"ev:live","name":"Boston Celtics vs Miami Heat","description":"NBA regular season","genres":["Basketball"],"releaseInfo":"LIVE"},
				{"id":"ev:other","name":"Unrelated concert special","description":"not this event"}
			]}`)
		case strings.HasPrefix(path, "/catalog/sport/replays"):
			if f.emptyReplay.Load() {
				_, _ = io.WriteString(w, `{"metas":[]}`)
				return
			}
			if f.noFeedReplay.Load() {
				_, _ = io.WriteString(w, `{"metas":[
					{"id":"ev:nofeed","name":"Boston Celtics vs Miami Heat Replay (locked)","description":"Paid plan only"}
				]}`)
				return
			}
			_, _ = io.WriteString(w, `{"metas":[
				{"id":"ev:replay","name":"Boston Celtics vs Miami Heat Full Match Replay","description":"Full game replay","genres":["Basketball"]},
				{"id":"ev:nofeed","name":"Boston Celtics vs Miami Heat Replay (locked)","description":"Paid plan only"},
				{"id":"ev:other-replay","name":"New York Knicks vs Brooklyn Nets Replay","description":"Another game"}
			]}`)
		case strings.HasPrefix(path, "/stream/sport/ev:live.json"):
			_, _ = fmt.Fprintf(w, `{"streams":[
				{"name":"Addon 4K","title":"2160p","url":%q},
				{"name":"Addon HD","title":"1920x1080","url":%q}
			]}`, f.dead.URL+"/dead-four-k.m3u8", f.stream.URL+"/live.m3u8")
		case strings.HasPrefix(path, "/stream/sport/ev:replay.json"):
			_, _ = fmt.Fprintf(w, `{"streams":[{"name":"Replay HD","title":"1080p","url":%q}]}`, f.stream.URL+"/replay.m3u8")
		case strings.HasPrefix(path, "/stream/sport/ev:other-replay.json"):
			_, _ = fmt.Fprintf(w, `{"streams":[{"name":"Replay HD","title":"720p","url":%q}]}`, f.stream.URL+"/other-replay.m3u8")
		case strings.HasPrefix(path, "/stream/sport/"):
			_, _ = io.WriteString(w, `{"streams":[]}`)
		case strings.HasPrefix(path, "/meta/sport/"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/meta/sport/"), ".json")
			_, _ = fmt.Fprintf(w, `{"meta":{"id":%q,"type":"sport","name":"Boston Celtics vs Miami Heat"}}`, id)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.addon.Close)
	return f
}

// emptySportsAddon is a valid manifest whose only shelf is empty.
func emptySportsAddon(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			_, _ = io.WriteString(w, `{"id":"fixture.empty","name":"Empty Sports","types":["sport"],
				"resources":[{"name":"catalog","types":["sport"]},{"name":"stream","types":["sport"]}],
				"catalogs":[{"type":"sport","id":"live","name":"Live Now","extra":[{"name":"search"}]}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"metas":[]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// offlineSportsAddon answers every request with a server error, the way a
// provider whose host is down does.
func offlineSportsAddon(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "provider down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// hangingSportsAddon never answers: the manifest request is held until the
// caller's deadline, exercising the provider timeout path.
func hangingSportsAddon(t *testing.T) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(func() {
		close(done)
		srv.Close()
	})
	return srv
}

func startSportsLiveTV(t *testing.T, f *sportsFixture) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.s.LiveTV.Start(ctx)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status := f.s.LiveTV.Status()
		if status.Prepared && !status.Refreshing {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("Live TV never finished its first refresh: %+v", f.s.LiveTV.Status())
}

func sportsProviderRoutes(f *sportsFixture) {
	mux := f.mux.(interface {
		Get(string, http.HandlerFunc)
		Post(string, http.HandlerFunc)
	})
	mux.Get("/api/sports/events/{id}/providers", f.s.handleSportsEventProviders)
	mux.Get("/api/livetv/addons/{source}/item/{type}/{id}", f.s.handleLiveAddonItem)
	mux.Post("/api/livetv/addons/{source}/item/{type}/{id}/play", f.s.handleLiveAddonAutoPlay)
	mux.Get("/api/livetv/channels", f.s.handleLiveChannels)
}

func configuredSportsLiveTV(f *sportsFixture, sources ...config.LiveTVSource) {
	f.cfg.LiveTV.Enabled = true
	f.cfg.LiveTV.Sources = sources
	// The legacy streaming addons must not be what answers: the SportsHub
	// search has to use the Live TV providers.
	f.cfg.Streaming.Addons = nil
}

func providerMatches(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["matches"].([]any)
	if !ok {
		t.Fatalf("missing matches: %+v", body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("match is not an object: %+v", item)
		}
		out = append(out, entry)
	}
	return out
}

func providerStates(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	raw, _ := body["sources"].([]any)
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out[fmt.Sprint(entry["id"])] = fmt.Sprint(entry["state"])
	}
	return out
}

// TestSportsHubWatchPlaysThroughLiveTVStremioProvider is the regression for
// Watch: the provider is discovered from Live TV sources (not the legacy
// streaming addons), and the first feed's failure falls through to the next
// feed through the ordinary automatic path.
func TestSportsHubWatchPlaysThroughLiveTVStremioProvider(t *testing.T) {
	f := newSportsFixture(t)
	provider := newSportsProviderFixture(t)
	configuredSportsLiveTV(f, config.LiveTVSource{
		ID: "highfly", Name: "HighFly", Kind: "stremio",
		URL: provider.addon.URL + "/manifest.json", Enabled: true,
	})
	startSportsLiveTV(t, f)
	sportsProviderRoutes(f)

	rec, body := f.do(t, "GET", "/api/sports/events/402/providers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("providers: %d %s", rec.Code, rec.Body.String())
	}
	if body["mode"] != "live" {
		t.Fatalf("mode = %v", body["mode"])
	}
	matches := providerMatches(t, body)
	if len(matches) != 1 {
		t.Fatalf("matches = %+v", matches)
	}
	match := matches[0]
	if match["source"] != "highfly" || match["sourceName"] != "HighFly" ||
		match["type"] != "sport" || match["itemId"] != "ev:live" {
		t.Fatalf("provider identity = %+v", match)
	}
	if tier := fmt.Sprint(match["confidenceTier"]); tier != "strong" && tier != "possible" {
		t.Fatalf("weak match offered: %+v", match)
	}
	for _, secret := range []string{provider.addon.URL, provider.stream.URL, provider.dead.URL, "/live.m3u8"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("provider details leaked %q: %s", secret, rec.Body.String())
		}
	}

	// Automatic playback ranks the feeds and tries the dead 4K feed first.
	rec, play := f.doAs(t, "member", "member", "POST",
		"/api/livetv/addons/highfly/item/sport/ev:live/play", `{"hls":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("automatic play: %d %s", rec.Code, rec.Body.String())
	}
	if play["sessionId"] == "" || play["url"] == "" {
		t.Fatalf("play response = %+v", play)
	}
	feed, _ := play["feed"].(map[string]any)
	if fmt.Sprint(feed["name"]) != "Addon HD" {
		t.Fatalf("automatic playback did not fall back to the working feed: %+v", play)
	}
	if provider.count("/dead-four-k.m3u8") == 0 {
		t.Fatal("the higher-ranked dead feed was never tried")
	}
	if provider.count("/live.m3u8") == 0 {
		t.Fatal("the working feed was never used")
	}
	if strings.Contains(rec.Body.String(), provider.stream.URL) {
		t.Fatalf("play leaked the provider URL: %s", rec.Body.String())
	}
	if f.s.LiveTV.Playing() != 1 {
		t.Fatalf("session not registered: %d", f.s.LiveTV.Playing())
	}
	f.s.LiveTV.Stop(play["sessionId"].(string), "member", false)
}

// TestSportsHubReplaySearchOnlyOffersAPlayableReplay is the regression for
// Search for Replay: a finished event searches replay shelves, reports only a
// replay with a real feed, and never claims one when nothing matches.
func TestSportsHubReplaySearchOnlyOffersAPlayableReplay(t *testing.T) {
	f := newSportsFixture(t)
	provider := newSportsProviderFixture(t)
	configuredSportsLiveTV(f, config.LiveTVSource{
		ID: "highfly", Name: "HighFly", Kind: "stremio",
		URL: provider.addon.URL + "/manifest.json", Enabled: true,
	})
	startSportsLiveTV(t, f)
	sportsProviderRoutes(f)

	rec, body := f.do(t, "GET", "/api/sports/events/402/providers?mode=replay", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("replay search: %d %s", rec.Code, rec.Body.String())
	}
	if body["mode"] != "replay" {
		t.Fatalf("mode = %v", body["mode"])
	}
	matches := providerMatches(t, body)
	if len(matches) != 1 {
		t.Fatalf("replay matches = %+v", matches)
	}
	replay := matches[0]
	if replay["itemId"] != "ev:replay" || replay["mode"] != "replay" {
		t.Fatalf("replay match = %+v", replay)
	}
	if replay["feeds"] != float64(1) {
		t.Fatalf("replay feeds = %v", replay["feeds"])
	}
	if states := providerStates(t, body); states["highfly"] != "complete" {
		t.Fatalf("provider state = %+v", states)
	}
	// A listing the provider names but has no feed for is not a replay.
	for _, match := range matches {
		if match["itemId"] == "ev:nofeed" {
			t.Fatalf("an unplayable replay was promised: %+v", match)
		}
	}

	// Live mode still uses the live shelf and does not offer the replay.
	_, liveBody := f.do(t, "GET", "/api/sports/events/402/providers?mode=live", "")
	liveMatches := providerMatches(t, liveBody)
	if len(liveMatches) != 1 || liveMatches[0]["itemId"] != "ev:live" {
		t.Fatalf("live matches = %+v", liveMatches)
	}

	// With an empty replay shelf the answer is a clear no-match, not a promise.
	provider.emptyReplay.Store(true)
	rec, emptyBody := f.do(t, "GET", "/api/sports/events/402/providers?mode=replay", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty replay search: %d %s", rec.Code, rec.Body.String())
	}
	if got := providerMatches(t, emptyBody); len(got) != 0 {
		t.Fatalf("invented a replay: %+v", got)
	}
	if states := providerStates(t, emptyBody); states["highfly"] != "empty" {
		t.Fatalf("empty replay state = %+v", states)
	}

	// A provider that lists the event but has no replay feed is reported as
	// exactly that: a miss, not an available replay.
	provider.emptyReplay.Store(false)
	provider.noFeedReplay.Store(true)
	rec, noFeedBody := f.do(t, "GET", "/api/sports/events/402/providers?mode=replay", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("no-feed replay search: %d %s", rec.Code, rec.Body.String())
	}
	if got := providerMatches(t, noFeedBody); len(got) != 0 {
		t.Fatalf("invented a replay from an unplayable listing: %+v", got)
	}
	if states := providerStates(t, noFeedBody); states["highfly"] != "no-feed" {
		t.Fatalf("no-feed replay state = %+v", states)
	}
}

// TestSportsProviderFailuresDoNotHideOtherProviders covers empty catalogs,
// provider errors and a provider that never answers. None of them may stop the
// provider that does answer.
func TestSportsProviderFailuresDoNotHideOtherProviders(t *testing.T) {
	f := newSportsFixture(t)
	good := newSportsProviderFixture(t)
	empty := emptySportsAddon(t)
	offline := offlineSportsAddon(t)
	hanging := hangingSportsAddon(t)
	configuredSportsLiveTV(f,
		config.LiveTVSource{ID: "highfly", Name: "HighFly", Kind: "stremio", URL: good.addon.URL + "/manifest.json", Enabled: true},
		config.LiveTVSource{ID: "empty", Name: "Empty Provider", Kind: "stremio", URL: empty.URL + "/manifest.json", Enabled: true},
		config.LiveTVSource{ID: "offline", Name: "Offline Provider", Kind: "stremio", URL: offline.URL + "/manifest.json", Enabled: true},
		config.LiveTVSource{ID: "slow", Name: "Slow Provider", Kind: "stremio", URL: hanging.URL + "/manifest.json", Enabled: true},
	)
	// Discovery is independent of the Live TV channel refresh: it reads the
	// providers directly, so a provider that hangs cannot delay the page load.
	sportsProviderRoutes(f)

	start := time.Now()
	rec, body := f.do(t, "GET", "/api/sports/events/402/providers", "")
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("providers: %d %s", rec.Code, rec.Body.String())
	}
	if elapsed > sportsProvidersWait+3*time.Second {
		t.Fatalf("provider search took %s", elapsed)
	}
	matches := providerMatches(t, body)
	if len(matches) != 1 || matches[0]["source"] != "highfly" {
		t.Fatalf("the working provider was hidden: %+v", matches)
	}
	states := providerStates(t, body)
	want := map[string]string{"highfly": "complete", "empty": "empty", "offline": "unavailable", "slow": "unavailable"}
	for id, state := range want {
		if states[id] != state {
			t.Fatalf("provider %s state = %q (want %q): %+v", id, states[id], state, states)
		}
	}
	if body["stale"] != true {
		t.Fatalf("stale flag = %v", body["stale"])
	}
}
