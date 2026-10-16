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
	path := filepath.Join(dataDir, "wrentv.db")
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
	// Intro/credits found by audio fingerprinting (chapter-based markers are
	// derived from the probe). NULL ranges were looked for and not found;
	// peers is how many other episodes the file was compared with.
	`CREATE TABLE file_markers (
		file_id TEXT PRIMARY KEY REFERENCES media_files(id) ON DELETE CASCADE,
		mtime INTEGER NOT NULL DEFAULT 0,
		intro_start REAL,
		intro_end REAL,
		credits_start REAL,
		credits_end REAL,
		peers INTEGER NOT NULL DEFAULT 0,
		analyzed_at INTEGER NOT NULL
	);`,
	// Per-user preferences (JSON), collections, and play history for stats.
	`CREATE TABLE user_prefs (
		user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		body TEXT NOT NULL DEFAULT '{}',
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE collections (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL CHECK (kind IN ('manual','smart','tmdb')),
		rules TEXT NOT NULL DEFAULT '{}',
		library_id TEXT REFERENCES libraries(id) ON DELETE CASCADE,
		poster_path TEXT NOT NULL DEFAULT '',
		backdrop_path TEXT NOT NULL DEFAULT '',
		sort TEXT NOT NULL DEFAULT '',
		item_limit INTEGER NOT NULL DEFAULT 0,
		show_on_home INTEGER NOT NULL DEFAULT 0,
		home_order INTEGER NOT NULL DEFAULT 0,
		show_missing INTEGER NOT NULL DEFAULT 0,
		visible_from TEXT NOT NULL DEFAULT '',
		visible_until TEXT NOT NULL DEFAULT '',
		titles TEXT NOT NULL DEFAULT '[]',
		synced_at INTEGER NOT NULL DEFAULT 0,
		sync_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE collection_items (
		collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
		item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		position INTEGER NOT NULL DEFAULT 0,
		added_at INTEGER NOT NULL,
		PRIMARY KEY (collection_id, item_id)
	);
	CREATE TABLE play_history (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL DEFAULT '',
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		item_id TEXT REFERENCES items(id) ON DELETE SET NULL,
		show_id TEXT NOT NULL DEFAULT '',
		library_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		show_title TEXT NOT NULL DEFAULT '',
		year INTEGER NOT NULL DEFAULT 0,
		poster_path TEXT NOT NULL DEFAULT '',
		file_id TEXT NOT NULL DEFAULT '',
		method TEXT NOT NULL DEFAULT '',
		engine TEXT NOT NULL DEFAULT '',
		video_copy INTEGER NOT NULL DEFAULT 1,
		height INTEGER NOT NULL DEFAULT 0,
		client TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL,
		position_start REAL NOT NULL DEFAULT 0,
		position_end REAL NOT NULL DEFAULT 0,
		duration REAL NOT NULL DEFAULT 0,
		played_seconds REAL NOT NULL DEFAULT 0,
		completed INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX play_history_started ON play_history(started_at DESC);
	CREATE INDEX play_history_user ON play_history(user_id, item_id, last_seen_at DESC);
	CREATE INDEX play_history_session ON play_history(session_id);`,
	// Streaming replaces torrents. Torrent-backed files go; media_files is
	// rebuilt so a file can point at a stored stream instead (runs with
	// foreign keys off, see fkOff). Streaming libraries are libraries with
	// source 'stream'. Users get permissions and per-user limits
	// (-1 = server default, 0 = no limit).
	`DELETE FROM file_markers WHERE file_id IN (SELECT id FROM media_files WHERE torrent_id IS NOT NULL);
	DELETE FROM media_files WHERE torrent_id IS NOT NULL;
	DROP TABLE torrents;

	CREATE TABLE streams (
		id TEXT PRIMARY KEY,
		item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		quality TEXT NOT NULL,
		info_hash TEXT NOT NULL,
		file_idx INTEGER NOT NULL DEFAULT -1,
		file_name TEXT NOT NULL DEFAULT '',
		size INTEGER NOT NULL DEFAULT 0,
		release TEXT NOT NULL DEFAULT '',
		addon TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		provider_ref TEXT NOT NULL DEFAULT '',
		provider_file TEXT NOT NULL DEFAULT '',
		failures INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE UNIQUE INDEX streams_item_quality ON streams(item_id, quality);
	CREATE INDEX streams_hash ON streams(info_hash);
	-- When each title was last looked up, so misses aren't retried constantly.
	CREATE TABLE stream_lookups (
		item_id TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
		looked_at INTEGER NOT NULL,
		found INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE stream_downloads (
		id TEXT PRIMARY KEY,
		item_id TEXT REFERENCES items(id) ON DELETE SET NULL,
		stream_id TEXT REFERENCES streams(id) ON DELETE SET NULL,
		title TEXT NOT NULL DEFAULT '',
		quality TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		path TEXT NOT NULL DEFAULT '',
		bytes INTEGER NOT NULL DEFAULT 0,
		total INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		requested_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX stream_downloads_status ON stream_downloads(status, created_at);

	CREATE TABLE media_files_new (
		id TEXT PRIMARY KEY,
		item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
		path TEXT NOT NULL,
		size INTEGER NOT NULL DEFAULT 0,
		mtime INTEGER NOT NULL DEFAULT 0,
		stream_id TEXT REFERENCES streams(id) ON DELETE CASCADE,
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
	INSERT INTO media_files_new (id, item_id, library_id, path, size, mtime, container, duration, video_codec, width, height, hdr,
		dv_profile, probe, probed_at, keyframes)
		SELECT id, item_id, library_id, path, size, mtime, container, duration, video_codec, width, height, hdr,
		dv_profile, probe, probed_at, keyframes FROM media_files;
	DROP TABLE media_files;
	ALTER TABLE media_files_new RENAME TO media_files;
	CREATE UNIQUE INDEX media_files_path ON media_files(path);
	CREATE INDEX media_files_item ON media_files(item_id);
	CREATE INDEX media_files_unprobed ON media_files(probed_at) WHERE probed_at = 0;
	CREATE INDEX media_files_stream ON media_files(stream_id) WHERE stream_id IS NOT NULL;

	ALTER TABLE libraries ADD COLUMN source TEXT NOT NULL DEFAULT 'files';
	ALTER TABLE users ADD COLUMN allow_streaming INTEGER NOT NULL DEFAULT 1;
	ALTER TABLE users ADD COLUMN allow_downloads INTEGER NOT NULL DEFAULT -1;
	ALTER TABLE users ADD COLUMN max_streams INTEGER NOT NULL DEFAULT -1;
	ALTER TABLE users ADD COLUMN max_downloads INTEGER NOT NULL DEFAULT -1;`,
}

// fkOff lists migrations (1-based) that rebuild tables. They run with
// foreign keys off so dropping the old table doesn't cascade, then check
// the result (https://www.sqlite.org/lang_altertable.html#otheralter).
var fkOff = map[int]bool{4: true}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (v INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	if err := d.QueryRowContext(ctx, `SELECT COALESCE(MAX(v),0) FROM schema_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		if err := d.apply(ctx, i+1, migrations[i]); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) apply(ctx context.Context, version int, stmt string) error {
	// One connection throughout: PRAGMA foreign_keys is per connection and
	// can't change inside a transaction.
	conn, err := d.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if fkOff[version] {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
			return err
		}
		defer conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("migration %d: %w", version, err)
	}
	if fkOff[version] {
		rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			return err
		}
		bad := rows.Next()
		rows.Close()
		if bad {
			return fmt.Errorf("migration %d: foreign key check failed", version)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (v) VALUES (?)`, version); err != nil {
		return err
	}
	return tx.Commit()
}

func Now() int64 { return time.Now().Unix() }
