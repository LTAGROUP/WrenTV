package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrSportsPreferenceConflict means the caller saved from a stale revision.
	ErrSportsPreferenceConflict = errors.New("sports preferences changed; reload before saving")
	// ErrSportsPreferenceRevisionExhausted protects the JavaScript-safe limit.
	ErrSportsPreferenceRevisionExhausted = errors.New("sports preference revision exhausted")
	// ErrSportsLinkOrder rejects an order that is not exactly the team's backups.
	ErrSportsLinkOrder = errors.New("link order must list every backup exactly once")
)

// MaxSportsPreferenceRevision keeps revisions exact in JavaScript.
const MaxSportsPreferenceRevision uint64 = 9007199254740991

// SportsTeamRow is a persisted team identity. The provider ID (not the display
// name) is the stable half of the primary key so a team rename keeps its links.
type SportsTeamRow struct {
	ID           string `json:"id"`
	League       string `json:"league"`
	ProviderID   string `json:"providerTeamId"`
	Name         string `json:"name"`
	Location     string `json:"location,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
	Abbreviation string `json:"abbreviation,omitempty"`
	LogoURL      string `json:"logoUrl,omitempty"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// SyncSportsTeams upserts the catalog with change detection: unchanged rows are
// not rewritten, so a 90-second cycle does not churn the SQLite file.
func (d *DB) SyncSportsTeams(ctx context.Context, teams []SportsTeamRow) (int, error) {
	if len(teams) == 0 {
		return 0, nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO sports_teams
		(id, league, provider_team_id, name, location, nickname, abbreviation, logo_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			location = excluded.location,
			nickname = excluded.nickname,
			abbreviation = excluded.abbreviation,
			logo_url = excluded.logo_url,
			updated_at = excluded.updated_at
		WHERE sports_teams.name <> excluded.name
			OR sports_teams.location <> excluded.location
			OR sports_teams.nickname <> excluded.nickname
			OR sports_teams.abbreviation <> excluded.abbreviation
			OR sports_teams.logo_url <> excluded.logo_url`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	now := time.Now().Unix()
	changed := 0
	for _, team := range teams {
		if team.ID == "" || team.League == "" {
			continue
		}
		res, err := stmt.ExecContext(ctx, team.ID, team.League, team.ProviderID, team.Name,
			team.Location, team.Nickname, team.Abbreviation, team.LogoURL, now)
		if err != nil {
			return changed, err
		}
		n, _ := res.RowsAffected()
		changed += int(n)
	}
	if err := tx.Commit(); err != nil {
		return changed, err
	}
	return changed, nil
}

// ListSportsTeams returns persisted teams, optionally for one league.
func (d *DB) ListSportsTeams(ctx context.Context, league string) ([]SportsTeamRow, error) {
	return d.listSportsTeams(ctx, league, "")
}

// ListSportsTeamsForLeagues returns the union of several leagues' teams in one
// query for the preferences editor.
func (d *DB) ListSportsTeamsForLeagues(ctx context.Context, leagues []string) ([]SportsTeamRow, error) {
	if len(leagues) == 0 {
		return []SportsTeamRow{}, nil
	}
	args := make([]any, len(leagues))
	for i, league := range leagues {
		args[i] = league
	}
	rows, err := d.QueryContext(ctx,
		`SELECT id, league, provider_team_id, name, location, nickname, abbreviation, logo_url, updated_at
		FROM sports_teams WHERE league IN (`+placeholders(len(leagues))+`) ORDER BY league, name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SportsTeamRow{}
	for rows.Next() {
		var t SportsTeamRow
		if err := rows.Scan(&t.ID, &t.League, &t.ProviderID, &t.Name, &t.Location,
			&t.Nickname, &t.Abbreviation, &t.LogoURL, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SportsTeamByID returns one persisted team identity.
func (d *DB) SportsTeamByID(ctx context.Context, id string) (*SportsTeamRow, error) {
	var t SportsTeamRow
	err := d.QueryRowContext(ctx,
		`SELECT id, league, provider_team_id, name, location, nickname, abbreviation, logo_url, updated_at
		FROM sports_teams WHERE id=?`, id).
		Scan(&t.ID, &t.League, &t.ProviderID, &t.Name, &t.Location, &t.Nickname, &t.Abbreviation, &t.LogoURL, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (d *DB) listSportsTeams(ctx context.Context, league, _ string) ([]SportsTeamRow, error) {
	query := `SELECT id, league, provider_team_id, name, location, nickname, abbreviation, logo_url, updated_at
		FROM sports_teams`
	args := []any{}
	if league != "" {
		query += ` WHERE league = ?`
		args = append(args, league)
	}
	query += ` ORDER BY league, name`
	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SportsTeamRow{}
	for rows.Next() {
		var t SportsTeamRow
		if err := rows.Scan(&t.ID, &t.League, &t.ProviderID, &t.Name, &t.Location,
			&t.Nickname, &t.Abbreviation, &t.LogoURL, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------- sports preferences ----------

// GetSportsPreferences returns the stored revision and raw document. A user
// with no row has revision 0 and no document; callers substitute the empty
// document without writing one.
func (d *DB) GetSportsPreferences(ctx context.Context, userID string) (uint64, []byte, error) {
	var revision uint64
	var document string
	err := d.QueryRowContext(ctx, `SELECT revision, document FROM sports_preferences WHERE user_id=?`, userID).
		Scan(&revision, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	return revision, []byte(document), nil
}

// SetSportsPreferences performs compare-and-swap inside one write transaction.
// Only the current revision may save; the returned revision is exactly one
// higher, which clients assert.
func (d *DB) SetSportsPreferences(ctx context.Context, userID string, expected uint64, document []byte) (uint64, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var current uint64
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT revision, document FROM sports_preferences WHERE user_id=?`, userID).
		Scan(&current, &stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if current != expected {
		return 0, ErrSportsPreferenceConflict
	}
	if current >= MaxSportsPreferenceRevision {
		return 0, ErrSportsPreferenceRevisionExhausted
	}
	next := current + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO sports_preferences (user_id, revision, document, updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET revision=excluded.revision, document=excluded.document, updated_at=excluded.updated_at`,
		userID, next, string(document), time.Now().Unix()); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

// ---------- team channel links ----------

// SportsLinkRow is one linked Live TV channel. ChannelRef is the last known
// playlist id; resolution at read time is authoritative because playlist ids
// are regenerated.
type SportsLinkRow struct {
	ID             string  `json:"id"`
	TeamID         string  `json:"teamId"`
	Slot           string  `json:"slot"`
	Position       int     `json:"position"`
	ChannelRef     string  `json:"channelRef,omitempty"`
	ChannelTvgID   string  `json:"channelTvgId,omitempty"`
	ChannelName    string  `json:"channelName"`
	ChannelLogo    string  `json:"channelLogo,omitempty"`
	SourceID       string  `json:"sourceId,omitempty"`
	SourceName     string  `json:"sourceName,omitempty"`
	AutoLinked     bool    `json:"autoLinked"`
	LinkConfidence float64 `json:"linkConfidence,omitempty"`
	MatchReason    string  `json:"matchReason,omitempty"`
	LastVerifiedAt int64   `json:"lastVerifiedAt,omitempty"`
	CreatedAt      int64   `json:"createdAt"`
	UpdatedAt      int64   `json:"updatedAt"`
}

const sportsLinkCols = `id, team_id, slot, position, channel_ref, channel_tvg_id, channel_name, channel_logo,
	source_id, source_name, auto_linked, link_confidence, match_reason, COALESCE(last_verified_at,0), created_at, updated_at`

func scanSportsLink(row interface{ Scan(...any) error }) (SportsLinkRow, error) {
	var l SportsLinkRow
	var auto int
	err := row.Scan(&l.ID, &l.TeamID, &l.Slot, &l.Position, &l.ChannelRef, &l.ChannelTvgID, &l.ChannelName,
		&l.ChannelLogo, &l.SourceID, &l.SourceName, &auto, &l.LinkConfidence, &l.MatchReason,
		&l.LastVerifiedAt, &l.CreatedAt, &l.UpdatedAt)
	l.AutoLinked = auto != 0
	return l, err
}

func (d *DB) querySportsLinks(ctx context.Context, query string, args ...any) ([]SportsLinkRow, error) {
	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SportsLinkRow{}
	for rows.Next() {
		link, err := scanSportsLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// SportsTeamLinks returns a team's primary and ordered backups.
func (d *DB) SportsTeamLinks(ctx context.Context, teamID string) ([]SportsLinkRow, error) {
	return d.querySportsLinks(ctx, `SELECT `+sportsLinkCols+` FROM sports_team_channel_links
		WHERE team_id=? ORDER BY slot DESC, position ASC`, teamID)
}

// SportsLinksForTeams batches the lookup used by hub cards and the team list.
func (d *DB) SportsLinksForTeams(ctx context.Context, teamIDs []string) (map[string][]SportsLinkRow, error) {
	result := map[string][]SportsLinkRow{}
	if len(teamIDs) == 0 {
		return result, nil
	}
	args := make([]any, len(teamIDs))
	for i, id := range teamIDs {
		args[i] = id
	}
	rows, err := d.querySportsLinks(ctx, `SELECT `+sportsLinkCols+` FROM sports_team_channel_links
		WHERE team_id IN (`+placeholders(len(teamIDs))+`) ORDER BY team_id, slot DESC, position ASC`, args...)
	if err != nil {
		return nil, err
	}
	for _, link := range rows {
		result[link.TeamID] = append(result[link.TeamID], link)
	}
	return result, nil
}

// SetSportsPrimaryLink creates or replaces a team's primary channel. A manual
// edit clears auto-link provenance.
func (d *DB) SetSportsPrimaryLink(ctx context.Context, teamID string, link SportsLinkRow) (*SportsLinkRow, error) {
	now := time.Now().Unix()
	id := link.ID
	if id == "" {
		id = NewID()
	}
	row := d.QueryRowContext(ctx, `INSERT INTO sports_team_channel_links
		(id, team_id, slot, position, channel_ref, channel_tvg_id, channel_name, channel_logo, source_id, source_name,
		 auto_linked, link_confidence, match_reason, created_at, updated_at)
		VALUES (?,?,'primary',0,?,?,?,?,?,?,0,0,'',?,?)
		ON CONFLICT (team_id, slot, position) DO UPDATE SET
			channel_ref=excluded.channel_ref, channel_tvg_id=excluded.channel_tvg_id, channel_name=excluded.channel_name,
			channel_logo=excluded.channel_logo, source_id=excluded.source_id, source_name=excluded.source_name,
			auto_linked=0, link_confidence=0, match_reason='', updated_at=excluded.updated_at
		RETURNING `+sportsLinkCols,
		id, teamID, link.ChannelRef, link.ChannelTvgID, link.ChannelName, link.ChannelLogo,
		link.SourceID, link.SourceName, now, now)
	saved, err := scanSportsLink(row)
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// AddSportsBackupLink appends a backup after the current last backup.
func (d *DB) AddSportsBackupLink(ctx context.Context, teamID string, link SportsLinkRow) (*SportsLinkRow, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var position int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM sports_team_channel_links
		WHERE team_id=? AND slot='backup'`, teamID).Scan(&position); err != nil {
		return nil, err
	}
	id := link.ID
	if id == "" {
		id = NewID()
	}
	now := time.Now().Unix()
	row := tx.QueryRowContext(ctx, `INSERT INTO sports_team_channel_links
		(id, team_id, slot, position, channel_ref, channel_tvg_id, channel_name, channel_logo, source_id, source_name,
		 auto_linked, link_confidence, match_reason, created_at, updated_at)
		VALUES (?,?,'backup',?,?,?,?,?,?,?,0,0,'',?,?)
		RETURNING `+sportsLinkCols,
		id, teamID, position, link.ChannelRef, link.ChannelTvgID, link.ChannelName, link.ChannelLogo,
		link.SourceID, link.SourceName, now, now)
	saved, err := scanSportsLink(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &saved, nil
}

// DeleteSportsLink removes one link, scoped to its team. Deleting a missing
// link is idempotent.
func (d *DB) DeleteSportsLink(ctx context.Context, teamID, linkID string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM sports_team_channel_links WHERE id=? AND team_id=?`, linkID, teamID)
	return err
}

// ReorderSportsBackups rewrites backup positions to 1..n. The order must name
// every current backup exactly once, so a stale page cannot drop links.
func (d *DB) ReorderSportsBackups(ctx context.Context, teamID string, ordered []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM sports_team_channel_links WHERE team_id=? AND slot='backup'`, teamID)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		current[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ordered) != len(current) {
		return ErrSportsLinkOrder
	}
	seen := map[string]bool{}
	for _, id := range ordered {
		if !current[id] || seen[id] {
			return ErrSportsLinkOrder
		}
		seen[id] = true
	}
	// Two passes keep the UNIQUE(team, slot, position) constraint satisfied
	// while positions are swapped.
	if _, err := tx.ExecContext(ctx, `UPDATE sports_team_channel_links SET position=position+1000000
		WHERE team_id=? AND slot='backup'`, teamID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE sports_team_channel_links SET position=?, updated_at=? WHERE id=? AND team_id=? AND slot='backup'`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().Unix()
	for i, id := range ordered {
		if _, err := stmt.ExecContext(ctx, i+1, now, id, teamID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertSportsAutoPrimaryLinks only inserts links for teams that still have no
// primary, so a manual choice made after preview is never replaced. Provenance
// is recorded on each inserted row.
func (d *DB) InsertSportsAutoPrimaryLinks(ctx context.Context, links []SportsLinkRow) ([]SportsLinkRow, error) {
	if len(links) == 0 {
		return []SportsLinkRow{}, nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO sports_team_channel_links
		(id, team_id, slot, position, channel_ref, channel_tvg_id, channel_name, channel_logo, source_id, source_name,
		 auto_linked, link_confidence, match_reason, created_at, updated_at)
		VALUES (?,?,'primary',0,?,?,?,?,?,?,1,?,?,?,?)
		ON CONFLICT (team_id, slot, position) DO NOTHING`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	saved := make([]SportsLinkRow, 0, len(links))
	for _, link := range links {
		id := link.ID
		if id == "" {
			id = NewID()
		}
		res, err := stmt.ExecContext(ctx, id, link.TeamID, link.ChannelRef, link.ChannelTvgID, link.ChannelName,
			link.ChannelLogo, link.SourceID, link.SourceName, link.LinkConfidence, link.MatchReason, now, now)
		if err != nil {
			return nil, fmt.Errorf("insert auto primary link for %s: %w", link.TeamID, err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		link.ID, link.Slot, link.Position = id, "primary", 0
		link.AutoLinked, link.CreatedAt, link.UpdatedAt = true, now, now
		saved = append(saved, link)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}

// normalizeLeague lowercases a league id for admin queries.
func normalizeLeague(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
