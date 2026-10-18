package tracking

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/simkl"
)

type call struct {
	Path, Query string
	Body        map[string]any
}

type fakeSimkl struct {
	mu       sync.Mutex
	calls    []call
	polls    int
	approved bool
}

func (f *fakeSimkl) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		c := call{Path: r.URL.Path, Query: r.URL.RawQuery}
		_ = json.Unmarshal(b, &c.Body)
		if r.URL.Path != "/oauth2/token" && r.URL.Path != "/oauth2/device" && r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/oauth2/device":
			json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "ABCD-EFGH",
				"verification_uri": "https://simkl.com/pin", "verification_uri_complete": "https://simkl.com/pin?user_code=ABCD-EFGH",
				"expires_in": 900, "interval": 0})
			return
		case "/oauth2/token":
			f.polls++
			if f.polls < 2 {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"authorization_pending"}`)
				return
			}
			io.WriteString(w, `{"access_token":"tok","refresh_token":"rt","expires_in":604800}`)
			return
		case "/users/settings":
			io.WriteString(w, `{"user":{"name":"sam"}}`)
			return
		case "/sync/all-items/movies/completed":
			io.WriteString(w, `{"movies":[{"status":"completed","last_watched_at":"2024-01-02T03:04:05Z","movie":{"title":"Heat","year":1995,"ids":{"simkl":1,"tmdb":"949"}}},
				{"status":"completed","movie":{"title":"Not Here","year":2001,"ids":{"tmdb":"5"}}}]}`)
			return
		case "/sync/all-items/shows/watching":
			io.WriteString(w, `{"shows":[{"status":"watching","show":{"title":"Severance","year":2022,"ids":{"imdb":"tt11280740"}},
				"seasons":[{"number":1,"episodes":[{"number":1,"watched_at":"2024-02-01T00:00:00Z"},{"number":2}]}]}]}`)
			return
		case "/sync/all-items/shows/completed", "/sync/all-items/shows/hold", "/sync/all-items/shows/dropped",
			"/sync/all-items/anime/watching", "/sync/all-items/anime/completed", "/sync/all-items/anime/hold", "/sync/all-items/anime/dropped":
			io.WriteString(w, `{}`)
			return
		}
		f.calls = append(f.calls, c)
		io.WriteString(w, `{}`)
	}
}

func (f *fakeSimkl) got() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func setup(t *testing.T) (*Service, *fakeSimkl, *db.DB, *db.User, map[string]*db.Item) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u := &db.User{Username: "a", DisplayName: "Alex"}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib := &db.Library{Name: "All", Type: "movies"}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	items := map[string]*db.Item{
		"movie": {LibraryID: lib.ID, Kind: "movie", Title: "Heat", Year: 1995, TMDBID: 949, MatchKey: "heat"},
		"show":  {LibraryID: lib.ID, Kind: "show", Title: "Severance", Year: 2022, TMDBID: 95396, IMDBID: "tt11280740", MatchKey: "sev"},
	}
	for _, k := range []string{"movie", "show"} {
		if err := d.InsertItem(ctx, items[k]); err != nil {
			t.Fatal(err)
		}
	}
	for i, k := range []string{"e1", "e2", "e3"} {
		e := &db.Item{LibraryID: lib.ID, Kind: "episode", ShowID: items["show"].ID, ParentID: items["show"].ID,
			Title: k, SeasonNumber: 1, EpisodeNumber: i + 1, MatchKey: "sev" + k}
		if err := d.InsertItem(ctx, e); err != nil {
			t.Fatal(err)
		}
		items[k] = e
	}
	f := &fakeSimkl{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	sk := simkl.New(func() string { return "cid" }, "v1.2")
	sk.Base = srv.URL
	s := New(d, sk, nil, nil)
	s.pollEvery = 10 * time.Millisecond
	return s, f, d, u, items
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestScrobbleLifecycle(t *testing.T) {
	s, f, d, u, items := setup(t)
	ctx := context.Background()
	if err := d.LinkSimkl(ctx, &db.SimklAccount{UserID: u.ID, AccessToken: "tok", RefreshToken: "rt", ExpiresAt: time.Now().Add(7 * 24 * time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	var clockMu sync.Mutex
	clock := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	ep := items["e2"]
	tick := func(pos float64, d time.Duration, watched bool) {
		advance(d)
		s.Progress(ctx, u, ep, pos, 1000, 0.85, watched, "Test")
	}
	tick(100, 0, false)              // start
	tick(110, 10*time.Second, false) // playing: nothing
	tick(110, 10*time.Second, false) // didn't move: pause
	tick(130, 60*time.Second, false) // moved: resume
	tick(860, 10*time.Second, true)  // crossed 85%: stop (marks watched)
	tick(900, 10*time.Second, false) // credits: nothing
	s.Stopped(u.ID, ep.ID)           // already scrobbled: nothing
	waitFor(t, func() bool { return len(f.got()) >= 4 })
	time.Sleep(50 * time.Millisecond)
	c := f.got()
	var paths []string
	for _, x := range c {
		paths = append(paths, x.Path)
	}
	if strings.Join(paths, ",") != "/scrobble/start,/scrobble/pause,/scrobble/start,/scrobble/stop" {
		t.Fatalf("calls = %v", paths)
	}
	stop := c[3]
	if !strings.Contains(stop.Query, "min_progress=85") || !strings.Contains(stop.Query, "app-name=wrentv") {
		t.Fatalf("stop query %q", stop.Query)
	}
	if stop.Body["progress"].(float64) != 86 {
		t.Fatalf("stop progress %v", stop.Body["progress"])
	}
	show := stop.Body["show"].(map[string]any)["ids"].(map[string]any)
	epi := stop.Body["episode"].(map[string]any)
	if show["imdb"] != "tt11280740" || show["tmdb"] != "95396" || epi["season"].(float64) != 1 || epi["number"].(float64) != 2 {
		t.Fatalf("stop body %v", stop.Body)
	}

	// A play that's abandoned is stopped (saved as paused on Simkl) after silence.
	s.Progress(ctx, u, items["movie"], 600, 6000, 0.85, false, "Test")
	advance(StopAfter)
	s.sweep()
	waitFor(t, func() bool { return len(f.got()) >= 6 })
	c = f.got()
	if c[4].Path != "/scrobble/start" || c[5].Path != "/scrobble/stop" || c[5].Body["progress"].(float64) != 10 {
		t.Fatalf("abandoned play calls %v %v", c[4], c[5])
	}

	// Marking by hand updates Simkl's history.
	s.Marked(ctx, u, items["show"], true)
	waitFor(t, func() bool { return len(f.got()) >= 7 })
	if c = f.got(); c[6].Path != "/sync/history" {
		t.Fatalf("mark call %v", c[6])
	}

	// Scrobbling off: nothing is sent.
	_ = d.SetSimklScrobble(ctx, u.ID, false)
	s.Progress(ctx, u, items["e3"], 10, 1000, 0.85, false, "Test")
	time.Sleep(100 * time.Millisecond)
	if n := len(f.got()); n != 7 {
		t.Fatalf("sent %d calls with scrobbling off", n)
	}
}

func TestLinkAndImport(t *testing.T) {
	s, _, d, u, items := setup(t)
	ctx := context.Background()
	p, err := s.LinkSimkl(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserCode != "ABCD-EFGH" || !strings.Contains(p.URLFull, "user_code") {
		t.Fatalf("pending %+v", p)
	}
	waitFor(t, func() bool {
		a, _ := d.GetSimkl(ctx, u.ID)
		return a != nil && a.ImportStatus == "done"
	})
	a, _ := d.GetSimkl(ctx, u.ID)
	if a.Username != "sam" || a.AccessToken != "tok" || a.ImportMovies != 1 || a.ImportEpisodes != 2 {
		t.Fatalf("account %+v", a)
	}
	for k, want := range map[string]bool{"movie": true, "e1": true, "e2": true, "e3": false} {
		ws, _ := d.GetWatchState(ctx, u.ID, items[k].ID)
		if got := ws != nil && ws.Watched; got != want {
			t.Errorf("%s watched = %v, want %v", k, got, want)
		}
	}
	if ws, _ := d.GetWatchState(ctx, u.ID, items["movie"].ID); ws.UpdatedAt != 1704164645 {
		t.Errorf("movie watched at %d", ws.UpdatedAt)
	}
	st, _ := s.SimklStatus(ctx, u.ID)
	if !st.Linked || st.Pending != nil || !st.Available {
		t.Fatalf("status %+v", st)
	}
}
