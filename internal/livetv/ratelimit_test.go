package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// rateLimitAddon is a small Stremio addon whose catalog can be switched between
// answering, refusing with 429 and answering with nothing at all. Its media is
// served from an extensionless path, so measuring a stream's type really costs
// a request. Every request path is recorded, so a test can prove how often a
// provider was asked for what.
type rateLimitAddon struct {
	server *httptest.Server
	mode   atomic.Value // "ok", "limited", "empty"
	// failFirst refuses that many initial catalog requests with the fixture's
	// Retry-After, so a short wait can be observed being honoured.
	failFirst    atomic.Int32
	catalogHits  atomic.Int32
	mediaHits    atomic.Int32
	manifestHits atomic.Int32
	retryAfter   string

	mu    sync.Mutex
	paths []string
}

func newRateLimitAddon(t *testing.T, retryAfter string) *rateLimitAddon {
	t.Helper()
	f := &rateLimitAddon{retryAfter: retryAfter}
	f.mode.Store("ok")
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifest.json"):
			f.manifestHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, sportAddonManifest)
		case strings.HasPrefix(r.URL.Path, "/catalog/sport/live"):
			f.catalogHits.Add(1)
			if f.failFirst.Load() > 0 && f.catalogHits.Load() <= f.failFirst.Load() {
				if f.retryAfter != "" {
					w.Header().Set("Retry-After", f.retryAfter)
				}
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch f.mode.Load() {
			case "limited":
				if f.retryAfter != "" {
					w.Header().Set("Retry-After", f.retryAfter)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			case "empty":
				_, _ = io.WriteString(w, `{"metas":[]}`)
			default:
				_, _ = io.WriteString(w, `{"metas":[{"id":"e1","name":"Final","genres":["Football"]}]}`)
			}
		case strings.HasPrefix(r.URL.Path, "/meta/sport/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"meta":{"id":"e1","type":"sport","name":"Final","description":"A final"}}`)
		case strings.HasPrefix(r.URL.Path, "/stream/sport/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"streams":[{"name":"Main","title":"1080p","url":%q}]}`, f.server.URL+"/media")
		case r.URL.Path == "/media":
			f.mediaHits.Add(1)
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *rateLimitAddon) source(id, kind string) config.LiveTVSource {
	return config.LiveTVSource{ID: id, Name: id, Kind: kind, URL: f.server.URL + "/manifest.json", Enabled: true}
}

func (f *rateLimitAddon) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// One playback action — the item's metadata and its streams — asks a
// request-counting provider for its manifest once. Before the manifest was
// reused, each of those read it again.
func TestAddonManifestIsReadOncePerViewerAction(t *testing.T) {
	f := newRateLimitAddon(t, "")
	s := New(&config.Config{}, t.TempDir())
	src := f.source("sport", "sport")
	ctx := context.Background()

	items, err := s.AddonItems(ctx, src, "sport", "live", "", 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if _, err := s.AddonMeta(ctx, src, "sport", "e1"); err != nil {
		t.Fatal(err)
	}
	streams, err := s.AddonStreams(ctx, src, "sport", "e1")
	if err != nil || len(streams) != 1 {
		t.Fatalf("streams = %+v, %v", streams, err)
	}
	if got := f.manifestHits.Load(); got != 1 {
		t.Fatalf("manifest requests = %d, want 1 (%v)", got, f.requests())
	}
	// The resources are still read: the cache is the manifest, never the content.
	for _, want := range []string{"/catalog/sport/live.json", "/meta/sport/e1.json", "/stream/sport/e1.json"} {
		found := false
		for _, path := range f.requests() {
			if path == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s was not requested: %v", want, f.requests())
		}
	}
}

// The settings page, the admin Check and the browse routes ask what an addon
// offers now, so they read the manifest again instead of reusing a cached one.
func TestAddonCapabilitiesReadsTheManifestAgain(t *testing.T) {
	f := newRateLimitAddon(t, "")
	s := New(&config.Config{}, t.TempDir())
	src := f.source("sport", "sport")
	ctx := context.Background()

	if _, err := s.AddonItems(ctx, src, "sport", "live", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddonCapabilities(ctx, src); err != nil {
		t.Fatal(err)
	}
	if got := f.manifestHits.Load(); got != 2 {
		t.Fatalf("manifest requests = %d, want a fresh read for the capabilities", got)
	}
}

// A forced refresh drops the cached manifests: the admin asked again, so the
// addon's own answer is what the settings page shows.
func TestForcedRefreshForgetsCachedManifests(t *testing.T) {
	f := newRateLimitAddon(t, "")
	cfg := &config.Config{LiveTV: config.LiveTVConfig{Enabled: true, Sources: []config.LiveTVSource{f.source("sport", "sport")}}}
	s := New(cfg, t.TempDir())
	ctx := context.Background()

	if _, err := s.AddonItems(ctx, f.source("sport", "sport"), "sport", "live", "", 0); err != nil {
		t.Fatal(err)
	}
	before := f.manifestHits.Load()
	s.Refresh(true)
	s.refresh(ctx, true)
	if f.manifestHits.Load() <= before {
		t.Fatal("a forced refresh reused the cached manifest")
	}
}

// A short Retry-After is waited out and the request is made once more, so a
// momentary burst does not surface as a provider failure.
func TestAddonRateLimitIsRetriedOnceWhenTheWaitIsShort(t *testing.T) {
	f := newRateLimitAddon(t, "1")
	f.failFirst.Store(1)
	s := New(&config.Config{}, t.TempDir())
	src := f.source("sport", "sport")

	start := time.Now()
	items, err := s.AddonItems(context.Background(), src, "sport", "live", "", 0)
	if err != nil {
		t.Fatalf("a short rate limit was not retried: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("the retry did not wait out the provider's Retry-After (%s)", elapsed)
	}
	if got := f.catalogHits.Load(); got != 2 {
		t.Fatalf("catalog requests = %d, want the one refusal and one retry", got)
	}
}

// A rate limit asking for longer than a request may wait is reported at once:
// it is not retried, and the wait the provider asked for is carried in the
// error so the viewer can be told when to come back.
func TestAddonRateLimitIsReportedWithoutRetrying(t *testing.T) {
	f := newRateLimitAddon(t, "30")
	f.mode.Store("limited")
	s := New(&config.Config{}, t.TempDir())
	src := f.source("sport", "sport")

	_, err := s.AddonItems(context.Background(), src, "sport", "live", "", 0)
	if !errors.Is(err, ErrAddonRateLimited) {
		t.Fatalf("err = %v, want ErrAddonRateLimited", err)
	}
	limit, ok := RateLimited(err)
	if !ok || limit.Status != http.StatusTooManyRequests || limit.RetryAfter != 30*time.Second {
		t.Fatalf("limit = %+v", limit)
	}
	if !strings.Contains(err.Error(), "asked to wait 30s") {
		t.Fatalf("the message doesn't say when to come back: %v", err)
	}
	if got := f.catalogHits.Load(); got != 1 {
		t.Fatalf("a rate limit was requested %d times", got)
	}
}

// A server error that says nothing about waiting is an ordinary temporary
// failure, never reported as a rate limit.
func TestAddonServerErrorIsNotARateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			_, _ = io.WriteString(w, sportAddonManifest)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	s := New(&config.Config{}, t.TempDir())
	src := config.LiveTVSource{ID: "sport", Name: "sport", Kind: "sport", URL: server.URL + "/manifest.json", Enabled: true}

	_, err := s.AddonItems(context.Background(), src, "sport", "live", "", 0)
	if !errors.Is(err, ErrAddonUnreachable) {
		t.Fatalf("err = %v, want ErrAddonUnreachable", err)
	}
	if errors.Is(err, ErrAddonRateLimited) {
		t.Fatalf("a plain 503 was reported as a rate limit: %v", err)
	}
}

// Retry-After is honoured in both forms the specification allows.
func TestRetryAfterHeaderForms(t *testing.T) {
	seconds := &http.Response{Header: http.Header{"Retry-After": []string{"45"}}}
	if got := retryAfter(seconds); got != 45*time.Second {
		t.Fatalf("seconds form = %s", got)
	}
	date := &http.Response{Header: http.Header{"Retry-After": []string{time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)}}}
	if got := retryAfter(date); got <= 0 || got > time.Minute {
		t.Fatalf("date form = %s", got)
	}
	none := &http.Response{Header: http.Header{}}
	if got := retryAfter(none); got != 0 {
		t.Fatalf("no header = %s", got)
	}
}

// A catalog read that failed for a reason that may pass leaves the provider's
// last known entries listed, so a rate limit or an outage never reads as "this
// addon has nothing". An addon that really answers with nothing does clear
// them, and content comes back when the addon offers it again.
func TestRefreshKeepsLastKnownEventsWhenTheCatalogFailsTemporarily(t *testing.T) {
	f := newRateLimitAddon(t, "30")
	cfg := &config.Config{LiveTV: config.LiveTVConfig{Enabled: true, Sources: []config.LiveTVSource{f.source("sport", "sport")}}}
	s := New(cfg, t.TempDir())
	ctx := context.Background()

	s.refresh(ctx, true)
	if got := s.EventsFor("sport"); len(got) != 1 || got[0].Name != "Final" {
		t.Fatalf("first refresh events = %+v", got)
	}
	if st := s.Status().Sources[0]; st.Warning != "" {
		t.Fatalf("a healthy refresh warned: %+v", st)
	}

	// The provider starts refusing this server.
	f.mode.Store("limited")
	s.refresh(ctx, true)
	kept := s.EventsFor("sport")
	if len(kept) != 1 || kept[0].Name != "Final" {
		t.Fatalf("a rate limit emptied the provider's list: %+v", kept)
	}
	st := s.Status().Sources[0]
	if !strings.Contains(st.Warning, "last known") {
		t.Fatalf("the retention was not reported: %+v", st)
	}
	// The failure is still reported as a problem: keeping the entries must not
	// hide that the catalog could not be read.
	if len(s.Status().Errors) == 0 {
		t.Fatal("the unreadable catalog was not reported")
	}

	// The addon answers again, with nothing at all: that is a real empty list.
	f.mode.Store("empty")
	s.refresh(ctx, true)
	if got := s.EventsFor("sport"); len(got) != 0 {
		t.Fatalf("an empty catalog kept stale events: %+v", got)
	}
	if st := s.Status().Sources[0]; st.Warning != "" {
		t.Fatalf("an empty catalog warned about retention: %+v", st)
	}

	f.mode.Store("ok")
	s.refresh(ctx, true)
	if got := s.EventsFor("sport"); len(got) != 1 {
		t.Fatalf("events after recovery = %+v", got)
	}
}

// The stream-type cache belongs to channels whose URL is fixed. A key that
// names an addon feed is resolved again for every attempt, so remembering its
// kind would both hand the next attempt a stale type and add a map entry per
// feed for as long as the process runs.
func TestAddonFeedKeysDoNotRememberStreamKind(t *testing.T) {
	f := newRateLimitAddon(t, "")
	s := New(&config.Config{LiveTV: config.LiveTVConfig{MaxStreams: 8}}, t.TempDir())
	ctx := context.Background()

	// A playlist channel's type is measured once and reused.
	for i := 0; i < 2; i++ {
		sess, err := s.PlayURLWithHeaders(ctx, "viewer", "chan-1", f.server.URL+"/media", "", nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if sess.Type() != "hls" {
			t.Fatalf("channel type = %q", sess.Type())
		}
		s.Stop(sess.ID, "viewer", false)
	}
	if got := f.mediaHits.Load(); got != 1 {
		t.Fatalf("a channel's type was measured %d times, want once", got)
	}

	// An addon feed resolved for each attempt is measured every time, and adds
	// nothing to the cache.
	for i := 0; i < 2; i++ {
		key := fmt.Sprintf("addons:feed-%d", i)
		sess, err := s.PlayURLWithHeaders(ctx, "viewer", key, f.server.URL+"/media", "", nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if sess.Type() != "hls" {
			t.Fatalf("feed type = %q", sess.Type())
		}
		s.Stop(sess.ID, "viewer", false)
	}
	if got := f.mediaHits.Load(); got != 3 {
		t.Fatalf("media requests = %d, want one per addon tune", got)
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	if _, polluted := s.sessions.kinds["addons:feed-0"]; polluted || len(s.sessions.kinds) != 1 {
		t.Fatalf("addon feed keys were cached: %v", s.sessions.kinds)
	}
}
