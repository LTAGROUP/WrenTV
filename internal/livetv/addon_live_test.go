package livetv

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

func refreshAddon(t *testing.T, src config.LiveTVSource) *Service {
	t.Helper()
	cfg := &config.Config{LiveTV: config.LiveTVConfig{Enabled: true, Sources: []config.LiveTVSource{src}}}
	s := New(cfg, t.TempDir())
	s.refresh(context.Background(), true)
	return s
}

// An addon's event catalogs are Live TV entries in the ordinary list. They are
// not channels: nothing is numbered, nothing enters the playlist, and only
// items the manifest can actually list and play are offered.
func TestAddonEventCatalogsBecomeLiveEntries(t *testing.T) {
	manifest := `{"id":"test.events","types":["sport","movie"],"resources":[
		{"name":"catalog","types":["sport","movie"]},
		{"name":"stream","types":["sport"]}],
		"catalogs":[
			{"type":"sport","id":"live","name":"Live Now"},
			{"type":"sport","id":"search","name":"Search","extra":[{"name":"search","isRequired":true}]},
			{"type":"movie","id":"films","name":"Films"}]}`
	f := newAddonFixture(t, manifest, func(w http.ResponseWriter, path string) bool {
		switch path {
		case "/catalog/sport/live.json":
			_, _ = io.WriteString(w, `{"metas":[
				{"id":"","name":"no identity"},
				{"id":"streamed:one","name":"Alpha vs Beta","genres":["Football"],"description":"League opener","releaseInfo":"LIVE","poster":"http://img/one.webp"},
				{"id":"streamed:one","name":"Alpha vs Beta duplicate","genres":["Football"]},
				{"id":"streamed:two","name":"Gamma vs Delta","genres":["Tennis"]}]}`)
			return true
		}
		return false
	})
	s := refreshAddon(t, f.source("highfly", "sport"))

	events := s.EventsFor("")
	if len(events) != 2 {
		t.Fatalf("events = %+v (errors %v, requests %v)", events, s.Status().Errors, f.requests())
	}
	first := events[0]
	if first.Source != "highfly" || first.SourceName != "highfly" || first.Type != "sport" {
		t.Fatalf("event identity = %+v", first)
	}
	if first.ItemID != "streamed:one" || first.Name != "Alpha vs Beta" || first.Group != "Football" {
		t.Fatalf("event = %+v", first)
	}
	if first.Logo != "http://img/one.webp" || first.Description != "League opener" || first.ReleaseInfo != "LIVE" {
		t.Fatalf("event metadata = %+v", first)
	}
	if events[1].ItemID != "streamed:two" || events[1].Group != "Tennis" {
		t.Fatalf("second event = %+v", events[1])
	}
	// Events are not channels: no playlist row, no number and no guide target.
	if chans := s.Channels(); len(chans) != 0 {
		t.Fatalf("events invented channels: %+v", chans)
	}
	// A search-only shelf cannot be listed without a query Wren TV never
	// chooses, and a film catalog is not Live TV: neither is requested.
	if f.saw("/catalog/sport/search.json") {
		t.Fatalf("search-only catalog was listed: %v", f.requests())
	}
	if f.saw("/catalog/movie/films.json") {
		t.Fatalf("a non-event catalog was read as Live TV: %v", f.requests())
	}
	// The picker has one entry for the addon, with its event count.
	providers := s.Providers()
	if len(providers) != 1 || !providers[0].Addon || providers[0].Events != 2 || providers[0].Channels != 0 {
		t.Fatalf("providers = %+v", providers)
	}
	if !s.Active() {
		t.Fatal("an addon with events but no channels left Live TV inactive")
	}
	// Filtering by the provider is the same value a channel uses.
	if got := s.EventsFor("highfly"); len(got) != 2 {
		t.Fatalf("events for source = %d", len(got))
	}
	if got := s.EventsFor("other"); len(got) != 0 {
		t.Fatalf("another source picked up the events: %+v", got)
	}
}

// A catalog whose content type has no stream resource is not playable, so it is
// reported as a capability limitation rather than offered as an event.
func TestAddonEventsRequireAPlayableType(t *testing.T) {
	manifest := `{"id":"test.metaonly","types":["sport"],"resources":[
		{"name":"catalog","types":["sport"]},{"name":"meta","types":["sport"]}],
		"catalogs":[{"type":"sport","id":"live","name":"Live"}]}`
	f := newAddonFixture(t, manifest, func(w http.ResponseWriter, path string) bool {
		if path == "/catalog/sport/live.json" {
			_, _ = io.WriteString(w, `{"metas":[{"id":"e1","name":"Unplayable"}]}`)
			return true
		}
		return false
	})
	s := refreshAddon(t, f.source("meta", "stremio"))

	if events := s.EventsFor(""); len(events) != 0 {
		t.Fatalf("an unplayable item was offered: %+v", events)
	}
	if f.saw("/catalog/sport/live.json") {
		t.Fatal("an unplayable catalog was fetched")
	}
	caps := s.Status().Sources[0].Capabilities
	if caps == nil || !strings.Contains(strings.Join(caps.Limitations, " "), "doesn't implement the stream resource") {
		t.Fatalf("limitations = %+v", caps)
	}
}

// The recorded HighFly manifest is the real event catalog case: every shelf it
// declares is read into the ordinary Live TV list, and an item appearing on two
// shelves stays one selectable entry.
func TestHighFlyEventsEnterTheLiveTVList(t *testing.T) {
	manifest, err := os.ReadFile("testdata/highfly-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	f := newAddonFixture(t, string(manifest), func(w http.ResponseWriter, path string) bool {
		if !strings.HasPrefix(path, "/catalog/sport/") || !strings.HasSuffix(path, ".json") {
			return false
		}
		_, _ = io.WriteString(w, `{"metas":[
			{"id":"streamed:one","name":"Alpha vs Beta","genres":["Football"],"releaseInfo":"LIVE"},
			{"id":"streamed:two","name":"Gamma vs Delta","genres":["Tennis"],"releaseInfo":"UPCOMING"}]}`)
		return true
	})
	s := refreshAddon(t, f.source("highfly", "sport"))

	events := s.EventsFor("highfly")
	if len(events) != 2 {
		t.Fatalf("highfly events = %+v", events)
	}
	if events[0].Source != "highfly" || events[0].Group != "Football" || events[0].ReleaseInfo != "LIVE" {
		t.Fatalf("first event = %+v", events[0])
	}
	if len(s.Channels()) != 0 {
		t.Fatalf("a sport manifest invented channels: %+v", s.Channels())
	}
	providers := s.Providers()
	if len(providers) != 1 || providers[0].Events != 2 || providers[0].Channels != 0 {
		t.Fatalf("highfly provider = %+v", providers)
	}
}

// guideServer serves the XMLTV document a test builds.
func guideServer(t *testing.T, doc string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, doc)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func tvAddonWithEPG(t *testing.T, epgURL string) config.LiveTVSource {
	t.Helper()
	manifest := `{"id":"test.tvguide","types":["tv"],"resources":[
		{"name":"catalog","types":["tv"]},{"name":"stream","types":["tv"]}],
		"catalogs":[{"type":"tv","id":"channels","name":"Channels"}]}`
	f := newAddonFixture(t, manifest, func(w http.ResponseWriter, path string) bool {
		if path == "/catalog/tv/channels.json" {
			_, _ = io.WriteString(w, `{"metas":[
				{"id":"ch:news","name":"News 24"},
				{"id":"ch:sport","name":"Sport One"}]}`)
			return true
		}
		return false
	})
	src := f.source("addon-tv", "stremio")
	src.EPGURL = epgURL
	return src
}

// A Stremio-backed provider uses the XMLTV guide associated with it through the
// existing ingestion and matching pipeline: a reliable channel id first, the
// display name after that. Programmes for unknown channels are not invented.
func TestAddonGuideUsesExistingMatching(t *testing.T) {
	now := time.Now().UTC()
	start := now.Add(-30*time.Minute).Format("20060102150405") + " +0000"
	stop := now.Add(30*time.Minute).Format("20060102150405") + " +0000"
	doc := `<tv>
		<channel id="ch:news"><display-name>News 24</display-name></channel>
		<channel id="other.sport"><display-name>Sport One</display-name></channel>
		<programme start="` + start + `" stop="` + stop + `" channel="ch:news"><title>News at Six</title></programme>
		<programme start="` + start + `" stop="` + stop + `" channel="other.sport"><title>Late Match</title></programme>
		<programme start="` + start + `" stop="` + stop + `" channel="unmapped"><title>Nobody</title></programme>
	</tv>`
	guide := guideServer(t, doc)
	s := refreshAddon(t, tvAddonWithEPG(t, guide.URL+"/epg.xml"))

	chans := s.Channels()
	if len(chans) != 2 {
		t.Fatalf("channels = %+v", chans)
	}
	status := s.Status()
	if status.GuideChannels != 2 {
		t.Fatalf("guide channels = %d, want the two matched channels (%+v)", status.GuideChannels, status.Sources[0].GuideErrors)
	}
	if status.GuideRefreshed.IsZero() {
		t.Fatal("the addon's guide was never fetched")
	}
	byName := map[string][]Program{}
	for _, view := range s.ScheduleFor(6, "addon-tv") {
		channel := s.Channel(view.ID)
		if channel == nil {
			t.Fatalf("guide row for an unknown channel %q", view.ID)
		}
		byName[channel.Name] = view.Programs
	}
	if got := byName["News 24"]; len(got) != 1 || got[0].Title != "News at Six" {
		t.Fatalf("news guide = %+v", got)
	}
	// "Sport One" has no tvg-id in the guide; the display name matches it.
	if got := byName["Sport One"]; len(got) != 1 || got[0].Title != "Late Match" {
		t.Fatalf("sport guide = %+v", got)
	}
	// No programme is attributed to a channel the guide did not name.
	for name, programs := range byName {
		for _, program := range programs {
			if program.Title == "Nobody" {
				t.Fatalf("an unmapped guide channel reached %q", name)
			}
		}
	}
}

// Without an associated guide the addon's channels work normally and no
// programme data is invented.
func TestAddonWithoutGuideStillWorks(t *testing.T) {
	s := refreshAddon(t, tvAddonWithEPG(t, ""))
	chans := s.Channels()
	if len(chans) != 2 {
		t.Fatalf("channels = %+v", chans)
	}
	if now, next := chans[0].Now, chans[0].Next; now != nil || next != nil {
		t.Fatalf("guide data was invented: %+v %+v", now, next)
	}
	status := s.Status()
	if status.GuideChannels != 0 || len(status.Sources[0].GuideErrors) != 0 {
		t.Fatalf("missing guide was reported as an error: %+v", status)
	}
}

// The same addon can contribute channels and events; both appear in one list
// and both are counted for the source.
func TestAddonChannelsAndEventsShareOneSource(t *testing.T) {
	manifest := `{"id":"test.both","types":["tv","sport"],"resources":[
		{"name":"catalog","types":["tv","sport"]},{"name":"stream","types":["tv","sport"]}],
		"catalogs":[
			{"type":"tv","id":"channels","name":"Channels"},
			{"type":"sport","id":"live","name":"Live"}]}`
	f := newAddonFixture(t, manifest, func(w http.ResponseWriter, path string) bool {
		switch path {
		case "/catalog/tv/channels.json":
			_, _ = io.WriteString(w, `{"metas":[{"id":"ch:one","name":"One TV"}]}`)
		case "/catalog/sport/live.json":
			_, _ = io.WriteString(w, `{"metas":[{"id":"e1","name":"Final"}]}`)
		default:
			return false
		}
		return true
	})
	s := refreshAddon(t, f.source("both", "stremio"))
	if channels := s.Channels(); len(channels) != 1 {
		t.Fatalf("channels = %+v", channels)
	}
	if events := s.EventsFor(""); len(events) != 1 || events[0].Name != "Final" {
		t.Fatalf("events = %+v", events)
	}
	providers := s.Providers()
	if len(providers) != 1 || providers[0].Channels != 1 || providers[0].Events != 1 {
		t.Fatalf("providers = %+v", providers)
	}
}

// The guide link is kept per source, so sharing one XMLTV file between an IPTV
// provider and an addon cannot mix their channels.
func TestSharedGuideLinkStaysScopedToItsSource(t *testing.T) {
	now := time.Now().UTC()
	start := now.Add(-30*time.Minute).Format("20060102150405") + " +0000"
	stop := now.Add(30*time.Minute).Format("20060102150405") + " +0000"
	doc := `<tv>
		<channel id="ch:news"><display-name>News 24</display-name></channel>
		<channel id="iptv.news"><display-name>IPTV News</display-name></channel>
		<programme start="` + start + `" stop="` + stop + `" channel="ch:news"><title>Addon News</title></programme>
		<programme start="` + start + `" stop="` + stop + `" channel="iptv.news"><title>IPTV News Hour</title></programme>
	</tv>`
	guide := guideServer(t, doc)
	iptv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"iptv.news\",IPTV News\nhttp://media.example/iptv.ts\n")
	}))
	t.Cleanup(iptv.Close)
	addon := tvAddonWithEPG(t, guide.URL+"/epg.xml")
	addon.EPGURL = guide.URL + "/epg.xml"
	m3u := config.LiveTVSource{ID: "plain", Name: "Plain IPTV", Kind: "m3u", URL: iptv.URL + "/playlist.m3u", EPGURL: guide.URL + "/epg.xml", Enabled: true}
	cfg := &config.Config{LiveTV: config.LiveTVConfig{Enabled: true, Sources: []config.LiveTVSource{addon, m3u}}}
	s := New(cfg, t.TempDir())
	s.refresh(context.Background(), true)

	for _, view := range s.ScheduleFor(6, "") {
		channel := s.Channel(view.ID)
		for _, program := range view.Programs {
			switch channel.Source {
			case "addon-tv":
				if program.Title != "Addon News" {
					t.Fatalf("addon channel %q borrowed %q", channel.Name, program.Title)
				}
			case "plain":
				if program.Title != "IPTV News Hour" {
					t.Fatalf("IPTV channel %q borrowed %q", channel.Name, program.Title)
				}
			}
		}
	}
	if got := len(s.ChannelsFor("addon-tv")); got != 2 {
		t.Fatalf("addon channels = %d", got)
	}
	if got := s.Providers(); len(got) != 2 || got[0].ID != "addon-tv" || got[1].ID != "plain" {
		t.Fatalf("provider order = %+v", got)
	}
}
