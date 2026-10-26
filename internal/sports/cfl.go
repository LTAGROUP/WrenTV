package sports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The CFL publishes its own public JSON fixtures; the provider identity stays
// namespaced so it never collides with ESPN football entries.
const (
	cflAPIBase          = "https://api.stats.cfl.ca"
	cflLeagueID         = "espn:football:cfl"
	cflMaxResponseBytes = 4 << 20
)

type cflFixture struct {
	ID         int      `json:"ID"`
	Week       int      `json:"week"`
	HomeTeamID *int     `json:"home_team_id"`
	AwayTeamID *int     `json:"away_team_id"`
	StartAt    string   `json:"start_at"`
	HomeScore  *int     `json:"home_team_score"`
	AwayScore  *int     `json:"away_team_score"`
	Status     string   `json:"game_status"`
	Broadcasts []string `json:"broadcasting_options"`
}

type cflFixtureResponse struct {
	Year       string       `json:"year"`
	Preseason  []cflFixture `json:"preseason"`
	Season     []cflFixture `json:"season"`
	Semifinals []cflFixture `json:"semiFinals"`
	Finals     []cflFixture `json:"finals"`
}

type cflTeam struct {
	ID             int    `json:"ID"`
	Name           string `json:"name"`
	Region         string `json:"region_label"`
	Abbreviation   string `json:"abbreviation"`
	Color          string `json:"primary_color"`
	AlternateColor string `json:"accent_color"`
	Logo           string `json:"logo_primary"`
}

func (s *Service) readCFLJSON(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cflAPIBase+path, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CFL %s HTTP %d", path, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, cflMaxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > cflMaxResponseBytes {
		return fmt.Errorf("CFL response exceeds limit")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode CFL %s: %w", path, err)
	}
	return nil
}

// fetchCFLScoreboardDate keeps fixtures around the viewer's UTC day. The
// scoreboard caller performs the final local-day selection.
func (s *Service) fetchCFLScoreboardDate(ctx context.Context, date string) ([]SportsGame, error) {
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, fmt.Errorf("invalid CFL date: %w", err)
	}
	// Team artwork/names are optional. A short independent request cannot turn
	// valid fixtures into an error when the metadata endpoint is unavailable.
	teamCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	teamsCh := make(chan []cflTeam, 1)
	go func() {
		var teams []cflTeam
		if s.readCFLJSON(teamCtx, "/teams", &teams) != nil {
			teams = nil
		}
		teamsCh <- teams
	}()
	var response cflFixtureResponse
	if err := s.readCFLJSON(ctx, fmt.Sprintf("/fixtures/%d", day.Year()), &response); err != nil {
		return nil, err
	}
	if response.Year != strconv.Itoa(day.Year()) {
		return nil, fmt.Errorf("CFL returned season %q for %d", response.Year, day.Year())
	}
	var teams []cflTeam
	select {
	case teams = <-teamsCh:
	case <-teamCtx.Done():
	}
	lookup := map[int]cflTeam{}
	for _, team := range teams {
		if team.ID > 0 {
			lookup[team.ID] = team
		}
	}
	games := normalizeCFLFixtures(response, lookup, time.Now())
	result := make([]SportsGame, 0, len(games))
	from, to := day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)
	for _, game := range games {
		if !game.StartTime.Before(from) && game.StartTime.Before(to) {
			result = append(result, game)
		}
	}
	return result, nil
}

func cflSportsTeam(id *int, teams map[int]cflTeam) SportsTeam {
	if id == nil || *id <= 0 {
		return SportsTeam{Name: "TBD"}
	}
	team := teams[*id]
	result := SportsTeam{
		ID: fmt.Sprintf("cfl:%d", *id), Name: strings.TrimSpace(team.Region + " " + team.Name),
		Location: team.Region, Nickname: team.Name, Abbreviation: team.Abbreviation,
		Color: team.Color, AlternateColor: team.AlternateColor,
	}
	if result.Name == "" {
		result.Name = fmt.Sprintf("Team %d", *id)
	}
	// Inline SVG, arbitrary hosts and private URLs are never promoted to artwork.
	if u, err := url.Parse(team.Logo); err == nil && u.Scheme == "https" && u.Host == "content.cfl.ca" && u.User == nil {
		result.LogoURL = u.String()
	}
	return result
}

func normalizeCFLFixtures(response cflFixtureResponse, teams map[int]cflTeam, now time.Time) []SportsGame {
	result := []SportsGame{}
	seen := map[int]bool{}
	groups := []struct {
		label    string
		fixtures []cflFixture
	}{
		{"Preseason", response.Preseason},
		{"Regular season", response.Season},
		{"Semifinals", response.Semifinals},
		{"Finals", response.Finals},
	}
	for _, group := range groups {
		for _, fixture := range group.fixtures {
			start, err := time.Parse(time.RFC3339, fixture.StartAt)
			if fixture.ID <= 0 || err != nil || seen[fixture.ID] {
				continue
			}
			seen[fixture.ID] = true
			game := SportsGame{
				ID: fmt.Sprintf("cfl:%d", fixture.ID), League: cflLeagueID, Sport: "football",
				EventKind: "matchup", StartTime: start, Status: SportsGameScheduled,
				StatusDetail: "Scheduled", EventContext: group.label,
				HomeTeam:   cflSportsTeam(fixture.HomeTeamID, teams),
				AwayTeam:   cflSportsTeam(fixture.AwayTeamID, teams),
				Broadcasts: fixture.Broadcasts,
			}
			if group.label == "Regular season" && fixture.Week > 0 {
				game.EventContext = fmt.Sprintf("Regular season · Week %d", fixture.Week)
			}
			game.Title = game.AwayTeam.Name + " at " + game.HomeTeam.Name
			status := strings.TrimSpace(fixture.Status)
			// The official feed has both Finished and the nested-quoted "Finished".
			if unquoted, err := strconv.Unquote(status); err == nil {
				status = unquoted
			}
			if strings.EqualFold(status, "Finished") {
				game.Status = SportsGameFinal
				game.StatusDetail = "Final"
			} else if status != "" {
				game.StatusDetail = status
			} else if start.Before(now) {
				game.StatusDetail = "Status unavailable"
			}
			if fixture.HomeScore != nil && *fixture.HomeScore >= 0 {
				game.HomeTeam.Score = strconv.Itoa(*fixture.HomeScore)
			}
			if fixture.AwayScore != nil && *fixture.AwayScore >= 0 {
				game.AwayTeam.Score = strconv.Itoa(*fixture.AwayScore)
			}
			if game.Status == SportsGameFinal && fixture.HomeScore != nil && fixture.AwayScore != nil &&
				*fixture.HomeScore >= 0 && *fixture.AwayScore >= 0 {
				game.HomeTeam.Winner = *fixture.HomeScore > *fixture.AwayScore
				game.AwayTeam.Winner = *fixture.AwayScore > *fixture.HomeScore
			}
			// Completed-fixture quarter/clock fields are not dependable live data,
			// and NFL field diagrams do not apply to the CFL.
			result = append(result, game)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].StartTime.Before(result[j].StartTime) })
	return result
}

// fetchCFLTeams exposes the same provider identity used by matchup participants.
func (s *Service) fetchCFLTeams(ctx context.Context) ([]SportsTeamRecord, error) {
	var raw []cflTeam
	if err := s.readCFLJSON(ctx, "/teams", &raw); err != nil {
		return nil, err
	}
	result := make([]SportsTeamRecord, 0, len(raw))
	seen := map[int]bool{}
	now := time.Now().UTC()
	for _, team := range raw {
		if team.ID <= 0 || strings.TrimSpace(team.Name) == "" || strings.TrimSpace(team.Region) == "" || seen[team.ID] {
			continue
		}
		seen[team.ID] = true
		normalized := cflSportsTeam(&team.ID, map[int]cflTeam{team.ID: team})
		result = append(result, SportsTeamRecord{
			ID: cflLeagueID + ":" + normalized.ID, League: cflLeagueID, ProviderID: normalized.ID,
			Name: normalized.Name, Location: normalized.Location, Nickname: normalized.Nickname,
			Abbreviation: normalized.Abbreviation, LogoURL: normalized.LogoURL, UpdatedAt: now,
		})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("CFL teams contains no named identities")
	}
	return result, nil
}

// ---------- CFL standings ----------

type StandingColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type LeagueStandingRow struct {
	Values       map[string]string `json:"values"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Abbreviation string            `json:"abbreviation,omitempty"`
	Kind         string            `json:"kind"`
}

type LeagueStandingGroup struct {
	ID          string              `json:"id"`
	Title       string              `json:"title"`
	SeasonLabel string              `json:"seasonLabel"`
	Columns     []StandingColumn    `json:"columns"`
	Rows        []LeagueStandingRow `json:"rows"`
	Season      int                 `json:"season"`
}

type LeagueStandings struct {
	UpdatedAt *time.Time            `json:"updatedAt,omitempty"`
	League    string                `json:"league"`
	State     string                `json:"state"`
	Source    string                `json:"source"`
	SourceURL string                `json:"sourceUrl"`
	Reason    string                `json:"reason,omitempty"`
	Groups    []LeagueStandingGroup `json:"groups"`
}

type standingsCache struct {
	entries map[string]*standingsSlot
	mu      sync.Mutex
}

type standingsSlot struct {
	expires time.Time
	mu      sync.Mutex
	value   LeagueStandings
}

type cflStandingRow struct {
	TeamID        int    `json:"team_id"`
	Season        int    `json:"season"`
	Abbreviation  string `json:"abbreviation"`
	Place         *int   `json:"place"`
	PlaceOverride *int   `json:"place_override"`
	GamesPlayed   *int   `json:"games_played"`
	Wins          *int   `json:"wins"`
	Losses        *int   `json:"losses"`
	Ties          *int   `json:"ties"`
	Points        *int   `json:"points"`
}

type cflStandingDivision struct {
	Name string           `json:"division_name"`
	Rows []cflStandingRow `json:"standings"`
}

type cflStandingsResponse struct {
	Data struct {
		Divisions map[string]cflStandingDivision `json:"divisions"`
	} `json:"data"`
}

func normalizeCFLStandings(raw cflStandingsResponse, teams []SportsTeamRecord, year int) []LeagueStandingGroup {
	if year < 1900 || year > 2200 {
		return nil
	}
	names := map[string]SportsTeamRecord{}
	for _, team := range teams {
		if team.League == cflLeagueID && strings.TrimSpace(team.Name) != "" {
			names[team.ProviderID] = team
		}
	}
	keys := []string{}
	for key := range raw.Data.Divisions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	groups := []LeagueStandingGroup{}
	for _, key := range keys {
		division := raw.Data.Divisions[key]
		if strings.TrimSpace(division.Name) == "" {
			continue
		}
		group := LeagueStandingGroup{
			ID: "cfl:" + key, Title: division.Name, Season: year,
			SeasonLabel: strconv.Itoa(year), Rows: []LeagueStandingRow{}, Columns: []StandingColumn{},
		}
		seen := map[int]bool{}
		present := map[string]bool{}
		for _, row := range division.Rows {
			team, known := names[fmt.Sprintf("cfl:%d", row.TeamID)]
			if row.Season != year || row.TeamID <= 0 || seen[row.TeamID] || !known {
				continue
			}
			seen[row.TeamID] = true
			values := map[string]string{}
			rank := row.Place
			if row.PlaceOverride != nil && *row.PlaceOverride > 0 {
				rank = row.PlaceOverride
			}
			for field, value := range map[string]*int{
				"rank": rank, "gamesPlayed": row.GamesPlayed, "wins": row.Wins,
				"losses": row.Losses, "ties": row.Ties, "points": row.Points,
			} {
				if value == nil || *value < 0 || (field == "rank" && *value == 0) {
					continue
				}
				values[field] = strconv.Itoa(*value)
				present[field] = true
			}
			if len(values) == 0 {
				continue
			}
			group.Rows = append(group.Rows, LeagueStandingRow{
				ID: team.ProviderID, Name: team.Name, Abbreviation: team.Abbreviation,
				Kind: "team", Values: values,
			})
		}
		for _, column := range []StandingColumn{
			{Key: "rank", Label: "Rank"}, {Key: "gamesPlayed", Label: "Played"},
			{Key: "wins", Label: "Wins"}, {Key: "losses", Label: "Losses"},
			{Key: "ties", Label: "Ties"}, {Key: "points", Label: "Points"},
		} {
			if present[column.Key] {
				group.Columns = append(group.Columns, column)
			}
		}
		sort.SliceStable(group.Rows, func(i, j int) bool {
			a, errA := strconv.Atoi(group.Rows[i].Values["rank"])
			b, errB := strconv.Atoi(group.Rows[j].Values["rank"])
			if errA != nil {
				return false
			}
			if errB != nil {
				return true
			}
			return a < b
		})
		if len(group.Rows) > 0 {
			groups = append(groups, group)
		}
	}
	return groups
}

// fetchCFLStandings discovers the latest non-future season, then requires every
// row to confirm the same year. Only the CFL currently exposes this contract.
func (s *Service) fetchCFLStandings(ctx context.Context) LeagueStandings {
	s.standings.mu.Lock()
	if s.standings.entries == nil {
		s.standings.entries = map[string]*standingsSlot{}
	}
	slot := s.standings.entries[cflLeagueID]
	if slot == nil {
		slot = &standingsSlot{}
		s.standings.entries[cflLeagueID] = slot
	}
	s.standings.mu.Unlock()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if time.Now().Before(slot.expires) {
		return slot.value
	}
	value := LeagueStandings{
		League: cflLeagueID, State: "unavailable", Source: "CFL", Groups: []LeagueStandingGroup{},
		Reason: "Official CFL standings are unavailable",
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var seasons []int
	err := s.readCFLJSON(requestCtx, "/seasons", &seasons)
	year := 0
	if err == nil {
		for _, season := range seasons {
			if season >= 1900 && season <= time.Now().UTC().Year() && season > year {
				year = season
			}
		}
		if year == 0 {
			err = fmt.Errorf("CFL returned no known current or past season")
		}
	}
	if err == nil {
		value.SourceURL = fmt.Sprintf("%s/standings/%d", cflAPIBase, year)
		var raw cflStandingsResponse
		err = s.readCFLJSON(requestCtx, fmt.Sprintf("/standings/%d", year), &raw)
		if err == nil {
			var teams []SportsTeamRecord
			teams, err = s.fetchCFLTeams(requestCtx)
			if err == nil {
				value.Groups = normalizeCFLStandings(raw, teams, year)
				if len(value.Groups) == 0 {
					err = fmt.Errorf("CFL table has no named rows for season %d", year)
				}
			}
		}
	}
	ttl := 15 * time.Minute
	if err == nil {
		now := time.Now().UTC()
		value.UpdatedAt = &now
		value.State = "available"
		value.Reason = ""
	} else {
		ttl = time.Minute
		if slot.value.UpdatedAt != nil {
			value = slot.value
			value.State = "stale"
			value.Reason = "Official CFL standings refresh unavailable; showing the last successful table"
		}
	}
	slot.value = value
	slot.expires = time.Now().Add(ttl)
	return value
}

// GetLeagueStandings returns the provider table for one competition. Only the
// CFL's official table is ported; other leagues report unavailable rather than
// an invented table.
func (s *Service) GetLeagueStandings(ctx context.Context, id string) LeagueStandings {
	if id == cflLeagueID {
		return s.fetchCFLStandings(ctx)
	}
	return LeagueStandings{
		League: id, State: "unavailable", Source: "Sports provider", Groups: []LeagueStandingGroup{},
		Reason: "Standings are not available for this competition",
	}
}
