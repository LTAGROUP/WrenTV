package omdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type memCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *memCache) CacheGet(_ context.Context, k string, _ int64) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.m[k]
	return b, ok
}

func (c *memCache) CachePut(_ context.Context, k string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = b
}

func TestByIMDB(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("apikey") != "k" || r.URL.Query().Get("i") != "tt0133093" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"Title":"The Matrix","imdbRating":"8.7","imdbVotes":"2,123,456","Metascore":"73",
			"Ratings":[{"Source":"Internet Movie Database","Value":"8.7/10"},{"Source":"Rotten Tomatoes","Value":"83%"},
			{"Source":"Metacritic","Value":"73/100"}],"Response":"True"}`))
	}))
	defer srv.Close()
	base = srv.URL + "/"
	c := New(func() string { return "k" }, &memCache{m: map[string][]byte{}})
	for range 2 {
		r, err := c.ByIMDB(context.Background(), "tt0133093")
		if err != nil {
			t.Fatal(err)
		}
		if r.IMDb != 8.7 || r.IMDbVotes != 2123456 || r.RottenTomatoes != 83 || r.Metacritic != 73 {
			t.Fatalf("ratings = %+v", r)
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", calls)
	}
}

func TestDisabledAndMissing(t *testing.T) {
	c := New(func() string { return "" }, &memCache{m: map[string][]byte{}})
	if c.Enabled() {
		t.Fatal("enabled without a key")
	}
	if _, err := c.ByIMDB(context.Background(), "tt1"); err == nil {
		t.Fatal("no error without a key")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Response":"False","Error":"Incorrect IMDb ID."}`))
	}))
	defer srv.Close()
	base = srv.URL + "/"
	c = New(func() string { return "k" }, &memCache{m: map[string][]byte{}})
	if _, err := c.ByIMDB(context.Background(), "tt9"); err == nil {
		t.Fatal("expected an error")
	}
}
