package db

import (
	"context"
	"testing"
)

// Usenet streams keep their failure count between plays: an old failure or
// a play to the end starts it over, so bad luck far apart doesn't drop one.
func TestStreamFailuresDecayAndReset(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:5", Title: "Film", TMDBID: 5, SeasonNumber: -1, EpisodeNumber: -1}
	if err := d.InsertItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	st := &Stream{ItemID: it.ID, Quality: "1080p", FileName: "Film.1080p.mkv", Size: 100, Provider: "usenet"}
	if err := d.PutStream(ctx, st, &MediaFile{ItemID: it.ID, LibraryID: lib.ID, Size: 100}); err != nil {
		t.Fatal(err)
	}
	if n, err := d.CountStreamFailure(ctx, st.ID); err != nil || n != 1 {
		t.Fatalf("first failure = %d, %v", n, err)
	}
	if n, _ := d.CountStreamFailure(ctx, st.ID); n != 2 {
		t.Fatalf("second failure right after = %d, want 2", n)
	}
	if err := d.ResetStreamFailures(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.CountStreamFailure(ctx, st.ID); n != 1 {
		t.Fatalf("after a play to the end = %d, want 1", n)
	}
	// A week and more since the last failure: it counts as the first again.
	d.ExecContext(ctx, `UPDATE streams SET updated_at=? WHERE id=?`, Now()-streamFailureWindow-60, st.ID)
	if n, _ := d.CountStreamFailure(ctx, st.ID); n != 1 {
		t.Fatalf("after an old failure = %d, want 1", n)
	}
}
