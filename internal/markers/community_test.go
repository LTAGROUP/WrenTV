package markers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

type memCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *memCache) CacheGet(_ context.Context, key string, _ int64) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.m[key]
	return b, ok
}

func (c *memCache) CachePut(_ context.Context, key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = body
}

func TestCommunityLookup(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if q.Get("imdb_id") != "tt0903747" || q.Get("season") != "1" || q.Get("episode") != "2" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"intro":{"start_sec":60,"end_sec":105,"confidence":0.9},"outro":{"start_sec":2700,"end_sec":null},"recap":null}`))
	}))
	defer srv.Close()
	old := communityBase
	communityBase = srv.URL
	defer func() { communityBase = old }()

	c := NewCommunity(&memCache{m: map[string][]byte{}}, nil)
	ctx := context.Background()
	m, err := c.Lookup(ctx, "tt0903747", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if m.Intro == nil || m.Intro.Start != 60 || m.Intro.End != 105 || m.Credits == nil || m.Credits.Start != 2700 || m.Source != "community" {
		t.Fatalf("got %+v", m)
	}
	// An open outro runs to the end of this file.
	if f := Fit(m, 2800); f.Credits == nil || f.Credits.End != 2800 {
		t.Fatalf("fit: %+v", f.Credits)
	}
	// Timed on a longer cut: the credits can't be in this file.
	if f := Fit(m, 2000); f.Credits != nil || f.Intro == nil {
		t.Fatalf("fit short: %+v", f)
	}
	// Cached, including "nothing timed".
	c.Lookup(ctx, "tt0903747", 1, 2)
	if m, _ := c.Lookup(ctx, "tt0000001", -1, -1); !m.Empty() {
		t.Fatalf("missing title: %+v", m)
	}
	c.Lookup(ctx, "tt0000001", -1, -1)
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d requests, want 2", n)
	}
}
