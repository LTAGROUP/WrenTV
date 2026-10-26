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
	"time"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
)

// addonFixture serves a manifest, one searchable catalogue and a stream link.
// The stream itself comes from a second server so playback exercises the real
// Live TV session and proxy instead of a fake URL.
type addonFixture struct {
	addon       *httptest.Server
	stream      *httptest.Server
	catalogue   atomic.Int64
	streamCalls atomic.Int64
	failStream  atomic.Bool
	// The fixture stream only serves a player identity, so a forwarded
	// referer/origin is observable and a forwarded credential is visible.
	sawIdentity  atomic.Bool
	leakedCookie atomic.Bool
}

func newAddonFixture(t *testing.T, f *sportsFixture) *addonFixture {
	t.Helper()
	af := &addonFixture{}
	af.stream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://fixture.example/watch" ||
			r.Header.Get("Origin") != "https://fixture.example" {
			http.Error(w, "browser identity required", http.StatusForbidden)
			return
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Api-Key") != "" {
			af.leakedCookie.Store(true)
		}
		af.sawIdentity.Store(true)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
	}))
	t.Cleanup(af.stream.Close)
	af.addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest.json"):
			_, _ = io.WriteString(w, `{"catalogs":[
				{"type":"tv","id":"sports-live","name":"Sports live","extra":[{"name":"search"}]},
				{"type":"tv","id":"replay","name":"Replay archive"}
			]}`)
		case strings.Contains(r.URL.Path, "/catalog/tv/sports-live/"):
			af.catalogue.Add(1)
			_, _ = io.WriteString(w, `{"metas":[
				{"id":"celtics-heat","name":"Boston Celtics vs Miami Heat live","description":"NBA feed http://secret.example/token"},
				{"id":"concert","name":"Unrelated concert special","description":"not this event"}
			]}`)
		case strings.Contains(r.URL.Path, "/catalog/tv/replay"):
			t.Error("replay catalogue must not be queried")
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/stream/tv/celtics-heat.json"):
			af.streamCalls.Add(1)
			if af.failStream.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// The provider asks for a referer and an origin, and also sends a
			// credential the proxy must never forward.
			_, _ = fmt.Fprintf(w, `{"streams":[{"url":%q,"name":"HD","behaviorHints":{"proxyHeaders":{"request":{"Referer":"https://fixture.example/watch","Origin":"https://fixture.example","Cookie":"sid=secret","X-Api-Key":"secret"}}}}]}`, af.stream.URL+"/live.m3u8")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(af.addon.Close)
	f.cfg.Streaming.Addons = []config.AddonConfig{{Name: "Fixture Addon", URL: af.addon.URL + "/manifest.json", Enabled: true}}
	return af
}

func addonTestFixture(t *testing.T) (*sportsFixture, *addonFixture) {
	t.Helper()
	f := newSportsFixture(t)
	af := newAddonFixture(t, f)
	mux := f.mux.(interface {
		Get(string, http.HandlerFunc)
		Post(string, http.HandlerFunc)
		Delete(string, http.HandlerFunc)
	})
	mux.Get("/api/sports/events/{id}/addons", f.s.handleSportsAddons)
	mux.Post("/api/sports/addons/{id}/quality", f.s.handleSportsAddonQuality)
	mux.Post("/api/sports/addons/{id}/play", f.s.handleSportsAddonPlay)
	return f, af
}

func TestSportsAddonDiscoveryKeepsSecretsServerSide(t *testing.T) {
	f, af := addonTestFixture(t)
	rec, body := f.do(t, "GET", "/api/sports/events/402/addons", "")
	if rec.Code != 200 {
		t.Fatalf("discovery: %d %s", rec.Code, rec.Body.String())
	}
	candidates := body["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %s", rec.Body.String())
	}
	candidate := candidates[0].(map[string]any)
	if !strings.Contains(fmt.Sprint(candidate["title"]), "Celtics") {
		t.Fatalf("wrong candidate: %+v", candidate)
	}
	if candidate["confidenceTier"] != "strong" && candidate["confidenceTier"] != "possible" {
		t.Fatalf("weak match offered: %+v", candidate)
	}
	if candidate["id"] == "" || candidate["addon"] != "Fixture Addon" {
		t.Fatalf("identity lost: %+v", candidate)
	}
	// URLs, stream links and provider credentials must never reach a client.
	for _, secret := range []string{"secret.example", "http://", "https://", af.stream.URL} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("response leaked %q: %s", secret, rec.Body.String())
		}
	}
	sources := body["sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["state"] != "complete" {
		t.Fatalf("source status = %+v", sources)
	}

	// A second request inside the TTL is served from cache.
	before := af.catalogue.Load()
	if rec, _ := f.do(t, "GET", "/api/sports/events/402/addons", ""); rec.Code != 200 {
		t.Fatalf("cached discovery status = %d", rec.Code)
	}
	if af.catalogue.Load() != before {
		t.Fatal("discovery cache missed")
	}
}

func TestSportsAddonPlaybackProbeAndCleanup(t *testing.T) {
	f, af := addonTestFixture(t)
	_, body := f.do(t, "GET", "/api/sports/events/402/addons", "")
	id := body["candidates"].([]any)[0].(map[string]any)["id"].(string)

	// ffprobe is absent in this environment: the honest answer is "unavailable",
	// not an invented resolution.
	rec, quality := f.do(t, "POST", "/api/sports/addons/"+id+"/quality", `{"addonId":"`+id+`"}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(fmt.Sprint(quality["error"]), "unavailable") {
		t.Fatalf("quality: %d %+v", rec.Code, quality)
	}

	rec, play := f.doAs(t, "member", "member", "POST", "/api/sports/addons/"+id+"/play", `{"hls":false}`)
	if rec.Code != 200 {
		t.Fatalf("play: %d %s", rec.Code, rec.Body.String())
	}
	if play["sessionId"] == "" || play["url"] == "" {
		t.Fatalf("play response = %+v", play)
	}
	if strings.Contains(rec.Body.String(), af.stream.URL) {
		t.Fatalf("play leaked the provider URL: %s", rec.Body.String())
	}
	if f.s.LiveTV.Playing() != 1 {
		t.Fatalf("session not registered: %d", f.s.LiveTV.Playing())
	}
	// Playback is measured with the same identity: a provider that refuses an
	// anonymous request still serves the playlist through the session, and the
	// addon's credential stayed on the server.
	sess := f.s.LiveTV.Get(play["sessionId"].(string))
	if sess == nil {
		t.Fatal("session missing")
	}
	rec = httptest.NewRecorder()
	sess.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live/"+sess.ID+"/index.m3u8", nil), "index.m3u8")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("playlist through the session = %d %s", rec.Code, rec.Body.String())
	}
	if !af.sawIdentity.Load() {
		t.Fatal("the addon's referer/origin never reached the provider")
	}
	if af.leakedCookie.Load() {
		t.Fatal("the addon's credential was forwarded to the provider")
	}
	if !f.s.LiveTV.Stop(play["sessionId"].(string), "member", false) || f.s.LiveTV.Playing() != 0 {
		t.Fatalf("session was not stopped: playing=%d", f.s.LiveTV.Playing())
	}

	// The identity survives one playback session but not expiry.
	src, err := f.s.addonSource(id, "")
	if err != nil {
		t.Fatal(err)
	}
	src.expires = time.Now().Add(-time.Minute)
	f.s.registerAddonSource(src)
	if rec, _ := f.doAs(t, "member", "member", "POST", "/api/sports/addons/"+id+"/play", `{}`); rec.Code != http.StatusGone {
		t.Fatalf("expired identity status = %d", rec.Code)
	}
	if rec, _ := f.doAs(t, "member", "member", "POST", "/api/sports/addons/not-a-token/play", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown identity status = %d", rec.Code)
	}
	if rec, _ := f.do(t, "POST", "/api/sports/addons/"+id+"/quality", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing addonId status = %d", rec.Code)
	}
}

func TestSportsAddonProviderFailureAndConcurrentProbes(t *testing.T) {
	f, af := addonTestFixture(t)
	_, body := f.do(t, "GET", "/api/sports/events/402/addons", "")
	id := body["candidates"].([]any)[0].(map[string]any)["id"].(string)

	af.failStream.Store(true)
	rec, failure := f.doAs(t, "member", "member", "POST", "/api/sports/addons/"+id+"/play", `{}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("provider failure status = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), af.addon.URL) || strings.Contains(fmt.Sprint(failure["error"]), "HTTP 500") {
		t.Fatalf("provider detail leaked: %s", rec.Body.String())
	}
	af.failStream.Store(false)

	// Concurrent plays each get their own session on the same identity.
	user, err := f.s.DB.UserByID(context.Background(), "member")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	statuses := make([]int, 4)
	ids := make([]string, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/sports/addons/"+id+"/play", strings.NewReader(`{}`))
			req = req.WithContext(context.WithValue(req.Context(), principalKey, &auth.Principal{User: user, Role: "member"}))
			rec := httptest.NewRecorder()
			f.mux.ServeHTTP(rec, req)
			statuses[index] = rec.Code
			out := map[string]any{}
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			if value, ok := out["sessionId"].(string); ok {
				ids[index] = value
			}
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, status := range statuses {
		if status != 200 {
			t.Fatalf("concurrent play %d status = %d", i, status)
		}
		if ids[i] == "" || seen[ids[i]] {
			t.Fatalf("session id %q missing or reused", ids[i])
		}
		seen[ids[i]] = true
	}
}
