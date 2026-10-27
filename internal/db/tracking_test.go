package db

import (
	"context"
	"testing"
)

func TestApplyExternalWatchedForOneTitle(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Username: "a", DisplayName: "Alex"}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib := &Library{Name: "Stream", Type: "movies"}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	marks := []ExternalMark{
		{Kind: "movie", TMDBID: 5, Season: -1, Episode: -1, WatchedAt: 100},
		{Kind: "movie", TMDBID: 6, Season: -1, Episode: -1, WatchedAt: 100},
		{Kind: "show", TMDBID: 777, Season: -1, Episode: -1, WatchedAt: 100},
		{Kind: "episode", TMDBID: 777, Season: 1, Episode: 1, WatchedAt: 100},
		{Kind: "episode", TMDBID: 777, Season: 1, Episode: 2, WatchedAt: 100},
	}
	if err := d.ImportExternalWatched(ctx, u.ID, marks); err != nil {
		t.Fatal(err)
	}
	movie := &Item{LibraryID: lib.ID, Kind: "movie", Title: "Five", TMDBID: 5, MatchKey: "m5"}
	other := &Item{LibraryID: lib.ID, Kind: "movie", Title: "Six", TMDBID: 6, MatchKey: "m6"}
	show := &Item{LibraryID: lib.ID, Kind: "show", Title: "Show", TMDBID: 777, MatchKey: "s777"}
	for _, it := range []*Item{movie, other, show} {
		if err := d.InsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	ep := &Item{LibraryID: lib.ID, Kind: "episode", ShowID: show.ID, ParentID: show.ID, Title: "Pilot", SeasonNumber: 1, EpisodeNumber: 1, MatchKey: "e1"}
	if err := d.InsertItem(ctx, ep); err != nil {
		t.Fatal(err)
	}
	watched := func(id string) bool {
		ws, _ := d.GetWatchState(ctx, u.ID, id)
		return ws != nil && ws.Watched
	}

	if err := d.ApplyExternalWatchedFor(ctx, "movie", 5); err != nil {
		t.Fatal(err)
	}
	if !watched(movie.ID) {
		t.Error("movie 5 not marked watched")
	}
	// Only the title asked about is touched.
	if watched(other.ID) || watched(ep.ID) {
		t.Error("another title was marked")
	}
	if got, _ := d.ExternalWatched(ctx, u.ID, "movie", []int{5, 6}); got[5] || !got[6] {
		t.Errorf("movie marks left %v, want only 6", got)
	}

	if err := d.ApplyExternalWatchedFor(ctx, "show", 777); err != nil {
		t.Fatal(err)
	}
	if !watched(ep.ID) {
		t.Error("S1E1 not marked watched")
	}
	if got, _ := d.ExternalWatched(ctx, u.ID, "show", []int{777}); got[777] {
		t.Error("show mark kept after the show was added")
	}
	var left int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_watched WHERE kind='episode' AND tmdb_id=777`).Scan(&left); err != nil || left != 1 {
		t.Errorf("episode marks left %d (%v), want the one not added yet", left, err)
	}
}
