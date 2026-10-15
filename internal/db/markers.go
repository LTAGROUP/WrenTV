package db

import (
	"context"
	"database/sql"
	"errors"
)

// FileMarkers are fingerprint results for one file. Nil ranges were looked
// for and not found.
type FileMarkers struct {
	FileID     string
	Mtime      int64
	Intro      *[2]float64
	Credits    *[2]float64
	Peers      int
	AnalyzedAt int64
}

func rng(s, e sql.NullFloat64) *[2]float64 {
	if !s.Valid || !e.Valid {
		return nil
	}
	return &[2]float64{s.Float64, e.Float64}
}

func (d *DB) GetFileMarkers(ctx context.Context, fileID string) (*FileMarkers, error) {
	m := &FileMarkers{FileID: fileID}
	var is, ie, cs, ce sql.NullFloat64
	err := d.QueryRowContext(ctx, `SELECT mtime, intro_start, intro_end, credits_start, credits_end, peers, analyzed_at
		FROM file_markers WHERE file_id=?`, fileID).Scan(&m.Mtime, &is, &ie, &cs, &ce, &m.Peers, &m.AnalyzedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.Intro, m.Credits = rng(is, ie), rng(cs, ce)
	return m, nil
}

func (d *DB) SetFileMarkers(ctx context.Context, m *FileMarkers) error {
	val := func(r *[2]float64, i int) any {
		if r == nil {
			return nil
		}
		return r[i]
	}
	_, err := d.ExecContext(ctx, `INSERT INTO file_markers (file_id, mtime, intro_start, intro_end, credits_start, credits_end, peers, analyzed_at)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(file_id) DO UPDATE SET mtime=excluded.mtime, intro_start=excluded.intro_start,
		intro_end=excluded.intro_end, credits_start=excluded.credits_start, credits_end=excluded.credits_end,
		peers=excluded.peers, analyzed_at=excluded.analyzed_at`,
		m.FileID, m.Mtime, val(m.Intro, 0), val(m.Intro, 1), val(m.Credits, 0), val(m.Credits, 1), m.Peers, Now())
	return err
}

// EpisodeFile is an episode's local file, for marker analysis.
type EpisodeFile struct {
	SeasonID string
	ItemID   string
	Episode  int
	FileID   string
	Path     string
	Duration float64
	Mtime    int64
	Markers  *FileMarkers // nil when never analyzed
}

// EpisodeFilesForMarkers lists local, probed episode files grouped by season
// (ordered by season, then episode), with any earlier analysis.
func (d *DB) EpisodeFilesForMarkers(ctx context.Context) ([]*EpisodeFile, error) {
	rows, err := d.QueryContext(ctx, `SELECT i.parent_id, i.id, i.episode_number, f.id, f.path, f.duration, f.mtime,
		m.file_id, m.mtime, m.intro_start, m.intro_end, m.credits_start, m.credits_end, m.peers, m.analyzed_at
		FROM items i JOIN media_files f ON f.item_id=i.id LEFT JOIN file_markers m ON m.file_id=f.id
		WHERE i.kind='episode' AND i.parent_id IS NOT NULL AND f.torrent_id IS NULL AND f.duration > 120
		ORDER BY i.parent_id, i.episode_number, f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EpisodeFile
	for rows.Next() {
		e := &EpisodeFile{}
		var mid sql.NullString
		var mm, peers, at sql.NullInt64
		var is, ie, cs, ce sql.NullFloat64
		if err := rows.Scan(&e.SeasonID, &e.ItemID, &e.Episode, &e.FileID, &e.Path, &e.Duration, &e.Mtime,
			&mid, &mm, &is, &ie, &cs, &ce, &peers, &at); err != nil {
			return nil, err
		}
		if mid.Valid {
			e.Markers = &FileMarkers{FileID: e.FileID, Mtime: mm.Int64, Intro: rng(is, ie), Credits: rng(cs, ce),
				Peers: int(peers.Int64), AnalyzedAt: at.Int64}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TopGenres returns the most common genres among movies and shows in the
// given libraries.
func (d *DB) TopGenres(ctx context.Context, libraryIDs []string, limit int) ([]string, error) {
	if len(libraryIDs) == 0 {
		return nil, nil
	}
	args := []any{}
	for _, l := range libraryIDs {
		args = append(args, l)
	}
	args = append(args, limit)
	rows, err := d.QueryContext(ctx, `SELECT g.value, COUNT(*) c FROM items i, json_each(i.genres) g
		WHERE i.kind IN ('movie','show') AND i.library_id IN (`+placeholders(len(libraryIDs))+`)
		GROUP BY g.value HAVING c >= 4 ORDER BY c DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		var c int
		if err := rows.Scan(&g, &c); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
