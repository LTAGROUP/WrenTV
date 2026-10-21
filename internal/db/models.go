package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

var ErrNotFound = errors.New("not found")

func NewID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- users ----------

type User struct {
	ID           string `json:"id"`
	ExternalID   string `json:"externalId,omitempty"`
	Username     string `json:"username"`
	DisplayName  string `json:"displayName"`
	Email        string `json:"email,omitempty"`
	PasswordHash string `json:"-"`
	IsAdmin      bool   `json:"isAdmin"`
	CreatedAt    int64  `json:"createdAt"`
	UserLimits
}

// UserLimits are per-user permissions. -1 means "use the server default";
// for the maximums 0 means no limit.
type UserLimits struct {
	AllowStreaming bool `json:"allowStreaming"`
	AllowDownloads int  `json:"allowDownloads"` // -1 default, 0 no, 1 yes
	MaxStreams     int  `json:"maxStreams"`
	MaxDownloads   int  `json:"maxDownloads"`
}

const userCols = `id, COALESCE(external_id,''), COALESCE(username,''), display_name, email, password_hash, is_admin, created_at,
	allow_streaming, allow_downloads, max_streams, max_downloads`

func scanUser(r interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := r.Scan(&u.ID, &u.ExternalID, &u.Username, &u.DisplayName, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt,
		&u.AllowStreaming, &u.AllowDownloads, &u.MaxStreams, &u.MaxDownloads)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (d *DB) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(d.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

func (d *DB) UserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(d.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username=? COLLATE NOCASE`, username))
}

func (d *DB) UserByExternalID(ctx context.Context, ext string) (*User, error) {
	return scanUser(d.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE external_id=?`, ext))
}

func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (d *DB) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d *DB) CreateUser(ctx context.Context, u *User) error {
	if u.ID == "" {
		u.ID = NewID()
	}
	u.CreatedAt = Now()
	var ext, uname any
	if u.ExternalID != "" {
		ext = u.ExternalID
	}
	if u.Username != "" {
		uname = u.Username
	}
	if u.UserLimits == (UserLimits{}) {
		u.UserLimits = DefaultUserLimits()
	}
	_, err := d.ExecContext(ctx, `INSERT INTO users (id, external_id, username, display_name, email, password_hash, is_admin, created_at,
		allow_streaming, allow_downloads, max_streams, max_downloads) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.ID, ext, uname, u.DisplayName, u.Email, u.PasswordHash, u.IsAdmin, u.CreatedAt,
		u.AllowStreaming, u.AllowDownloads, u.MaxStreams, u.MaxDownloads)
	return err
}

// DefaultUserLimits defers everything to the server defaults.
func DefaultUserLimits() UserLimits {
	return UserLimits{AllowStreaming: true, AllowDownloads: -1, MaxStreams: -1, MaxDownloads: -1}
}

func (d *DB) UpdateUserProfile(ctx context.Context, id, displayName, email string, isAdmin bool) error {
	_, err := d.ExecContext(ctx, `UPDATE users SET display_name=?, email=?, is_admin=? WHERE id=?`, displayName, email, isAdmin, id)
	return err
}

func (d *DB) UpdateUserLimits(ctx context.Context, id string, l UserLimits) error {
	_, err := d.ExecContext(ctx, `UPDATE users SET allow_streaming=?, allow_downloads=?, max_streams=?, max_downloads=? WHERE id=?`,
		l.AllowStreaming, l.AllowDownloads, l.MaxStreams, l.MaxDownloads, id)
	return err
}

func (d *DB) DeleteUser(ctx context.Context, id string) error {
	if _, err := d.ExecContext(ctx, `DELETE FROM layouts WHERE user_id=? AND user_id != ''`, id); err != nil {
		return err
	}
	_, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}

func (d *DB) SetLibraryAccess(ctx context.Context, userID string, libraryIDs []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_access WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, l := range libraryIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_access (user_id, library_id) VALUES (?,?)`, userID, l); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) LibraryAccess(ctx context.Context, userID string) ([]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT library_id FROM library_access WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------- libraries ----------

type Library struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Paths []string `json:"paths"`
	// Source is "files" (folders on disk) or "stream" (a streaming library).
	Source     string `json:"source"`
	CreatedAt  int64  `json:"createdAt"`
	LastScanAt int64  `json:"lastScanAt"`
	ItemCount  int    `json:"itemCount"`
}

func (d *DB) ListLibraries(ctx context.Context) ([]*Library, error) {
	rows, err := d.QueryContext(ctx, `SELECT l.id, l.name, l.type, l.paths, l.source, l.created_at, COALESCE(l.last_scan_at,0),
		(SELECT COUNT(*) FROM items i WHERE i.library_id=l.id AND i.kind IN ('movie','show'))
		FROM libraries l ORDER BY l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Library
	for rows.Next() {
		l := &Library{}
		var paths string
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &paths, &l.Source, &l.CreatedAt, &l.LastScanAt, &l.ItemCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(paths), &l.Paths)
		out = append(out, l)
	}
	return out, rows.Err()
}

func (d *DB) GetLibrary(ctx context.Context, id string) (*Library, error) {
	libs, err := d.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range libs {
		if l.ID == id {
			return l, nil
		}
	}
	return nil, ErrNotFound
}

func (d *DB) CreateLibrary(ctx context.Context, l *Library) error {
	l.ID = NewID()
	l.CreatedAt = Now()
	if l.Source == "" {
		l.Source = "files"
	}
	p, _ := json.Marshal(l.Paths)
	_, err := d.ExecContext(ctx, `INSERT INTO libraries (id, name, type, paths, source, created_at) VALUES (?,?,?,?,?,?)`,
		l.ID, l.Name, l.Type, string(p), l.Source, l.CreatedAt)
	return err
}

func (d *DB) UpdateLibrary(ctx context.Context, l *Library) error {
	p, _ := json.Marshal(l.Paths)
	_, err := d.ExecContext(ctx, `UPDATE libraries SET name=?, paths=? WHERE id=?`, l.Name, string(p), l.ID)
	return err
}

func (d *DB) DeleteLibrary(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM libraries WHERE id=?`, id)
	return err
}

func (d *DB) TouchLibraryScan(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `UPDATE libraries SET last_scan_at=? WHERE id=?`, Now(), id)
	return err
}

// ---------- items ----------

type Item struct {
	ID            string   `json:"id"`
	LibraryID     string   `json:"libraryId"`
	Kind          string   `json:"kind"`
	ParentID      string   `json:"parentId,omitempty"`
	ShowID        string   `json:"showId,omitempty"`
	MatchKey      string   `json:"-"`
	Title         string   `json:"title"`
	SortTitle     string   `json:"-"`
	OriginalTitle string   `json:"originalTitle,omitempty"`
	Year          int      `json:"year,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	Tagline       string   `json:"tagline,omitempty"`
	TMDBID        int      `json:"tmdbId,omitempty"`
	IMDBID        string   `json:"imdbId,omitempty"`
	SeasonNumber  int      `json:"seasonNumber"`
	EpisodeNumber int      `json:"episodeNumber"`
	PosterPath    string   `json:"posterPath,omitempty"`
	BackdropPath  string   `json:"backdropPath,omitempty"`
	StillPath     string   `json:"stillPath,omitempty"`
	LogoPath      string   `json:"logoPath,omitempty"`
	Rating        float64  `json:"rating,omitempty"`
	ContentRating string   `json:"contentRating,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	RuntimeMin    int      `json:"runtimeMin,omitempty"`
	AirDate       string   `json:"airDate,omitempty"`
	MatchState    string   `json:"matchState"`
	AddedAt       int64    `json:"addedAt"`
	UpdatedAt     int64    `json:"updatedAt"`

	// Per-user, filled by queries that join watch_state.
	Progress *WatchState `json:"progress,omitempty"`
	// ShowTitle is filled for episodes in mixed lists (home, search).
	ShowTitle string `json:"showTitle,omitempty"`
	// Aggregates for shows/seasons.
	ChildCount   int `json:"childCount,omitempty"`
	UnwatchedCnt int `json:"unwatchedCount,omitempty"`
}

const itemCols = `i.id, i.library_id, i.kind, COALESCE(i.parent_id,''), COALESCE(i.show_id,''), i.match_key, i.title, i.sort_title,
	i.original_title, i.year, i.overview, i.tagline, i.tmdb_id, i.imdb_id, i.season_number, i.episode_number, i.poster_path,
	i.backdrop_path, i.still_path, i.logo_path, i.rating, i.content_rating, i.genres, i.runtime_min, i.air_date, i.match_state,
	i.added_at, i.updated_at`

func scanItem(r interface{ Scan(...any) error }, extra ...any) (*Item, error) {
	it := &Item{}
	var genres string
	dest := []any{&it.ID, &it.LibraryID, &it.Kind, &it.ParentID, &it.ShowID, &it.MatchKey, &it.Title, &it.SortTitle,
		&it.OriginalTitle, &it.Year, &it.Overview, &it.Tagline, &it.TMDBID, &it.IMDBID, &it.SeasonNumber, &it.EpisodeNumber,
		&it.PosterPath, &it.BackdropPath, &it.StillPath, &it.LogoPath, &it.Rating, &it.ContentRating, &genres, &it.RuntimeMin,
		&it.AirDate, &it.MatchState, &it.AddedAt, &it.UpdatedAt}
	err := r.Scan(append(dest, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(genres), &it.Genres)
	return it, nil
}

func (d *DB) GetItem(ctx context.Context, id string) (*Item, error) {
	return scanItem(d.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id=?`, id))
}

// UpsertTopLevel finds or creates a movie/show by its match key (normalized title+year).
// Returns the item and whether it was created.
func (d *DB) UpsertTopLevel(ctx context.Context, it *Item) (*Item, bool, error) {
	existing, err := scanItem(d.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.library_id=? AND i.kind=? AND i.match_key=?`,
		it.LibraryID, it.Kind, it.MatchKey))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	return it, true, d.InsertItem(ctx, it)
}

func (d *DB) InsertItem(ctx context.Context, it *Item) error {
	if it.ID == "" {
		it.ID = NewID()
	}
	now := Now()
	if it.AddedAt == 0 {
		it.AddedAt = now
	}
	it.UpdatedAt = now
	if it.SortTitle == "" {
		it.SortTitle = SortTitle(it.Title)
	}
	if it.MatchState == "" {
		it.MatchState = "pending"
	}
	genres, _ := json.Marshal(it.Genres)
	_, err := d.ExecContext(ctx, `INSERT INTO items (id, library_id, kind, parent_id, show_id, match_key, title, sort_title, original_title,
		year, overview, tagline, tmdb_id, imdb_id, season_number, episode_number, poster_path, backdrop_path, still_path, logo_path, rating,
		content_rating, genres, runtime_min, air_date, match_state, added_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, it.LibraryID, it.Kind, nullStr(it.ParentID), nullStr(it.ShowID), it.MatchKey, it.Title, it.SortTitle, it.OriginalTitle,
		it.Year, it.Overview, it.Tagline, it.TMDBID, it.IMDBID, it.SeasonNumber, it.EpisodeNumber, it.PosterPath, it.BackdropPath,
		it.StillPath, it.LogoPath, it.Rating, it.ContentRating, string(genres), it.RuntimeMin, it.AirDate, it.MatchState, it.AddedAt, it.UpdatedAt)
	return err
}

// UpdateItemMetadata writes matched metadata (everything except identity/hierarchy).
func (d *DB) UpdateItemMetadata(ctx context.Context, it *Item) error {
	if it.SortTitle == "" || it.MatchState == "matched" {
		it.SortTitle = SortTitle(it.Title)
	}
	genres, _ := json.Marshal(it.Genres)
	_, err := d.ExecContext(ctx, `UPDATE items SET title=?, sort_title=?, original_title=?, year=?, overview=?, tagline=?, tmdb_id=?, imdb_id=?,
		poster_path=?, backdrop_path=?, still_path=?, logo_path=?, rating=?, content_rating=?, genres=?, runtime_min=?, air_date=?,
		match_state=?, updated_at=? WHERE id=?`,
		it.Title, it.SortTitle, it.OriginalTitle, it.Year, it.Overview, it.Tagline, it.TMDBID, it.IMDBID, it.PosterPath, it.BackdropPath,
		it.StillPath, it.LogoPath, it.Rating, it.ContentRating, string(genres), it.RuntimeMin, it.AirDate, it.MatchState, Now(), it.ID)
	return err
}

// FindChild returns a season (by number) under a show, or an episode (by number) under a season.
func (d *DB) FindChild(ctx context.Context, parentID, kind string, number int) (*Item, error) {
	col := "season_number"
	if kind == "episode" {
		col = "episode_number"
	}
	return scanItem(d.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.parent_id=? AND i.kind=? AND i.`+col+`=?`, parentID, kind, number))
}

type ItemQuery struct {
	LibraryIDs []string // restrict to these (access control); nil = no restriction
	LibraryID  string
	Kind       string
	ParentID   string
	ShowID     string
	Search     string
	Sort       string // "title" (default), "added", "year", "rating", "episode"
	Limit      int
	Offset     int
	UserID     string // join watch state for this user
	Genre      string // items tagged with this genre
	TMDBIDs    []int  // items matched to these TMDB IDs
	MinRating  float64

	// Smart-collection filters.
	AnyGenres      []string // tagged with at least one of these
	ExcludeGenres  []string
	YearFrom       int
	YearTo         int
	ContentRatings []string
	AddedSince     int64
	Unwatched      bool // needs UserID; shows count as unwatched while any episode is
	MinHeight      int  // movies with a file at least this tall (shows: any episode)
	HDR            bool
	CollectionID   string // manual collection members; Sort "custom" keeps their order
	WatchlistOf    string // this user's watchlist; Sort "watchlist" is newest added first
}

func (d *DB) QueryItems(ctx context.Context, q ItemQuery) ([]*Item, int, error) {
	var where []string
	var args []any
	if q.LibraryIDs != nil {
		if len(q.LibraryIDs) == 0 {
			return nil, 0, nil
		}
		where = append(where, `i.library_id IN (`+placeholders(len(q.LibraryIDs))+`)`)
		for _, l := range q.LibraryIDs {
			args = append(args, l)
		}
	}
	if q.LibraryID != "" {
		where = append(where, `i.library_id=?`)
		args = append(args, q.LibraryID)
	}
	if q.Kind != "" {
		kinds := strings.Split(q.Kind, ",")
		where = append(where, `i.kind IN (`+placeholders(len(kinds))+`)`)
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	if q.ParentID != "" {
		where = append(where, `i.parent_id=?`)
		args = append(args, q.ParentID)
	}
	if q.ShowID != "" {
		where = append(where, `i.show_id=?`)
		args = append(args, q.ShowID)
	}
	if q.Genre != "" {
		where = append(where, `EXISTS (SELECT 1 FROM json_each(i.genres) g WHERE g.value=?)`)
		args = append(args, q.Genre)
	}
	if q.TMDBIDs != nil {
		if len(q.TMDBIDs) == 0 {
			return nil, 0, nil
		}
		where = append(where, `i.tmdb_id IN (`+placeholders(len(q.TMDBIDs))+`)`)
		for _, id := range q.TMDBIDs {
			args = append(args, id)
		}
	}
	if q.MinRating > 0 {
		where = append(where, `i.rating >= ?`)
		args = append(args, q.MinRating)
	}
	if q.Search != "" {
		where = append(where, `(i.title LIKE ? OR i.original_title LIKE ?)`)
		s := "%" + q.Search + "%"
		args = append(args, s, s)
	}
	if len(q.AnyGenres) > 0 {
		where = append(where, `EXISTS (SELECT 1 FROM json_each(i.genres) g WHERE g.value IN (`+placeholders(len(q.AnyGenres))+`))`)
		for _, g := range q.AnyGenres {
			args = append(args, g)
		}
	}
	if len(q.ExcludeGenres) > 0 {
		where = append(where, `NOT EXISTS (SELECT 1 FROM json_each(i.genres) g WHERE g.value IN (`+placeholders(len(q.ExcludeGenres))+`))`)
		for _, g := range q.ExcludeGenres {
			args = append(args, g)
		}
	}
	if q.YearFrom > 0 {
		where = append(where, `i.year >= ?`)
		args = append(args, q.YearFrom)
	}
	if q.YearTo > 0 {
		where = append(where, `i.year <= ?`)
		args = append(args, q.YearTo)
	}
	if len(q.ContentRatings) > 0 {
		where = append(where, `i.content_rating IN (`+placeholders(len(q.ContentRatings))+`)`)
		for _, c := range q.ContentRatings {
			args = append(args, c)
		}
	}
	if q.AddedSince > 0 {
		where = append(where, `i.added_at >= ?`)
		args = append(args, q.AddedSince)
	}
	if q.Unwatched && q.UserID != "" {
		where = append(where, `(CASE WHEN i.kind IN ('show','season') THEN EXISTS (SELECT 1 FROM items e WHERE (e.show_id=i.id OR e.parent_id=i.id) AND e.kind='episode'
			AND NOT EXISTS (SELECT 1 FROM watch_state ws WHERE ws.item_id=e.id AND ws.user_id=? AND ws.watched=1))
			ELSE NOT EXISTS (SELECT 1 FROM watch_state ws WHERE ws.item_id=i.id AND ws.user_id=? AND ws.watched=1) END)`)
		args = append(args, q.UserID, q.UserID)
	}
	if q.MinHeight > 0 || q.HDR {
		cond := `f.height >= ?`
		fargs := []any{q.MinHeight}
		if q.HDR {
			cond += ` AND f.hdr != ''`
		}
		where = append(where, `EXISTS (SELECT 1 FROM media_files f WHERE (f.item_id=i.id OR f.item_id IN (SELECT e.id FROM items e WHERE e.show_id=i.id)) AND `+cond+`)`)
		args = append(args, fargs...)
	}
	if q.CollectionID != "" {
		where = append(where, `i.id IN (SELECT item_id FROM collection_items WHERE collection_id=?)`)
		args = append(args, q.CollectionID)
	}
	if q.WatchlistOf != "" {
		where = append(where, `i.id IN (SELECT item_id FROM watchlist WHERE user_id=?)`)
		args = append(args, q.WatchlistOf)
	}
	// Hide movies/episodes that have no playable file. Streaming libraries
	// list titles before their streams are found (they resolve on play), but
	// a streamed movie gives way once the same movie exists as a real file.
	where = append(where, `(i.kind IN ('show','season') OR EXISTS (SELECT 1 FROM media_files f WHERE f.item_id=i.id)
		OR i.library_id IN (SELECT id FROM libraries WHERE source='stream'))`)
	where = append(where, `NOT (i.kind='movie' AND i.tmdb_id>0 AND i.library_id IN (SELECT id FROM libraries WHERE source='stream')
		AND EXISTS (SELECT 1 FROM items x JOIN media_files xf ON xf.item_id=x.id JOIN libraries xl ON xl.id=x.library_id
			WHERE x.kind='movie' AND x.tmdb_id=i.tmdb_id AND xl.source='files'))`)
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM items i`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "i.sort_title, i.year"
	var orderArgs []any
	switch q.Sort {
	case "custom":
		if q.CollectionID != "" {
			order = "(SELECT ci.position FROM collection_items ci WHERE ci.collection_id=? AND ci.item_id=i.id), i.sort_title"
			orderArgs = append(orderArgs, q.CollectionID)
		}
	case "watchlist":
		if q.WatchlistOf != "" {
			order = "(SELECT wl.added_at FROM watchlist wl WHERE wl.user_id=? AND wl.item_id=i.id) DESC"
			orderArgs = append(orderArgs, q.WatchlistOf)
		}
	case "added_asc":
		order = "i.added_at"
	case "year_asc":
		order = "i.year, i.sort_title"
	case "added":
		order = "i.added_at DESC"
	case "year":
		order = "i.year DESC, i.sort_title"
	case "rating":
		order = "i.rating DESC, i.sort_title"
	case "random":
		order = "RANDOM()"
	case "episode":
		order = "i.season_number, i.episode_number"
	}
	limit := q.Limit
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	sel := `SELECT ` + itemCols + `, ` + aggCols + ` FROM items i`
	selArgs := []any{}
	if q.UserID != "" {
		sel = `SELECT ` + itemCols + `, ` + aggColsUser + `, w.position, w.duration, w.watched, w.updated_at FROM items i LEFT JOIN watch_state w ON w.item_id=i.id AND w.user_id=?`
		selArgs = append(selArgs, q.UserID, q.UserID, q.UserID)
	}
	all := append(append(append(selArgs, args...), orderArgs...), limit, q.Offset)
	rows, err := d.QueryContext(ctx, sel+w+` ORDER BY `+order+` LIMIT ? OFFSET ?`, all...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		var childCount, unwatched int
		var pos, dur sql.NullFloat64
		var watched, upd sql.NullInt64
		var it *Item
		if q.UserID != "" {
			it, err = scanItem(rows, &childCount, &unwatched, &pos, &dur, &watched, &upd)
		} else {
			it, err = scanItem(rows, &childCount, &unwatched)
		}
		if err != nil {
			return nil, 0, err
		}
		it.ChildCount, it.UnwatchedCnt = childCount, unwatched
		if pos.Valid {
			it.Progress = &WatchState{Position: pos.Float64, Duration: dur.Float64, Watched: watched.Int64 == 1, UpdatedAt: upd.Int64}
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// aggCols counts episodes for shows/seasons (childCount) and unwatched episodes.
const aggCols = `(CASE i.kind WHEN 'show' THEN (SELECT COUNT(*) FROM items e WHERE e.show_id=i.id AND e.kind='episode')
	WHEN 'season' THEN (SELECT COUNT(*) FROM items e WHERE e.parent_id=i.id) ELSE 0 END), 0`

const aggColsUser = `(CASE i.kind WHEN 'show' THEN (SELECT COUNT(*) FROM items e WHERE e.show_id=i.id AND e.kind='episode')
	WHEN 'season' THEN (SELECT COUNT(*) FROM items e WHERE e.parent_id=i.id) ELSE 0 END),
	(CASE i.kind WHEN 'show' THEN (SELECT COUNT(*) FROM items e WHERE e.show_id=i.id AND e.kind='episode'
		AND NOT EXISTS (SELECT 1 FROM watch_state ws WHERE ws.item_id=e.id AND ws.user_id=? AND ws.watched=1))
	WHEN 'season' THEN (SELECT COUNT(*) FROM items e WHERE e.parent_id=i.id
		AND NOT EXISTS (SELECT 1 FROM watch_state ws WHERE ws.item_id=e.id AND ws.user_id=? AND ws.watched=1)) ELSE 0 END)`

// PendingMatches returns top-level items (and their episodes via show) awaiting metadata.
func (d *DB) PendingMatches(ctx context.Context, limit int) ([]*Item, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.match_state='pending' AND i.kind IN ('movie','show') ORDER BY i.added_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (d *DB) ItemsByShow(ctx context.Context, showID string) ([]*Item, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.show_id=? ORDER BY i.season_number, i.episode_number`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PruneEmpty deletes movies/episodes with no files, then empty seasons and shows.
func (d *DB) PruneEmpty(ctx context.Context, libraryID string) error {
	stmts := []string{
		`DELETE FROM items WHERE library_id=? AND kind IN ('movie','episode') AND NOT EXISTS (SELECT 1 FROM media_files f WHERE f.item_id=items.id)`,
		`DELETE FROM items WHERE library_id=? AND kind='season' AND NOT EXISTS (SELECT 1 FROM items e WHERE e.parent_id=items.id)`,
		`DELETE FROM items WHERE library_id=? AND kind='show' AND NOT EXISTS (SELECT 1 FROM items s WHERE s.parent_id=items.id)`,
	}
	for _, s := range stmts {
		if _, err := d.ExecContext(ctx, s, libraryID); err != nil {
			return err
		}
	}
	return nil
}

// ---------- media files ----------

type MediaFile struct {
	ID        string `json:"id"`
	ItemID    string `json:"itemId"`
	LibraryID string `json:"libraryId"`
	Path      string `json:"-"`
	Size      int64  `json:"size"`
	MTime     int64  `json:"-"`
	// StreamID is set for a stored stream (no file on disk).
	StreamID   string  `json:"streamId,omitempty"`
	Container  string  `json:"container"`
	Duration   float64 `json:"duration"`
	VideoCodec string  `json:"videoCodec"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	HDR        string  `json:"hdr,omitempty"`
	DVProfile  int     `json:"dvProfile,omitempty"`
	Probe      string  `json:"-"`
	ProbedAt   int64   `json:"-"`
	// StreamRank orders a quality's streams (0 is the best); Tag says
	// where a stream comes from ("Usenet · Remux").
	StreamRank int    `json:"streamRank,omitempty"`
	Tag        string `json:"tag,omitempty"`
}

const fileCols = `id, item_id, library_id, path, size, mtime, COALESCE(stream_id,''), container, duration,
	video_codec, width, height, hdr, dv_profile, probe, probed_at, stream_rank, tag`

func scanFile(r interface{ Scan(...any) error }) (*MediaFile, error) {
	f := &MediaFile{}
	err := r.Scan(&f.ID, &f.ItemID, &f.LibraryID, &f.Path, &f.Size, &f.MTime, &f.StreamID, &f.Container,
		&f.Duration, &f.VideoCodec, &f.Width, &f.Height, &f.HDR, &f.DVProfile, &f.Probe, &f.ProbedAt, &f.StreamRank, &f.Tag)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

func (d *DB) GetFile(ctx context.Context, id string) (*MediaFile, error) {
	return scanFile(d.QueryRowContext(ctx, `SELECT `+fileCols+` FROM media_files WHERE id=?`, id))
}

func (d *DB) FileByPath(ctx context.Context, path string) (*MediaFile, error) {
	return scanFile(d.QueryRowContext(ctx, `SELECT `+fileCols+` FROM media_files WHERE path=?`, path))
}

func (d *DB) FilesForItem(ctx context.Context, itemID string) ([]*MediaFile, error) {
	return d.queryFiles(ctx, `SELECT `+fileCols+` FROM media_files WHERE item_id=? ORDER BY height DESC, stream_rank, size DESC`, itemID)
}

func (d *DB) FilesForLibrary(ctx context.Context, libraryID string) ([]*MediaFile, error) {
	return d.queryFiles(ctx, `SELECT `+fileCols+` FROM media_files WHERE library_id=? AND stream_id IS NULL`, libraryID)
}

func (d *DB) UnprobedFiles(ctx context.Context, limit int) ([]*MediaFile, error) {
	return d.queryFiles(ctx, `SELECT `+fileCols+` FROM media_files WHERE probed_at=0 AND stream_id IS NULL LIMIT ?`, limit)
}

func (d *DB) queryFiles(ctx context.Context, q string, args ...any) ([]*MediaFile, error) {
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MediaFile
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (d *DB) InsertFile(ctx context.Context, f *MediaFile) error {
	if f.ID == "" {
		f.ID = NewID()
	}
	_, err := d.ExecContext(ctx, `INSERT INTO media_files (id, item_id, library_id, path, size, mtime, stream_id,
		container, video_codec, width, height, hdr) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, f.ID, f.ItemID, f.LibraryID, f.Path, f.Size, f.MTime,
		nullStr(f.StreamID), f.Container, f.VideoCodec, f.Width, f.Height, f.HDR)
	return err
}

// UnguessedFiles lists unprobed files with no media info at all.
func (d *DB) UnguessedFiles(ctx context.Context) ([]*MediaFile, error) {
	return d.queryFiles(ctx, `SELECT `+fileCols+` FROM media_files WHERE probed_at=0 AND width=0 AND container='' AND video_codec='' AND hdr=''`)
}

// UpdateFileGuesses saves name-based media info without marking files probed.
func (d *DB) UpdateFileGuesses(ctx context.Context, files []*MediaFile) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `UPDATE media_files SET container=?, video_codec=?, width=?, height=?, hdr=? WHERE id=? AND probed_at=0`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, f := range files {
		if _, err := st.ExecContext(ctx, f.Container, f.VideoCodec, f.Width, f.Height, f.HDR, f.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateFileStat records a changed size/mtime and invalidates the probe.
func (d *DB) UpdateFileStat(ctx context.Context, id string, size, mtime int64) error {
	_, err := d.ExecContext(ctx, `UPDATE media_files SET size=?, mtime=?, probed_at=0, keyframes=NULL WHERE id=?`, size, mtime, id)
	return err
}

func (d *DB) UpdateFileProbe(ctx context.Context, f *MediaFile) error {
	_, err := d.ExecContext(ctx, `UPDATE media_files SET container=?, duration=?, video_codec=?, width=?, height=?, hdr=?, dv_profile=?,
		probe=?, probed_at=? WHERE id=?`, f.Container, f.Duration, f.VideoCodec, f.Width, f.Height, f.HDR, f.DVProfile, f.Probe, Now(), f.ID)
	return err
}

func (d *DB) DeleteFile(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM media_files WHERE id=?`, id)
	return err
}

func (d *DB) Keyframes(ctx context.Context, fileID string) ([]byte, error) {
	var b []byte
	err := d.QueryRowContext(ctx, `SELECT keyframes FROM media_files WHERE id=?`, fileID).Scan(&b)
	return b, err
}

func (d *DB) SetKeyframes(ctx context.Context, fileID string, b []byte) error {
	_, err := d.ExecContext(ctx, `UPDATE media_files SET keyframes=? WHERE id=?`, b, fileID)
	return err
}

// ---------- watch state ----------

type WatchState struct {
	Position  float64 `json:"position"`
	Duration  float64 `json:"duration"`
	Watched   bool    `json:"watched"`
	UpdatedAt int64   `json:"updatedAt"`
}

// SetProgress records a position; reaching watchedAt (a fraction of dur)
// marks the item watched. It reports whether this report did that.
func (d *DB) SetProgress(ctx context.Context, userID, itemID string, pos, dur, watchedAt float64) (bool, error) {
	watched := dur > 0 && pos/dur >= watchedAt
	var was bool
	if watched {
		pos = 0
		_ = d.QueryRowContext(ctx, `SELECT watched FROM watch_state WHERE user_id=? AND item_id=?`, userID, itemID).Scan(&was)
	}
	_, err := d.ExecContext(ctx, `INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET position=excluded.position, duration=excluded.duration,
		watched=MAX(watch_state.watched, excluded.watched), updated_at=excluded.updated_at`,
		userID, itemID, pos, dur, watched, Now())
	return watched && !was, err
}

// SetWatched marks an item (and for shows/seasons, every episode under it).
func (d *DB) SetWatched(ctx context.Context, userID, itemID string, watched bool) error {
	_, err := d.ExecContext(ctx, `INSERT INTO watch_state (user_id, item_id, position, duration, watched, updated_at)
		SELECT ?, id, 0, 0, ?, ? FROM items WHERE id=? OR parent_id=? OR show_id=?
		ON CONFLICT(user_id, item_id) DO UPDATE SET watched=excluded.watched, position=0, updated_at=excluded.updated_at`,
		userID, watched, Now(), itemID, itemID, itemID)
	return err
}

// ContinueWatching returns movies and episodes watched for more than after
// seconds but not finished, most recent first.
func (d *DB) ContinueWatching(ctx context.Context, userID string, libraryIDs []string, after, limit int) ([]*Item, error) {
	if len(libraryIDs) == 0 {
		return nil, nil
	}
	args := []any{userID, after}
	for _, l := range libraryIDs {
		args = append(args, l)
	}
	args = append(args, limit)
	rows, err := d.QueryContext(ctx, `SELECT `+itemCols+`, w.position, w.duration, w.watched, w.updated_at FROM watch_state w
		JOIN items i ON i.id=w.item_id
		WHERE w.user_id=? AND w.watched=0 AND w.position > ? AND i.kind IN ('movie','episode')
		AND i.library_id IN (`+placeholders(len(libraryIDs))+`)
		ORDER BY w.updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		ws := &WatchState{}
		it, err := scanItem(rows, &ws.Position, &ws.Duration, &ws.Watched, &ws.UpdatedAt)
		if err != nil {
			return nil, err
		}
		it.Progress = ws
		out = append(out, it)
	}
	return out, rows.Err()
}

// NextEpisode returns the first unwatched episode of a show for a user.
func (d *DB) NextEpisode(ctx context.Context, userID, showID string) (*Item, error) {
	return scanItem(d.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.show_id=? AND i.kind='episode' AND i.season_number > 0
		AND NOT EXISTS (SELECT 1 FROM watch_state w WHERE w.item_id=i.id AND w.user_id=? AND w.watched=1)
		AND EXISTS (SELECT 1 FROM media_files f WHERE f.item_id=i.id)
		ORDER BY i.season_number, i.episode_number LIMIT 1`, showID, userID))
}

// Watchlisted lists every user's watchlist, newest added first.
func (d *DB) Watchlisted(ctx context.Context) (users, items []string, err error) {
	rows, err := d.QueryContext(ctx, `SELECT user_id, item_id FROM watchlist ORDER BY added_at DESC`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u, i string
		if err := rows.Scan(&u, &i); err != nil {
			return nil, nil, err
		}
		users, items = append(users, u), append(items, i)
	}
	return users, items, rows.Err()
}

// FirstUnwatched is a show's first episode the user hasn't watched,
// whether or not it has a file yet.
func (d *DB) FirstUnwatched(ctx context.Context, userID, showID string) (*Item, error) {
	return scanItem(d.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.show_id=? AND i.kind='episode' AND i.season_number > 0
		AND NOT EXISTS (SELECT 1 FROM watch_state w WHERE w.item_id=i.id AND w.user_id=? AND w.watched=1)
		ORDER BY i.season_number, i.episode_number LIMIT 1`, showID, userID))
}

// SetWatchlist adds an item to (or removes it from) a user's watchlist.
func (d *DB) SetWatchlist(ctx context.Context, userID, itemID string, on bool) error {
	if !on {
		_, err := d.ExecContext(ctx, `DELETE FROM watchlist WHERE user_id=? AND item_id=?`, userID, itemID)
		return err
	}
	_, err := d.ExecContext(ctx, `INSERT INTO watchlist (user_id, item_id, added_at) VALUES (?,?,?) ON CONFLICT DO NOTHING`, userID, itemID, Now())
	return err
}

func (d *DB) OnWatchlist(ctx context.Context, userID, itemID string) bool {
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM watchlist WHERE user_id=? AND item_id=?`, userID, itemID).Scan(&n)
	return n > 0
}

func (d *DB) GetWatchState(ctx context.Context, userID, itemID string) (*WatchState, error) {
	ws := &WatchState{}
	err := d.QueryRowContext(ctx, `SELECT position, duration, watched, updated_at FROM watch_state WHERE user_id=? AND item_id=?`, userID, itemID).
		Scan(&ws.Position, &ws.Duration, &ws.Watched, &ws.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return ws, err
}

// ---------- metadata cache ----------

func (d *DB) CacheGet(ctx context.Context, key string, maxAge int64) ([]byte, bool) {
	var b []byte
	var at int64
	if err := d.QueryRowContext(ctx, `SELECT body, fetched_at FROM metadata_cache WHERE key=?`, key).Scan(&b, &at); err != nil {
		return nil, false
	}
	if Now()-at > maxAge {
		return nil, false
	}
	return b, true
}

func (d *DB) CachePut(ctx context.Context, key string, body []byte) {
	_, _ = d.ExecContext(ctx, `INSERT INTO metadata_cache (key, body, fetched_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET body=excluded.body, fetched_at=excluded.fetched_at`, key, body, Now())
}

// ---------- helpers ----------

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SortTitle strips leading articles so "The Matrix" sorts under M.
func SortTitle(t string) string {
	l := strings.ToLower(strings.TrimSpace(t))
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(l, a) && len(l) > len(a) {
			return l[len(a):]
		}
	}
	return l
}
