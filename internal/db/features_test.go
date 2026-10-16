package db

import (
	"context"
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
	if err := d.TrackPlay(ctx, u.ID, m.ID, 4, 6000); err != nil {
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
