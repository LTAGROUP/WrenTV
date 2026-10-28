package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type noCache struct{}

func (noCache) CacheGet(context.Context, string, int64) ([]byte, bool) { return nil, false }
func (noCache) CachePut(context.Context, string, []byte)               {}

// A lookup shared with a caller that gives up must still answer the callers
// that joined it: a shelf's warming timing out can't fail the title opened.
func TestSharedLookupOutlivesFirstCaller(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"id":1,"title":"Conviction"}`))
	}))
	defer srv.Close()
	old := base
	base = srv.URL
	defer func() { base = old }()
	c := New("key", "en-US", noCache{})

	first, cancel := context.WithTimeout(Prefetch(Interactive(context.Background())), 50*time.Millisecond)
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		_, err := c.Movie(first, 1)
		errs <- err
	}()
	time.Sleep(10 * time.Millisecond)
	m, err := c.Movie(Interactive(context.Background()), 1)
	if err != nil {
		t.Fatalf("joined lookup failed: %v", err)
	}
	if m.Title != "Conviction" {
		t.Fatalf("title %q", m.Title)
	}
	if err := <-errs; err == nil {
		t.Error("the caller that gave up got an answer")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("%d upstream requests, want 1", n)
	}
}

func TestServiceNames(t *testing.T) {
	for _, c := range []struct {
		name, service string
		want          bool
	}{
		{"Paramount+ Amazon Channel", "paramount plus", true},
		{"Paramount Plus Premium", "paramount plus", true},
		{"Paramount Plus", "paramount plus", true},
		{"Paramount+ with Showtime", "paramount plus", true},
		{"Max", "max", true},
		{"Max Amazon Channel", "max", true},
		{"MaxdomeX", "max", false},
		{"Netflix basic with Ads", "netflix", true},
		{"Peacock Premium Plus", "peacock", true},
		{"Disney Plus", "disney plus", true},
		{"Hulu", "netflix", false},
	} {
		if got := sameService(providerName(c.name), c.service); got != c.want {
			t.Errorf("sameService(%q, %q) = %v", c.name, c.service, got)
		}
	}
}
