package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// addonFixture serves a manifest and the resources a test asks for. Every
// request path is recorded, so a test can prove the addon was asked for the
// resource path the Stremio protocol calls for.
type addonFixture struct {
	t        *testing.T
	server   *httptest.Server
	manifest string

	mu      sync.Mutex
	paths   []string
	handler func(w http.ResponseWriter, path string) bool
}

func newAddonFixture(t *testing.T, manifest string, handler func(w http.ResponseWriter, path string) bool) *addonFixture {
	t.Helper()
	f := &addonFixture{t: t, manifest: manifest, handler: handler}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, f.manifest)
			return
		}
		if f.handler != nil && f.handler(w, r.URL.Path) {
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *addonFixture) source(id, kind string) config.LiveTVSource {
	return config.LiveTVSource{ID: id, Name: id, Kind: kind, URL: f.server.URL + "/manifest.json", Enabled: true}
}

func (f *addonFixture) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *addonFixture) saw(path string) bool {
	for _, got := range f.requests() {
		if got == path {
			return true
		}
	}
	return false
}

const tvAddonManifest = `{"id":"test.tv","types":["tv"],"resources":["catalog","meta","stream"],
	"catalogs":[{"type":"tv","id":"channels","name":"Channels"}]}`

const sportAddonManifest = `{"id":"test.sport","types":["sport"],
	"resources":[{"name":"catalog","types":["sport"]},{"name":"meta","types":["sport"]},{"name":"stream","types":["sport"]}],
	"catalogs":[{"type":"sport","id":"live","name":"Live","extra":[{"name":"search"},{"name":"skip"}]}]}`

const filmAddonManifest = `{"id":"test.films","types":["movie"],
	"resources":[{"name":"catalog","types":["movie"]},{"name":"meta","types":["movie"]},{"name":"stream","types":["movie"]}],
	"catalogs":[{"type":"movie","id":"films","name":"Films"}]}`

// A catalog without a search extra is filtered by the query's words, so a
// provider naming the same title differently is not lost to a phrase mismatch.
func TestAddonCatalogWithoutSearchFiltersByWords(t *testing.T) {
	f := newAddonFixture(t, filmAddonManifest, func(w http.ResponseWriter, path string) bool {
		if path != "/catalog/movie/films.json" {
			return false
		}
		_, _ = io.WriteString(w, `{"metas":[
			{"id":"m1","name":"2001: A Space Odyssey"},
			{"id":"m2","name":"Space Jam"},
			{"id":"m3","name":"An Odyssey Through Space and Time"}
		]}`)
		return true
	})
	svc := New(&config.Config{}, t.TempDir())
	items, err := svc.AddonItems(context.Background(), f.source("films", "stremio"), "movie", "films", "space odyssey", 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, ",") != "m1,m3" {
		t.Fatalf("filtered items = %v", ids)
	}
}

// The recorded sports.highfly manifest is the required regression case: it
// declares sport catalogs only, and it must be accepted, browsable and
// playable without a single hardcoded league or event id.
func TestHighFlyManifestIsAcceptedAsASportAddon(t *testing.T) {
	manifest, err := os.ReadFile("testdata/highfly-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var f *addonFixture
	f = newAddonFixture(t, string(manifest), func(w http.ResponseWriter, path string) bool {
		switch {
		case path == "/catalog/sport/sports_live.json":
			_, _ = io.WriteString(w, `{"metas":[
				{"id":"streamed:one","name":"Alpha vs Beta","genres":["Football"],"releaseInfo":"LIVE"},
				{"id":"leaf:one","name":"Leaf recording","genres":["Hockey"]}]}`)
		case path == "/meta/sport/streamed:one.json":
			_, _ = io.WriteString(w, `{"meta":{"id":"streamed:one","type":"sport","name":"Alpha vs Beta","genres":["Football"]}}`)
		case path == "/stream/sport/streamed:one.json":
			_, _ = io.WriteString(w, `{"streams":[
				{"name":"🔒 Premium","title":"🔒 Upgrade to watch","url":"https://www.google.com"},
				{"name":"Tip: Use an external player","title":"mpv","url":"`+f.server.URL+`/health"},
				{"name":"Leaf (Strong)","title":"1920x1080","url":"https://media.example/live/index.m3u8"}]}`)
		default:
			return false
		}
		return true
	})
	src := f.source("highfly", "stremio") // the CONVENTIONAL addon kind, not "sport"
	s := New(&config.Config{}, t.TempDir())
	ctx := context.Background()

	caps, err := s.AddonCapabilities(ctx, src)
	if err != nil {
		t.Fatalf("the real HighFly manifest was rejected: %v", err)
	}
	if len(caps.Types) != 1 || caps.Types[0] != "sport" {
		t.Fatalf("types = %v", caps.Types)
	}
	if caps.SportCatalogs() != 17 || len(caps.Catalogs) != 17 {
		t.Fatalf("catalogs = %d (%d sport), want 17", len(caps.Catalogs), caps.SportCatalogs())
	}
	if !caps.Browsable || !caps.Playable || caps.HasTVChannels() {
		t.Fatalf("capabilities = %+v", caps)
	}
	if len(caps.Models) != 1 || caps.Models[0].Model != "events" || !caps.Models[0].Stream || !caps.Models[0].Meta {
		t.Fatalf("model = %+v", caps.Models)
	}

	// Acceptance: no channels, no error, and the channel path explains itself.
	if n, _, err := s.Check(ctx, src); err != nil || n != 0 {
		t.Fatalf("check = %d channels, %v; a sport-only addon is valid", n, err)
	}
	if _, err := s.StremioChannels(ctx, src); !errors.Is(err, ErrNoTVChannels) {
		t.Fatalf("channel listing = %v", err)
	}

	// Browsing uses the declared content type and extra names.
	items, err := s.AddonItems(ctx, src, "sport", "sports_live", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "streamed:one" || items[0].Type != "sport" {
		t.Fatalf("items = %+v", items)
	}
	if !f.saw("/catalog/sport/sports_live.json") {
		t.Fatalf("catalog request path = %v", f.requests())
	}

	meta, err := s.AddonMeta(ctx, src, "sport", "streamed:one")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "Alpha vs Beta" || meta.Type != "sport" {
		t.Fatalf("meta = %+v", meta)
	}
	streams, err := s.AddonStreams(ctx, src, "sport", "streamed:one")
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || !strings.Contains(streams[0].URL, "media.example") {
		t.Fatalf("streams = %+v", streams)
	}
	if !f.saw("/stream/sport/streamed:one.json") || !f.saw("/meta/sport/streamed:one.json") {
		t.Fatalf("resource paths = %v", f.requests())
	}
}

// The same rules are applied to every manifest shape: what is usable is
// reported, and what is not becomes a limitation instead of a rejection.
func TestAddonAcceptanceIsCapabilityBased(t *testing.T) {
	cases := []struct {
		name        string
		manifest    string
		browsable   bool
		playable    bool
		tvChannels  bool
		wantLimits  []string
		checkNoErr  bool
		wantCatalog int
	}{
		{name: "tv channels", manifest: tvAddonManifest, browsable: true, playable: true, tvChannels: true, checkNoErr: true, wantCatalog: 1},
		{name: "sport events", manifest: sportAddonManifest, browsable: true, playable: true, checkNoErr: true, wantCatalog: 1},
		{name: "other catalog type", manifest: filmAddonManifest, browsable: true, playable: true, checkNoErr: true, wantCatalog: 1},
		{
			name:       "no catalogs but meta and stream",
			manifest:   `{"id":"test.direct","types":["movie"],"resources":[{"name":"meta","types":["movie"]},{"name":"stream","types":["movie"]}],"catalogs":[]}`,
			playable:   true,
			checkNoErr: true,
			wantLimits: []string{"nothing to browse", "opened by id"},
		},
		{
			name:        "no catalog, meta or stream resources",
			manifest:    `{"id":"test.empty","types":["movie"],"resources":[{"name":"addon_catalog","types":["movie"]}],"catalogs":[{"type":"movie","id":"films","name":"Films"}]}`,
			checkNoErr:  true,
			wantLimits:  []string{"catalog resource", "stream resource"},
			wantCatalog: 1,
		},
		{
			name:        "configuration required",
			manifest:    `{"id":"test.config","name":"Configurable","types":["tv"],"resources":["catalog"],"catalogs":[{"type":"tv","id":"a","name":"A"}],"behaviorHints":{"configurable":true,"configurationRequired":true}}`,
			browsable:   true,
			tvChannels:  true,
			checkNoErr:  true,
			wantLimits:  []string{"must be configured"},
			wantCatalog: 1,
		},
		{
			name:        "required unsupported extra",
			manifest:    `{"id":"test.genre","types":["movie"],"resources":["catalog","stream"],"catalogs":[{"type":"movie","id":"films","name":"Films","extra":[{"name":"genre","isRequired":true}]}]}`,
			playable:    true,
			checkNoErr:  true,
			wantLimits:  []string{"can't supply"},
			wantCatalog: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAddonFixture(t, tc.manifest, nil)
			src := f.source("a", "stremio")
			s := New(&config.Config{}, t.TempDir())
			ctx := context.Background()

			caps, err := s.AddonCapabilities(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			if caps.Browsable != tc.browsable || caps.Playable != tc.playable {
				t.Fatalf("browsable/playable = %v/%v, want %v/%v", caps.Browsable, caps.Playable, tc.browsable, tc.playable)
			}
			if caps.HasTVChannels() != tc.tvChannels {
				t.Fatalf("tv channels = %v", caps.HasTVChannels())
			}
			if len(caps.Catalogs) != tc.wantCatalog {
				t.Fatalf("catalogs = %+v, want %d", caps.Catalogs, tc.wantCatalog)
			}
			joined := strings.Join(caps.Limitations, " | ")
			for _, want := range tc.wantLimits {
				if !strings.Contains(joined, want) {
					t.Fatalf("limitations = %q, want %q", joined, want)
				}
			}
			if tc.checkNoErr {
				if _, _, err := s.Check(ctx, src); err != nil {
					t.Fatalf("a valid manifest was rejected by Check: %v", err)
				}
			}
		})
	}
}

// A catalog that only answers a search stays searchable; it is not offered as
// a browsable shelf and is not treated as broken.
func TestAddonSearchOnlyCatalog(t *testing.T) {
	f := newAddonFixture(t, `{"id":"test.search","types":["movie"],"resources":["catalog","stream"],
		"catalogs":[{"type":"movie","id":"films","name":"Films","extra":[{"name":"search","isRequired":true}]}]}`,
		func(w http.ResponseWriter, path string) bool {
			if path == "/catalog/movie/films/search=alpha.json" {
				_, _ = io.WriteString(w, `{"metas":[{"id":"film:one","name":"Alpha"}]}`)
				return true
			}
			return false
		})
	s := New(&config.Config{}, t.TempDir())
	src := f.source("a", "stremio")
	ctx := context.Background()
	caps, err := s.AddonCapabilities(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.Catalogs) != 1 || caps.Catalogs[0].Browsable || !caps.Catalogs[0].Searchable {
		t.Fatalf("catalog = %+v", caps.Catalogs)
	}
	// The addon still has something to browse (with a query), but the shelf
	// itself is not offered as a plain list.
	if !caps.Browsable {
		t.Fatal("a search-only catalog left the addon with nothing to browse")
	}
	// Without a query the request is refused locally rather than sent as a
	// request the addon cannot answer.
	if _, err := s.AddonItems(ctx, src, "movie", "films", "", 0); !errors.Is(err, ErrCatalogUnsupported) {
		t.Fatalf("search-only catalog without a query = %v", err)
	}
	items, err := s.AddonItems(ctx, src, "movie", "films", "alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "film:one" {
		t.Fatalf("items = %+v", items)
	}
}

// A catalog that requires an extra Wren TV cannot supply is reported as
// unsupported, and the browse request says so rather than sending a bad extra.
func TestAddonUnsupportedCatalogAndUnknownCatalog(t *testing.T) {
	f := newAddonFixture(t, `{"id":"test.genre","types":["movie"],"resources":["catalog"],
		"catalogs":[{"type":"movie","id":"films","name":"Films","extra":[{"name":"country","isRequired":true}]}]}`, nil)
	s := New(&config.Config{}, t.TempDir())
	src := f.source("a", "stremio")
	if _, err := s.AddonItems(context.Background(), src, "movie", "films", "", 0); !errors.Is(err, ErrCatalogUnsupported) {
		t.Fatalf("unsupported catalog = %v", err)
	}
	if _, err := s.AddonItems(context.Background(), src, "movie", "nope", "", 0); !errors.Is(err, ErrUnknownCatalog) {
		t.Fatalf("unknown catalog = %v", err)
	}
	// Neither refusal reached a catalog path: an unsupported catalog and an
	// unknown one are both answered locally. The manifest itself is read once
	// and reused, however many catalog requests follow.
	for _, path := range f.requests() {
		if path != "/manifest.json" {
			t.Fatalf("an unsupported or unknown catalog was requested: %v", f.requests())
		}
	}
}

// An item with no playable stream is a normal, honest answer, and a type with
// no stream resource is reported as unsupported rather than as empty.
func TestAddonMissingStreamsAndUnsupportedResources(t *testing.T) {
	f := newAddonFixture(t, filmAddonManifest, func(w http.ResponseWriter, path string) bool {
		switch path {
		case "/stream/movie/movie:paid.json":
			_, _ = io.WriteString(w, `{"streams":[{"name":"Premium","title":"🔒 Upgrade to watch","url":"https://www.google.com"}]}`)
			return true
		case "/stream/movie/movie:none.json":
			_, _ = io.WriteString(w, `{"streams":[]}`)
			return true
		}
		return false
	})
	s := New(&config.Config{}, t.TempDir())
	src := f.source("a", "stremio")
	ctx := context.Background()
	for _, id := range []string{"movie:paid", "movie:none"} {
		if _, err := s.AddonStreams(ctx, src, "movie", id); !errors.Is(err, ErrNoStreams) {
			t.Fatalf("%s = %v, want ErrNoStreams", id, err)
		}
	}
	// The manifest has no stream resource for tv: that is a different answer
	// from "this item has no stream".
	if _, err := s.AddonStreams(ctx, src, "tv", "one"); !errors.Is(err, ErrUnsupportedResource) {
		t.Fatalf("unsupported type = %v", err)
	}
	if _, err := s.AddonMeta(ctx, src, "tv", "one"); !errors.Is(err, ErrUnsupportedResource) {
		t.Fatalf("unsupported meta = %v", err)
	}
}

// A manifest that isn't Stremio JSON is invalid; an empty JSON object is not a
// manifest either; neither is confused with a network failure.
func TestAddonManifestValidation(t *testing.T) {
	cases := map[string]string{
		"html":      `<html><body>Not found</body></html>`,
		"empty":     `{}`,
		"array":     `[]`,
		"truncated": `{not json`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			s := New(&config.Config{}, t.TempDir())
			src := config.LiveTVSource{ID: "bad", Kind: "stremio", URL: server.URL + "/manifest.json", Enabled: true}
			if _, err := s.AddonCapabilities(context.Background(), src); !errors.Is(err, ErrAddonInvalidResponse) {
				t.Fatalf("%s = %v, want ErrAddonInvalidResponse", name, err)
			}
		})
	}
	// A closed server is unreachable, never invalid.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	src := config.LiveTVSource{ID: "off", Kind: "stremio", URL: closed.URL + "/manifest.json", Enabled: true}
	s := New(&config.Config{}, t.TempDir())
	if _, err := s.AddonCapabilities(context.Background(), src); !errors.Is(err, ErrAddonUnreachable) {
		t.Fatalf("offline = %v, want ErrAddonUnreachable", err)
	}
}

// A refresh imports TV channels from an addon that has them, and keeps a
// catalog-only addon valid beside it: no error, no channel rows, capabilities
// reported for the settings page.
func TestAddonRefreshKeepsBothContentModels(t *testing.T) {
	tv := newAddonFixture(t, tvAddonManifest, func(w http.ResponseWriter, path string) bool {
		if path == "/catalog/tv/channels.json" {
			_, _ = io.WriteString(w, `{"metas":[{"id":"ch:one","name":"Channel One","genres":["News"]}]}`)
			return true
		}
		return false
	})
	sport := newAddonFixture(t, sportAddonManifest, func(w http.ResponseWriter, path string) bool {
		if path == "/catalog/sport/live.json" {
			_, _ = io.WriteString(w, `{"metas":[{"id":"e1","name":"Final","genres":["Football"]}]}`)
			return true
		}
		return false
	})
	cfg := &config.Config{LiveTV: config.LiveTVConfig{Enabled: true, Sources: []config.LiveTVSource{
		tv.source("tvaddon", "stremio"),
		sport.source("sportaddon", "sport"),
	}}}
	s := New(cfg, t.TempDir())
	s.refresh(context.Background(), true)

	if got := len(s.Channels()); got != 1 {
		t.Fatalf("channels = %d, want the tv addon's channel", got)
	}
	// Both sources are in the one picker: a manifest is a provider whether it
	// publishes channels, events or both.
	providers := s.Providers()
	if len(providers) != 2 || providers[0].ID != "tvaddon" || providers[1].ID != "sportaddon" {
		t.Fatalf("providers = %+v", providers)
	}
	if !providers[1].Addon || providers[1].Channels != 0 || providers[1].Events != 1 {
		t.Fatalf("sport provider = %+v", providers[1])
	}
	st := s.Status()
	if len(st.Sources) != 2 {
		t.Fatalf("statuses = %+v", st.Sources)
	}
	if st.Sources[0].Capabilities == nil || !st.Sources[0].Capabilities.HasTVChannels() || st.Sources[0].Error != "" {
		t.Fatalf("tv addon status = %+v", st.Sources[0])
	}
	sportStatus := st.Sources[1]
	if sportStatus.Error != "" || sportStatus.Note == "" || sportStatus.Channels != 0 || sportStatus.Events != 1 {
		t.Fatalf("sport addon status = %+v, want a note, one event and no error", sportStatus)
	}
	if sportStatus.Capabilities == nil || !sportStatus.Capabilities.HasBrowsableModel() {
		t.Fatalf("sport capabilities = %+v", sportStatus.Capabilities)
	}
	if st.AddonSources != 1 || st.SportSources != 1 {
		t.Fatalf("status counts = addon %d, sport %d", st.AddonSources, st.SportSources)
	}
	if !s.Active() {
		t.Fatal("live TV is inactive")
	}
	// The addon without channels created no playlist or guide row.
	if got := s.ChannelsFor("sportaddon"); len(got) != 0 {
		t.Fatalf("sport addon channels = %+v", got)
	}
	if got := s.ScheduleFor(6, "sportaddon"); len(got) != 0 {
		t.Fatalf("sport addon guide = %+v", got)
	}
	// Its catalog item is a Live TV entry instead of a channel row.
	if got := s.EventsFor("sportaddon"); len(got) != 1 || got[0].Name != "Final" {
		t.Fatalf("sport addon events = %+v", got)
	}
}

// Two addons can publish the same item id. Their streams, capabilities and
// content types stay separate.
func TestAddonProvidersKeepOverlappingIDsSeparate(t *testing.T) {
	makeAddon := func(name, mediaURL string) *addonFixture {
		manifest := fmt.Sprintf(`{"id":%q,"types":["sport","movie"],
			"resources":[{"name":"catalog","types":["sport","movie"]},{"name":"stream","types":["sport","movie"]}],
			"catalogs":[{"type":"sport","id":"live","name":"Live"},{"type":"movie","id":"films","name":"Films"}]}`, name)
		return newAddonFixture(t, manifest, func(w http.ResponseWriter, path string) bool {
			if strings.HasPrefix(path, "/stream/") && strings.Contains(path, "shared:one") {
				_, _ = fmt.Fprintf(w, `{"streams":[{"name":%q,"url":%q}]}`, name, mediaURL)
				return true
			}
			return false
		})
	}
	a := makeAddon("Addon A", "https://a.example/live.m3u8")
	b := makeAddon("Addon B", "https://b.example/live.m3u8")
	s := New(&config.Config{}, t.TempDir())
	ctx := context.Background()
	for _, tc := range []struct {
		fixture *addonFixture
		id      string
		name    string
		want    string
	}{{a, "a", "Addon A", "a.example"}, {b, "b", "Addon B", "b.example"}} {
		streams, err := s.AddonStreams(ctx, tc.fixture.source(tc.id, "sport"), "sport", "shared:one")
		if err != nil {
			t.Fatal(err)
		}
		if len(streams) != 1 || !strings.Contains(streams[0].URL, tc.want) {
			t.Fatalf("addon %s streams = %+v", tc.id, streams)
		}
		caps, err := s.AddonCapabilities(ctx, tc.fixture.source(tc.id, "sport"))
		if err != nil {
			t.Fatal(err)
		}
		if caps.ID != tc.name || len(caps.Models) != 2 {
			t.Fatalf("addon %s capabilities = %+v", tc.id, caps)
		}
	}
}
