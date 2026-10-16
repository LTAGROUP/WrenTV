package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// TestStreamingMigration upgrades a database that has torrent-backed files:
// those go, local files (and their markers) stay, and streams can be stored.
func TestStreamingMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(ON)", filepath.Join(dir, "wrentv.db"))
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	old := &DB{raw}
	if _, err := old.ExecContext(ctx, `CREATE TABLE schema_version (v INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := old.apply(ctx, i+1, migrations[i]); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO libraries (id, name, type, created_at) VALUES ('lib','Movies','movies',1)`,
		`INSERT INTO items (id, library_id, kind, match_key, title, sort_title, added_at, updated_at) VALUES ('m1','lib','movie','a','Local','local',1,1)`,
		`INSERT INTO items (id, library_id, kind, match_key, title, sort_title, added_at, updated_at) VALUES ('m2','lib','movie','b','Torrent','torrent',1,1)`,
		`INSERT INTO torrents (id, library_id, magnet, info_hash, status, created_at, updated_at) VALUES ('t1','lib','magnet:?','abc','ready',1,1)`,
		`INSERT INTO media_files (id, item_id, library_id, path) VALUES ('f1','m1','lib','/media/Local.mkv')`,
		`INSERT INTO media_files (id, item_id, library_id, path, torrent_id) VALUES ('f2','m2','lib','torrent://t1/0/x.mkv','t1')`,
		`INSERT INTO file_markers (file_id, analyzed_at) VALUES ('f1',1)`,
		`INSERT INTO file_markers (file_id, analyzed_at) VALUES ('f2',1)`,
		`INSERT INTO users (id, username, created_at) VALUES ('u1','me',1)`,
	} {
		if _, err := old.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	raw.Close()

	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var files, markers int
	d.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_files`).Scan(&files)
	d.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_markers`).Scan(&markers)
	if files != 1 || markers != 1 {
		t.Fatalf("files=%d markers=%d, want the local file and its markers only", files, markers)
	}
	u, err := d.UserByID(ctx, "u1")
	if err != nil || u.UserLimits != DefaultUserLimits() {
		t.Fatalf("user limits = %+v, %v", u, err)
	}

	lib, err := d.StreamLibrary(ctx, "movies")
	if err != nil || lib.Source != "stream" {
		t.Fatalf("stream library = %+v, %v", lib, err)
	}
	it := &Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:1", Title: "Streamed", TMDBID: 1, SeasonNumber: -1, EpisodeNumber: -1}
	if err := d.InsertItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	st := &Stream{ItemID: it.ID, Quality: "1080p", InfoHash: "def"}
	if err := d.PutStream(ctx, st, &MediaFile{ItemID: it.ID, LibraryID: lib.ID, Height: 1080}); err != nil {
		t.Fatal(err)
	}
	// Replacing the quality's stream replaces its file too.
	st2 := &Stream{ItemID: it.ID, Quality: "1080p", InfoHash: "ghi"}
	if err := d.PutStream(ctx, st2, &MediaFile{ItemID: it.ID, LibraryID: lib.ID, Height: 1080}); err != nil {
		t.Fatal(err)
	}
	fs, _ := d.FilesForItem(ctx, it.ID)
	if len(fs) != 1 || fs[0].StreamID != st2.ID {
		t.Fatalf("files = %+v", fs)
	}
	items, _, _ := d.QueryItems(ctx, ItemQuery{LibraryID: lib.ID, Kind: "movie"})
	if len(items) != 1 {
		t.Fatalf("streamed movie should be listed, got %d", len(items))
	}
	// Once the movie exists as a real file, the stream gives way.
	d.ExecContext(ctx, `UPDATE items SET tmdb_id=1 WHERE id='m1'`)
	items, _, _ = d.QueryItems(ctx, ItemQuery{LibraryID: lib.ID, Kind: "movie"})
	if len(items) != 0 {
		t.Fatalf("streamed movie should be hidden by the local file, got %d", len(items))
	}
	if eq, err := d.FileEquivalent(ctx, it); err != nil || eq.ID != "m1" {
		t.Fatalf("file equivalent = %+v, %v", eq, err)
	}
}
