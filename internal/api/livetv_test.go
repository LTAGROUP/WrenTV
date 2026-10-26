package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/livetv"
)

// Each provider keeps its own id through an edit, a duplicate id is split
// apart instead of merging two providers, and removing one leaves the other
// untouched.
func TestApplyLiveTVKeepsProvidersIndependent(t *testing.T) {
	cfg := &config.Config{LiveTV: config.LiveTVConfig{Sources: []config.LiveTVSource{
		{ID: "a", Name: "Provider A", Kind: "m3u", URL: "http://a.example/list.m3u", Enabled: true},
		{ID: "b", Name: "Provider B", Kind: "m3u", URL: "http://b.example/list.m3u", Enabled: true},
	}}}
	s := &Server{Cfg: cfg}

	updated := []config.LiveTVSource{
		{ID: "a", Name: "Provider A", Kind: "m3u", URL: "http://a.example/list.m3u", Enabled: true},
		{ID: "b", Name: "Provider B", Kind: "m3u", URL: "http://b.example/new-list.m3u", Enabled: true},
	}
	refetch, _, err := s.applyLiveTV(context.Background(), &liveTVPatch{Sources: &updated})
	if err != nil || !refetch {
		t.Fatalf("update = refetch %v, err %v", refetch, err)
	}
	if len(cfg.LiveTV.Sources) != 2 || cfg.LiveTV.Sources[0].URL != "http://a.example/list.m3u" || cfg.LiveTV.Sources[1].URL != "http://b.example/new-list.m3u" {
		t.Fatalf("providers after update = %+v", cfg.LiveTV.Sources)
	}

	duplicate := []config.LiveTVSource{
		{ID: "a", Name: "Provider A", Kind: "m3u", URL: "http://a.example/list.m3u", Enabled: true},
		{ID: "a", Name: "Provider B", Kind: "m3u", URL: "http://b.example/new-list.m3u", Enabled: true},
	}
	if _, _, err := s.applyLiveTV(context.Background(), &liveTVPatch{Sources: &duplicate}); err != nil {
		t.Fatal(err)
	}
	if len(cfg.LiveTV.Sources) != 2 || cfg.LiveTV.Sources[0].ID == cfg.LiveTV.Sources[1].ID {
		t.Fatalf("duplicate ids were not separated: %+v", cfg.LiveTV.Sources)
	}
	if cfg.LiveTV.Sources[1].URL != "http://b.example/new-list.m3u" {
		t.Fatalf("second provider changed while separating ids: %+v", cfg.LiveTV.Sources)
	}

	removed := cfg.LiveTV.Sources[:1]
	if _, _, err := s.applyLiveTV(context.Background(), &liveTVPatch{Sources: &removed}); err != nil {
		t.Fatal(err)
	}
	if len(cfg.LiveTV.Sources) != 1 || cfg.LiveTV.Sources[0].Name != "Provider A" {
		t.Fatalf("providers after removal = %+v", cfg.LiveTV.Sources)
	}
}

func TestCleanSourceOnlyRejectsMalformedGuideLinks(t *testing.T) {
	base := config.LiveTVSource{Kind: "m3u", URL: "http://provider.example/list.m3u", Name: "Provider"}
	bad := base
	bad.EPGURL = "ftp://guide.example/epg.xml"
	if _, err := cleanSource(bad, config.LiveTVSource{}); err == nil {
		t.Fatal("a non-HTTP guide link was accepted")
	}
	// A well-formed guide host is accepted even when it currently fails DNS;
	// that failure belongs in the provider's optional guide status, not save.
	unresolvable := base
	unresolvable.EPGURL = "https://zipline.nocn.ddnsfree.com/u/merged2_epg.xml.gz"
	if _, err := cleanSource(unresolvable, config.LiveTVSource{}); err != nil {
		t.Fatalf("a syntactically valid guide link was rejected: %v", err)
	}
}

// The Check result tells an admin whether the manifest is malformed, the link
// is wrong, or the network is down; only the last one is worth retrying. A
// manifest without TV channels is not a failure at all.
func TestLiveTVCheckClassifiesAddonFailures(t *testing.T) {
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			_, _ = fmt.Fprint(w, `{"id":"sport.only","types":["sport"],"resources":[{"name":"catalog","types":["sport"]},{"name":"stream","types":["sport"]}],"catalogs":[{"type":"sport","id":"live","name":"Live"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer addon.Close()
	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html>not an addon</html>`)
	}))
	defer malformed.Close()
	svc := livetv.New(&config.Config{}, t.TempDir())
	s := &Server{Cfg: &config.Config{}, LiveTV: svc}
	check := func(url string) (int, string) {
		body := fmt.Sprintf(`{"id":"x","name":"Test","kind":"stremio","url":%q,"enabled":true}`, url)
		rec := httptest.NewRecorder()
		s.handleLiveTVCheck(rec, httptest.NewRequest(http.MethodPost, "/api/admin/livetv/check", strings.NewReader(body)))
		var res struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		return rec.Code, res.Code
	}
	// A sport-only manifest is accepted, not rejected as a non-Live-TV addon.
	if status, code := check(addon.URL + "/manifest.json"); status != http.StatusOK {
		t.Fatalf("sport-only addon = %d %q", status, code)
	}
	// A page that isn't Stremio JSON is an invalid manifest.
	if status, code := check(malformed.URL + "/manifest.json"); status != http.StatusBadGateway || code != "invalid_manifest" {
		t.Fatalf("malformed addon = %d %q", status, code)
	}
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	if status, code := check(closed.URL + "/manifest.json"); status != http.StatusBadGateway || code != "addon_unreachable" {
		t.Fatalf("offline addon = %d %q", status, code)
	}
}

func TestLiveChannelsAndGuideCanSelectOneProvider(t *testing.T) {
	playlist := func(url string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"shared\" group-title=\"News\",Shared News\n%s\n", url)
		}))
	}
	a := playlist("http://provider-a.example/live.m3u8")
	defer a.Close()
	b := playlist("http://provider-b.example/live.m3u8")
	defer b.Close()

	cfg := &config.Config{LiveTV: config.LiveTVConfig{
		Enabled: true,
		Sources: []config.LiveTVSource{
			{ID: "a", Name: "Provider A", Kind: "m3u", URL: a.URL, Enabled: true},
			{ID: "b", Name: "Provider B", Kind: "m3u", URL: b.URL, Enabled: true},
		},
	}}
	svc := livetv.New(cfg, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !svc.Active() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(svc.Channels()) != 2 {
		t.Fatalf("channels = %d, want 2 provider channels", len(svc.Channels()))
	}
	server := &Server{LiveTV: svc}

	rec := httptest.NewRecorder()
	server.handleLiveChannels(rec, httptest.NewRequest(http.MethodGet, "/api/livetv/channels?source=a", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("channels status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Channels  []livetv.ChannelView `json:"channels"`
		Providers []livetv.Provider    `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Channels) != 1 || body.Channels[0].Source != "a" {
		t.Fatalf("provider a channels = %+v", body.Channels)
	}
	if len(body.Providers) != 2 || body.Providers[0].ID != "a" || body.Providers[1].ID != "b" {
		t.Fatalf("providers = %+v", body.Providers)
	}
	if body.Channels[0].Name != "Shared News" {
		t.Fatalf("channel = %+v", body.Channels[0])
	}

	rec = httptest.NewRecorder()
	server.handleLiveGuide(rec, httptest.NewRequest(http.MethodGet, "/api/livetv/guide?hours=6&source=b", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("guide status = %d: %s", rec.Code, rec.Body.String())
	}
	var guide struct {
		Channels []livetv.GuideView `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &guide); err != nil {
		t.Fatal(err)
	}
	if len(guide.Channels) != 1 || guide.Channels[0].ID != svc.ChannelsFor("b")[0].ID {
		t.Fatalf("provider b guide = %+v", guide.Channels)
	}
}
