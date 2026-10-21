package db

import (
	"context"
	"sort"
	"strings"
	"time"
)

// ---------- history ----------

// HistoryFilter narrows play history. Zero values don't filter.
type HistoryFilter struct {
	Since, Until int64
	UserID       string
	// Kind is "movie" or "episode".
	Kind string
	// Method is "direct", "remux" or "transcode".
	Method string
	// ItemID matches the item played or, for a show, any of its episodes.
	ItemID    string
	LibraryID string
	// Query matches the title or show title.
	Query         string
	Offset, Limit int
}

const historyCols = `h.id, h.user_id, ` + userNameExpr + `, COALESCE(h.item_id,''), h.show_id, h.kind, h.title, h.show_title, h.year,
	COALESCE(NULLIF(i.poster_path,''), h.poster_path), h.method, h.engine, h.video_copy, h.height, h.client, h.started_at, h.last_seen_at,
	h.position_end, h.duration, h.played_seconds, h.completed, h.library_id, h.ip, h.quality, h.stream_height, h.reasons`

func scanHistory(sc interface{ Scan(...any) error }) (HistoryRow, error) {
	var h HistoryRow
	err := sc.Scan(&h.ID, &h.UserID, &h.UserName, &h.ItemID, &h.ShowID, &h.Kind, &h.Title, &h.ShowTitle, &h.Year, &h.PosterPath,
		&h.Method, &h.Engine, &h.VideoCopy, &h.Height, &h.Client, &h.StartedAt, &h.LastSeenAt, &h.PositionEnd, &h.Duration,
		&h.PlayedSeconds, &h.Completed, &h.LibraryID, &h.IP, &h.Quality, &h.StreamHeight, &h.Reasons)
	return h, err
}

// History returns one page of matching plays, newest first, and how many
// match in all.
func (d *DB) History(ctx context.Context, f HistoryFilter) ([]HistoryRow, int, error) {
	where := []string{"h.started_at >= ?"}
	args := []any{f.Since}
	add := func(cond string, v ...any) {
		where = append(where, cond)
		args = append(args, v...)
	}
	if f.Until > 0 {
		add("h.started_at < ?", f.Until)
	}
	if f.UserID != "" {
		add("h.user_id = ?", f.UserID)
	}
	if f.Kind != "" {
		add("h.kind = ?", f.Kind)
	}
	switch f.Method {
	case "direct", "remux":
		add("h.method = ?", f.Method)
	case "transcode":
		add("h.method = 'transcode'")
	}
	if f.ItemID != "" {
		add("(h.item_id = ? OR h.show_id = ?)", f.ItemID, f.ItemID)
	}
	if f.LibraryID != "" {
		add("h.library_id = ?", f.LibraryID)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
		add(`(h.title LIKE ? ESCAPE '\' OR h.show_title LIKE ? ESCAPE '\')`, like, like)
	}
	from := ` FROM play_history h JOIN users u ON u.id=h.user_id LEFT JOIN items i ON i.id=h.item_id WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.QueryContext(ctx, `SELECT `+historyCols+from+` ORDER BY h.started_at DESC LIMIT ? OFFSET ?`, append(args, limit, max(f.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []HistoryRow{}
	for rows.Next() {
		h, err := scanHistory(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, h)
	}
	return out, total, rows.Err()
}

// ---------- breakdowns ----------

// methodClass is how a play was sent: "direct", "remux" or "transcode".
func methodClass(method string, videoCopy bool) string {
	switch method {
	case "direct", "remux":
		return method
	}
	return "transcode"
}

// ResLabel names a picture height.
func ResLabel(h int) string {
	switch {
	case h >= 2000:
		return "4K"
	case h >= 1000:
		return "1080p"
	case h >= 700:
		return "720p"
	case h > 0:
		return "SD"
	}
	return "Unknown"
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func sortedCounts(m map[string]int) []Count {
	out := []Count{}
	for k, v := range m {
		out = append(out, Count{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// MethodSplit counts one player's or viewer's plays by how they were sent.
type MethodSplit struct {
	Name      string `json:"name"`
	Direct    int    `json:"direct"`
	Remux     int    `json:"remux"`
	Transcode int    `json:"transcode"`
}

func (m *MethodSplit) add(cls string) {
	switch cls {
	case "direct":
		m.Direct++
	case "remux":
		m.Remux++
	default:
		m.Transcode++
	}
}

func sortedSplits(m map[string]*MethodSplit) []MethodSplit {
	out := []MethodSplit{}
	for _, v := range m {
		out = append(out, *v)
	}
	total := func(x MethodSplit) int { return x.Direct + x.Remux + x.Transcode }
	sort.Slice(out, func(i, j int) bool {
		if total(out[i]) != total(out[j]) {
			return total(out[i]) > total(out[j])
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// peakConcurrency sweeps play spans for the most at once. onStart is told
// how many were playing as each one began.
func peakConcurrency(spans [][2]int64, onStart func(at int64, n int)) (peak int, at int64) {
	type ev struct {
		t     int64
		delta int
	}
	evs := make([]ev, 0, len(spans)*2)
	for _, s := range spans {
		evs = append(evs, ev{s[0], 1}, ev{s[1], -1})
	}
	// Ends sort before starts at the same moment: back to back isn't at once.
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		return evs[i].delta < evs[j].delta
	})
	n := 0
	for _, e := range evs {
		n += e.delta
		if e.delta > 0 {
			onStart(e.t, n)
			if n > peak {
				peak, at = n, e.t
			}
		}
	}
	return
}

// LibPlays is a library's plays in a period.
type LibPlays struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Type  string  `json:"type"`
	Plays int     `json:"plays"`
	Users int     `json:"users"`
	Hours float64 `json:"hours"`
}

func (d *DB) libraryPlays(ctx context.Context, since int64) ([]LibPlays, error) {
	rows, err := d.QueryContext(ctx, `SELECT l.id, l.name, l.type, COUNT(h.id), COUNT(DISTINCT h.user_id), COALESCE(SUM(h.played_seconds),0)/3600.0
		FROM libraries l LEFT JOIN play_history h ON h.library_id=l.id AND h.started_at >= ? GROUP BY l.id ORDER BY 4 DESC, l.name`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibPlays{}
	for rows.Next() {
		var l LibPlays
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &l.Plays, &l.Users, &l.Hours); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// MonthStat is one calendar month's plays.
type MonthStat struct {
	Month    string  `json:"month"` // 2026-10
	Plays    int     `json:"plays"`
	Movies   int     `json:"movies"`
	Episodes int     `json:"episodes"`
	Hours    float64 `json:"hours"`
}

// monthly covers the last twelve months, oldest first, in local time.
func (d *DB) monthly(ctx context.Context, tz int) ([]MonthStat, error) {
	now := time.Now().UTC().Add(time.Duration(tz) * time.Second)
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -11, 0)
	out := make([]MonthStat, 12)
	idx := map[string]int{}
	for i := range out {
		out[i].Month = first.AddDate(0, i, 0).Format("2006-01")
		idx[out[i].Month] = i
	}
	rows, err := d.QueryContext(ctx, `SELECT strftime('%Y-%m', started_at + ?, 'unixepoch') m, kind, COUNT(*), SUM(played_seconds)/3600.0
		FROM play_history WHERE started_at >= ? GROUP BY m, kind`, tz, first.Unix()-int64(tz))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m, kind string
		var n int
		var h float64
		if err := rows.Scan(&m, &kind, &n, &h); err != nil {
			return nil, err
		}
		i, ok := idx[m]
		if !ok {
			continue
		}
		out[i].Plays += n
		out[i].Hours += h
		switch kind {
		case "movie":
			out[i].Movies += n
		case "episode":
			out[i].Episodes += n
		}
	}
	return out, rows.Err()
}

// ---------- viewers ----------

// Viewer is a user with their all-time watching.
type Viewer struct {
	UserID     string  `json:"userId"`
	Name       string  `json:"name"`
	Admin      bool    `json:"admin"`
	Joined     int64   `json:"joined"`
	Plays      int     `json:"plays"`
	Hours      float64 `json:"hours"`
	LastSeen   int64   `json:"lastSeen,omitempty"`
	LastPlayed string  `json:"lastPlayed,omitempty"`
	LastClient string  `json:"lastClient,omitempty"`
	LastIP     string  `json:"lastIp,omitempty"`
}

// Viewers lists every user, most recently active first.
func (d *DB) Viewers(ctx context.Context) ([]Viewer, error) {
	rows, err := d.QueryContext(ctx, `SELECT u.id, `+userNameExpr+`, u.is_admin, u.created_at, COUNT(h.id), COALESCE(SUM(h.played_seconds),0)/3600.0,
		COALESCE(MAX(h.last_seen_at),0) FROM users u LEFT JOIN play_history h ON h.user_id=u.id GROUP BY u.id ORDER BY 7 DESC, 2`)
	if err != nil {
		return nil, err
	}
	out := []Viewer{}
	for rows.Next() {
		var v Viewer
		if err := rows.Scan(&v.UserID, &v.Name, &v.Admin, &v.Joined, &v.Plays, &v.Hours, &v.LastSeen); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	rows.Close()
	for i := range out {
		_ = d.QueryRowContext(ctx, `SELECT CASE WHEN show_title != '' THEN show_title ELSE title END, client, ip FROM play_history
			WHERE user_id=? ORDER BY last_seen_at DESC LIMIT 1`, out[i].UserID).Scan(&out[i].LastPlayed, &out[i].LastClient, &out[i].LastIP)
	}
	return out, rows.Err()
}

// Totals is plays and watch time over a span.
type Totals struct {
	Label string  `json:"label"`
	Plays int     `json:"plays"`
	Hours float64 `json:"hours"`
}

// UsedFrom is a player or address a viewer watched from.
type UsedFrom struct {
	Name     string  `json:"name"`
	Plays    int     `json:"plays"`
	Hours    float64 `json:"hours"`
	LastSeen int64   `json:"lastSeen"`
	// Detail is the last player used from an address.
	Detail string `json:"detail,omitempty"`
}

// ViewerDetail is one viewer's page.
type ViewerDetail struct {
	Viewer
	Totals    []Totals     `json:"totals"`
	Players   []UsedFrom   `json:"players"`
	Addresses []UsedFrom   `json:"addresses"`
	TopTitles []TopTitle   `json:"topTitles"`
	Methods   MethodSplit  `json:"methods"`
	Recent    []HistoryRow `json:"recent"`
}

func (d *DB) ViewerDetail(ctx context.Context, userID string) (*ViewerDetail, error) {
	vs, err := d.Viewers(ctx)
	if err != nil {
		return nil, err
	}
	v := &ViewerDetail{}
	for _, x := range vs {
		if x.UserID == userID {
			v.Viewer = x
		}
	}
	if v.UserID == "" {
		return nil, ErrNotFound
	}
	now := time.Now().Unix()
	for _, span := range []struct {
		label string
		secs  int64
	}{{"Last 24 hours", 86400}, {"Last 7 days", 7 * 86400}, {"Last 30 days", 30 * 86400}, {"All time", now}} {
		t := Totals{Label: span.label}
		_ = d.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(played_seconds),0)/3600.0 FROM play_history WHERE user_id=? AND started_at >= ?`,
			userID, now-span.secs).Scan(&t.Plays, &t.Hours)
		v.Totals = append(v.Totals, t)
	}
	used := func(col string) ([]UsedFrom, error) {
		rows, err := d.QueryContext(ctx, `SELECT `+col+`, COUNT(*), SUM(played_seconds)/3600.0, MAX(last_seen_at),
			(SELECT h2.client FROM play_history h2 WHERE h2.user_id=h.user_id AND h2.`+col+`=h.`+col+` ORDER BY h2.last_seen_at DESC LIMIT 1)
			FROM play_history h WHERE user_id=? AND `+col+` != '' GROUP BY `+col+` ORDER BY 4 DESC LIMIT 20`, userID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []UsedFrom{}
		for rows.Next() {
			var u UsedFrom
			if err := rows.Scan(&u.Name, &u.Plays, &u.Hours, &u.LastSeen, &u.Detail); err != nil {
				return nil, err
			}
			if col == "client" {
				u.Detail = ""
			}
			out = append(out, u)
		}
		return out, rows.Err()
	}
	if v.Players, err = used("client"); err != nil {
		return nil, err
	}
	if v.Addresses, err = used("ip"); err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, `SELECT CASE WHEN h.kind='episode' THEN h.show_id ELSE COALESCE(h.item_id,'') END,
		CASE WHEN h.kind='episode' THEN h.show_title ELSE h.title END t, MAX(CASE WHEN h.kind='episode' THEN COALESCE(sh.year,0) ELSE h.year END),
		COALESCE(MAX(CASE WHEN h.kind='episode' THEN sh.poster_path ELSE i.poster_path END), MAX(h.poster_path)), COUNT(*), 1, SUM(h.played_seconds)/3600.0
		FROM play_history h LEFT JOIN items i ON i.id=h.item_id LEFT JOIN items sh ON sh.id=h.show_id
		WHERE h.user_id=? GROUP BY 1, t ORDER BY 7 DESC LIMIT 10`, userID)
	if err != nil {
		return nil, err
	}
	v.TopTitles = []TopTitle{}
	for rows.Next() {
		var t TopTitle
		if err := rows.Scan(&t.ItemID, &t.Title, &t.Year, &t.PosterPath, &t.Plays, &t.Users, &t.Hours); err != nil {
			rows.Close()
			return nil, err
		}
		v.TopTitles = append(v.TopTitles, t)
	}
	rows.Close()
	mrows, err := d.QueryContext(ctx, `SELECT method, video_copy FROM play_history WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var m string
		var vc bool
		if mrows.Scan(&m, &vc) == nil {
			v.Methods.add(methodClass(m, vc))
		}
	}
	mrows.Close()
	v.Recent, _, err = d.History(ctx, HistoryFilter{UserID: userID, Limit: 25})
	return v, err
}

// ---------- recently added ----------

// Added is a movie or episode recently added to a library.
type Added struct {
	ItemID     string `json:"itemId"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	ShowID     string `json:"showId,omitempty"`
	ShowTitle  string `json:"showTitle,omitempty"`
	Season     int    `json:"season,omitempty"`
	Episode    int    `json:"episode,omitempty"`
	Year       int    `json:"year,omitempty"`
	PosterPath string `json:"posterPath,omitempty"`
	LibraryID  string `json:"libraryId"`
	AddedAt    int64  `json:"addedAt"`
}

func (d *DB) RecentlyAdded(ctx context.Context, limit int) ([]Added, error) {
	rows, err := d.QueryContext(ctx, `SELECT i.id, i.kind, i.title, COALESCE(i.show_id,''), COALESCE(sh.title,''), i.season_number, i.episode_number,
		i.year, COALESCE(NULLIF(i.poster_path,''), sh.poster_path, ''), i.library_id, i.added_at
		FROM items i LEFT JOIN items sh ON sh.id=i.show_id WHERE i.kind IN ('movie','episode') ORDER BY i.added_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Added{}
	for rows.Next() {
		var a Added
		if err := rows.Scan(&a.ItemID, &a.Kind, &a.Title, &a.ShowID, &a.ShowTitle, &a.Season, &a.Episode, &a.Year, &a.PosterPath,
			&a.LibraryID, &a.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
