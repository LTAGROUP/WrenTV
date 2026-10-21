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
	"github.com/LTAGROUP/wrentv/internal/streaming"
)

type layoutFixture struct {
	s     *Server
	lib   *db.Library
	owner *db.User
	user  *db.User
	mux   chi.Router
}

func newLayoutFixture(t *testing.T) *layoutFixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	f := &layoutFixture{}
	for i, name := range []string{"owner", "user"} {
		u := &db.User{Username: name, AllowStreaming: i == 0, AllowDownloads: -1, MaxStreams: -1, MaxDownloads: -1}
		if err := d.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.owner = u
		} else {
			f.user = u
		}
	}
	f.lib = &db.Library{Name: "Movies", Type: "movies", Source: "stream", Paths: []string{t.TempDir()}}
	if err := d.CreateLibrary(ctx, f.lib); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"A", "B", "C", "D"} {
		it := &db.Item{LibraryID: f.lib.ID, Kind: "movie", Title: title, MatchKey: title, Genres: []string{"Action"}, TMDBID: 42}
		if title != "A" {
			it.TMDBID = 0
		}
		if err := d.InsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{}
	tm := tmdb.New("key", "en-US", cannedTMDB{})
	f.s = &Server{Cfg: cfg, DB: d, TMDB: tm, Streaming: streaming.New(streaming.Deps{Cfg: cfg, DB: d, TMDB: tm})}
	f.mux = chi.NewRouter()
	f.mux.Get("/api/layouts/{scope}", f.s.handleLayout)
	f.mux.Get("/api/layouts/{scope}/config", f.s.handleLayoutConfig)
	f.mux.Put("/api/layouts/{scope}", f.s.handleSetLayout)
	f.mux.Delete("/api/layouts/{scope}", f.s.handleResetLayout)
	f.mux.Get("/api/hubs/{id}", f.s.handleHub)
	return f
}

func (f *layoutFixture) do(t *testing.T, u *db.User, method, path, body string, out any) int {
	t.Helper()
	role := "user"
	if u == f.owner {
		role = "owner"
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), principalKey, &auth.Principal{User: u, Role: role}))
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, req)
	if out != nil && w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code
}

type layoutResp struct {
	Rows   []layoutRow `json:"rows"`
	Custom bool        `json:"custom"`
}

func rowIDs(rows []layoutRow) []string {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestLayoutBuiltinDefaults(t *testing.T) {
	f := newLayoutFixture(t)
	var out layoutResp
	if code := f.do(t, f.user, "GET", "/api/layouts/home", "", &out); code != 200 {
		t.Fatalf("status %d", code)
	}
	ids := strings.Join(rowIDs(out.Rows), ",")
	for _, want := range []string{"trending", "recentlyAdded:" + f.lib.ID, "category:genres:all:"} {
		if !strings.Contains(ids, want) {
			t.Errorf("home missing %q: %s", want, ids)
		}
	}
	if code := f.do(t, f.user, "GET", "/api/layouts/library:nope", "", nil); code != 404 {
		t.Errorf("unknown library: %d", code)
	}
}

func TestLayoutDefaultAndOwn(t *testing.T) {
	f := newLayoutFixture(t)
	scope := "/api/layouts/library:" + f.lib.ID
	if code := f.do(t, f.user, "PUT", scope+"?target=default", `{"entries":[]}`, nil); code != 403 {
		t.Fatalf("user set default: %d", code)
	}
	if code := f.do(t, f.owner, "PUT", scope+"?target=default", `{"entries":[{"type":"shelf","ref":"unwatched"},{"type":"shelf","ref":"recentlyAdded","hidden":true}]}`, nil); code != 204 {
		t.Fatalf("set default: %d", code)
	}
	var out layoutResp
	f.do(t, f.user, "GET", scope, "", &out)
	if ids := rowIDs(out.Rows); len(ids) != 1 || ids[0] != "unwatched" || out.Custom {
		t.Fatalf("user should inherit the default: %v custom=%v", ids, out.Custom)
	}

	if code := f.do(t, f.user, "PUT", scope, `{"entries":[{"type":"shelf","ref":"recentlyAdded"}]}`, nil); code != 204 {
		t.Fatalf("set own: %d", code)
	}
	f.do(t, f.user, "GET", scope, "", &out)
	if ids := rowIDs(out.Rows); len(ids) != 1 || ids[0] != "recentlyAdded:"+f.lib.ID || !out.Custom {
		t.Fatalf("user's own layout: %v custom=%v", ids, out.Custom)
	}
	// The owner still sees the default.
	f.do(t, f.owner, "GET", scope, "", &out)
	if ids := rowIDs(out.Rows); len(ids) != 1 || ids[0] != "unwatched" {
		t.Fatalf("owner: %v", ids)
	}

	var cfg struct {
		Entries    []layoutOption `json:"entries"`
		Custom     bool           `json:"custom"`
		HasDefault bool           `json:"hasDefault"`
		Options    []layoutOption `json:"options"`
	}
	f.do(t, f.user, "GET", scope+"/config", "", &cfg)
	if !cfg.Custom || !cfg.HasDefault || len(cfg.Entries) != 1 || cfg.Entries[0].Title != "Recently Added" || len(cfg.Options) == 0 {
		t.Fatalf("config: %+v", cfg)
	}

	if code := f.do(t, f.user, "DELETE", scope, "", nil); code != 204 {
		t.Fatalf("reset: %d", code)
	}
	f.do(t, f.user, "GET", scope, "", &out)
	if ids := rowIDs(out.Rows); len(ids) != 1 || ids[0] != "unwatched" {
		t.Fatalf("after reset: %v", ids)
	}
	if code := f.do(t, f.user, "PUT", scope, `{"entries":[{"type":"bogus","ref":"x"}]}`, nil); code != 400 {
		t.Fatalf("bad entry: %d", code)
	}
}

func TestHubs(t *testing.T) {
	f := newLayoutFixture(t)
	var out struct {
		Hub  hubCard     `json:"hub"`
		Rows []layoutRow `json:"rows"`
	}
	if code := f.do(t, f.owner, "GET", "/api/hubs/genre-action", "", &out); code != 200 {
		t.Fatalf("status %d", code)
	}
	if out.Hub.Name != "Action" || len(out.Rows) == 0 {
		t.Fatalf("seeded hub: %+v rows=%d", out.Hub, len(out.Rows))
	}
	if code := f.do(t, f.user, "GET", "/api/hubs/genre%3Amovie%3A%3AAction", "", &out); code != 200 {
		t.Fatalf("genre hub status %d", code)
	}
	if ids := strings.Join(rowIDs(out.Rows), ","); !strings.Contains(ids, "recent") || len(out.Rows[0].Items) != 4 {
		t.Fatalf("genre hub rows: %s", ids)
	}
	if code := f.do(t, f.user, "GET", "/api/hubs/missing", "", nil); code != 404 {
		t.Fatalf("missing hub: %d", code)
	}
}

func TestLayoutStreamingLeavesOutLibraryShelves(t *testing.T) {
	f := newLayoutFixture(t)
	f.s.Cfg.Streaming = config.StreamingConfig{Enabled: true, TorBoxKey: "key", ProviderOrder: []string{"torbox"}}
	if !f.s.Streaming.Active() {
		t.Skip("streaming not active in the fixture")
	}
	libraryOnly := []string{"recentlyAdded", "topRated", "unwatched"}
	scope := "/api/layouts/library:" + f.lib.ID
	var out layoutResp
	// The owner streams; the user doesn't, so still sees what the server has.
	f.do(t, f.owner, "GET", scope, "", &out)
	for _, id := range rowIDs(out.Rows) {
		for _, ref := range libraryOnly {
			if strings.HasPrefix(id, ref) {
				t.Errorf("streaming page has %q", id)
			}
		}
	}
	f.do(t, f.user, "GET", scope, "", &out)
	if ids := strings.Join(rowIDs(out.Rows), ","); !strings.Contains(ids, "recentlyAdded:"+f.lib.ID) {
		t.Errorf("library page without streaming: %s", ids)
	}

	var hub struct {
		Rows []layoutRow `json:"rows"`
	}
	f.do(t, f.owner, "GET", "/api/hubs/genre%3Amovie%3A%3AAction", "", &hub)
	for _, row := range hub.Rows {
		if row.Kind == "items" {
			t.Errorf("streaming genre hub has library shelf %q", row.ID)
		}
	}
}

func TestDistinctArt(t *testing.T) {
	got := distinctArt([][]string{{"a", "b"}, {"a", "c"}, {"a"}, nil})
	if want := []string{"a", "c", "a", ""}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}
