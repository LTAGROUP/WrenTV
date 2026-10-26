package livetv

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// Addon feeds advertise their quality in their own labels. Ranking repeats that
// claim and never invents one.
func TestAddonStreamQualityReadsAddonLabels(t *testing.T) {
	cases := map[string]int{
		"1080p":                    1080,
		"1080i":                    1080,
		"1080p60":                  1080,
		"1920x1080":                1080,
		"4K HDR":                   2160,
		"UHD":                      2160,
		"Full HD":                  1080,
		"HD":                       720,
		"SD":                       480,
		"Server 3":                 0,
		"2026":                     0,
		"English commentary only":  0,
		"720p · Server 2 (Strong)": 720,
	}
	for label, want := range cases {
		if got := AddonStreamQuality(AddonStream{Title: label}); got != want {
			t.Errorf("quality of %q = %d, want %d", label, got, want)
		}
	}
}

func TestRankAddonStreamsPrefersQualityThenHLS(t *testing.T) {
	streams := []AddonStream{
		{Name: "Backup", Title: "720p", URL: "https://cdn.example/backup.m3u8"},
		{Name: "Main", Title: "1920x1080", URL: "https://cdn.example/main.m3u8"},
		{Name: "File", Title: "1080p", URL: "https://cdn.example/file.mp4"},
		{Name: "Unknown", URL: "https://cdn.example/unknown"},
	}
	ranked := RankAddonStreams(streams)
	want := []string{"Main", "File", "Backup", "Unknown"}
	if len(ranked) != len(want) {
		t.Fatalf("ranked %d feeds, want %d", len(ranked), len(want))
	}
	for i, name := range want {
		if ranked[i].Name != name {
			t.Fatalf("ranked[%d] = %q, want %q (all: %+v)", i, ranked[i].Name, name, ranked)
		}
	}
	// Ranking never changes the caller's slice: the addon's own order is kept
	// for browsing.
	if streams[0].Name != "Backup" {
		t.Fatalf("ranking reordered its input: %+v", streams)
	}
}

func TestRankAddonStreamsKeepsAddonOrderForEqualFeeds(t *testing.T) {
	streams := []AddonStream{
		{Name: "One", Title: "1080p", URL: "https://cdn.example/1.m3u8"},
		{Name: "Two", Title: "1080p", URL: "https://cdn.example/2.m3u8"},
	}
	ranked := RankAddonStreams(streams)
	if ranked[0].Name != "One" || ranked[1].Name != "Two" {
		t.Fatalf("equal feeds were reordered: %+v", ranked)
	}
}

// Paid plans and player tips are messages, not feeds: automatic playback must
// never spend an attempt on one.
func TestPlayableAddonURLRejectsMessages(t *testing.T) {
	for _, label := range []string{
		"🔒 Premium only", "Premium required", "Requires premium", "Upgrade your plan",
		"Subscribe to watch", "Sign in to watch", "Log in to watch",
		"Streams unavailable", "Streams temporarily unavailable",
		"Use an external player", "Tip: external player",
		"Not available in your region", "Unavailable in your region",
	} {
		if playableAddonURL("https://cdn.example/feed.m3u8", label) {
			t.Errorf("%q was accepted as a feed", label)
		}
	}
	for _, label := range []string{"Main feed", "1080p Strong", "Premium Sports HD 1080p"} {
		if !playableAddonURL("https://cdn.example/feed.m3u8", label) {
			t.Errorf("%q was rejected as a feed", label)
		}
	}
	if playableAddonURL("magnet:?xt=urn:btih:abc", "Torrent") {
		t.Error("a torrent was accepted as a feed")
	}
	if playableAddonURL("https://", "Broken") {
		t.Error("a malformed link was accepted as a feed")
	}
}

func newCheckMedia(t *testing.T, contentType string, status int, body string, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A feed starts when it answers with media. An expired token that answers 200
// with a web page, a refused token and a host that never answers must all be
// reported as failures so the next feed is tried.
func TestCheckAddonStreamClassifiesFailures(t *testing.T) {
	s := New(&config.Config{}, t.TempDir())
	ctx := context.Background()

	good := newCheckMedia(t, "application/vnd.apple.mpegurl", http.StatusOK, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n", 0)
	if err := s.CheckAddonStream(ctx, AddonStream{URL: good.URL + "/index.m3u8"}); err != nil {
		t.Fatalf("a working playlist was rejected: %v", err)
	}
	// A progressive file needs no playlist, so its first bytes are enough.
	progressive := newCheckMedia(t, "video/mp4", http.StatusOK, "\x00\x00\x00\x18ftypmp42", 0)
	if err := s.CheckAddonStream(ctx, AddonStream{URL: progressive.URL + "/film.mp4"}); err != nil {
		t.Fatalf("a working progressive file was rejected: %v", err)
	}

	refused := newCheckMedia(t, "", http.StatusForbidden, "", 0)
	if err := s.CheckAddonStream(ctx, AddonStream{URL: refused.URL + "/index.m3u8"}); err == nil {
		t.Fatal("a 403 feed passed its check")
	}

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>this link has expired</html>")
	}))
	t.Cleanup(page.Close)
	if err := s.CheckAddonStream(ctx, AddonStream{URL: page.URL + "/index.m3u8"}); err == nil {
		t.Fatal("an expired token served as a web page passed its check")
	}

	empty := newCheckMedia(t, "video/mp4", http.StatusOK, "", 0)
	if err := s.CheckAddonStream(ctx, AddonStream{URL: empty.URL + "/film.mp4"}); err == nil {
		t.Fatal("an empty feed passed its check")
	}

	if err := s.CheckAddonStream(ctx, AddonStream{URL: "not-a-url"}); err == nil {
		t.Fatal("a malformed link passed its check")
	}
}

// An addon's TV channel is tuned with the same rules as a catalog event: the
// best feed that starts wins, and a provider that refuses every check still
// reaches the player with its best link.
func TestResolveStremioPrefersAStreamThatStarts(t *testing.T) {
	working := newCheckMedia(t, "application/vnd.apple.mpegurl", http.StatusOK, "#EXTM3U\n", 0)
	dead := newCheckMedia(t, "", http.StatusForbidden, "", 0)
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"streams":[{"name":"Main","title":"2160p","url":"`+dead.URL+
			`/main.m3u8"},{"name":"Backup","title":"1080p","url":"`+working.URL+`/backup.m3u8"}]}`)
	}))
	t.Cleanup(addon.Close)
	s := New(&config.Config{}, t.TempDir())
	link, _, _, err := s.resolveStremio(context.Background(), &Channel{URL: addon.URL + "/stream/tv/ch:one.json", UserAgent: "Addon/1"})
	if err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if link != working.URL+"/backup.m3u8" {
		t.Fatalf("resolved the dead feed: %q", link)
	}

	// Every feed refuses the check: the best-ranked one is still returned, so a
	// provider that only serves its own client is not hidden from the viewer.
	onlyDead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"streams":[{"name":"Main","title":"2160p","url":"`+dead.URL+`/main.m3u8"}]}`)
	}))
	t.Cleanup(onlyDead.Close)
	link, _, _, err = s.resolveStremio(context.Background(), &Channel{URL: onlyDead.URL + "/stream/tv/ch:one.json", UserAgent: "Addon/1"})
	if err != nil || link != dead.URL+"/main.m3u8" {
		t.Fatalf("fallback = %q, %v", link, err)
	}
}

// A feed that hangs is given a bounded window and then reported as a failure,
// so automatic playback can move on instead of waiting indefinitely.
func TestCheckAddonStreamTimesOut(t *testing.T) {
	slow := newCheckMedia(t, "application/vnd.apple.mpegurl", http.StatusOK, "#EXTM3U\n", 2*time.Second)
	s := New(&config.Config{}, t.TempDir())
	s.AddonCheckWait = 200 * time.Millisecond
	start := time.Now()
	err := s.CheckAddonStream(context.Background(), AddonStream{URL: slow.URL + "/index.m3u8"})
	if err == nil {
		t.Fatal("a hanging feed passed its check")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the check waited %s on a dead feed", elapsed)
	}
}
