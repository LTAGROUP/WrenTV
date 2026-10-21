package api

import (
	"context"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
)

// A title only counts as recently watched (and so drives Because You
// Watched) once it's past the Continue Watching threshold or finished.
func TestRecentlyWatchedThreshold(t *testing.T) {
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
	movie := func(title string) *db.Item {
		it := &db.Item{LibraryID: lib.ID, Kind: "movie", Title: title, MatchKey: title}
		if err := d.InsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
		return it
	}
	opened, started, finished := movie("Opened"), movie("Started"), movie("Finished")
	for _, p := range []struct {
		it  *db.Item
		pos float64
	}{{opened, 5}, {started, 90}, {finished, 5900}} {
		if _, err := d.SetProgress(ctx, u.ID, p.it.ID, p.pos, 6000, 0.9); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{Cfg: &config.Config{}, DB: d}
	titles := func() map[string]bool {
		got := map[string]bool{}
		for _, it := range s.recentlyWatched(ctx, u.ID, []string{lib.ID}, 20) {
			got[it.Title] = true
		}
		return got
	}
	if got := titles(); got["Opened"] || !got["Started"] || !got["Finished"] {
		t.Errorf("default 30s threshold: got %v", got)
	}

	prefs, _ := d.GetPrefs(ctx, u.ID)
	prefs.ContinueAfter = 120
	if err := d.SetPrefs(ctx, u.ID, prefs); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got["Opened"] || got["Started"] || !got["Finished"] {
		t.Errorf("120s threshold: got %v", got)
	}
}
