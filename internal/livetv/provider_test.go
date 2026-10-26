package livetv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// A second guide host that cannot be resolved is an optional-source failure:
// the first guide and the provider's channels must still load, and the admin
// must see the failed guide beside that provider.
func TestOptionalGuideFailureKeepsProviderChannels(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute).UTC()
	stop := start.Add(time.Hour)
	guideServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<tv><channel id="one"><display-name>News One</display-name></channel>`+
			`<programme start="%s +0000" stop="%s +0000" channel="one"><title>Good Guide</title></programme></tv>`,
			start.Format("20060102150405"), stop.Format("20060102150405"))
	}))
	defer guideServer.Close()
	playlistServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "#EXTM3U url-tvg=%q\n"+
			"#EXTINF:-1 tvg-id=\"one\" group-title=\"News\",News One\nhttp://provider.example/news.m3u8\n",
			guideServer.URL+",http://zipline.nocn.ddnsfree.invalid/u/merged2_epg.xml.gz")
	}))
	defer playlistServer.Close()

	cfg := &config.Config{LiveTV: config.LiveTVConfig{
		Enabled: true,
		Sources: []config.LiveTVSource{{
			ID: "main", Name: "Main provider", Kind: "m3u", URL: playlistServer.URL, Enabled: true,
		}},
	}}
	s := New(cfg, t.TempDir())
	s.client = &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "zipline.nocn.ddnsfree.invalid") {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{
				Err: "no such host", Name: req.URL.Host,
			}}
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
	s.refresh(context.Background(), true)

	if !s.Active() || len(s.Channels()) != 1 {
		t.Fatalf("channels = %d, active = %v", len(s.Channels()), s.Active())
	}
	st := s.Status()
	if len(st.Sources) != 1 {
		t.Fatalf("source statuses = %+v", st.Sources)
	}
	src := st.Sources[0]
	if src.Channels != 1 || src.Error != "" {
		t.Fatalf("playlist result = %+v, want a usable provider", src)
	}
	if len(src.GuideErrors) == 0 || !strings.Contains(strings.Join(src.GuideErrors, " "), "no such host") {
		t.Fatalf("guide error not reported beside provider: %+v", src)
	}
	if !strings.Contains(strings.Join(src.GuideErrors, " "), "DNS") {
		t.Fatalf("guide error does not identify a DNS failure: %+v", src)
	}
	// The working guide must still be applied; an optional failure must not
	// throw away programmes from the other guide source.
	if views := s.ScheduleFor(6, "main"); len(views) != 1 || len(views[0].Programs) != 1 || views[0].Programs[0].Title != "Good Guide" {
		t.Fatalf("working guide was lost: %+v", views)
	}
	if len(st.Errors) == 0 || !strings.Contains(strings.Join(st.Errors, " "), "Main provider") {
		t.Fatalf("aggregate error lost the provider name: %+v", st.Errors)
	}
}

// Two providers can publish the same channel name and tvg-id. Their channels,
// playback URLs and guides must stay separate.
func TestProvidersKeepDuplicateChannelIdentitiesAndGuidesSeparate(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute).UTC()
	stop := start.Add(time.Hour)
	guideDoc := func(title string) string {
		return fmt.Sprintf(`<tv><channel id="shared"><display-name>Shared News</display-name></channel>`+
			`<programme start="%s +0000" stop="%s +0000" channel="shared"><title>%s</title></programme></tv>`,
			start.Format("20060102150405"), stop.Format("20060102150405"), title)
	}
	guideA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, guideDoc("A News"))
	}))
	defer guideA.Close()
	guideB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, guideDoc("B News"))
	}))
	defer guideB.Close()

	playlist := func(guideURL, streamURL string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "#EXTM3U url-tvg=%q\n#EXTINF:-1 tvg-id=\"shared\" group-title=\"News\",Shared News\n%s\n", guideURL, streamURL)
		}))
	}
	providerA := playlist(guideA.URL, "http://provider-a.example/live.m3u8")
	defer providerA.Close()
	providerB := playlist(guideB.URL, "http://provider-b.example/live.m3u8")
	defer providerB.Close()

	cfg := &config.Config{LiveTV: config.LiveTVConfig{
		Enabled: true,
		Sources: []config.LiveTVSource{
			{ID: "a", Name: "Provider A", Kind: "m3u", URL: providerA.URL, Enabled: true},
			{ID: "b", Name: "Provider B", Kind: "m3u", URL: providerB.URL, Enabled: true},
		},
	}}
	s := New(cfg, t.TempDir())
	s.refresh(context.Background(), true)

	if got := len(s.Channels()); got != 2 {
		t.Fatalf("all channels = %d, want 2", got)
	}
	providers := s.Providers()
	if len(providers) != 2 || providers[0].ID != "a" || providers[1].ID != "b" {
		t.Fatalf("providers = %+v", providers)
	}
	for _, p := range providers {
		if p.Channels != 1 {
			t.Fatalf("provider %q channels = %d", p.ID, p.Channels)
		}
	}

	a := s.ChannelsFor("a")
	b := s.ChannelsFor("b")
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("provider a = %+v, provider b = %+v", a, b)
	}
	if a[0].ID == b[0].ID {
		t.Fatalf("duplicate channel names got one identity: %q", a[0].ID)
	}
	if a[0].Source != "a" || b[0].Source != "b" {
		t.Fatalf("source identity lost: %+v / %+v", a[0].Channel, b[0].Channel)
	}
	if !strings.Contains(a[0].URL, "provider-a") || !strings.Contains(b[0].URL, "provider-b") {
		t.Fatalf("playback URLs mixed: %q / %q", a[0].URL, b[0].URL)
	}

	guideAView := s.ScheduleFor(6, "a")
	guideBView := s.ScheduleFor(6, "b")
	if len(guideAView) != 1 || len(guideBView) != 1 {
		t.Fatalf("guide views = %+v / %+v", guideAView, guideBView)
	}
	if len(guideAView[0].Programs) != 1 || guideAView[0].Programs[0].Title != "A News" {
		t.Fatalf("provider a guide = %+v", guideAView)
	}
	if len(guideBView[0].Programs) != 1 || guideBView[0].Programs[0].Title != "B News" {
		t.Fatalf("provider b guide = %+v", guideBView)
	}
	if got := s.ScheduleFor(6, "a")[0].ID; got != a[0].ID {
		t.Fatalf("provider a guide id = %q, want %q", got, a[0].ID)
	}
	if got := s.ScheduleFor(6, "b")[0].ID; got != b[0].ID {
		t.Fatalf("provider b guide id = %q, want %q", got, b[0].ID)
	}

	st := s.Status()
	if len(st.Sources) != 2 || st.Sources[0].GuideChannels != 1 || st.Sources[1].GuideChannels != 1 {
		t.Fatalf("source guide counts = %+v", st.Sources)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// A sport-only manifest is a valid addon, not an invalid Live TV provider.
// Asking it for channels is an honest "none", while the addon itself passes
// acceptance and reports what it can browse.
func TestStremioManifestWithoutTVCatalogsIsAccepted(t *testing.T) {
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest.json"):
			// Shaped like the real sports.highfly manifest: sport catalogs, no tv.
			_, _ = fmt.Fprint(w, `{"id":"community.sports","types":["sport"],"resources":[{"name":"catalog","types":["sport"]},{"name":"meta","types":["sport"]},{"name":"stream","types":["sport"]}],"catalogs":[{"type":"sport","id":"sports_live","name":"Live Now"},{"type":"movie","id":"films","name":"Films"}]}`)
		case strings.HasSuffix(r.URL.Path, "/catalog/sport/sports_live.json"):
			_, _ = fmt.Fprint(w, `{"metas":[{"id":"streamed:one","name":"Alpha vs Beta"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer addon.Close()
	src := config.LiveTVSource{ID: "sports", Name: "sports.highfly", Kind: "stremio", URL: addon.URL + "/manifest.json"}
	s := New(&config.Config{}, t.TempDir())

	// No channel model: the channel listing says so, which is not a failure.
	if _, err := s.StremioChannels(context.Background(), src); !errors.Is(err, ErrNoTVChannels) {
		t.Fatalf("channel listing error = %v, want ErrNoTVChannels", err)
	} else if !strings.Contains(err.Error(), "sport") {
		t.Fatalf("channel error does not name the declared catalog type: %v", err)
	}

	// Acceptance is the manifest. A valid addon checks with no channels and
	// reports its capabilities instead.
	n, _, err := s.Check(context.Background(), src)
	if err != nil || n != 0 {
		t.Fatalf("check = %d channels, %v; want 0 channels and no error", n, err)
	}
	caps, err := s.AddonCapabilities(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.HasBrowsableModel() || caps.HasTVChannels() || !caps.Browsable || !caps.Playable {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.SportCatalogs() != 1 {
		t.Fatalf("sport catalogs = %d, want 1", caps.SportCatalogs())
	}
	if caps.Models == nil || len(caps.Models) != 2 {
		t.Fatalf("models = %+v", caps.Models)
	}

	// An addon that is simply down is a different problem and must not be
	// accepted as if it had answered.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	offline := config.LiveTVSource{ID: "off", Name: "Offline", Kind: "stremio", URL: closed.URL + "/manifest.json"}
	if _, err := s.StremioChannels(context.Background(), offline); !errors.Is(err, ErrAddonUnreachable) || errors.Is(err, ErrNoTVChannels) {
		t.Fatalf("network error = %v, want ErrAddonUnreachable, not an incompatible addon", err)
	}
	if _, err := s.AddonCapabilities(context.Background(), offline); !errors.Is(err, ErrAddonUnreachable) {
		t.Fatalf("capability discovery error = %v, want ErrAddonUnreachable", err)
	}
}

func TestStremioMalformedManifestIsAnInvalidResponse(t *testing.T) {
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			_, _ = fmt.Fprint(w, `{not stremio json`)
			return
		}
		http.NotFound(w, r)
	}))
	defer addon.Close()
	src := config.LiveTVSource{ID: "bad", Name: "Bad addon", Kind: "stremio", URL: addon.URL + "/manifest.json"}
	s := New(&config.Config{}, t.TempDir())
	if _, err := s.StremioChannels(context.Background(), src); !errors.Is(err, ErrAddonInvalidResponse) {
		t.Fatalf("malformed manifest = %v, want ErrAddonInvalidResponse", err)
	} else if errors.Is(err, ErrNoTVChannels) || errors.Is(err, ErrAddonUnreachable) {
		t.Fatalf("malformed manifest was misclassified: %v", err)
	}
}

func TestStremioSearchOnlyCatalogsAreReportedSeparately(t *testing.T) {
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			_, _ = fmt.Fprint(w, `{"catalogs":[{"type":"tv","id":"search","name":"Search","extra":[{"name":"search","isRequired":true}]}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer addon.Close()
	src := config.LiveTVSource{ID: "search", Name: "Search addon", Kind: "stremio", URL: addon.URL + "/manifest.json"}
	s := New(&config.Config{}, t.TempDir())
	if _, err := s.StremioChannels(context.Background(), src); !errors.Is(err, ErrUnlistableAddon) {
		t.Fatalf("manifest error = %v, want ErrUnlistableAddon", err)
	}
}

// A native EPG catalog may mark date and skip as required. That is still a
// browsable channel list: request today's date, paginate with skip, and read
// metasDetailed as well as metas.
func TestStremioRequiredDateAndSkipCatalogListsChannels(t *testing.T) {
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/manifest.json"):
			_, _ = fmt.Fprint(w, `{"catalogs":[{"type":"tv","id":"live","name":"Live","extra":[{"name":"date","isRequired":true},{"name":"skip","isRequired":true}]}]}`)
		case strings.HasPrefix(p, "/catalog/tv/live/date=") && strings.Contains(p, "skip=0"):
			if !regexp.MustCompile(`date=\d{4}-\d{2}-\d{2}`).MatchString(p) {
				t.Errorf("catalog path has no date: %s", p)
			}
			_, _ = fmt.Fprint(w, `{"metasDetailed":[{"id":"epg:one","type":"tv","name":"EPG One","poster":"http://logo/one.png","videos":[{"id":"epg:one:1","title":"Show"}]}]}`)
		case strings.HasPrefix(p, "/catalog/tv/live/date=") && strings.Contains(p, "skip=1"):
			_, _ = fmt.Fprint(w, `{"metasDetailed":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer addon.Close()
	src := config.LiveTVSource{ID: "epg", Name: "Native EPG", Kind: "stremio", URL: addon.URL + "/manifest.json"}
	s := New(&config.Config{}, t.TempDir())
	chans, err := s.StremioChannels(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 1 || chans[0].Name != "EPG One" || chans[0].URL != addon.URL+"/stream/tv/epg:one.json" {
		t.Fatalf("channels = %+v", chans)
	}
}

// An empty channel catalog is a valid addon with nothing listed right now.
// It is not a manifest error and must not be reported as one.
func TestStremioEmptyCatalogIsNotAManifestError(t *testing.T) {
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest.json"):
			_, _ = fmt.Fprint(w, `{"id":"empty.tv","types":["tv"],"resources":["catalog","stream"],"catalogs":[{"type":"tv","id":"channels","name":"Channels"}]}`)
		case strings.HasSuffix(r.URL.Path, "/catalog/tv/channels.json"):
			_, _ = fmt.Fprint(w, `{"metas":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer addon.Close()
	src := config.LiveTVSource{ID: "empty", Name: "Empty", Kind: "stremio", URL: addon.URL + "/manifest.json"}
	s := New(&config.Config{}, t.TempDir())
	n, _, err := s.Check(context.Background(), src)
	if err != nil || n != 0 {
		t.Fatalf("empty catalog check = %d channels, %v; want 0 and no error", n, err)
	}
	caps, err := s.AddonCapabilities(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.HasTVChannels() || !caps.Browsable {
		t.Fatalf("empty tv catalog capabilities = %+v", caps)
	}
}
