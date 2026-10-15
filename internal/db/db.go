// Package db is the server's SQLite store (pure Go driver, WAL mode).
package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func Open(dataDir string) (*DB, error) {
	path := filepath.Join(dataDir, "haven.db")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", path)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a small pool keeps readers concurrent while
	// busy_timeout serializes writers.
	sqldb.SetMaxOpenConns(8)
	d := &DB{sqldb}
	if err := d.migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

var migrations = []string{
	`CREATE TABLE users (
		id TEXT PRIMARY KEY,
		external_id TEXT UNIQUE,
		username TEXT UNIQUE,
		display_name TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL DEFAULT '',
		is_admin INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE libraries (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		type TEXT NOT NULL CHECK (type IN ('movies','shows')),
		paths TEXT NOT NULL DEFAULT '[]',
		created_at INTEGER NOT NULL,
		last_scan_at INTEGER
	);
	-- Local-mode sharing. In Supabase mode access comes from server_shares.
	CREATE TABLE library_access (
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
		PRIMARY KEY (user_id, library_id)
	);
	CREATE TABLE items (
		id TEXT PRIMARY KEY,
		library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK (kind IN ('movie','show','season','episode')),
		parent_id TEXT REFERENCES items(id) ON DELETE CASCADE,
		show_id TEXT REFERENCES items(id) ON DELETE CASCADE,
		match_key TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL,
		sort_title TEXT NOT NULL,
		original_title TEXT NOT NULL DEFAULT '',
		year INTEGER NOT NULL DEFAULT 0,
		overview TEXT NOT NULL DEFAULT '',
		tagline TEXT NOT NULL DEFAULT '',
		tmdb_id INTEGER NOT NULL DEFAULT 0,
		imdb_id TEXT NOT NULL DEFAULT '',
		season_number INTEGER NOT NULL DEFAULT -1,
		episode_number INTEGER NOT NULL DEFAULT -1,
		poster_path TEXT NOT NULL DEFAULT '',
		backdrop_path TEXT NOT NULL DEFAULT '',
		still_path TEXT NOT NULL DEFAULT '',
		logo_path TEXT NOT NULL DEFAULT '',
		rating REAL NOT NULL DEFAULT 0,
		content_rating TEXT NOT NULL DEFAULT '',
		genres TEXT NOT NULL DEFAULT '[]',
		runtime_min INTEGER NOT NULL DEFAULT 0,
		air_date TEXT NOT NULL DEFAULT '',
		match_state TEXT NOT NULL DEFAULT 'pending',
		added_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX items_library ON items(library_id, kind, sort_title);
	CREATE INDEX items_parent ON items(parent_id, season_number, episode_number);
	CREATE INDEX items_show ON items(show_id);
	CREATE INDEX items_added ON items(library_id, added_at DESC);
	CREATE UNIQUE INDEX items_match_key ON items(library_id, kind, match_key) WHERE kind IN ('movie','show');
	CREATE INDEX items_match_state ON items(match_state);

	CREATE TABLE torrents (
		id TEXT PRIMARY KEY,
		library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
		magnet TEXT NOT NULL,
		info_hash TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		provider TEXT NOT NULL DEFAULT '',
		provider_ref TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		external_ref TEXT NOT NULL DEFAULT '',
		added_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);

	CREATE TABLE media_files (
		id TEXT PRIMARY KEY,
		item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
		path TEXT NOT NULL,
		size INTEGER NOT NULL DEFAULT 0,
		mtime INTEGER NOT NULL DEFAULT 0,
		torrent_id TEXT REFERENCES torrents(id) ON DELETE CASCADE,
		torrent_file_index INTEGER NOT NULL DEFAULT -1,
		container TEXT NOT NULL DEFAULT '',
		duration REAL NOT NULL DEFAULT 0,
		video_codec TEXT NOT NULL DEFAULT '',
		width INTEGER NOT NULL DEFAULT 0,
		height INTEGER NOT NULL DEFAULT 0,
		hdr TEXT NOT NULL DEFAULT '',
		dv_profile INTEGER NOT NULL DEFAULT 0,
		probe TEXT NOT NULL DEFAULT '',
		probed_at INTEGER NOT NULL DEFAULT 0,
		keyframes BLOB
	);
	CREATE UNIQUE INDEX media_files_path ON media_files(path);
	CREATE INDEX media_files_item ON media_files(item_id);
	CREATE INDEX media_files_unprobed ON media_files(probed_at) WHERE probed_at = 0;

	CREATE TABLE watch_state (
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		position REAL NOT NULL DEFAULT 0,
		duration REAL NOT NULL DEFAULT 0,
		watched INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (user_id, item_id)
	);
	CREATE INDEX watch_state_recent ON watch_state(user_id, updated_at DESC);

	CREATE TABLE metadata_cache (
		key TEXT PRIMARY KEY,
		body BLOB NOT NULL,
		fetched_at INTEGER NOT NULL
	);`,
}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (v INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	if err := d.QueryRowContext(ctx, `SELECT COALESCE(MAX(v),0) FROM schema_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (v) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func Now() int64 { return time.Now().Unix() }
