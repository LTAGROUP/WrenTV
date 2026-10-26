package livetv

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// Provider headers are narrowed to a display identity: an addon may ask for a
// referer or an origin, never for a cookie, a token or a header it names itself.
func TestSanitizeRequestHeadersKeepsOnlyTheAllowlist(t *testing.T) {
	got := SanitizeRequestHeaders(map[string]string{
		"user-agent":      " Player/2 ",
		"Referer":         "https://provider.example/watch",
		"ORIGIN":          "https://provider.example",
		"Cookie":          "sid=secret",
		"Authorization":   "Bearer secret",
		"X-Api-Key":       "secret",
		"X-Forwarded-For": "10.0.0.1",
	})
	if len(got) != 3 {
		t.Fatalf("headers = %+v", got)
	}
	if got["User-Agent"] != "Player/2" {
		t.Fatalf("user agent = %q", got["User-Agent"])
	}
	if got["Referer"] != "https://provider.example/watch" || got["Origin"] != "https://provider.example" {
		t.Fatalf("identity = %+v", got)
	}
	for _, name := range []string{"Cookie", "Authorization", "X-Api-Key", "X-Forwarded-For"} {
		if _, ok := got[name]; ok {
			t.Fatalf("%s was forwarded: %+v", name, got)
		}
	}
	if SanitizeRequestHeaders(nil) != nil || SanitizeRequestHeaders(map[string]string{}) != nil {
		t.Fatal("an empty set must stay nil")
	}
	if got := SanitizeRequestHeaders(map[string]string{"Cookie": "sid=secret"}); got != nil {
		t.Fatalf("credential-only set = %+v", got)
	}
}

// A newline in a value would add a header or split the upstream request, so the
// value is dropped instead of repaired. Overlong values are dropped too.
func TestSanitizeRequestHeadersRejectsUnsafeValues(t *testing.T) {
	got := SanitizeRequestHeaders(map[string]string{
		"Referer":    "https://provider.example/\r\nCookie: sid=secret",
		"Origin":     "https://provider.example/\nX-Injected: 1",
		"User-Agent": strings.Repeat("a", maxRequestHeaderValue+1),
	})
	if got != nil {
		t.Fatalf("unsafe headers survived: %+v", got)
	}
	if got := SanitizeRequestHeaders(map[string]string{"Referer": "  "}); got != nil {
		t.Fatalf("blank header survived: %+v", got)
	}
	// A value at the cap is still usable.
	long := "https://provider.example/" + strings.Repeat("a", maxRequestHeaderValue-len("https://provider.example/"))
	if got := SanitizeRequestHeaders(map[string]string{"Referer": long}); got["Referer"] != long {
		t.Fatal("a value at the cap was dropped")
	}
}

// Playback sends the identity the provider asked for and nothing else, so a
// provider that refuses anonymous requests is played the same way it was
// probed.
func TestPlayURLWithHeadersForwardsOnlySanitizedHeaders(t *testing.T) {
	type seen struct{ referer, origin, cookie, agent string }
	var got seen
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = seen{
			referer: r.Header.Get("Referer"),
			origin:  r.Header.Get("Origin"),
			cookie:  r.Header.Get("Cookie"),
			agent:   r.Header.Get("User-Agent"),
		}
		if r.Header.Get("Referer") != "https://provider.example/watch" {
			http.Error(w, "referer required", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
	}))
	defer upstream.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, t.TempDir())
	sess, err := s.PlayURLWithHeaders(context.Background(), "u", "addon:x", upstream.URL+"/live.m3u8", "Player/2", map[string]string{
		"Referer": "https://provider.example/watch",
		"Origin":  "https://provider.example",
		"Cookie":  "sid=secret",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(sess.ID, "u", false)

	rec := httptest.NewRecorder()
	sess.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live/x/index.m3u8", nil), "index.m3u8")
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist through the proxy = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("playlist body = %q", rec.Body.String())
	}
	if got.referer != "https://provider.example/watch" || got.origin != "https://provider.example" {
		t.Fatalf("identity not forwarded: %+v", got)
	}
	if got.cookie != "" {
		t.Fatalf("credential forwarded: %+v", got)
	}
	if got.agent != "Player/2" {
		t.Fatalf("agent = %q", got.agent)
	}
}
