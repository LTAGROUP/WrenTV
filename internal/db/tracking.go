package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// SimklAccount is a user's linked Simkl account.
type SimklAccount struct {
	UserID         string `json:"-"`
	AccessToken    string `json:"-"`
	RefreshToken   string `json:"-"`
	ExpiresAt      int64  `json:"-"`
	Username       string `json:"username"`
	Scrobble       bool   `json:"scrobble"`
	LinkedAt       int64  `json:"linkedAt"`
	ImportStatus   string `json:"importStatus"` // "", "running", "done" or an error
	ImportAt       int64  `json:"importAt,omitempty"`
	ImportMovies   int    `json:"importMovies"`
	ImportEpisodes int    `json:"importEpisodes"`
}

// GetSimkl returns the user's linked account, or nil.
func (d *DB) GetSimkl(ctx context.Context, userID string) (*SimklAccount, error) {
	a := &SimklAccount{UserID: userID}
	err := d.QueryRowContext(ctx, `SELECT access_token, refresh_token, expires_at, username, scrobble, linked_at,
		import_status, import_at, import_movies, import_episodes FROM simkl_accounts WHERE user_id=?`, userID).
		Scan(&a.AccessToken, &a.RefreshToken, &a.ExpiresAt, &a.Username, &a.Scrobble, &a.LinkedAt,
			&a.ImportStatus, &a.ImportAt, &a.ImportMovies, &a.ImportEpisodes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// LinkSimkl stores a newly approved account, replacing any earlier one.
func (d *DB) LinkSimkl(ctx context.Context, a *SimklAccount) error {
	_, err := d.ExecContext(ctx, `INSERT INTO simkl_accounts (user_id, access_token, refresh_token, expires_at, username, scrobble, linked_at)
		VALUES (?,?,?,?,?,1,?) ON CONFLICT(user_id) DO UPDATE SET access_token=excluded.access_token, refresh_token=excluded.refresh_token,
		expires_at=excluded.expires_at, username=excluded.username, linked_at=excluded.linked_at`,
		a.UserID, a.AccessToken, a.RefreshToken, a.ExpiresAt, a.Username, Now())
	return err
}

func (d *DB) SetSimklTokens(ctx context.Context, userID, access, refresh string, expires int64) error {
	_, err := d.ExecContext(ctx, `UPDATE simkl_accounts SET access_token=?, refresh_token=?, expires_at=? WHERE user_id=?`,
		access, refresh, expires, userID)
	return err
}

func (d *DB) SetSimklScrobble(ctx context.Context, userID string, on bool) error {
	_, err := d.ExecContext(ctx, `UPDATE simkl_accounts SET scrobble=? WHERE user_id=?`, on, userID)
	return err
}

func (d *DB) SetSimklImport(ctx context.Context, userID, status string, movies, episodes int) error {
	_, err := d.ExecContext(ctx, `UPDATE simkl_accounts SET import_status=?, import_at=?, import_movies=?, import_episodes=? WHERE user_id=?`,
		status, Now(), movies, episodes, userID)
	return err
}

func (d *DB) UnlinkSimkl(ctx context.Context, userID string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM simkl_accounts WHERE user_id=?`, userID)
	return err
}

// ExternalItem is a movie or show with the ids trackers know it by.
type ExternalItem struct {
	ID     string
	Kind   string
	TMDBID int
	IMDBID string
	Title  string
	Year   int
}

// ExternalItems lists every movie and show, for matching a tracker's history.
func (d *DB) ExternalItems(ctx context.Context) ([]ExternalItem, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, kind, tmdb_id, imdb_id, title, year FROM items WHERE kind IN ('movie','show')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExternalItem
	for rows.Next() {
		var e ExternalItem
		var imdb sql.NullString
		var tmdb sql.NullInt64
		var year sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Kind, &tmdb, &imdb, &e.Title, &year); err != nil {
			return nil, err
		}
		e.TMDBID, e.IMDBID, e.Year = int(tmdb.Int64), strings.TrimSpace(imdb.String), int(year.Int64)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EpisodeIDs maps [season, episode] to the item id of each episode of a show.
func (d *DB) EpisodeIDs(ctx context.Context, showID string) (map[[2]int]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, season_number, episode_number FROM items WHERE show_id=? AND kind='episode'`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]int]string{}
	for rows.Next() {
		var id string
		var s, e int
		if err := rows.Scan(&id, &s, &e); err != nil {
			return nil, err
		}
		out[[2]int{s, e}] = id
	}
	return out, rows.Err()
}

// ImportWatched marks items watched (item id → when, unix seconds) without
// touching ones already watched. It returns how many changed.
func (d *DB) ImportWatched(ctx context.Context, userID string, marks map[string]int64) (int, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at) VALUES (?,?,0,0,1,?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET watched=1, position=0 WHERE watch_state.watched=0`)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	n := 0
	for id, at := range marks {
		if at <= 0 {
			at = Now()
		}
		res, err := st.ExecContext(ctx, userID, id, at)
		if err != nil {
			return n, err
		}
		if c, _ := res.RowsAffected(); c > 0 {
			n++
		}
	}
	return n, tx.Commit()
}

// ImportProgress records where a user paused an item (unix seconds) unless
// it's already watched or has newer progress here. It reports whether it
// changed anything.
func (d *DB) ImportProgress(ctx context.Context, userID, itemID string, pos, dur float64, at int64) (bool, error) {
	if at <= 0 {
		at = Now()
	}
	res, err := d.ExecContext(ctx, `INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at) VALUES (?,?,?,?,0,?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET position=excluded.position, duration=excluded.duration, updated_at=excluded.updated_at
		WHERE watch_state.watched=0 AND watch_state.updated_at < excluded.updated_at`, userID, itemID, pos, dur, at)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ExternalMark is something a tracker says a user watched, by TMDB id (the
// show's, for an episode). Season and Episode are -1 for a movie or show.
type ExternalMark struct {
	Kind      string
	TMDBID    int
	Season    int
	Episode   int
	WatchedAt int64
}

// ImportExternalWatched keeps marks for titles the server doesn't have.
func (d *DB) ImportExternalWatched(ctx context.Context, userID string, marks []ExternalMark) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO external_watched (user_id, kind, tmdb_id, season, episode, watched_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT DO UPDATE SET watched_at=excluded.watched_at`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, m := range marks {
		if m.WatchedAt <= 0 {
			m.WatchedAt = Now()
		}
		if _, err := st.ExecContext(ctx, userID, m.Kind, m.TMDBID, m.Season, m.Episode, m.WatchedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyExternalWatched turns marks for titles the server now has into
// watch state, then drops them, so unmarking a title later sticks.
// Episodes not added yet keep their marks.
func (d *DB) ApplyExternalWatched(ctx context.Context) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at)
			SELECT x.user_id, i.id, 0, 0, 1, x.watched_at FROM external_watched x
			JOIN items i ON i.kind='movie' AND i.tmdb_id=x.tmdb_id WHERE x.kind='movie'
			ON CONFLICT(user_id, item_id) DO UPDATE SET watched=1, position=0 WHERE watch_state.watched=0`,
		`INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at)
			SELECT x.user_id, e.id, 0, 0, 1, x.watched_at FROM external_watched x
			JOIN items s ON s.kind='show' AND s.tmdb_id=x.tmdb_id
			JOIN items e ON e.show_id=s.id AND e.kind='episode' AND e.season_number=x.season AND e.episode_number=x.episode
			WHERE x.kind='episode'
			ON CONFLICT(user_id, item_id) DO UPDATE SET watched=1, position=0 WHERE watch_state.watched=0`,
		`DELETE FROM external_watched WHERE
			(kind IN ('movie','show') AND EXISTS (SELECT 1 FROM items i WHERE i.kind=external_watched.kind AND i.tmdb_id=external_watched.tmdb_id))
			OR (kind='episode' AND EXISTS (SELECT 1 FROM items s JOIN items e ON e.show_id=s.id AND e.kind='episode'
				WHERE s.kind='show' AND s.tmdb_id=external_watched.tmdb_id AND e.season_number=external_watched.season AND e.episode_number=external_watched.episode))`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyExternalWatchedFor is ApplyExternalWatched for one title, by its kind
// ("movie" or "show") and TMDB id: what a title just added needs, without
// sweeping every user's marks.
func (d *DB) ApplyExternalWatchedFor(ctx context.Context, kind string, tmdbID int) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var qs []string
	switch kind {
	case "movie":
		qs = []string{
			`INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at)
				SELECT x.user_id, i.id, 0, 0, 1, x.watched_at FROM external_watched x
				JOIN items i ON i.kind='movie' AND i.tmdb_id=x.tmdb_id WHERE x.kind='movie' AND x.tmdb_id=?1
				ON CONFLICT(user_id, item_id) DO UPDATE SET watched=1, position=0 WHERE watch_state.watched=0`,
			`DELETE FROM external_watched WHERE kind='movie' AND tmdb_id=?1
				AND EXISTS (SELECT 1 FROM items i WHERE i.kind='movie' AND i.tmdb_id=?1)`,
		}
	case "show":
		qs = []string{
			`INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at)
				SELECT x.user_id, e.id, 0, 0, 1, x.watched_at FROM external_watched x
				JOIN items s ON s.kind='show' AND s.tmdb_id=x.tmdb_id
				JOIN items e ON e.show_id=s.id AND e.kind='episode' AND e.season_number=x.season AND e.episode_number=x.episode
				WHERE x.kind='episode' AND x.tmdb_id=?1
				ON CONFLICT(user_id, item_id) DO UPDATE SET watched=1, position=0 WHERE watch_state.watched=0`,
			`DELETE FROM external_watched WHERE tmdb_id=?1 AND (
				(kind='show' AND EXISTS (SELECT 1 FROM items i WHERE i.kind='show' AND i.tmdb_id=?1))
				OR (kind='episode' AND EXISTS (SELECT 1 FROM items s JOIN items e ON e.show_id=s.id AND e.kind='episode'
					WHERE s.kind='show' AND s.tmdb_id=?1 AND e.season_number=external_watched.season AND e.episode_number=external_watched.episode)))`,
		}
	default:
		return nil
	}
	for _, q := range qs {
		if _, err := tx.ExecContext(ctx, q, tmdbID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ExternalWatched reports which of these movies or shows (by TMDB id) a
// user watched elsewhere; a show counts once it's finished.
func (d *DB) ExternalWatched(ctx context.Context, userID, kind string, tmdbIDs []int) (map[int]bool, error) {
	out := map[int]bool{}
	if len(tmdbIDs) == 0 {
		return out, nil
	}
	args := []any{userID, kind}
	for _, id := range tmdbIDs {
		args = append(args, id)
	}
	rows, err := d.QueryContext(ctx, `SELECT tmdb_id FROM external_watched WHERE user_id=? AND kind=? AND tmdb_id IN (`+placeholders(len(tmdbIDs))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ExternalProgress is where a tracker says a user paused a title the server
// doesn't have, by TMDB id (the show's, for an episode).
type ExternalProgress struct {
	Kind     string // "movie" or "episode"
	TMDBID   int
	Season   int
	Episode  int
	Percent  float64
	PausedAt int64
}

// ImportExternalProgress keeps pause points for titles the server doesn't
// have.
func (d *DB) ImportExternalProgress(ctx context.Context, userID string, ps []ExternalProgress) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO external_progress (user_id, kind, tmdb_id, season, episode, percent, paused_at) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT DO UPDATE SET percent=excluded.percent, paused_at=excluded.paused_at WHERE excluded.paused_at >= external_progress.paused_at`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, p := range ps {
		if p.PausedAt <= 0 {
			p.PausedAt = Now()
		}
		if _, err := st.ExecContext(ctx, userID, p.Kind, p.TMDBID, p.Season, p.Episode, p.Percent, p.PausedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyExternalProgressFor turns pause points for one title just added (kind
// "movie" or "show", by TMDB id) into resume positions from its runtime,
// then drops them. A pause older than the user's own progress loses.
func (d *DB) ApplyExternalProgressFor(ctx context.Context, kind string, tmdbID int) error {
	var match string
	switch kind {
	case "movie":
		match = `JOIN items i ON i.kind='movie' AND i.tmdb_id=x.tmdb_id WHERE x.kind='movie'`
	case "show":
		match = `JOIN items s ON s.kind='show' AND s.tmdb_id=x.tmdb_id
			JOIN items i ON i.show_id=s.id AND i.kind='episode' AND i.season_number=x.season AND i.episode_number=x.episode
			WHERE x.kind='episode'`
	default:
		return nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at)
			SELECT x.user_id, i.id, i.runtime_min*60*x.percent/100, i.runtime_min*60, 0, x.paused_at FROM external_progress x
			` + match + ` AND x.tmdb_id=?1 AND i.runtime_min > 0
			ON CONFLICT(user_id, item_id) DO UPDATE SET position=excluded.position, duration=excluded.duration, updated_at=excluded.updated_at
			WHERE watch_state.watched=0 AND watch_state.updated_at < excluded.updated_at`,
		`DELETE FROM external_progress WHERE rowid IN (SELECT x.rowid FROM external_progress x ` + match + ` AND x.tmdb_id=?1)`,
	} {
		if _, err := tx.ExecContext(ctx, q, tmdbID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
