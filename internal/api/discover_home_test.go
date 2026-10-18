package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
	"github.com/LTAGROUP/wrentv/internal/streaming"
)

// cannedTMDB answers every TMDB request from the cache: a page of titles
// whose IDs depend on the path, so rows differ.
type cannedTMDB struct{}

func (cannedTMDB) CacheGet(_ context.Context, key string, _ int64) ([]byte, bool) {
	base := len(strings.SplitN(key, "?", 2)[0]) * 100
	var results []string
	for i := 1; i <= 8; i++ {
		id := base + i
		if strings.Contains(key, "/trending/all") && i == 1 {
			id = 42 // the movie in the library
		}
		results = append(results, fmt.Sprintf(`{"id":%d,"media_type":"movie","title":"T%d","poster_path":"/p.jpg","backdrop_path":"/b.jpg","overview":"o"}`, id, id))
	}
	body := `{"results":[` + strings.Join(results, ",") + `]}`
	if strings.Contains(key, "/movie/42?") {
		body = `{"id":42,"recommendations":{"results":[` + strings.Join(results, ",") + `]},"similar":{"results":[]}}`
	}
	return []byte(body), true
}

func (cannedTMDB) CachePut(context.Context, string, []byte) {}

func TestDiscoverHome(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u := &db.User{Username: "u", AllowStreaming: true, AllowDownloads: -1, MaxStreams: -1, MaxDownloads: -1}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib := &db.Library{Name: "Streaming Movies", Type: "movies", Source: "stream", Paths: []string{t.TempDir()}}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	movie := &db.Item{LibraryID: lib.ID, Kind: "movie", Title: "Watched", TMDBID: 42}
	if err := d.InsertItem(ctx, movie); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetProgress(ctx, u.ID, movie.ID, 600, 6000, 0.9); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	tm := tmdb.New("key", "en-US", cannedTMDB{})
	s := &Server{Cfg: cfg, DB: d, TMDB: tm, Streaming: streaming.New(streaming.Deps{Cfg: cfg, DB: d, TMDB: tm})}

	req := httptest.NewRequest("GET", "/api/discover/home", nil)
	req = req.WithContext(context.WithValue(req.Context(), principalKey, &auth.Principal{User: u, Role: "owner"}))
	w := httptest.NewRecorder()
	s.handleDiscoverHome(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var out struct {
		Enabled          bool          `json:"enabled"`
		ContinueWatching []*db.Item    `json:"continueWatching"`
		RecentlyWatched  []*db.Item    `json:"recentlyWatched"`
		Hero             []titleView   `json:"hero"`
		Rows             []discoverRow `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Enabled || len(out.ContinueWatching) != 1 || len(out.RecentlyWatched) != 1 {
		t.Fatalf("enabled=%v continue=%d recent=%d", out.Enabled, len(out.ContinueWatching), len(out.RecentlyWatched))
	}
	ids := map[string]bool{}
	for _, r := range out.Rows {
		ids[r.ID] = true
	}
	for _, want := range []string{"trending", "because-" + movie.ID, "popular-movies", "top-shows", "action", "documentary"} {
		if !ids[want] {
			t.Errorf("missing row %q (have %v)", want, ids)
		}
	}
	if out.Rows[0].ID != "trending" || out.Rows[0].Items[0].Item == nil || out.Rows[0].Items[0].Item.ID != movie.ID {
		t.Errorf("trending's first title should link to the library movie: %+v", out.Rows[0].Items[0])
	}
	if len(out.Hero) == 0 {
		t.Error("no hero titles")
	}
}

func TestDiscoverBrowse(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u := &db.User{Username: "u", AllowStreaming: true, AllowDownloads: -1, MaxStreams: -1, MaxDownloads: -1}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	tm := tmdb.New("key", "en-US", cannedTMDB{})
	s := &Server{Cfg: cfg, DB: d, TMDB: tm, Streaming: streaming.New(streaming.Deps{Cfg: cfg, DB: d, TMDB: tm})}
	req := httptest.NewRequest("GET", "/api/discover/browse?type=show&sort=top_rated&genre=18&page=2", nil)
	req = req.WithContext(context.WithValue(req.Context(), principalKey, &auth.Principal{User: u, Role: "owner"}))
	w := httptest.NewRecorder()
	s.handleDiscoverBrowse(w, req)
	var out struct {
		Items []titleView `json:"items"`
		Page  int         `json:"page"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if out.Page != 2 || len(out.Items) != 8 || out.Items[0].MediaType != "show" {
		t.Fatalf("page=%d items=%d first=%+v", out.Page, len(out.Items), out.Items)
	}
}
