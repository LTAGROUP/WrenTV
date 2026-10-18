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
