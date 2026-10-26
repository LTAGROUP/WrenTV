package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/livetv"
)

// A generic addon fixture: any content type, its own media server, and a
// manifest that says exactly what it serves.
func genericAddon(t *testing.T, manifest string, media map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			_, _ = io.WriteString(w, manifest)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/stream/") {
			_, _ = fmt.Fprintf(w, `{"streams":[{"name":"Feed","title":"1080p","url":%q}]}`, media[r.URL.Path])
			return
		}
		if strings.HasPrefix(r.URL.Path, "/meta/") {
			_, _ = fmt.Fprintf(w, `{"meta":{"id":%q,"type":"movie","name":"A Film","description":"Details"}}`, "film:one")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/catalog/movie/films") {
			_, _ = io.WriteString(w, `{"metas":[{"id":"film:one","name":"A Film","genres":["Drama"],"releaseInfo":"2026"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &paths
}

// Browse an addon whose only catalogs are films: the generic path offers the
// catalog, item metadata and opaque feeds, and plays them through the ordinary
// Live TV session.
func TestLiveAddonBrowsesAnyCatalogType(t *testing.T) {
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
	}))
	defer media.Close()
	addon, paths := genericAddon(t, `{"id":"test.films","types":["movie"],
		"resources":[{"name":"catalog","types":["movie"]},{"name":"meta","types":["movie"]},{"name":"stream","types":["movie"]}],
		"catalogs":[{"type":"movie","id":"films","name":"Films"}]}`,
		map[string]string{"/stream/movie/film:one.json": media.URL + "/film.m3u8"})
	src := config.LiveTVSource{ID: "films", Name: "Films addon", Kind: "stremio", URL: addon.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, src)

	// The source describes its catalogs and capabilities.
	rec, body := h.do(t, "GET", "/api/livetv/addons/films", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("source = %d %s", rec.Code, rec.Body.String())
	}
	catalogs, _ := body["catalogs"].([]any)
	if len(catalogs) != 1 || catalogs[0].(map[string]any)["type"] != "movie" {
		t.Fatalf("catalogs = %s", rec.Body.String())
	}
	caps, _ := body["capabilities"].(map[string]any)
	if caps["browsable"] != true || caps["playable"] != true {
		t.Fatalf("capabilities = %s", rec.Body.String())
	}

	// The catalog is requested under its declared content type.
	rec, body = h.do(t, "GET", "/api/livetv/addons/films/catalog/movie/films", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["type"] != "movie" {
		t.Fatalf("items = %s", rec.Body.String())
	}
	sawCatalog := false
	for _, p := range *paths {
		if p == "/catalog/movie/films.json" {
			sawCatalog = true
		}
	}
	if !sawCatalog {
		t.Fatalf("catalog path = %v", *paths)
	}

	// The item is opened with its type, and the feed is opaque.
	rec, body = h.do(t, "GET", "/api/livetv/addons/films/item/movie/film:one", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("item = %d %s", rec.Code, rec.Body.String())
	}
	if item, _ := body["item"].(map[string]any); item["name"] != "A Film" || item["type"] != "movie" {
		t.Fatalf("item = %s", rec.Body.String())
	}
	streams, _ := body["streams"].([]any)
	if len(streams) != 1 || streams[0].(map[string]any)["id"] == "" {
		t.Fatalf("streams = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), media.URL) {
		t.Fatalf("the provider URL leaked: %s", rec.Body.String())
	}

	// Playing uses the generic addon feed route and the same session shape.
	rec, body = h.do(t, "POST", "/api/livetv/addons/streams/"+streams[0].(map[string]any)["id"].(string)+"/play", `{"hls":true}`)
	if rec.Code != http.StatusOK || body["sessionId"] == "" {
		t.Fatalf("play = %d %s", rec.Code, rec.Body.String())
	}
	sess := h.s.LiveTV.Get(body["sessionId"].(string))
	if sess == nil {
		t.Fatal("session missing")
	}
	proxied := httptest.NewRecorder()
	sess.ServeHTTP(proxied, httptest.NewRequest(http.MethodGet, "/live/"+sess.ID+"/index.m3u8", nil), "index.m3u8")
	if proxied.Code != http.StatusOK || !strings.Contains(proxied.Body.String(), "#EXTM3U") {
		t.Fatalf("playlist through the session = %d %s", proxied.Code, proxied.Body.String())
	}
	h.s.LiveTV.Stop(sess.ID, "viewer", false)
}

// A valid addon with no catalogs is listed and checked successfully, with its
// limitations shown; it is never called an invalid manifest.
func TestLiveAddonWithoutCatalogsIsValidButNotBrowsable(t *testing.T) {
	addon, _ := genericAddon(t, `{"id":"test.direct","types":["movie"],
		"resources":[{"name":"meta","types":["movie"]},{"name":"stream","types":["movie"]}],"catalogs":[]}`,
		map[string]string{"/stream/movie/x.json": "https://media.example/x.m3u8"})
	src := config.LiveTVSource{ID: "direct", Name: "Direct", Kind: "stremio", URL: addon.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, src)

	rec, body := h.do(t, "POST", "/api/admin/livetv/check", `{"id":"direct","name":"Direct","kind":"stremio","url":"`+addon.URL+`/manifest.json","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("check = %d %s", rec.Code, rec.Body.String())
	}
	if body["browsable"] != false || body["playable"] != true || body["channels"] != float64(0) {
		t.Fatalf("check = %s", rec.Body.String())
	}
	limits, _ := body["limitations"].([]any)
	joined := fmt.Sprint(limits...)
	if !strings.Contains(joined, "nothing to browse") || !strings.Contains(joined, "opened by id") {
		t.Fatalf("limitations = %s", rec.Body.String())
	}

	// Opening the source is a successful answer with an empty catalog list and
	// the same limitations, not an error.
	rec, body = h.do(t, "GET", "/api/livetv/addons/direct", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("source = %d %s", rec.Code, rec.Body.String())
	}
	if catalogs, ok := body["catalogs"].([]any); !ok || len(catalogs) != 0 {
		t.Fatalf("catalogs = %s", rec.Body.String())
	}
	if caps, _ := body["capabilities"].(map[string]any); caps["browsable"] != false {
		t.Fatalf("capabilities = %s", rec.Body.String())
	}
}

// Unsupported resources, missing streams and unknown catalogs are separate
// answers, and malformed or unreachable addons are separate again.
func TestLiveAddonErrorClassification(t *testing.T) {
	addon, _ := genericAddon(t, `{"id":"test.meta","types":["movie"],
		"resources":[{"name":"catalog","types":["movie"]},{"name":"meta","types":["movie"]}],
		"catalogs":[{"type":"movie","id":"films","name":"Films"}]}`,
		map[string]string{})
	src := config.LiveTVSource{ID: "meta", Name: "Meta only", Kind: "stremio", URL: addon.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, src)

	// The manifest has no stream resource: that is an unsupported resource,
	// not "no streams right now".
	rec, body := h.do(t, "GET", "/api/livetv/addons/meta/item/movie/film:one", "")
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "unsupported_resource" {
		t.Fatalf("unsupported resource = %d %s", rec.Code, rec.Body.String())
	}
	rec, body = h.do(t, "GET", "/api/livetv/addons/meta/catalog/movie/nope", "")
	if rec.Code != http.StatusNotFound || body["code"] != "unknown_catalog" {
		t.Fatalf("unknown catalog = %d %s", rec.Code, rec.Body.String())
	}
	rec, body = h.do(t, "GET", "/api/livetv/addons/meta/catalog/tv/films", "")
	if rec.Code != http.StatusNotFound || body["code"] != "unknown_catalog" {
		t.Fatalf("catalog for an unserved type = %d %s", rec.Code, rec.Body.String())
	}

	// A catalog that exists but answers with placeholders is "no streams".
	noStreams, _ := genericAddon(t, `{"id":"test.placeholder","types":["movie"],
		"resources":["catalog","stream"],"catalogs":[{"type":"movie","id":"films","name":"Films"}]}`,
		map[string]string{"/stream/movie/film:one.json": "https://www.google.com"})
	placeholder := config.LiveTVSource{ID: "ph", Name: "Placeholder", Kind: "stremio", URL: noStreams.URL + "/manifest.json", Enabled: true}
	ph := newLiveSportTestServer(t, placeholder)
	rec, body = ph.do(t, "GET", "/api/livetv/addons/ph/item/movie/film:one", "")
	if rec.Code != http.StatusNotFound || body["code"] != "no_streams" {
		t.Fatalf("no streams = %d %s", rec.Code, rec.Body.String())
	}

	// A manifest that is not Stremio JSON is an invalid manifest.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>nope</html>")
	}))
	defer bad.Close()
	badSrc := config.LiveTVSource{ID: "bad", Name: "Bad", Kind: "stremio", URL: bad.URL + "/manifest.json", Enabled: true}
	bh := newLiveSportTestServer(t, badSrc)
	rec, body = bh.do(t, "GET", "/api/livetv/addons/bad", "")
	if rec.Code != http.StatusBadGateway || body["code"] != "invalid_manifest" {
		t.Fatalf("invalid manifest = %d %s", rec.Code, rec.Body.String())
	}

	// An addon that is down is unreachable, not invalid.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	offSrc := config.LiveTVSource{ID: "off", Name: "Off", Kind: "stremio", URL: closed.URL + "/manifest.json", Enabled: true}
	oh := newLiveSportTestServer(t, offSrc)
	rec, body = oh.do(t, "GET", "/api/livetv/addons/off", "")
	if rec.Code != http.StatusBadGateway || body["code"] != "addon_unreachable" {
		t.Fatalf("unreachable = %d %s", rec.Code, rec.Body.String())
	}
}

// Two addons publishing the same item id keep separate feeds: a token resolves
// only against the addon that issued it.
func TestLiveAddonOverlappingIDsStayIsolated(t *testing.T) {
	mediaA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
	}))
	defer mediaA.Close()
	mediaB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
	}))
	defer mediaB.Close()
	manifest := `{"id":"test.%s","types":["sport"],
		"resources":[{"name":"catalog","types":["sport"]},{"name":"stream","types":["sport"]}],
		"catalogs":[{"type":"sport","id":"live","name":"Live"}]}`
	addonA, pathsA := genericAddon(t, fmt.Sprintf(manifest, "a"), map[string]string{"/stream/sport/shared:one.json": mediaA.URL + "/a.m3u8"})
	addonB, pathsB := genericAddon(t, fmt.Sprintf(manifest, "b"), map[string]string{"/stream/sport/shared:one.json": mediaB.URL + "/b.m3u8"})
	srcA := config.LiveTVSource{ID: "a", Name: "Addon A", Kind: "sport", URL: addonA.URL + "/manifest.json", Enabled: true}
	srcB := config.LiveTVSource{ID: "b", Name: "Addon B", Kind: "sport", URL: addonB.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, srcA, srcB)

	open := func(source string) string {
		t.Helper()
		rec, body := h.do(t, "GET", "/api/livetv/addons/"+source+"/item/sport/shared:one", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s item = %d %s", source, rec.Code, rec.Body.String())
		}
		streams, _ := body["streams"].([]any)
		if len(streams) != 1 {
			t.Fatalf("%s streams = %s", source, rec.Body.String())
		}
		return streams[0].(map[string]any)["id"].(string)
	}
	tokenA, tokenB := open("a"), open("b")
	if tokenA == tokenB {
		t.Fatal("two addons issued the same feed token")
	}
	for _, tc := range []struct{ token, want string }{{tokenA, "/a.m3u8"}, {tokenB, "/b.m3u8"}} {
		rec, body := h.do(t, "POST", "/api/livetv/addons/streams/"+tc.token+"/play", `{"hls":false}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("play = %d %s", rec.Code, rec.Body.String())
		}
		sess := h.s.LiveTV.Get(body["sessionId"].(string))
		if sess == nil {
			t.Fatal("session missing")
		}
		out := httptest.NewRecorder()
		sess.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/live/"+sess.ID+"/index.m3u8", nil), "index.m3u8")
		if out.Code != http.StatusOK {
			t.Fatalf("proxy = %d", out.Code)
		}
		h.s.LiveTV.Stop(sess.ID, "viewer", false)
	}
	// Each addon was asked for its own stream, never the other's.
	aAsked, bAsked := false, false
	for _, p := range *pathsA {
		aAsked = aAsked || p == "/stream/sport/shared:one.json"
	}
	for _, p := range *pathsB {
		bAsked = bAsked || p == "/stream/sport/shared:one.json"
	}
	if !aAsked || !bAsked {
		t.Fatalf("stream paths = %v / %v", *pathsA, *pathsB)
	}
}

// Saving settings discovers an addon's capabilities and persists them, so a
// restart describes the addon without asking it again.
func TestLiveAddonCapabilitiesPersistAcrossSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	addon, _ := genericAddon(t, `{"id":"test.sport","types":["sport"],
		"resources":[{"name":"catalog","types":["sport"]},{"name":"stream","types":["sport"]}],
		"catalogs":[{"type":"sport","id":"live","name":"Live"}]}`, map[string]string{})
	server := &Server{Cfg: cfg, LiveTV: livetv.New(cfg, dir)}
	sources := []config.LiveTVSource{{
		ID: "sport", Name: "Sport", Kind: "sport", URL: addon.URL + "/manifest.json", Enabled: true,
		// A client-supplied claim is never trusted: the server discovers the
		// manifest and stores what it found.
		Capabilities: &config.AddonCapabilities{ID: "spoofed", Playable: true},
	}}
	if _, _, err := server.applyLiveTV(context.Background(), &liveTVPatch{Sources: &sources}); err != nil {
		t.Fatal(err)
	}
	saved := cfg.LiveTV.Sources[0]
	if saved.Capabilities == nil || saved.Capabilities.SportCatalogs() != 1 || !saved.Capabilities.Browsable {
		t.Fatalf("capabilities were not discovered on save: %+v", saved.Capabilities)
	}
	if saved.Capabilities.ID == "spoofed" {
		t.Fatal("client-supplied capabilities were stored instead of discovered ones")
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.LiveTV.Sources) != 1 {
		t.Fatalf("reloaded sources = %+v", reloaded.LiveTV.Sources)
	}
	caps := reloaded.LiveTV.Sources[0].Capabilities
	if caps == nil {
		t.Fatal("capabilities did not survive a reload")
	}
	if caps.ID != "test.sport" || caps.SportCatalogs() != 1 || len(caps.Models) != 1 || !caps.Playable {
		t.Fatalf("reloaded capabilities = %+v", caps)
	}
	// They are also what the settings page and the Live TV picker read.
	view := server.liveTVView()
	body, _ := json.Marshal(view)
	if !strings.Contains(string(body), "test.sport") {
		t.Fatalf("settings view lost the capabilities: %s", body)
	}
}

// A server can mix conventional IPTV providers with Stremio addons. They share
// one source list, but stay different kinds: a provider keeps its channels and
// guide rows, while a sport-only addon is browsable and never turns its events
// into channels, XMLTV programmes, or a provider row with an empty channel
// count.
func TestMixedIPTVAndAddonSourcesStaySeparateKinds(t *testing.T) {
	playlist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"news\" group-title=\"News\",News One\nhttp://provider.example/news.m3u8\n")
	}))
	defer playlist.Close()
	f := newSportAddonFixture(t)
	sources := []config.LiveTVSource{
		{ID: "iptv", Name: "Main IPTV", Kind: "m3u", URL: playlist.URL, Enabled: true},
		// HighFly is saved as an ordinary Stremio addon, like an admin would
		// paste it. The manifest, not the kind that was picked, decides that it
		// lists sport and no channels.
		{ID: "highfly", Name: "HighFly", Kind: "stremio", URL: f.addon.URL + "/manifest.json", Enabled: true},
	}
	h := newLiveSportTestServer(t, sources...)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.s.LiveTV.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for len(h.s.LiveTV.Channels()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// The channel endpoint is the picker's source list, and every configured
	// source is in it: the addon is a provider too, with its own counts.
	rec, _ := h.do(t, "GET", "/api/livetv/channels", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("channels = %d %s", rec.Code, rec.Body.String())
	}
	var view struct {
		Channels  []livetv.ChannelView `json:"channels"`
		Providers []livetv.Provider    `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Channels) != 1 || view.Channels[0].Source != "iptv" || view.Channels[0].Name != "News One" {
		t.Fatalf("channels = %s", rec.Body.String())
	}
	if len(view.Providers) != 2 {
		t.Fatalf("providers = %s, want both configured sources", rec.Body.String())
	}
	p := view.Providers[0]
	if p.ID != "iptv" || p.Kind != "m3u" || p.Addon || !p.Enabled || p.Channels != 1 {
		t.Fatalf("provider = %+v", p)
	}
	addonProvider := view.Providers[1]
	if addonProvider.ID != "highfly" || !addonProvider.Addon || addonProvider.Channels != 0 {
		t.Fatalf("addon provider = %+v", addonProvider)
	}

	// The addon is a source in the same picker, described by its manifest.
	rec, body := h.do(t, "GET", "/api/livetv/addons", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("addons = %d %s", rec.Code, rec.Body.String())
	}
	addons, _ := body["sources"].([]any)
	if len(addons) != 1 {
		t.Fatalf("addon sources = %s", rec.Body.String())
	}
	addonView, _ := addons[0].(map[string]any)
	if addonView["id"] != "highfly" || addonView["name"] != "HighFly" || addonView["enabled"] != true {
		t.Fatalf("addon source = %s", rec.Body.String())
	}

	// The guide covers channels only: the addon contributes no programme row.
	rec, body = h.do(t, "GET", "/api/livetv/guide?hours=6", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("guide = %d %s", rec.Code, rec.Body.String())
	}
	guideRows, _ := body["channels"].([]any)
	if len(guideRows) != 1 || guideRows[0].(map[string]any)["id"] != view.Channels[0].ID {
		t.Fatalf("guide = %s", rec.Body.String())
	}

	// Selecting each source keeps them apart: the addon id finds no channels,
	// and the provider id still finds its own.
	rec, body = h.do(t, "GET", "/api/livetv/channels?source=highfly", "")
	if filtered, _ := body["channels"].([]any); len(filtered) != 0 {
		t.Fatalf("addon source returned channels: %s", rec.Body.String())
	}
	rec, body = h.do(t, "GET", "/api/livetv/channels?source=iptv", "")
	if filtered, _ := body["channels"].([]any); len(filtered) != 1 {
		t.Fatalf("provider source lost its channel: %s", rec.Body.String())
	}

	// The addon is still browsable: its sport catalogs list the events the
	// picker opens, with playable feeds behind their own ids.
	rec, body = h.do(t, "GET", "/api/livetv/addons/highfly/catalog/sport/sports_live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	items, _ := body["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("catalog items = %s", rec.Body.String())
	}
	for _, item := range items {
		meta, _ := item.(map[string]any)
		if meta["type"] != "sport" {
			t.Fatalf("catalog item = %+v, want sport events", meta)
		}
	}
}
