package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// An addon offering several feeds for one sports event, with their own media
// servers so a feed that fails is a real request that fails.
type autoMedia struct {
	server *httptest.Server
	hits   atomic.Int32
}

func newAutoMedia(t *testing.T, contentType string, status int, body string, delay time.Duration) *autoMedia {
	t.Helper()
	m := &autoMedia{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.hits.Add(1)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(m.server.Close)
	return m
}

const autoHLS = "application/vnd.apple.mpegurl"

// newRefusingMedia is a media server that is refusing this server for now and
// says when to come back, like a provider shedding load.
func newRefusingMedia(t *testing.T, retryAfter string) *autoMedia {
	t.Helper()
	m := &autoMedia{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.hits.Add(1)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(m.server.Close)
	return m
}

func newAutoAddon(t *testing.T, feeds [][3]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest.json"):
			_, _ = io.WriteString(w, `{"id":"test.auto","name":"Auto addon","types":["sport"],
				"resources":[{"name":"catalog","types":["sport"]},{"name":"stream","types":["sport"]}],
				"catalogs":[{"type":"sport","id":"live","name":"Live"}]}`)
		case strings.HasPrefix(r.URL.Path, "/stream/sport/e1.json"):
			streams := make([]map[string]any, 0, len(feeds))
			for _, feed := range feeds {
				streams = append(streams, map[string]any{"name": feed[0], "title": feed[1], "url": feed[2]})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"streams": streams})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func autoHarness(t *testing.T, addon *httptest.Server) *liveSportTestServer {
	t.Helper()
	src := config.LiveTVSource{ID: "highfly", Name: "HighFly", Kind: "stremio", URL: addon.URL + "/manifest.json", Enabled: true}
	return newLiveSportTestServer(t, src)
}

// playing asserts the returned session really serves the feed's playlist
// through the proxy, so "a stream started" is observed rather than assumed.
func playing(t *testing.T, h *liveSportTestServer, sessionID string) {
	t.Helper()
	sess := h.s.LiveTV.Get(sessionID)
	if sess == nil {
		t.Fatalf("no session %q", sessionID)
	}
	rec := httptest.NewRecorder()
	sess.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live/"+sess.ID+"/index.m3u8", nil), "index.m3u8")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("playlist through the session = %d %s", rec.Code, rec.Body.String())
	}
	h.s.LiveTV.Stop(sess.ID, "viewer", false)
}

// One click plays the best feed the addon offers, without asking the viewer to
// choose and without exposing the provider's link.
func TestLiveAddonAutoPlayStartsTheBestStream(t *testing.T) {
	main := newAutoMedia(t, autoHLS, http.StatusOK, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n", 0)
	addon := newAutoAddon(t, [][3]string{
		{"Backup", "720p", main.server.URL + "/backup.m3u8"},
		{"Main", "1920x1080", main.server.URL + "/main.m3u8"},
	})
	h := autoHarness(t, addon)

	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	feed, _ := body["feed"].(map[string]any)
	if feed["name"] != "Main" || feed["quality"] != float64(1080) {
		t.Fatalf("feed = %s", rec.Body.String())
	}
	if attempts, _ := body["attempts"].([]any); len(attempts) != 0 {
		t.Fatalf("a working first feed still recorded failures: %s", rec.Body.String())
	}
	// The 720p backup was not needed, but it is still offered as a fallback if
	// the browser itself can't play the chosen feed.
	if body["remaining"] != float64(1) {
		t.Fatalf("remaining = %v", body["remaining"])
	}
	if strings.Contains(rec.Body.String(), main.server.URL) {
		t.Fatalf("the provider URL leaked: %s", rec.Body.String())
	}
	playing(t, h, body["sessionId"].(string))
}

// A dead feed is not a failed playback: the next suitable stream starts.
func TestLiveAddonAutoPlayFallsBackToTheNextStream(t *testing.T) {
	dead := newAutoMedia(t, autoHLS, http.StatusForbidden, "", 0)
	working := newAutoMedia(t, autoHLS, http.StatusOK, "#EXTM3U\n", 0)
	addon := newAutoAddon(t, [][3]string{
		{"Main", "1080p", dead.server.URL + "/main.m3u8"},
		{"Backup", "720p", working.server.URL + "/backup.m3u8"},
	})
	h := autoHarness(t, addon)

	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	feed, _ := body["feed"].(map[string]any)
	if feed["name"] != "Backup" || feed["quality"] != float64(720) {
		t.Fatalf("feed = %s", rec.Body.String())
	}
	tried, _ := body["tried"].([]any)
	if len(tried) != 2 {
		t.Fatalf("tried = %s", rec.Body.String())
	}
	attempts, _ := body["attempts"].([]any)
	if len(attempts) != 1 {
		t.Fatalf("attempts = %s", rec.Body.String())
	}
	if failure := attempts[0].(map[string]any); !strings.Contains(fmt.Sprint(failure["error"]), "403") {
		t.Fatalf("the failure doesn't say why: %s", rec.Body.String())
	}
	if dead.hits.Load() != 1 {
		t.Fatalf("the dead feed was requested %d times", dead.hits.Load())
	}
	playing(t, h, body["sessionId"].(string))
}

// Placeholders, player tips, torrents and malformed links are not streams; an
// item that offers only those has nothing to play.
func TestLiveAddonAutoPlayRejectsPlaceholderOnlyItems(t *testing.T) {
	addon := newAutoAddon(t, [][3]string{
		{"🔒 Premium", "Upgrade to watch", "https://www.google.com"},
		{"Tip", "Use an external player", "https://example.invalid/player"},
		{"Torrent", "1080p", "magnet:?xt=urn:btih:abc"},
		{"Broken", "720p", "https://"},
	})
	h := autoHarness(t, addon)

	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	if rec.Code != http.StatusNotFound || body["code"] != "no_streams" {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	if h.s.LiveTV.Playing() != 0 {
		t.Fatal("a placeholder started a session")
	}
}

// When nothing starts the viewer gets an honest failure, the feeds that were
// tried, and no session left behind.
func TestLiveAddonAutoPlayReportsWhenNothingStarts(t *testing.T) {
	broken := newAutoMedia(t, autoHLS, http.StatusServiceUnavailable, "", 0)
	expired := newAutoMedia(t, "text/html", http.StatusOK, "<html>token expired</html>", 0)
	addon := newAutoAddon(t, [][3]string{
		{"Main", "1080p", broken.server.URL + "/main.m3u8"},
		{"Backup", "720p", expired.server.URL + "/backup.m3u8"},
	})
	h := autoHarness(t, addon)

	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	if rec.Code != http.StatusBadGateway || body["code"] != "no_working_stream" {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	if tried, _ := body["tried"].([]any); len(tried) != 2 {
		t.Fatalf("tried = %s", rec.Body.String())
	}
	if attempts, _ := body["attempts"].([]any); len(attempts) != 2 {
		t.Fatalf("attempts = %s", rec.Body.String())
	}
	if body["remaining"] != float64(0) {
		t.Fatalf("remaining = %v", body["remaining"])
	}
	if !strings.Contains(fmt.Sprint(body["error"]), "Tried 2 streams") {
		t.Fatalf("error = %v", body["error"])
	}
	if h.s.LiveTV.Playing() != 0 {
		t.Fatal("a failed feed left a session behind")
	}
}

// A feed that already failed in this playback attempt is skipped, not retried:
// the client sends the keys it tried and the server moves on.
func TestLiveAddonAutoPlayNeverRetriesATriedStream(t *testing.T) {
	dead := newAutoMedia(t, autoHLS, http.StatusForbidden, "", 0)
	working := newAutoMedia(t, autoHLS, http.StatusOK, "#EXTM3U\n", 0)
	addon := newAutoAddon(t, [][3]string{
		{"Main", "1080p", dead.server.URL + "/main.m3u8"},
		{"Backup", "720p", working.server.URL + "/backup.m3u8"},
	})
	h := autoHarness(t, addon)

	_, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	tried, _ := body["tried"].([]any)
	if len(tried) != 2 {
		t.Fatalf("tried = %s", body["tried"])
	}
	if sid, _ := body["sessionId"].(string); sid != "" {
		h.s.LiveTV.Stop(sid, "viewer", false)
	}
	dead.hits.Store(0)
	working.hits.Store(0)

	skip, _ := json.Marshal(map[string]any{"hls": true, "skip": tried})
	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", string(skip))
	if rec.Code != http.StatusBadGateway || body["code"] != "no_working_stream" {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fmt.Sprint(body["error"]), "already tried") {
		t.Fatalf("error = %v", body["error"])
	}
	if dead.hits.Load() != 0 || working.hits.Load() != 0 {
		t.Fatalf("a tried feed was requested again (dead=%d working=%d)", dead.hits.Load(), working.hits.Load())
	}
}

// A provider refusing this server is not a dead feed: automatic playback stops
// at the first refusal and says when to come back, instead of asking for the
// rest of the feed list into the same limit.
func TestLiveAddonAutoPlayStopsAtAProviderRateLimit(t *testing.T) {
	limited := newRefusingMedia(t, "30")
	spare := newAutoMedia(t, autoHLS, http.StatusOK, "#EXTM3U\n", 0)
	addon := newAutoAddon(t, [][3]string{
		{"Main", "1080p", limited.server.URL + "/main.m3u8"},
		{"Backup", "720p", spare.server.URL + "/backup.m3u8"},
	})
	h := autoHarness(t, addon)

	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	if rec.Code != http.StatusTooManyRequests || body["code"] != "rate_limited" {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fmt.Sprint(body["error"]), "rate limiting") {
		t.Fatalf("the failure doesn't say why: %s", rec.Body.String())
	}
	// The client is told when the provider asked for, so it can wait instead of
	// retrying into the limit.
	if got := rec.Header().Get("Retry-After"); got != "31" {
		t.Fatalf("Retry-After = %q, want the provider's own wait", got)
	}
	if limited.hits.Load() != 1 {
		t.Fatalf("the rate-limited feed was requested %d times", limited.hits.Load())
	}
	if spare.hits.Load() != 0 {
		t.Fatal("a later feed was requested into the same rate limit")
	}
	// The feed that was refused is reported as tried, so the player never asks
	// for it again.
	if tried, _ := body["tried"].([]any); len(tried) != 1 {
		t.Fatalf("tried = %s", rec.Body.String())
	}
	if h.s.LiveTV.Playing() != 0 {
		t.Fatal("a rate-limited attempt left a session behind")
	}
}

// The addon's own stream endpoint refusing this server is reported the same
// way, before any feed is checked at all.
func TestLiveAddonAutoPlayReportsARateLimitedStreamEndpoint(t *testing.T) {
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest.json"):
			_, _ = io.WriteString(w, `{"id":"test.auto","name":"Auto addon","types":["sport"],
				"resources":[{"name":"catalog","types":["sport"]},{"name":"stream","types":["sport"]}],
				"catalogs":[{"type":"sport","id":"live","name":"Live"}]}`)
		case strings.HasPrefix(r.URL.Path, "/stream/sport/e1.json"):
			w.Header().Set("Retry-After", "45")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(addon.Close)
	h := autoHarness(t, addon)

	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	if rec.Code != http.StatusTooManyRequests || body["code"] != "rate_limited" {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "46" {
		t.Fatalf("Retry-After = %q", got)
	}
	if h.s.LiveTV.Playing() != 0 {
		t.Fatal("a rate-limited stream endpoint left a session behind")
	}
}

// A feed that hangs is bounded by the check timeout, and the next feed is tried.
func TestLiveAddonAutoPlayFallsBackAfterAStreamTimesOut(t *testing.T) {
	slow := newAutoMedia(t, autoHLS, http.StatusOK, "#EXTM3U\n", 2*time.Second)
	working := newAutoMedia(t, autoHLS, http.StatusOK, "#EXTM3U\n", 0)
	addon := newAutoAddon(t, [][3]string{
		{"Main", "1080p", slow.server.URL + "/main.m3u8"},
		{"Backup", "720p", working.server.URL + "/backup.m3u8"},
	})
	h := autoHarness(t, addon)
	h.s.LiveTV.AddonCheckWait = 250 * time.Millisecond

	start := time.Now()
	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("auto play waited %s on a hanging feed", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	feed, _ := body["feed"].(map[string]any)
	if feed["name"] != "Backup" {
		t.Fatalf("feed = %s", rec.Body.String())
	}
	attempts, _ := body["attempts"].([]any)
	if len(attempts) != 1 || !strings.Contains(fmt.Sprint(attempts[0].(map[string]any)["error"]), "time") {
		t.Fatalf("attempts = %s", rec.Body.String())
	}
	playing(t, h, body["sessionId"].(string))
}

// One request tries a bounded number of feeds and reports the rest, so a long
// list of dead links becomes several short requests instead of one that hangs.
func TestLiveAddonAutoPlayContinuesInAnotherRequest(t *testing.T) {
	feeds := make([][3]string, 0, 5)
	for i := 0; i < 5; i++ {
		dead := newAutoMedia(t, autoHLS, http.StatusNotFound, "", 0)
		feeds = append(feeds, [3]string{fmt.Sprintf("Feed %d", i+1), "1080p", dead.server.URL + fmt.Sprintf("/%d.m3u8", i)})
	}
	h := autoHarness(t, newAutoAddon(t, feeds))

	rec, body := h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", `{"hls":true}`)
	if rec.Code != http.StatusBadGateway || body["code"] != "no_working_stream" {
		t.Fatalf("auto play = %d %s", rec.Code, rec.Body.String())
	}
	tried, _ := body["tried"].([]any)
	if len(tried) != addonAutoTries || body["remaining"] != float64(1) {
		t.Fatalf("first round = %s", rec.Body.String())
	}

	skip, _ := json.Marshal(map[string]any{"hls": true, "skip": tried})
	rec, body = h.do(t, "POST", "/api/livetv/addons/highfly/item/sport/e1/play", string(skip))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("second round = %d %s", rec.Code, rec.Body.String())
	}
	if body["remaining"] != float64(0) || body["tried"] == nil {
		t.Fatalf("second round = %s", rec.Body.String())
	}
	if !strings.Contains(fmt.Sprint(body["error"]), "Tried 1 stream") {
		t.Fatalf("second round error = %v", body["error"])
	}
}
