package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ---------- user preferences ----------

// Prefs follow the account to every device that uses this server.
type Prefs struct {
	// AudioLanguage is an ISO 639-1 code ("en"); empty keeps the file's default track.
	AudioLanguage string `json:"audioLanguage"`
	// SubtitleLanguage turns subtitles on in this language ("fr"); empty is off.
	SubtitleLanguage string `json:"subtitleLanguage"`
}

func DefaultPrefs() Prefs { return Prefs{AudioLanguage: "en"} }

func (d *DB) GetPrefs(ctx context.Context, userID string) (Prefs, error) {
	p := DefaultPrefs()
	var body string
	err := d.QueryRowContext(ctx, `SELECT body FROM user_prefs WHERE user_id=?`, userID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(body), &p)
	return p, nil
}

func (d *DB) SetPrefs(ctx context.Context, userID string, p Prefs) error {
	b, _ := json.Marshal(p)
	_, err := d.ExecContext(ctx, `INSERT INTO user_prefs (user_id, body, updated_at) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET body=excluded.body, updated_at=excluded.updated_at`, userID, string(b), Now())
	return err
}

// ---------- collections ----------

// CollectionRules drive smart collections (library filters) and TMDB
// collections (an external source matched against the library).
type CollectionRules struct {
	// Smart
	Kind           string   `json:"kind,omitempty"` // "movie", "show" or "" for both
	Genres         []string `json:"genres,omitempty"`
	ExcludeGenres  []string `json:"excludeGenres,omitempty"`
	YearFrom       int      `json:"yearFrom,omitempty"`
	YearTo         int      `json:"yearTo,omitempty"`
	MinRating      float64  `json:"minRating,omitempty"`
	ContentRatings []string `json:"contentRatings,omitempty"`
	AddedDays      int      `json:"addedDays,omitempty"`
	Unwatched      bool     `json:"unwatched,omitempty"`
	MinHeight      int      `json:"minHeight,omitempty"`
	HDR            bool     `json:"hdr,omitempty"`
	TitleContains  string   `json:"titleContains,omitempty"`

	// TMDB: "collection" (a franchise), "list", "trending", "popular",
	// "top_rated", "now_playing", "upcoming", "airing".
	Source    string `json:"source,omitempty"`
	SourceID  int    `json:"sourceId,omitempty"`
	MediaType string `json:"mediaType,omitempty"` // "movie" or "show" for chart sources
}

// CollectionTitle is one entry of a TMDB-sourced collection, in source order.
type CollectionTitle struct {
	TMDBID     int    `json:"tmdbId"`
	MediaType  string `json:"mediaType"`
	Title      string `json:"title"`
	Year       int    `json:"year,omitempty"`
	PosterPath string `json:"posterPath,omitempty"`
}

type Collection struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Kind         string            `json:"kind"`
	Rules        CollectionRules   `json:"rules"`
	LibraryID    string            `json:"libraryId,omitempty"`
	PosterPath   string            `json:"posterPath,omitempty"`
	BackdropPath string            `json:"backdropPath,omitempty"`
	Sort         string            `json:"sort"`
	ItemLimit    int               `json:"itemLimit"`
	ShowOnHome   bool              `json:"showOnHome"`
	HomeOrder    int               `json:"homeOrder"`
	ShowMissing  bool              `json:"showMissing"`
	VisibleFrom  string            `json:"visibleFrom"` // "MM-DD", empty = always
	VisibleUntil string            `json:"visibleUntil"`
	Titles       []CollectionTitle `json:"-"`
	SyncedAt     int64             `json:"syncedAt"`
	SyncError    string            `json:"syncError,omitempty"`
	CreatedAt    int64             `json:"createdAt"`
	UpdatedAt    int64             `json:"updatedAt"`

	// Filled by handlers.
	ItemCount int  `json:"itemCount"`
	Active    bool `json:"active"`
}

// ActiveOn reports whether the visibility window (which may wrap the new
// year, e.g. 12-01 to 01-06) includes t.
func (c *Collection) ActiveOn(t time.Time) bool {
	if c.VisibleFrom == "" || c.VisibleUntil == "" {
		return true
	}
	md := t.Format("01-02")
	if c.VisibleFrom <= c.VisibleUntil {
		return md >= c.VisibleFrom && md <= c.VisibleUntil
	}
	return md >= c.VisibleFrom || md <= c.VisibleUntil
}

const collectionCols = `id, name, description, kind, rules, COALESCE(library_id,''), poster_path, backdrop_path, sort, item_limit,
	show_on_home, home_order, show_missing, visible_from, visible_until, titles, synced_at, sync_error, created_at, updated_at`

func scanCollection(r interface{ Scan(...any) error }) (*Collection, error) {
	c := &Collection{}
	var rules, titles string
	err := r.Scan(&c.ID, &c.Name, &c.Description, &c.Kind, &rules, &c.LibraryID, &c.PosterPath, &c.BackdropPath, &c.Sort, &c.ItemLimit,
		&c.ShowOnHome, &c.HomeOrder, &c.ShowMissing, &c.VisibleFrom, &c.VisibleUntil, &titles, &c.SyncedAt, &c.SyncError, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(rules), &c.Rules)
	_ = json.Unmarshal([]byte(titles), &c.Titles)
	return c, nil
}

func (d *DB) ListCollections(ctx context.Context) ([]*Collection, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+collectionCols+` FROM collections ORDER BY home_order, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Collection
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) GetCollection(ctx context.Context, id string) (*Collection, error) {
	return scanCollection(d.QueryRowContext(ctx, `SELECT `+collectionCols+` FROM collections WHERE id=?`, id))
}

func (d *DB) CollectionByTMDB(ctx context.Context, source string, sourceID int) (*Collection, error) {
	cs, err := d.ListCollections(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		if c.Kind == "tmdb" && c.Rules.Source == source && c.Rules.SourceID == sourceID {
			return c, nil
		}
	}
	return nil, ErrNotFound
}

func (d *DB) SaveCollection(ctx context.Context, c *Collection) error {
	now := Now()
	if c.ID == "" {
		c.ID = NewID()
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	rules, _ := json.Marshal(c.Rules)
	if c.Titles == nil {
		c.Titles = []CollectionTitle{}
	}
	titles, _ := json.Marshal(c.Titles)
	_, err := d.ExecContext(ctx, `INSERT INTO collections (`+strings.Replace(collectionCols, "COALESCE(library_id,'')", "library_id", 1)+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, kind=excluded.kind, rules=excluded.rules,
		library_id=excluded.library_id, poster_path=excluded.poster_path, backdrop_path=excluded.backdrop_path, sort=excluded.sort,
		item_limit=excluded.item_limit, show_on_home=excluded.show_on_home, home_order=excluded.home_order, show_missing=excluded.show_missing,
		visible_from=excluded.visible_from, visible_until=excluded.visible_until, titles=excluded.titles, synced_at=excluded.synced_at,
		sync_error=excluded.sync_error, updated_at=excluded.updated_at`,
		c.ID, c.Name, c.Description, c.Kind, string(rules), nullStr(c.LibraryID), c.PosterPath, c.BackdropPath, c.Sort, c.ItemLimit,
		c.ShowOnHome, c.HomeOrder, c.ShowMissing, c.VisibleFrom, c.VisibleUntil, string(titles), c.SyncedAt, c.SyncError, c.CreatedAt, c.UpdatedAt)
	return err
}

func (d *DB) DeleteCollection(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM collections WHERE id=?`, id)
	return err
}

// AddToCollection appends items to a manual collection (already-present items keep their place).
func (d *DB) AddToCollection(ctx context.Context, collectionID string, itemIDs []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pos int
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0) FROM collection_items WHERE collection_id=?`, collectionID).Scan(&pos)
	for _, id := range itemIDs {
		pos++
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO collection_items (collection_id, item_id, position, added_at) VALUES (?,?,?,?)`,
			collectionID, id, pos, Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) RemoveFromCollection(ctx context.Context, collectionID, itemID string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM collection_items WHERE collection_id=? AND item_id=?`, collectionID, itemID)
	return err
}

// ReorderCollection sets positions from the given order.
func (d *DB) ReorderCollection(ctx context.Context, collectionID string, itemIDs []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range itemIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE collection_items SET position=? WHERE collection_id=? AND item_id=?`, i+1, collectionID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CollectionsWithItem lists manual collections containing an item.
func (d *DB) CollectionsWithItem(ctx context.Context, itemID string) ([]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT collection_id FROM collection_items WHERE item_id=?`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------- genres ----------

type GenreCount struct {
	Genre        string `json:"genre"`
	Count        int    `json:"count"`
	Movies       int    `json:"movies"`
	Shows        int    `json:"shows"`
	BackdropPath string `json:"backdropPath,omitempty"`
}

// GenreCounts lists every genre among movies and shows in the given
// libraries, with a representative backdrop (the best-rated title's).
func (d *DB) GenreCounts(ctx context.Context, libraryIDs []string) ([]GenreCount, error) {
	if len(libraryIDs) == 0 {
		return []GenreCount{}, nil
	}
	args := []any{}
	for _, l := range libraryIDs {
		args = append(args, l)
	}
	rows, err := d.QueryContext(ctx, `SELECT g.value, COUNT(*), SUM(i.kind='movie'), SUM(i.kind='show'),
		(SELECT i2.backdrop_path FROM items i2, json_each(i2.genres) g2 WHERE g2.value=g.value AND i2.backdrop_path != ''
			AND i2.library_id IN (`+placeholders(len(libraryIDs))+`) ORDER BY i2.rating DESC LIMIT 1)
		FROM items i, json_each(i.genres) g
		WHERE i.kind IN ('movie','show') AND i.library_id IN (`+placeholders(len(libraryIDs))+`)
		GROUP BY g.value ORDER BY g.value`, append(append([]any{}, args...), args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GenreCount{}
	for rows.Next() {
		var g GenreCount
		var bd sql.NullString
		if err := rows.Scan(&g.Genre, &g.Count, &g.Movies, &g.Shows, &bd); err != nil {
			return nil, err
		}
		g.BackdropPath = bd.String
		out = append(out, g)
	}
	return out, rows.Err()
}

// ---------- play history ----------

type PlayStart struct {
	SessionID, UserID, FileID, Method, Engine, Client string
	Item                                              *Item
	ShowTitle                                         string
	VideoCopy                                         bool
	Height                                            int
	Position, Duration                                float64
}

// StartPlay records a playback. Restarting the same title within ten minutes
// (a quality or audio change opens a new session) continues the same entry.
func (d *DB) StartPlay(ctx context.Context, p PlayStart) error {
	now := Now()
	it := p.Item
	var id string
	err := d.QueryRowContext(ctx, `SELECT id FROM play_history WHERE user_id=? AND item_id=? AND completed=0 AND last_seen_at > ?
		ORDER BY last_seen_at DESC LIMIT 1`, p.UserID, it.ID, now-600).Scan(&id)
	if err == nil {
		_, err = d.ExecContext(ctx, `UPDATE play_history SET session_id=?, file_id=?, method=?, engine=?, video_copy=?, height=?, client=?,
			last_seen_at=? WHERE id=?`, p.SessionID, p.FileID, p.Method, p.Engine, p.VideoCopy, p.Height, p.Client, now, id)
		return err
	}
	poster := it.PosterPath
	_, err = d.ExecContext(ctx, `INSERT INTO play_history (id, session_id, user_id, item_id, show_id, library_id, kind, title, show_title, year,
		poster_path, file_id, method, engine, video_copy, height, client, started_at, last_seen_at, position_start, position_end, duration)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		NewID(), p.SessionID, p.UserID, it.ID, it.ShowID, it.LibraryID, it.Kind, it.Title, p.ShowTitle, it.Year, poster,
		p.FileID, p.Method, p.Engine, p.VideoCopy, p.Height, p.Client, now, now, p.Position, p.Position, p.Duration)
	return err
}

// TrackPlay adds watch time from a progress report. Time counts only as far
// as the position advanced, capped by wall-clock time, so pauses and seeks
// don't inflate it.
func (d *DB) TrackPlay(ctx context.Context, userID, itemID string, pos, dur float64) error {
	now := Now()
	var id string
	var last int64
	var prev float64
	err := d.QueryRowContext(ctx, `SELECT id, last_seen_at, position_end FROM play_history WHERE user_id=? AND item_id=? AND last_seen_at > ?
		ORDER BY last_seen_at DESC LIMIT 1`, userID, itemID, now-6*3600).Scan(&id, &last, &prev)
	if err != nil {
		return nil // no recorded start (e.g. marked watched from a menu)
	}
	delta := pos - prev
	if lim := float64(now-last) + 5; delta > lim {
		delta = lim
	}
	if delta < 0 {
		delta = 0
	}
	completed := dur > 0 && pos/dur >= 0.9
	_, err = d.ExecContext(ctx, `UPDATE play_history SET last_seen_at=?, position_end=?, duration=CASE WHEN ? > 0 THEN ? ELSE duration END,
		played_seconds=played_seconds+?, completed=MAX(completed, ?) WHERE id=?`, now, pos, dur, dur, delta, completed, id)
	return err
}

// PlayBySession returns the latest position recorded for a live session.
func (d *DB) PlayBySession(ctx context.Context, sessionID string) (pos, dur float64, client string) {
	_ = d.QueryRowContext(ctx, `SELECT position_end, duration, client FROM play_history WHERE session_id=? ORDER BY last_seen_at DESC LIMIT 1`, sessionID).
		Scan(&pos, &dur, &client)
	return
}

type HistoryRow struct {
	ID            string  `json:"id"`
	UserID        string  `json:"userId"`
	UserName      string  `json:"userName"`
	ItemID        string  `json:"itemId,omitempty"`
	ShowID        string  `json:"showId,omitempty"`
	Kind          string  `json:"kind"`
	Title         string  `json:"title"`
	ShowTitle     string  `json:"showTitle,omitempty"`
	Year          int     `json:"year,omitempty"`
	PosterPath    string  `json:"posterPath,omitempty"`
	Method        string  `json:"method"`
	Engine        string  `json:"engine"`
	VideoCopy     bool    `json:"videoCopy"`
	Height        int     `json:"height"`
	Client        string  `json:"client"`
	StartedAt     int64   `json:"startedAt"`
	LastSeenAt    int64   `json:"lastSeenAt"`
	PositionEnd   float64 `json:"position"`
	Duration      float64 `json:"duration"`
	PlayedSeconds float64 `json:"playedSeconds"`
	Completed     bool    `json:"completed"`
}

const userNameExpr = `COALESCE(NULLIF(u.display_name,''), NULLIF(u.username,''), NULLIF(u.email,''), u.id)`

func (d *DB) RecentHistory(ctx context.Context, since int64, userID string, limit int) ([]HistoryRow, error) {
	q := `SELECT h.id, h.user_id, ` + userNameExpr + `, COALESCE(h.item_id,''), h.show_id, h.kind, h.title, h.show_title, h.year, h.poster_path,
		h.method, h.engine, h.video_copy, h.height, h.client, h.started_at, h.last_seen_at, h.position_end, h.duration, h.played_seconds, h.completed
		FROM play_history h JOIN users u ON u.id=h.user_id WHERE h.started_at >= ?`
	args := []any{since}
	if userID != "" {
		q += ` AND h.user_id=?`
		args = append(args, userID)
	}
	q += ` ORDER BY h.started_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryRow{}
	for rows.Next() {
		var h HistoryRow
		if err := rows.Scan(&h.ID, &h.UserID, &h.UserName, &h.ItemID, &h.ShowID, &h.Kind, &h.Title, &h.ShowTitle, &h.Year, &h.PosterPath,
			&h.Method, &h.Engine, &h.VideoCopy, &h.Height, &h.Client, &h.StartedAt, &h.LastSeenAt, &h.PositionEnd, &h.Duration,
			&h.PlayedSeconds, &h.Completed); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---------- stats ----------

type DayStat struct {
	Date  string  `json:"date"`
	Plays int     `json:"plays"`
	Hours float64 `json:"hours"`
}

type TopTitle struct {
	ItemID     string  `json:"itemId"`
	Title      string  `json:"title"`
	Year       int     `json:"year,omitempty"`
	PosterPath string  `json:"posterPath,omitempty"`
	Plays      int     `json:"plays"`
	Users      int     `json:"users"`
	Hours      float64 `json:"hours"`
}

type TopUser struct {
	UserID   string  `json:"userId"`
	Name     string  `json:"name"`
	Plays    int     `json:"plays"`
	Hours    float64 `json:"hours"`
	LastSeen int64   `json:"lastSeen"`
	LastPlay string  `json:"lastPlayed"`
}

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type PlayStats struct {
	Plays     int        `json:"plays"`
	Hours     float64    `json:"hours"`
	Users     int        `json:"users"`
	Titles    int        `json:"titles"`
	Daily     []DayStat  `json:"daily"`
	ByHour    []int      `json:"byHour"`
	ByWeekday []int      `json:"byWeekday"` // Sunday first
	ByMethod  []Count    `json:"byMethod"`
	ByClient  []Count    `json:"byClient"`
	TopMovies []TopTitle `json:"topMovies"`
	TopShows  []TopTitle `json:"topShows"`
	TopUsers  []TopUser  `json:"topUsers"`
}

// Stats aggregates play history since a time. tzOffset (seconds east of
// UTC) buckets days and hours in the viewer's local time.
func (d *DB) Stats(ctx context.Context, since int64, days int, tzOffset int) (*PlayStats, error) {
	s := &PlayStats{ByHour: make([]int, 24), ByWeekday: make([]int, 7)}
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(played_seconds),0)/3600.0, COUNT(DISTINCT user_id),
		COUNT(DISTINCT COALESCE(NULLIF(show_id,''), item_id)) FROM play_history WHERE started_at >= ?`, since).
		Scan(&s.Plays, &s.Hours, &s.Users, &s.Titles); err != nil {
		return nil, err
	}

	byDay := map[string]*DayStat{}
	start := time.Unix(since, 0).UTC().Add(time.Duration(tzOffset) * time.Second)
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		byDay[date] = &DayStat{Date: date}
	}
	rows, err := d.QueryContext(ctx, `SELECT date(started_at + ?, 'unixepoch'), CAST(strftime('%H', started_at + ?, 'unixepoch') AS INTEGER),
		CAST(strftime('%w', started_at + ?, 'unixepoch') AS INTEGER), played_seconds FROM play_history WHERE started_at >= ?`,
		tzOffset, tzOffset, tzOffset, since)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day string
		var hour, wd int
		var secs float64
		if err := rows.Scan(&day, &hour, &wd, &secs); err != nil {
			rows.Close()
			return nil, err
		}
		if ds := byDay[day]; ds != nil {
			ds.Plays++
			ds.Hours += secs / 3600
		}
		s.ByHour[hour%24]++
		s.ByWeekday[wd%7]++
	}
	rows.Close()
	for i := 0; i < days; i++ {
		s.Daily = append(s.Daily, *byDay[start.AddDate(0, 0, i).Format("2006-01-02")])
	}

	counts := func(col string) ([]Count, error) {
		rows, err := d.QueryContext(ctx, `SELECT `+col+`, COUNT(*) c FROM play_history WHERE started_at >= ? GROUP BY 1 ORDER BY c DESC LIMIT 12`, since)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []Count{}
		for rows.Next() {
			var c Count
			if err := rows.Scan(&c.Name, &c.Count); err != nil {
				return nil, err
			}
			if c.Name == "" {
				c.Name = "Unknown"
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	if s.ByMethod, err = counts(`CASE WHEN method='transcode' AND video_copy=1 THEN 'audio transcode' ELSE method END`); err != nil {
		return nil, err
	}
	if s.ByClient, err = counts(`client`); err != nil {
		return nil, err
	}

	top := func(q string) ([]TopTitle, error) {
		rows, err := d.QueryContext(ctx, q, since)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []TopTitle{}
		for rows.Next() {
			var t TopTitle
			if err := rows.Scan(&t.ItemID, &t.Title, &t.Year, &t.PosterPath, &t.Plays, &t.Users, &t.Hours); err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		return out, rows.Err()
	}
	if s.TopMovies, err = top(`SELECT COALESCE(h.item_id,''), h.title, h.year, COALESCE(i.poster_path, h.poster_path), COUNT(*) c,
		COUNT(DISTINCT h.user_id), SUM(h.played_seconds)/3600.0 FROM play_history h LEFT JOIN items i ON i.id=h.item_id
		WHERE h.kind='movie' AND h.started_at >= ? GROUP BY h.title, h.year ORDER BY c DESC, 7 DESC LIMIT 10`); err != nil {
		return nil, err
	}
	if s.TopShows, err = top(`SELECT h.show_id, h.show_title, COALESCE(sh.year,0), COALESCE(sh.poster_path, h.poster_path), COUNT(*) c,
		COUNT(DISTINCT h.user_id), SUM(h.played_seconds)/3600.0 FROM play_history h LEFT JOIN items sh ON sh.id=h.show_id
		WHERE h.kind='episode' AND h.started_at >= ? GROUP BY h.show_id ORDER BY c DESC, 7 DESC LIMIT 10`); err != nil {
		return nil, err
	}

	rows, err = d.QueryContext(ctx, `SELECT h.user_id, `+userNameExpr+`, COUNT(*), SUM(h.played_seconds)/3600.0, MAX(h.last_seen_at),
		(SELECT CASE WHEN h2.show_title != '' THEN h2.show_title ELSE h2.title END FROM play_history h2 WHERE h2.user_id=h.user_id ORDER BY h2.last_seen_at DESC LIMIT 1)
		FROM play_history h JOIN users u ON u.id=h.user_id WHERE h.started_at >= ? GROUP BY h.user_id ORDER BY 4 DESC LIMIT 20`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	s.TopUsers = []TopUser{}
	for rows.Next() {
		var u TopUser
		if err := rows.Scan(&u.UserID, &u.Name, &u.Plays, &u.Hours, &u.LastSeen, &u.LastPlay); err != nil {
			return nil, err
		}
		s.TopUsers = append(s.TopUsers, u)
	}
	return s, rows.Err()
}

type LibraryStats struct {
	Movies      int     `json:"movies"`
	Shows       int     `json:"shows"`
	Episodes    int     `json:"episodes"`
	Files       int     `json:"files"`
	Bytes       int64   `json:"bytes"`
	Hours       float64 `json:"hours"`
	Resolutions []Count `json:"resolutions"`
	HDR         []Count `json:"hdr"`
	VideoCodecs []Count `json:"videoCodecs"`
	Containers  []Count `json:"containers"`
	Libraries   []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Type  string `json:"type"`
		Items int    `json:"items"`
		Bytes int64  `json:"bytes"`
	} `json:"libraries"`
}

func (d *DB) LibraryStats(ctx context.Context) (*LibraryStats, error) {
	s := &LibraryStats{}
	if err := d.QueryRowContext(ctx, `SELECT SUM(kind='movie'), SUM(kind='show'), SUM(kind='episode') FROM items`).
		Scan(nullInt(&s.Movies), nullInt(&s.Shows), nullInt(&s.Episodes)); err != nil {
		return nil, err
	}
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size),0), COALESCE(SUM(duration),0)/3600.0 FROM media_files`).
		Scan(&s.Files, &s.Bytes, &s.Hours); err != nil {
		return nil, err
	}
	counts := func(q string) []Count {
		out := []Count{}
		rows, err := d.QueryContext(ctx, q)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var c Count
			if rows.Scan(&c.Name, &c.Count) == nil {
				out = append(out, c)
			}
		}
		return out
	}
	s.Resolutions = counts(`SELECT CASE WHEN width >= 3200 THEN '4K' WHEN width >= 1800 THEN '1080p' WHEN width >= 1200 THEN '720p'
		WHEN width > 0 THEN 'SD' ELSE 'Not analyzed' END r, COUNT(*) c FROM media_files GROUP BY r ORDER BY c DESC`)
	s.HDR = counts(`SELECT CASE WHEN hdr IN ('DV','DV+HDR10') THEN 'Dolby Vision' WHEN hdr != '' THEN hdr WHEN width > 0 AND probed_at > 0 THEN 'SDR' ELSE 'Not analyzed' END h,
		COUNT(*) c FROM media_files GROUP BY h ORDER BY c DESC`)
	s.VideoCodecs = counts(`SELECT CASE WHEN video_codec != '' THEN UPPER(video_codec) ELSE 'Not analyzed' END v, COUNT(*) c FROM media_files GROUP BY v ORDER BY c DESC`)
	s.Containers = counts(`SELECT CASE WHEN container != '' THEN UPPER(container) ELSE 'Not analyzed' END v, COUNT(*) c FROM media_files GROUP BY v ORDER BY c DESC`)
	rows, err := d.QueryContext(ctx, `SELECT l.id, l.name, l.type, (SELECT COUNT(*) FROM items i WHERE i.library_id=l.id AND i.kind IN ('movie','show')),
		(SELECT COALESCE(SUM(size),0) FROM media_files f WHERE f.library_id=l.id) FROM libraries l ORDER BY l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Type  string `json:"type"`
			Items int    `json:"items"`
			Bytes int64  `json:"bytes"`
		}
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &l.Items, &l.Bytes); err != nil {
			return nil, err
		}
		s.Libraries = append(s.Libraries, l)
	}
	return s, rows.Err()
}

type nullIntDest struct{ p *int }

func (n nullIntDest) Scan(v any) error {
	switch x := v.(type) {
	case int64:
		*n.p = int(x)
	case nil:
		*n.p = 0
	}
	return nil
}

func nullInt(p *int) any { return nullIntDest{p} }

// NormalizeLang maps ISO 639-2 codes ("eng", "ger") and names to ISO 639-1 ("en", "de").
func NormalizeLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if v, ok := lang3[s]; ok {
		return v
	}
	if i := strings.IndexAny(s, "-_"); i > 0 {
		return NormalizeLang(s[:i])
	}
	return s
}

var lang3 = map[string]string{
	"eng": "en", "english": "en", "spa": "es", "spanish": "es", "fre": "fr", "fra": "fr", "french": "fr",
	"ger": "de", "deu": "de", "german": "de", "ita": "it", "italian": "it", "por": "pt", "portuguese": "pt",
	"jpn": "ja", "japanese": "ja", "kor": "ko", "korean": "ko", "chi": "zh", "zho": "zh", "chinese": "zh",
	"rus": "ru", "russian": "ru", "hin": "hi", "hindi": "hi", "ara": "ar", "arabic": "ar", "dut": "nl", "nld": "nl",
	"swe": "sv", "nor": "no", "nob": "no", "dan": "da", "fin": "fi", "pol": "pl", "tur": "tr", "gre": "el", "ell": "el",
	"heb": "he", "tha": "th", "vie": "vi", "ind": "id", "cze": "cs", "ces": "cs", "hun": "hu", "rum": "ro", "ron": "ro",
	"ukr": "uk", "tam": "ta", "tel": "te", "may": "ms", "msa": "ms", "fil": "tl", "tgl": "tl", "per": "fa", "fas": "fa",
}
