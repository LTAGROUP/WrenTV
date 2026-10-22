package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestFeaturesSmoke(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Username: "a", DisplayName: "Alex"}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib := &Library{Name: "Movies", Type: "movies"}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	m := &Item{LibraryID: lib.ID, Kind: "movie", Title: "Heat", Year: 1995, Genres: []string{"Crime", "Drama"}, Rating: 8.3, MatchKey: "heat"}
	if err := d.InsertItem(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertFile(ctx, &MediaFile{ItemID: m.ID, LibraryID: lib.ID, Path: "/x/heat.mkv", Size: 1}); err != nil {
		t.Fatal(err)
	}

	p, _ := d.GetPrefs(ctx, u.ID)
	if p.AudioLanguage != "en" {
		t.Fatalf("default audio language %q", p.AudioLanguage)
	}
	p.AudioLanguage = "ja"
	if err := d.SetPrefs(ctx, u.ID, p); err != nil {
		t.Fatal(err)
	}
	if p, _ = d.GetPrefs(ctx, u.ID); p.AudioLanguage != "ja" {
		t.Fatalf("prefs not saved: %+v", p)
	}

	gs, err := d.GenreCounts(ctx, []string{lib.ID})
	if err != nil || len(gs) != 2 {
		t.Fatalf("genres %v %v", gs, err)
	}
	items, _, err := d.QueryItems(ctx, ItemQuery{Kind: "movie,show", AnyGenres: []string{"Crime"}, ExcludeGenres: []string{"Horror"},
		YearFrom: 1990, YearTo: 2000, Unwatched: true, UserID: u.ID, AddedSince: 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("smart query %v %v", len(items), err)
	}

	c := &Collection{Name: "Mine", Kind: "manual"}
	if err := d.SaveCollection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := d.AddToCollection(ctx, c.ID, []string{m.ID}); err != nil {
		t.Fatal(err)
	}
	items, _, err = d.QueryItems(ctx, ItemQuery{CollectionID: c.ID, Sort: "custom"})
	if err != nil || len(items) != 1 {
		t.Fatalf("collection query %v %v", len(items), err)
	}

	if err := d.StartPlay(ctx, PlayStart{SessionID: "s1", UserID: u.ID, Item: m, Method: "direct", Client: "Chrome on macOS", Duration: 6000}); err != nil {
		t.Fatal(err)
	}
	if err := d.TrackPlay(ctx, u.ID, m.ID, 4, 6000, 0.9); err != nil {
		t.Fatal(err)
	}
	st, err := d.Stats(ctx, time.Now().Add(-24*time.Hour).Unix(), 2, 0)
	if err != nil || st.Plays != 1 || len(st.TopMovies) != 1 || len(st.Daily) != 2 {
		t.Fatalf("stats %+v %v", st, err)
	}
	if _, err := d.LibraryStats(ctx); err != nil {
		t.Fatal(err)
	}
	if h, err := d.RecentHistory(ctx, 0, "", 10); err != nil || len(h) != 1 || h[0].UserName != "Alex" {
		t.Fatalf("history %v %v", h, err)
	}
}

func TestActiveOn(t *testing.T) {
	c := &Collection{VisibleFrom: "12-01", VisibleUntil: "01-06"}
	for d, want := range map[string]bool{"2026-12-15": true, "2027-01-03": true, "2026-07-04": false} {
		tm, _ := time.Parse("2006-01-02", d)
		if c.ActiveOn(tm) != want {
			t.Errorf("%s: want %v", d, want)
		}
	}
}

func TestNextEpisodeStreamed(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Username: "a", DisplayName: "Alex"}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"files", "stream"} {
		lib := &Library{Name: src, Type: "shows", Source: src}
		if err := d.CreateLibrary(ctx, lib); err != nil {
			t.Fatal(err)
		}
		show := &Item{LibraryID: lib.ID, Kind: "show", Title: "Show", MatchKey: "show"}
		if err := d.InsertItem(ctx, show); err != nil {
			t.Fatal(err)
		}
		var eps []*Item
		for i, air := range []string{"2020-01-01", "2020-01-08", "2020-01-15", "2999-01-01"} {
			e := &Item{LibraryID: lib.ID, Kind: "episode", ShowID: show.ID, ParentID: show.ID, Title: air,
				SeasonNumber: 1, EpisodeNumber: i + 1, AirDate: air, MatchKey: air}
			if err := d.InsertItem(ctx, e); err != nil {
				t.Fatal(err)
			}
			eps = append(eps, e)
		}
		// Only episode 3 has a file; episode 1 is watched.
		if err := d.InsertFile(ctx, &MediaFile{ItemID: eps[2].ID, LibraryID: lib.ID, Path: "/x/" + src + ".mkv", Size: 1}); err != nil {
			t.Fatal(err)
		}
		if err := d.SetWatched(ctx, u.ID, eps[0].ID, true); err != nil {
			t.Fatal(err)
		}
		// A file library skips to the episode it has; a streamed one offers
		// the next aired episode, file or not.
		want := map[string]int{"files": 3, "stream": 2}[src]
		next, err := d.NextEpisode(ctx, u.ID, show.ID)
		if err != nil || next.EpisodeNumber != want {
			t.Fatalf("%s: next %+v, %v; want episode %d", src, next, err, want)
		}
		for _, e := range eps[1:3] {
			_ = d.SetWatched(ctx, u.ID, e.ID, true)
		}
		// The unaired episode isn't offered.
		if next, err := d.NextEpisode(ctx, u.ID, show.ID); err == nil {
			t.Fatalf("%s: offered unaired %+v", src, next)
		}
	}
}

// TestShowWatchedGoesStale checks that a show marked watched mid-season is no
// longer watched once a new episode arrives.
func TestShowWatchedGoesStale(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Username: "a", DisplayName: "Alex"}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib := &Library{Name: "s", Type: "shows", Source: "stream"}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	show := &Item{LibraryID: lib.ID, Kind: "show", Title: "Show", MatchKey: "show"}
	if err := d.InsertItem(ctx, show); err != nil {
		t.Fatal(err)
	}
	ep := func(n int) {
		e := &Item{LibraryID: lib.ID, Kind: "episode", ShowID: show.ID, ParentID: show.ID, SeasonNumber: 1, EpisodeNumber: n, MatchKey: fmt.Sprint(n)}
		if err := d.InsertItem(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	watched := func() (bool, bool) {
		items, _, err := d.QueryItems(ctx, ItemQuery{LibraryIDs: []string{lib.ID}, Kind: "show", UserID: u.ID})
		if err != nil || len(items) != 1 || items[0].Progress == nil {
			t.Fatalf("query: %v %+v", err, items)
		}
		return items[0].Progress.Watched, d.EpisodesWatched(ctx, u.ID, show.ID)
	}
	ep(1)
	if err := d.SetWatched(ctx, u.ID, show.ID, true); err != nil {
		t.Fatal(err)
	}
	if a, b := watched(); !a || !b {
		t.Fatalf("watched before new episode: %v %v", a, b)
	}
	ep(2)
	if a, b := watched(); a || b {
		t.Fatalf("still watched after new episode: %v %v", a, b)
	}
}
