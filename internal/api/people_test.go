package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// personTMDB is one actor: two movies (one twice, as two characters), a
// show and a talk show appearance.
type personTMDB struct{}

func (personTMDB) CacheGet(_ context.Context, key string, _ int64) ([]byte, bool) {
	if strings.TrimPrefix(strings.SplitN(key, "?", 2)[0], "tmdb:") != "/person/7" {
		return nil, false
	}
	return []byte(`{"id":7,"name":"Ann Actor","biography":"Born somewhere.","birthday":"1970-02-03","known_for_department":"Acting",
		"combined_credits":{"cast":[
			{"id":10,"media_type":"movie","title":"Old Film","release_date":"1999-05-01","poster_path":"/a.jpg","vote_count":900,"character":"Jane"},
			{"id":10,"media_type":"movie","title":"Old Film","release_date":"1999-05-01","poster_path":"/a.jpg","vote_count":900,"character":"June"},
			{"id":11,"media_type":"movie","title":"New Film","release_date":"2023-01-01","poster_path":"/b.jpg","vote_count":50,"character":"Lead"},
			{"id":20,"media_type":"tv","name":"A Show","first_air_date":"2010-01-01","poster_path":"/c.jpg","vote_count":300,"character":"Detective","genre_ids":[18]},
			{"id":30,"media_type":"tv","name":"Late Talk","first_air_date":"2005-01-01","vote_count":10,"character":"Self","genre_ids":[10767]}
		],"crew":[]}}`), true
}

func (personTMDB) CachePut(context.Context, string, []byte) {}

func TestPerson(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u := &db.User{Username: "u", AllowDownloads: -1, MaxStreams: -1, MaxDownloads: -1}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib := &db.Library{Name: "Movies", Type: "movies", Paths: []string{t.TempDir()}}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	old := &db.Item{LibraryID: lib.ID, Kind: "movie", Title: "Old Film", MatchKey: "old", TMDBID: 10}
	if err := d.InsertItem(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertFile(ctx, &db.MediaFile{ItemID: old.ID, LibraryID: lib.ID, Path: "/movies/old.mkv"}); err != nil {
		t.Fatal(err)
	}

	s := &Server{Cfg: &config.Config{}, DB: d, TMDB: tmdb.New("key", "en-US", personTMDB{})}
	req := httptest.NewRequest("GET", "/api/people/7", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", "7")
	req = req.WithContext(context.WithValue(context.WithValue(req.Context(), chi.RouteCtxKey, rc), principalKey, &auth.Principal{User: u, Role: "owner"}))
	w := httptest.NewRecorder()
	s.handlePerson(w, req)
	var out personView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if out.Name != "Ann Actor" || out.Birthday != "1970-02-03" || out.Biography == "" {
		t.Errorf("details: %+v", out)
	}
	rows := map[string][]creditView{}
	var titles []string
	for _, r := range out.Rows {
		rows[r.Title] = r.Credits
		titles = append(titles, r.Title)
	}
	if strings.Join(titles, ",") != "Known for,In your library,Movies,TV Shows" {
		t.Fatalf("rows: %v", titles)
	}
	if k := rows["Known for"]; len(k) != 3 || k[0].Title != "Old Film" || k[0].Role != "Jane / June" || k[0].Item == nil || k[0].Item.ID != old.ID {
		t.Errorf("known for: %+v", k)
	}
	if m := rows["Movies"]; len(m) != 2 || m[0].Title != "New Film" || m[0].Item != nil {
		t.Errorf("movies, newest first: %+v", m)
	}
	if tv := rows["TV Shows"]; len(tv) != 1 || tv[0].Role != "Detective" || tv[0].MediaType != "show" {
		t.Errorf("shows, without talk shows: %+v", tv)
	}
	if lib := rows["In your library"]; len(lib) != 1 || lib[0].TMDBID != 10 {
		t.Errorf("in library: %+v", lib)
	}
}
