package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
	"github.com/LTAGROUP/wrentv/internal/simkl"
	"github.com/LTAGROUP/wrentv/internal/streaming"
)

func TestMDBListPath(t *testing.T) {
	for in, want := range map[string]string{
		"garycrawfordgc/latest-tv-shows":                           "/lists/garycrawfordgc/latest-tv-shows",
		"https://mdblist.com/lists/garycrawfordgc/latest-tv-shows": "/lists/garycrawfordgc/latest-tv-shows",
		"mdblist.com/lists/a/b/":                                   "",
		"https://www.mdblist.com/lists/a.b/c_d/json":               "/lists/a.b/c_d",
		"not a list": "",
	} {
		got, _ := mdblistPath(in)
		if got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestLibraryRowsFromExternalLists(t *testing.T) {
	ctx := context.Background()
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lists/someone/best-of/json":
			w.Write([]byte(`[{"id":42,"title":"Have It","mediatype":"movie","release_year":2001},
				{"id":7,"title":"A Show","mediatype":"show","release_year":2010},{"id":0,"title":"No ID","mediatype":"movie"}]`))
		case "/discover/trending/tv/week_100.json":
			w.Write([]byte(`[{"title":"Trend","ids":{"simkl_id":1,"tmdb":"99"}},{"title":"No TMDB","ids":{"simkl_id":2}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ext.Close()
	oldMDB, oldTrend := mdblistBase, simkl.TrendingURL
	mdblistBase = ext.URL
	simkl.TrendingURL = func(kind, interval string) string {
		return ext.URL + "/discover/trending/" + kind + "/" + interval + "_100.json"
	}
	defer func() { mdblistBase, simkl.TrendingURL = oldMDB, oldTrend }()

	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u := &db.User{Username: "u", AllowStreaming: false, AllowDownloads: -1, MaxStreams: -1, MaxDownloads: -1}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib := &db.Library{Name: "Movies", Type: "movies", Paths: []string{t.TempDir()}}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	movie := &db.Item{LibraryID: lib.ID, Kind: "movie", Title: "Have It", TMDBID: 42}
	if err := d.InsertItem(ctx, movie); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertFile(ctx, &db.MediaFile{ItemID: movie.ID, LibraryID: lib.ID, Path: "/x/have.mkv", Size: 1}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	tm := tmdb.New("key", "en-US", cannedTMDB{})
	s := &Server{Cfg: cfg, DB: d, TMDB: tm, Streaming: streaming.New(streaming.Deps{Cfg: cfg, DB: d, TMDB: tm})}
	call := func(h http.HandlerFunc, method, target, body, libID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", libID)
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
		req = req.WithContext(context.WithValue(ctx, principalKey, &auth.Principal{User: u, Role: "owner"}))
		w := httptest.NewRecorder()
		h(w, req)
		return w
	}

	var made []db.Collection
	for _, body := range []string{
		`{"kind":"tmdb","rules":{"source":"mdblist","sourceRef":"https://mdblist.com/lists/someone/best-of"},"libraryId":"` + lib.ID + `"}`,
		`{"kind":"tmdb","rules":{"source":"simkl","sourceRef":"trending/tv/week"},"libraryId":"` + lib.ID + `","showMissing":true}`,
	} {
		w := call(s.handleCreateCollection, "POST", "/api/collections", body, "")
		if w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body)
		}
		var c db.Collection
		_ = json.Unmarshal(w.Body.Bytes(), &c)
		made = append(made, c)
	}
	if made[0].Name != "Best Of" || made[1].Name != "Trending Shows This Week" {
		t.Errorf("names: %q, %q", made[0].Name, made[1].Name)
	}
	if w := call(s.handleCreateCollection, "POST", "/api/collections", `{"kind":"tmdb","rules":{"source":"mdblist","sourceRef":"nope"}}`, ""); w.Code != 400 {
		t.Errorf("bad MDBList ref: %d", w.Code)
	}

	// Simkl's chart first, then MDBList's.
	if w := call(s.handleLibraryRowsOrder, "PUT", "/", `{"ids":["`+made[1].ID+`","`+made[0].ID+`"]}`, lib.ID); w.Code != 204 {
		t.Fatalf("order: %d %s", w.Code, w.Body)
	}
	w := call(s.handleLibraryRows, "GET", "/", "", lib.ID)
	var rows []struct {
		Collection db.Collection `json:"collection"`
		Items      []*db.Item    `json:"items"`
		Missing    []titleView   `json:"missing"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err, w.Body)
	}
	if len(rows) != 2 || rows[0].Collection.ID != made[1].ID || rows[1].Collection.ID != made[0].ID {
		t.Fatalf("rows: %s", w.Body)
	}
	if len(rows[0].Missing) != 1 || rows[0].Missing[0].TMDBID != 99 || rows[0].Missing[0].MediaType != "show" {
		t.Errorf("simkl row: %+v", rows[0].Missing)
	}
	if len(rows[1].Items) != 1 || rows[1].Items[0].ID != movie.ID || len(rows[1].Missing) != 0 {
		t.Errorf("mdblist row: items %d missing %d", len(rows[1].Items), len(rows[1].Missing))
	}

	// Taking one off the list turns its row off.
	call(s.handleLibraryRowsOrder, "PUT", "/", `{"ids":["`+made[0].ID+`"]}`, lib.ID)
	if c, _ := d.GetCollection(ctx, made[1].ID); c.ShowInLibrary {
		t.Error("row still on")
	}
}
