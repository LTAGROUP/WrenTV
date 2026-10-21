package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// calendarTMDB is two shows: 1 is airing (season 2, weekly from a week
// ago), 2 ended years back.
type calendarTMDB struct{ start time.Time }

func (c calendarTMDB) CacheGet(_ context.Context, key string, _ int64) ([]byte, bool) {
	path := strings.TrimPrefix(strings.SplitN(key, "?", 2)[0], "tmdb:")
	day := func(n int) string { return c.start.AddDate(0, 0, 7*n).Format(time.DateOnly) }
	switch path {
	case "/tv/1":
		return []byte(`{"status":"Returning Series","last_air_date":"` + day(1) + `","last_episode_to_air":{"season_number":2},"next_episode_to_air":{"season_number":2,"air_date":"` + day(2) + `"}}`), true
	case "/tv/1/season/2":
		var eps []string
		for i := 0; i < 8; i++ {
			eps = append(eps, fmt.Sprintf(`{"season_number":2,"episode_number":%d,"name":"E%d","air_date":"%s"}`, i+1, i+1, day(i)))
		}
		return []byte(`{"season_number":2,"episodes":[` + strings.Join(eps, ",") + `]}`), true
	case "/tv/2":
		return []byte(`{"status":"Ended","last_air_date":"2015-05-01","last_episode_to_air":{"season_number":5}}`), true
	}
	return nil, false
}

func (calendarTMDB) CachePut(context.Context, string, []byte) {}

func TestCalendar(t *testing.T) {
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
	lib := &db.Library{Name: "TV", Type: "shows", Paths: []string{t.TempDir()}}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	start := time.Now().AddDate(0, 0, -7)
	airing := &db.Item{LibraryID: lib.ID, Kind: "show", Title: "Airing", MatchKey: "airing", TMDBID: 1, AirDate: "2024-01-01"}
	ended := &db.Item{LibraryID: lib.ID, Kind: "show", Title: "Ended", MatchKey: "ended", TMDBID: 2, AirDate: "2010-01-01"}
	for _, it := range []*db.Item{airing, ended} {
		if err := d.InsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	// The first two episodes are in the library; the first is watched.
	var eps []*db.Item
	for i := 1; i <= 2; i++ {
		e := &db.Item{LibraryID: lib.ID, Kind: "episode", ParentID: airing.ID, ShowID: airing.ID, Title: fmt.Sprintf("E%d", i),
			SeasonNumber: 2, EpisodeNumber: i, AirDate: start.AddDate(0, 0, 7*(i-1)).Format(time.DateOnly)}
		if err := d.InsertItem(ctx, e); err != nil {
			t.Fatal(err)
		}
		if err := d.InsertFile(ctx, &db.MediaFile{ItemID: e.ID, LibraryID: lib.ID, Path: fmt.Sprintf("/tv/e%d.mkv", i)}); err != nil {
			t.Fatal(err)
		}
		eps = append(eps, e)
	}
	if err := d.SetWatched(ctx, u.ID, eps[0].ID, true); err != nil {
		t.Fatal(err)
	}

	s := &Server{Cfg: &config.Config{}, DB: d, TMDB: tmdb.New("key", "en-US", calendarTMDB{start})}
	req := httptest.NewRequest("GET", "/api/calendar", nil)
	req = req.WithContext(context.WithValue(req.Context(), principalKey, &auth.Principal{User: u, Role: "owner"}))
	w := httptest.NewRecorder()
	s.handleCalendar(w, req)
	var out struct {
		Episodes []calendarEpisode `json:"episodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	// A week back to a month ahead: episodes 1 to 6 (days -7 to +28).
	if len(out.Episodes) != 6 {
		t.Fatalf("got %d episodes: %+v", len(out.Episodes), out.Episodes)
	}
	first, second := out.Episodes[0], out.Episodes[1]
	if first.Show.ID != airing.ID || first.ItemID != eps[0].ID || !first.Watched || !first.Watching || first.UpNext {
		t.Errorf("first episode: %+v", first)
	}
	if second.ItemID != eps[1].ID || second.Watched || !second.UpNext {
		t.Errorf("second episode should be up next: %+v", second)
	}
	if out.Episodes[2].ItemID != "" || out.Episodes[2].Date <= second.Date {
		t.Errorf("third episode isn't in the library and airs later: %+v", out.Episodes[2])
	}
}
