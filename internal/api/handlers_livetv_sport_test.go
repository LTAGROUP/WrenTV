package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/livetv"
)

// A sports addon is a Stremio manifest whose catalogs use the sport content
// type. The fixture mirrors the real shape: sport catalogs with search/skip
// extras, event metadata, and a streams array that mixes paywalled
// placeholders with playable HLS links.
type sportAddonFixture struct {
	addon  *httptest.Server
	stream *httptest.Server

	mu    sync.Mutex
	mode  string
	paths []string

	// The upstream media server only answers with the addon's referer, so a
	// proxy fetch proves the feed's headers are applied and credentials dropped.
	sawReferer   atomic.Bool
	leakedCookie atomic.Bool
}

func (f *sportAddonFixture) setMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *sportAddonFixture) getMode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *sportAddonFixture) record(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, path)
}

func (f *sportAddonFixture) requests(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, path := range f.paths {
		if strings.HasPrefix(path, prefix) {
			n++
		}
	}
	return n
}

func newSportAddonFixture(t *testing.T) *sportAddonFixture {
	t.Helper()
	f := &sportAddonFixture{}
	f.stream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://addon.example/" {
			http.Error(w, "addon identity required", http.StatusForbidden)
			return
		}
		if r.Header.Get("Cookie") != "" {
			f.leakedCookie.Store(true)
		}
		f.sawReferer.Store(true)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
	}))
	t.Cleanup(f.stream.Close)
	f.addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		f.record(path)
		w.Header().Set("Content-Type", "application/json")
		mode := f.getMode()
		switch {
		case strings.HasSuffix(path, "/manifest.json"):
			if mode == "bad-manifest" {
				_, _ = io.WriteString(w, `{not stremio json`)
				return
			}
			if mode == "tv-only" {
				_, _ = io.WriteString(w, `{"id":"test.tv","types":["tv"],"resources":["catalog","stream"],"catalogs":[{"type":"tv","id":"channels","name":"Channels"}]}`)
				return
			}
			if mode == "no-catalogs" {
				_, _ = io.WriteString(w, `{"id":"test.empty","types":["movie"],"resources":[{"name":"meta","types":["movie"]},{"name":"stream","types":["movie"]}],"catalogs":[]}`)
				return
			}
			// Resources are declared per content type, like the real addon's
			// manifest: catalog, meta and stream all serve sport.
			_, _ = io.WriteString(w, `{"id":"test.sports","types":["sport","movie"],"resources":[
				{"name":"catalog","types":["sport","movie"]},
				{"name":"meta","types":["sport"],"idPrefixes":["streamed"]},
				{"name":"stream","types":["sport"],"idPrefixes":["streamed"]}
			],"catalogs":[
				{"type":"sport","id":"sports_live","name":"Live Now","extra":[{"name":"search"},{"name":"skip"}]},
				{"type":"movie","id":"films","name":"Films"}
			]}`)
		case strings.HasPrefix(path, "/catalog/sport/sports_live"):
			if mode == "empty" {
				_, _ = io.WriteString(w, `{"metas":[]}`)
				return
			}
			if strings.Contains(path, "search=") {
				_, _ = io.WriteString(w, `{"metas":[{"id":"streamed:one","name":"Alpha vs Beta","genres":["Football"],"releaseInfo":"LIVE"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"metas":[
				{"id":"","name":"no identity"},
				{"id":"streamed:one","name":"Alpha vs Beta","poster":"http://img.example/one.webp","background":"http://img.example/one-bg.webp","genres":["Football"],"description":"Alpha vs Beta\nLIVE NOW","releaseInfo":"LIVE","category":"football"},
				{"id":"streamed:one","name":"Alpha vs Beta duplicate","genres":["Football"]},
				{"id":"streamed:two","name":"Gamma vs Delta","genres":["Tennis"]}
			]}`)
		case strings.HasPrefix(path, "/meta/sport/"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/meta/sport/"), ".json")
			_, _ = fmt.Fprintf(w, `{"meta":{"id":%q,"type":"sport","name":"Alpha vs Beta","description":"Alpha vs Beta LIVE NOW","background":"http://img.example/one-bg.webp","genres":["Football"],"releaseInfo":"LIVE","category":"football"}}`, id)
		case strings.HasPrefix(path, "/stream/sport/"):
			if mode == "no-streams" {
				_, _ = io.WriteString(w, `{"streams":[
					{"name":"🔒 Leaf (Strong) · PRIME: CBS","title":"🔒 Upgrade to Premium","url":"https://www.google.com"},
					{"name":"Tip: Use an external player","title":"mpv recommended","url":"`+f.addon.URL+`/health"}
				]}`)
				return
			}
			// The addon asks for browser identity and also sends a cookie the
			// proxy must never forward. Paywalled entries and the player tip are
			// placeholders, not media.
			_, _ = fmt.Fprintf(w, `{"streams":[
				{"name":"🔒 Leaf (Strong) · PRIME: CBS","title":"1920x1080 · 🔒 Upgrade to Premium","url":"https://www.google.com"},
				{"name":"Tip: Use an external player","title":"mpv recommended","url":%q},
				{"name":"Leaf (Strong) · NOW: TNT SPORT 1","title":"1920x1080 · ~10.0 Mbps","url":%q,"behaviorHints":{"proxyHeaders":{"request":{"Referer":"https://addon.example/","Cookie":"sid=secret"}}}},
				{"name":"CDN (Premium)","title":"🔒 Upgrade to watch","url":"https://mirror.example/premium.m3u8"}
			]}`, f.addon.URL+"/health", f.stream.URL+"/live.m3u8")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.addon.Close)
	return f
}

// liveSportTestServer wires the Live TV routes the viewer and the admin page
// use, so the handlers run with the real chi URL parameters.
type liveSportTestServer struct {
	s   *Server
	mux *chi.Mux
}

func newLiveSportTestServer(t *testing.T, sources ...config.LiveTVSource) *liveSportTestServer {
	t.Helper()
	cfg := &config.Config{LiveTV: config.LiveTVConfig{Enabled: true, Sources: sources}}
	s := &Server{Cfg: cfg, LiveTV: livetv.New(cfg, t.TempDir())}
	mux := chi.NewRouter()
	mux.Get("/api/livetv/channels", s.handleLiveChannels)
	mux.Get("/api/livetv/guide", s.handleLiveGuide)
	mux.Get("/api/livetv/addons", s.handleLiveAddons)
	mux.Get("/api/livetv/addons/{source}", s.handleLiveAddonSource)
	mux.Get("/api/livetv/addons/{source}/catalog/{type}/{catalog}", s.handleLiveAddonCatalog)
	mux.Get("/api/livetv/addons/{source}/item/{type}/{id}", s.handleLiveAddonItem)
	mux.Post("/api/livetv/addons/{source}/item/{type}/{id}/play", s.handleLiveAddonAutoPlay)
	mux.Post("/api/livetv/addons/streams/{id}/play", s.handleLiveAddonPlay)
	mux.Get("/api/livetv/sports", s.handleLiveSports)
	mux.Get("/api/livetv/sports/{source}", s.handleLiveSportSource)
	mux.Get("/api/livetv/sports/{source}/catalog/{catalog}", s.handleLiveSportCatalog)
	mux.Get("/api/livetv/sports/{source}/event/{event}", s.handleLiveSportEvent)
	mux.Post("/api/livetv/sports/streams/{id}/play", s.handleLiveSportPlay)
	mux.Post("/api/admin/livetv/check", s.handleLiveTVCheck)
	return &liveSportTestServer{s: s, mux: mux}
}

func (h *liveSportTestServer) do(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), principalKey, &auth.Principal{User: &db.User{ID: "viewer"}, Role: "owner"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func TestLiveSportAddonIsListedAndBrowseable(t *testing.T) {
	f := newSportAddonFixture(t)
	src := config.LiveTVSource{ID: "highfly", Name: "Sports Streams", Kind: "sport", URL: f.addon.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, src)

	// Listing sources is a configuration read: no addon request is made.
	rec, body := h.do(t, "GET", "/api/livetv/sports", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sources status = %d: %s", rec.Code, rec.Body.String())
	}
	sources, _ := body["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("sources = %s", rec.Body.String())
	}
	first := sources[0].(map[string]any)
	if first["id"] != "highfly" || first["name"] != "Sports Streams" || first["enabled"] != true {
		t.Fatalf("source = %+v", first)
	}
	if f.requests("/manifest.json") != 0 {
		t.Fatalf("listing sources fetched a manifest")
	}

	// Only sport catalogs are offered; the movie shelf is not a Live TV source.
	rec, body = h.do(t, "GET", "/api/livetv/sports/highfly", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalogs status = %d: %s", rec.Code, rec.Body.String())
	}
	catalogs, _ := body["catalogs"].([]any)
	if len(catalogs) != 1 || catalogs[0].(map[string]any)["id"] != "sports_live" {
		t.Fatalf("catalogs = %s", rec.Body.String())
	}

	// Browsing an event shelf reads the sport catalog; unnamed and duplicate
	// items are dropped instead of producing two rows for one event.
	rec, body = h.do(t, "GET", "/api/livetv/sports/highfly/catalog/sports_live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog status = %d: %s", rec.Code, rec.Body.String())
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %s", rec.Body.String())
	}
	item := items[0].(map[string]any)
	if item["id"] != "streamed:one" || item["name"] != "Alpha vs Beta" || item["group"] != "Football" {
		t.Fatalf("item = %+v", item)
	}
	if item["releaseInfo"] != "LIVE" || item["category"] != "football" {
		t.Fatalf("item metadata = %+v", item)
	}

	// A search is passed with the Stremio search extra, not a local text match.
	rec, body = h.do(t, "GET", "/api/livetv/sports/highfly/catalog/sports_live?search=Alpha%20vs%20Beta", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d: %s", rec.Code, rec.Body.String())
	}
	if items, _ := body["items"].([]any); len(items) != 1 {
		t.Fatalf("search items = %s", rec.Body.String())
	}
	if f.requests("/catalog/sport/sports_live/search=Alpha vs Beta") != 1 {
		t.Fatalf("search was not sent as the catalog extra: %+v", f.paths)
	}

	// An unknown shelf is a 404, not an empty list.
	rec, body = h.do(t, "GET", "/api/livetv/sports/highfly/catalog/nope", "")
	if rec.Code != http.StatusNotFound || body["code"] != "unknown_catalog" {
		t.Fatalf("unknown catalog = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLiveSportEventStreamsAreOpaqueAndPlayable(t *testing.T) {
	f := newSportAddonFixture(t)
	src := config.LiveTVSource{ID: "highfly", Name: "Sports Streams", Kind: "sport", URL: f.addon.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, src)

	rec, body := h.do(t, "GET", "/api/livetv/sports/highfly/event/streamed:one", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("event status = %d: %s", rec.Code, rec.Body.String())
	}
	event, _ := body["event"].(map[string]any)
	if event["name"] != "Alpha vs Beta" || event["releaseInfo"] != "LIVE" || event["category"] != "football" {
		t.Fatalf("event = %+v", event)
	}
	streams, _ := body["streams"].([]any)
	// Paywalled placeholders (google.com), the external-player tip and the
	// "upgrade to watch" entry are not media; exactly one feed remains.
	if len(streams) != 1 {
		t.Fatalf("streams = %s", rec.Body.String())
	}
	stream := streams[0].(map[string]any)
	if stream["id"] == "" || !strings.Contains(fmt.Sprint(stream["name"]), "TNT SPORT 1") {
		t.Fatalf("stream = %+v", stream)
	}
	// Artwork links are part of an event's metadata. The provider's play link
	// and its request headers are not: they stay server-side behind the token.
	raw := rec.Body.String()
	for _, secret := range []string{f.stream.URL, "live.m3u8", "sid=secret", "Referer", "google.com", "/health"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("stream response leaked %q: %s", secret, raw)
		}
	}

	// Playing the feed resolves the addon again and starts the ordinary Live TV
	// session, so the player sees the usual session shape.
	rec, body = h.do(t, "POST", "/api/livetv/sports/streams/"+stream["id"].(string)+"/play", `{"hls":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("play status = %d: %s", rec.Code, rec.Body.String())
	}
	if body["sessionId"] == "" || body["type"] != "hls" || !strings.HasPrefix(fmt.Sprint(body["url"]), "/live/") {
		t.Fatalf("play response = %s", rec.Body.String())
	}
	if f.requests("/stream/sport/streamed:one.json") != 2 {
		t.Fatalf("play did not resolve a fresh link: %+v", f.paths)
	}
	// The feed plays through the ordinary Live TV session: the media request
	// carries the addon's referer, and its cookie never leaves the server.
	sess := h.s.LiveTV.Get(body["sessionId"].(string))
	if sess == nil {
		t.Fatal("session missing")
	}
	rec = httptest.NewRecorder()
	sess.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live/"+sess.ID+"/index.m3u8", nil), "index.m3u8")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("feed through the live session = %d %s", rec.Code, rec.Body.String())
	}
	if !f.sawReferer.Load() {
		t.Fatal("the addon's referer never reached the media server")
	}
	if f.leakedCookie.Load() {
		t.Fatal("the addon's credential was forwarded to the media server")
	}
	if !h.s.LiveTV.Stop(sess.ID, "viewer", false) {
		t.Fatal("the sports session could not be stopped")
	}

	if rec, _ := h.do(t, "POST", "/api/livetv/sports/streams/not-a-token/play", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown feed status = %d", rec.Code)
	}
}

func TestLiveSportEmptyCatalogAndUnavailableStreams(t *testing.T) {
	f := newSportAddonFixture(t)
	src := config.LiveTVSource{ID: "highfly", Name: "Sports Streams", Kind: "sport", URL: f.addon.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, src)

	// Nothing on right now is a valid answer, not a manifest error.
	f.setMode("empty")
	rec, body := h.do(t, "GET", "/api/livetv/sports/highfly/catalog/sports_live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty catalog status = %d: %s", rec.Code, rec.Body.String())
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("empty catalog = %s", rec.Body.String())
	}

	// An event whose only entries are placeholders is reported honestly.
	f.setMode("no-streams")
	rec, body = h.do(t, "GET", "/api/livetv/sports/highfly/event/streamed:one", "")
	if rec.Code != http.StatusNotFound || body["code"] != "no_streams" {
		t.Fatalf("no streams = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLiveSportAddonFailureClassification(t *testing.T) {
	f := newSportAddonFixture(t)
	src := config.LiveTVSource{ID: "highfly", Name: "Sports Streams", Kind: "sport", URL: f.addon.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, src)

	// A manifest with no sport catalogs is incompatible, not unreachable.
	f.setMode("tv-only")
	rec, body := h.do(t, "GET", "/api/livetv/sports/highfly", "")
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "incompatible_addon" {
		t.Fatalf("tv-only addon = %d %s", rec.Code, rec.Body.String())
	}

	// Malformed JSON is an invalid manifest, not a network failure.
	f.setMode("bad-manifest")
	rec, body = h.do(t, "GET", "/api/livetv/sports/highfly", "")
	if rec.Code != http.StatusBadGateway || body["code"] != "invalid_manifest" {
		t.Fatalf("malformed manifest = %d %s", rec.Code, rec.Body.String())
	}

	// An addon that is down must be distinguishable from an incompatible one.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	offline := newLiveSportTestServer(t, config.LiveTVSource{
		ID: "off", Name: "Offline", Kind: "sport", URL: closed.URL + "/manifest.json", Enabled: true,
	})
	rec, body = offline.do(t, "GET", "/api/livetv/sports/off", "")
	if rec.Code != http.StatusBadGateway || body["code"] != "addon_unreachable" {
		t.Fatalf("offline addon = %d %s", rec.Code, rec.Body.String())
	}

	// An id that is not a configured sport source never reaches the network.
	rec, _ = h.do(t, "GET", "/api/livetv/sports/unknown", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown source status = %d", rec.Code)
	}

	// A disabled addon is reported as disabled rather than probed.
	disabled := newLiveSportTestServer(t, config.LiveTVSource{
		ID: "off2", Name: "Disabled", Kind: "sport", URL: f.addon.URL + "/manifest.json", Enabled: false,
	})
	rec, _ = disabled.do(t, "GET", "/api/livetv/sports/off2", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("disabled addon status = %d", rec.Code)
	}
}

// The admin Check button must validate a sports addon against its own model.
func TestLiveTVCheckAcceptsSportAddon(t *testing.T) {
	f := newSportAddonFixture(t)
	h := newLiveSportTestServer(t)
	check := func(url string) (*httptest.ResponseRecorder, map[string]any) {
		body := fmt.Sprintf(`{"id":"highfly","name":"Sports","kind":"sport","url":%q,"enabled":true}`, url)
		return h.do(t, "POST", "/api/admin/livetv/check", body)
	}
	rec, body := check(f.addon.URL + "/manifest.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("sport addon check = %d %s", rec.Code, rec.Body.String())
	}
	caps, _ := body["capabilities"].(map[string]any)
	if body["kind"] != "addon" || caps == nil || caps["types"] == nil {
		t.Fatalf("check response = %s", rec.Body.String())
	}
	if body["channels"] != float64(0) || body["catalogs"] != float64(2) || body["browsable"] != true {
		t.Fatalf("check counts = %s", rec.Body.String())
	}
	// The first browsable non-channel catalog is sampled so the admin sees
	// that browsing actually lists events.
	samples, _ := body["samples"].([]any)
	if len(samples) != 2 || samples[0].(map[string]any)["items"] != float64(2) {
		t.Fatalf("check samples = %s", rec.Body.String())
	}

	// The same manifest checked as a conventional Stremio addon is accepted
	// too: acceptance comes from the manifest, not the kind the admin picked.
	payload := `{"id":"x","name":"Sports","kind":"stremio","url":"` + f.addon.URL + `/manifest.json","enabled":true}`
	rec, out := h.do(t, "POST", "/api/admin/livetv/check", payload)
	if rec.Code != http.StatusOK || out["kind"] != "addon" {
		t.Fatalf("sport manifest as stremio = %d %s", rec.Code, rec.Body.String())
	}

	// A TV-channel addon is still a valid addon: the check accepts it and
	// reports its channel model. Only the sports browser, which asked for
	// sport catalogs, says they aren't there.
	f.setMode("tv-only")
	rec, out = check(f.addon.URL + "/manifest.json")
	if rec.Code != http.StatusOK || out["kind"] != "addon" {
		t.Fatalf("tv addon check = %d %s", rec.Code, rec.Body.String())
	}
	unconfigured := newLiveSportTestServer(t, config.LiveTVSource{
		ID: "tvaddon", Name: "TV addon", Kind: "sport", URL: f.addon.URL + "/manifest.json", Enabled: true,
	})
	rec, out = unconfigured.do(t, "GET", "/api/livetv/sports/tvaddon", "")
	if rec.Code != http.StatusUnprocessableEntity || out["code"] != "incompatible_addon" {
		t.Fatalf("tv addon browsed as sport = %d %s", rec.Code, rec.Body.String())
	}

	// A valid addon with no catalogs is accepted and its limitation is clear.
	f.setMode("no-catalogs")
	rec, out = h.do(t, "POST", "/api/admin/livetv/check", payload)
	if rec.Code != http.StatusOK || out["browsable"] != false {
		t.Fatalf("catalog-less addon = %d %s", rec.Code, rec.Body.String())
	}
	if limits, _ := out["limitations"].([]any); len(limits) == 0 {
		t.Fatalf("catalog-less addon has no limitation: %s", rec.Body.String())
	}
}

// A sports addon is a provider in the same picker as IPTV, but it adds no
// channel row and no invented XMLTV guide. An associated guide link is used
// through the ordinary pipeline.
func TestSportSourceStaysOutOfTheChannelPipeline(t *testing.T) {
	cfg := &config.Config{LiveTV: config.LiveTVConfig{
		Enabled: true,
		Sources: []config.LiveTVSource{
			{ID: "iptv", Name: "IPTV", Kind: "m3u", URL: "http://provider.example/list.m3u", Enabled: true},
			{ID: "sports", Name: "Sports", Kind: "sport", URL: "http://addon.example/manifest.json", Enabled: true},
		},
	}}
	svc := livetv.New(cfg, t.TempDir())
	providers := svc.Providers()
	if len(providers) != 2 || providers[0].ID != "iptv" || providers[1].ID != "sports" {
		t.Fatalf("providers = %+v", providers)
	}
	if !providers[1].Addon || providers[1].Channels != 0 {
		t.Fatalf("sports provider = %+v", providers[1])
	}
	// The sport source is still a Live TV source for the viewer.
	sources := svc.SportSources()
	if len(sources) != 1 || sources[0].ID != "sports" || !sources[0].Enabled {
		t.Fatalf("sport sources = %+v", sources)
	}
	// A manifest never supplies XMLTV; an explicit guide link is honored, and
	// without one the source has no guide rather than an invented one.
	if got := livetv.GuideURLs(config.LiveTVSource{Kind: "sport", EPGURL: "http://guide.example/epg.xml"}, nil); len(got) != 1 {
		t.Fatalf("sport source ignored its guide link: %v", got)
	}
	if got := livetv.GuideURLs(config.LiveTVSource{Kind: "sport"}, nil); len(got) != 0 {
		t.Fatalf("sport source invented a guide: %v", got)
	}
	// No playlist has loaded yet; the enabled sport source is what keeps Live
	// TV available while the addon is browsed.
	if !svc.Active() {
		t.Fatal("live TV is inactive despite an enabled sports addon")
	}
}
