package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ---------- streams ----------

// Stream is a release stored for a movie or episode at one quality: the
// torrent (info hash) or Usenet post (TorBox's hash and NZB link) and the
// file inside it. A quality keeps a few, best first (Rank 0). No bytes are
// stored; the stream plays through whichever debrid account has it cached. Provider,
// ProviderRef and ProviderFile remember where it was last found, so playback
// doesn't have to look it up again.
type Stream struct {
	ID           string `json:"id"`
	ItemID       string `json:"itemId"`
	Quality      string `json:"quality"`
	Rank         int    `json:"rank"`
	Kind         string `json:"kind"` // "torrent" or "usenet"
	InfoHash     string `json:"infoHash"`
	NZB          string `json:"-"`
	Link         string `json:"-"` // addon play link; empty for streams added to a debrid account here
	FileIdx      int    `json:"fileIdx"`
	FileName     string `json:"fileName"`
	Size         int64  `json:"size"`
	Release      string `json:"release"`
	Addon        string `json:"addon"`
	Provider     string `json:"provider"`
	ProviderRef  string `json:"-"`
	ProviderFile string `json:"-"`
	Failures     int    `json:"failures"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

const streamCols = `id, item_id, quality, info_hash, file_idx, file_name, size, release, addon, provider, provider_ref, provider_file,
	failures, created_at, updated_at, rank, kind, nzb, link`

func scanStream(r interface{ Scan(...any) error }) (*Stream, error) {
	st := &Stream{}
	err := r.Scan(&st.ID, &st.ItemID, &st.Quality, &st.InfoHash, &st.FileIdx, &st.FileName, &st.Size, &st.Release, &st.Addon,
		&st.Provider, &st.ProviderRef, &st.ProviderFile, &st.Failures, &st.CreatedAt, &st.UpdatedAt, &st.Rank, &st.Kind, &st.NZB, &st.Link)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return st, err
}

func (d *DB) GetStream(ctx context.Context, id string) (*Stream, error) {
	return scanStream(d.QueryRowContext(ctx, `SELECT `+streamCols+` FROM streams WHERE id=?`, id))
}

func (d *DB) StreamsForItem(ctx context.Context, itemID string) ([]*Stream, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+streamCols+` FROM streams WHERE item_id=? ORDER BY quality DESC, rank`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Stream
	for rows.Next() {
		st, err := scanStream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// PutStream stores a stream for an item and quality together with the
// media_files row that makes it playable. The same file stored again
// replaces the old row.
func (d *DB) PutStream(ctx context.Context, st *Stream, f *MediaFile) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The old stream's file goes with it (ON DELETE CASCADE).
	if _, err := tx.ExecContext(ctx, `DELETE FROM streams WHERE item_id=? AND quality=? AND info_hash=? AND file_name=? AND link=? AND nzb=?`,
		st.ItemID, st.Quality, st.InfoHash, st.FileName, st.Link, st.NZB); err != nil {
		return err
	}
	if st.ID == "" {
		st.ID = NewID()
	}
	if st.Kind == "" {
		st.Kind = "torrent"
	}
	now := Now()
	st.CreatedAt, st.UpdatedAt = now, now
	if _, err := tx.ExecContext(ctx, `INSERT INTO streams (`+streamCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		st.ID, st.ItemID, st.Quality, st.InfoHash, st.FileIdx, st.FileName, st.Size, st.Release, st.Addon, st.Provider,
		st.ProviderRef, st.ProviderFile, st.Failures, st.CreatedAt, st.UpdatedAt, st.Rank, st.Kind, st.NZB, st.Link); err != nil {
		return err
	}
	if f.ID == "" {
		f.ID = NewID()
	}
	f.StreamID = st.ID
	f.Path = "stream:" + st.ID
	f.StreamRank = st.Rank
	if _, err := tx.ExecContext(ctx, `INSERT INTO media_files (id, item_id, library_id, path, size, mtime, stream_id,
		container, video_codec, width, height, hdr, stream_rank, tag) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, f.ID, f.ItemID, f.LibraryID, f.Path,
		f.Size, now, f.StreamID, f.Container, f.VideoCodec, f.Width, f.Height, f.HDR, f.StreamRank, f.Tag); err != nil {
		return err
	}
	return tx.Commit()
}

// SetStreamProvider remembers where a stream was found.
func (d *DB) SetStreamProvider(ctx context.Context, id, provider, ref, file string) error {
	_, err := d.ExecContext(ctx, `UPDATE streams SET provider=?, provider_ref=?, provider_file=?, failures=0, updated_at=? WHERE id=?`,
		provider, ref, file, Now(), id)
	return err
}

// StreamFailed counts a failed playback; the caller drops streams that keep failing.
func (d *DB) StreamFailed(ctx context.Context, id string) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `UPDATE streams SET failures=failures+1, provider='', provider_ref='', provider_file='', updated_at=?
		WHERE id=? RETURNING failures`, Now(), id).Scan(&n)
	return n, err
}

func (d *DB) DeleteStream(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM streams WHERE id=?`, id)
	return err
}

// ClearStreams forgets every stored stream for an item (forces a new lookup).
func (d *DB) ClearStreams(ctx context.Context, itemID string) error {
	if _, err := d.ExecContext(ctx, `DELETE FROM streams WHERE item_id=?`, itemID); err != nil {
		return err
	}
	_, err := d.ExecContext(ctx, `DELETE FROM stream_lookups WHERE item_id=?`, itemID)
	return err
}

// ---------- lookups ----------

type StreamLookup struct {
	ItemID   string `json:"itemId"`
	LookedAt int64  `json:"lookedAt"`
	Found    int    `json:"found"`
	Error    string `json:"error,omitempty"`
}

func (d *DB) GetStreamLookup(ctx context.Context, itemID string) (*StreamLookup, error) {
	l := &StreamLookup{ItemID: itemID}
	err := d.QueryRowContext(ctx, `SELECT looked_at, found, error FROM stream_lookups WHERE item_id=?`, itemID).Scan(&l.LookedAt, &l.Found, &l.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

func (d *DB) PutStreamLookup(ctx context.Context, l StreamLookup) error {
	_, err := d.ExecContext(ctx, `INSERT INTO stream_lookups (item_id, looked_at, found, error) VALUES (?,?,?,?)
		ON CONFLICT(item_id) DO UPDATE SET looked_at=excluded.looked_at, found=excluded.found, error=excluded.error`,
		l.ItemID, l.LookedAt, l.Found, l.Error)
	return err
}

// ---------- streaming libraries ----------

// StreamLibrary returns the streaming library of a type ("movies" or
// "shows"), creating it when missing.
func (d *DB) StreamLibrary(ctx context.Context, typ string) (*Library, error) {
	libs, err := d.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range libs {
		if l.Source == "stream" && l.Type == typ {
			return l, nil
		}
	}
	name := map[string]string{"movies": "Streaming Movies", "shows": "Streaming Shows"}[typ]
	if name == "" {
		return nil, fmt.Errorf("unknown library type %q", typ)
	}
	l := &Library{Name: name, Type: typ, Paths: []string{}, Source: "stream"}
	return l, d.CreateLibrary(ctx, l)
}

// LibraryIDsBySource lists library ids of one source, without counting items.
func (d *DB) LibraryIDsBySource(ctx context.Context, source string) ([]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT id FROM libraries WHERE source=?`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// FileEquivalent finds the same movie or episode in a files library (by TMDB
// id, and season/episode number for episodes), so a real file wins over a
// stream. Returns ErrNotFound when there is none with a file.
func (d *DB) FileEquivalent(ctx context.Context, it *Item) (*Item, error) {
	switch it.Kind {
	case "movie":
		if it.TMDBID == 0 {
			return nil, ErrNotFound
		}
		return scanItem(d.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i JOIN libraries l ON l.id=i.library_id
			WHERE i.kind='movie' AND i.tmdb_id=? AND l.source='files' AND EXISTS (SELECT 1 FROM media_files f WHERE f.item_id=i.id)
			LIMIT 1`, it.TMDBID))
	case "episode":
		var showTMDB int
		if err := d.QueryRowContext(ctx, `SELECT tmdb_id FROM items WHERE id=?`, it.ShowID).Scan(&showTMDB); err != nil || showTMDB == 0 {
			return nil, ErrNotFound
		}
		return scanItem(d.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i JOIN items sh ON sh.id=i.show_id JOIN libraries l ON l.id=i.library_id
			WHERE i.kind='episode' AND sh.tmdb_id=? AND i.season_number=? AND i.episode_number=? AND l.source='files'
			AND EXISTS (SELECT 1 FROM media_files f WHERE f.item_id=i.id) LIMIT 1`, showTMDB, it.SeasonNumber, it.EpisodeNumber))
	}
	return nil, ErrNotFound
}

// FindStreamTitle returns the movie or show for a TMDB id in a streaming library.
func (d *DB) FindStreamTitle(ctx context.Context, libraryID, kind string, tmdbID int) (*Item, error) {
	return scanItem(d.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.library_id=? AND i.kind=? AND i.tmdb_id=?`,
		libraryID, kind, tmdbID))
}

// ---------- server downloads ----------

// Download saves a stream as a real file in a library (download mode).
type Download struct {
	ID          string `json:"id"`
	ItemID      string `json:"itemId,omitempty"`
	StreamID    string `json:"streamId,omitempty"`
	Title       string `json:"title"`
	Quality     string `json:"quality"`
	Status      string `json:"status"` // queued, downloading, done, failed, canceled
	Path        string `json:"path,omitempty"`
	Bytes       int64  `json:"bytes"`
	Total       int64  `json:"total"`
	Error       string `json:"error,omitempty"`
	RequestedBy string `json:"requestedBy,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

const downloadCols = `id, COALESCE(item_id,''), COALESCE(stream_id,''), title, quality, status, path, bytes, total, error, requested_by,
	created_at, updated_at`

func scanDownload(r interface{ Scan(...any) error }) (*Download, error) {
	dl := &Download{}
	err := r.Scan(&dl.ID, &dl.ItemID, &dl.StreamID, &dl.Title, &dl.Quality, &dl.Status, &dl.Path, &dl.Bytes, &dl.Total, &dl.Error,
		&dl.RequestedBy, &dl.CreatedAt, &dl.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return dl, err
}

func (d *DB) InsertDownload(ctx context.Context, dl *Download) error {
	if dl.ID == "" {
		dl.ID = NewID()
	}
	dl.CreatedAt, dl.UpdatedAt = Now(), Now()
	_, err := d.ExecContext(ctx, `INSERT INTO stream_downloads (`+downloadCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		dl.ID, nullStr(dl.ItemID), nullStr(dl.StreamID), dl.Title, dl.Quality, dl.Status, dl.Path, dl.Bytes, dl.Total, dl.Error,
		dl.RequestedBy, dl.CreatedAt, dl.UpdatedAt)
	return err
}

func (d *DB) UpdateDownload(ctx context.Context, dl *Download) error {
	dl.UpdatedAt = Now()
	_, err := d.ExecContext(ctx, `UPDATE stream_downloads SET status=?, path=?, bytes=?, total=?, error=?, updated_at=? WHERE id=?`,
		dl.Status, dl.Path, dl.Bytes, dl.Total, dl.Error, dl.UpdatedAt, dl.ID)
	return err
}

func (d *DB) GetDownload(ctx context.Context, id string) (*Download, error) {
	return scanDownload(d.QueryRowContext(ctx, `SELECT `+downloadCols+` FROM stream_downloads WHERE id=?`, id))
}

// ActiveDownloadFor returns a queued or running download of an item.
func (d *DB) ActiveDownloadFor(ctx context.Context, itemID string) (*Download, error) {
	return scanDownload(d.QueryRowContext(ctx, `SELECT `+downloadCols+` FROM stream_downloads
		WHERE item_id=? AND status IN ('queued','downloading') LIMIT 1`, itemID))
}

// ListDownloads returns recent downloads, newest first. With status set, only those.
func (d *DB) ListDownloads(ctx context.Context, status string, limit int) ([]*Download, error) {
	q := `SELECT ` + downloadCols + ` FROM stream_downloads`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Download
	for rows.Next() {
		dl, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

// DownloadedBytes is the total size of finished downloads still on record.
func (d *DB) DownloadedBytes(ctx context.Context) (int64, error) {
	var n int64
	err := d.QueryRowContext(ctx, `SELECT COALESCE(SUM(total),0) FROM stream_downloads WHERE status='done'`).Scan(&n)
	return n, err
}

// ResetInterruptedDownloads requeues downloads a restart cut off.
func (d *DB) ResetInterruptedDownloads(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `UPDATE stream_downloads SET status='queued', bytes=0 WHERE status='downloading'`)
	return err
}

func (d *DB) DeleteDownload(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM stream_downloads WHERE id=?`, id)
	return err
}
