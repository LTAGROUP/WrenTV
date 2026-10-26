package sports

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// ESPN returns substitution flags either directly or wrapped with timing
// metadata.
type substitutionFlag bool

func (f *substitutionFlag) UnmarshalJSON(data []byte) error {
	var direct bool
	if err := json.Unmarshal(data, &direct); err == nil {
		*f = substitutionFlag(direct)
		return nil
	}
	var wrapped struct {
		DidSub bool `json:"didSub"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	*f = substitutionFlag(wrapped.DidSub)
	return nil
}

type teamContextRoster struct {
	Team struct {
		ID string `json:"id"`
	} `json:"team"`
	Formation string `json:"formation"`
	Roster    []struct {
		Athlete struct {
			ID          string         `json:"id"`
			DisplayName string         `json:"displayName"`
			Headshot    playerHeadshot `json:"headshot"`
		} `json:"athlete"`
		Position struct {
			DisplayName string `json:"displayName"`
		} `json:"position"`
		Stats []struct {
			Name         string `json:"name"`
			DisplayValue string `json:"displayValue"`
		} `json:"stats"`
		Jersey    string           `json:"jersey"`
		Starter   *bool            `json:"starter"`
		SubbedIn  substitutionFlag `json:"subbedIn"`
		SubbedOut substitutionFlag `json:"subbedOut"`
	} `json:"roster"`
}

type teamContextStandings struct {
	Groups []struct {
		Header    string `json:"header"`
		Standings struct {
			Entries []struct {
				ID    string `json:"id"`
				Team  string `json:"team"`
				Stats []struct {
					Name         string `json:"name"`
					DisplayValue string `json:"displayValue"`
				} `json:"stats"`
			} `json:"entries"`
		} `json:"standings"`
	} `json:"groups"`
}

func normalizeTeamContext(game *SportsGame, summary teamSportSummary) {
	soccer := strings.HasPrefix(game.League, "soccer-")
	if game.Detail == nil {
		return
	}
	if !soccer && !strings.Contains(game.League, "college") {
		return
	}
	if soccer {
		seenTeams := map[string]bool{}
		for _, roster := range summary.Rosters {
			id := roster.Team.ID
			if (id != game.HomeTeam.ID && id != game.AwayTeam.ID) || seenTeams[id] {
				continue
			}
			seenTeams[id] = true
			lineup := SportsLineup{TeamID: id, Formation: roster.Formation, Players: []SportsLineupPlayer{}}
			seen := map[string]bool{}
			for _, player := range roster.Roster {
				if player.Starter == nil || player.Athlete.ID == "" || player.Athlete.DisplayName == "" || seen[player.Athlete.ID] || len(lineup.Players) >= 40 {
					continue
				}
				seen[player.Athlete.ID] = true
				lineup.Players = append(lineup.Players, SportsLineupPlayer{
					ID: player.Athlete.ID, Name: player.Athlete.DisplayName, Number: player.Jersey,
					Position: player.Position.DisplayName, Starter: *player.Starter,
					SubbedIn: bool(player.SubbedIn), SubbedOut: bool(player.SubbedOut),
				})
			}
			if len(lineup.Players) > 0 {
				game.Detail.Lineups = append(game.Detail.Lineups, lineup)
			}
		}
	}
	for _, group := range summary.Standings.Groups {
		if group.Header == "" || len(game.Detail.Standings) >= 8 {
			continue
		}
		normalized := SportsStandingGroup{Title: group.Header, Rows: []SportsStandingRow{}}
		seen := map[string]bool{}
		for _, row := range group.Standings.Entries {
			if row.ID == "" || row.Team == "" || seen[row.ID] || len(normalized.Rows) >= 64 {
				continue
			}
			seen[row.ID] = true
			entry := SportsStandingRow{TeamID: row.ID, Name: row.Team}
			for _, stat := range row.Stats {
				switch stat.Name {
				case "rank":
					entry.Rank = stat.DisplayValue
				case "gamesPlayed":
					entry.Played = stat.DisplayValue
				case "points":
					entry.Points = stat.DisplayValue
				case "pointDifferential":
					entry.GoalDifference = stat.DisplayValue
				case "overall":
					entry.Record = stat.DisplayValue
				case "vs. Conf.":
					entry.ConferenceRecord = stat.DisplayValue
				}
			}
			normalized.Rows = append(normalized.Rows, entry)
		}
		if len(normalized.Rows) > 0 {
			game.Detail.Standings = append(game.Detail.Standings, normalized)
		}
	}
}

// Roster match stats are separate from season totals and require an appearance.
func normalizeSoccerPlayerStats(game SportsGame, rosters []teamContextRoster) []SportsPlayerGameStats {
	if game.Status == SportsGameScheduled || !strings.HasPrefix(game.League, "soccer-") {
		return nil
	}
	fields := []struct{ name, label string }{
		{"totalGoals", "G"}, {"goalAssists", "A"}, {"totalShots", "SH"}, {"shotsOnTarget", "SOT"},
		{"saves", "SV"}, {"foulsCommitted", "FC"}, {"yellowCards", "YC"}, {"redCards", "RC"},
	}
	seen := map[string]bool{}
	var rows []SportsPlayerGameStats
	for _, team := range rosters {
		if team.Team.ID != game.AwayTeam.ID && team.Team.ID != game.HomeTeam.ID {
			continue
		}
		for _, player := range team.Roster {
			key := team.Team.ID + ":" + player.Athlete.ID
			if player.Athlete.ID == "" || player.Athlete.DisplayName == "" || seen[key] {
				continue
			}
			values := map[string]string{}
			for _, stat := range player.Stats {
				values[stat.Name] = strings.TrimSpace(stat.DisplayValue)
			}
			if appearance, err := strconv.Atoi(values["appearances"]); err != nil || appearance != 1 {
				continue
			}
			row := SportsPlayerGameStats{
				ID: player.Athlete.ID, TeamID: team.Team.ID, Name: player.Athlete.DisplayName,
				HeadshotURL: string(player.Athlete.Headshot), Category: "Match stats",
			}
			for _, field := range fields {
				value := values[field.name]
				if n, err := strconv.Atoi(value); err == nil && n >= 0 {
					row.Stats = append(row.Stats, SportsPlayerStatistic{Label: field.label, Value: value})
				}
			}
			if len(row.Stats) > 0 {
				rows = append(rows, row)
				seen[key] = true
			}
		}
	}
	return rows
}

func normalizeScoreHistory(game SportsGame, p teamSportSummary) *SportsScoreHistory {
	if (game.Status != SportsGameFinal && game.Status != SportsGameLive) ||
		(game.League != "nba" && game.League != "wnba" && game.League != "mens-college-basketball" && game.League != "womens-college-basketball") ||
		p.Header.ID != providerEventID(game) || len(p.Header.Competitions) != 1 || len(p.Plays) < 2 {
		return nil
	}
	c := p.Header.Competitions[0]
	if (game.Status == SportsGameFinal) != c.Status.Type.Completed || len(c.Competitors) != 2 ||
		game.AwayTeam.ID == "" || game.HomeTeam.ID == "" || game.AwayTeam.ID == game.HomeTeam.ID {
		return nil
	}
	finals := map[string]float64{}
	for _, team := range c.Competitors {
		expected := game.HomeTeam.ID
		if team.HomeAway == "away" {
			expected = game.AwayTeam.ID
		} else if team.HomeAway != "home" {
			return nil
		}
		score, err := strconv.ParseFloat(espnScore(team.Score), 64)
		if team.ID != expected || err != nil || !validScore(&score) {
			return nil
		}
		if _, duplicate := finals[team.HomeAway]; duplicate {
			return nil
		}
		finals[team.HomeAway] = score
	}
	first, last := p.Plays[0], p.Plays[len(p.Plays)-1]
	// Require the opening countdown observed in the supported complete
	// sources; an early scoreless play alone does not establish start coverage.
	openingSeconds := map[string]float64{"nba": 12 * 60, "wnba": 10 * 60, "mens-college-basketball": 20 * 60, "womens-college-basketball": 10 * 60}[game.League]
	openingClock, openingOK := scoreClock(first.Clock.DisplayValue)
	if !openingOK || openingClock != openingSeconds {
		return nil
	}
	if first.Period.Number != 1 || !validScore(first.AwayScore) || !validScore(first.HomeScore) ||
		*first.AwayScore != 0 || *first.HomeScore != 0 ||
		(game.Status == SportsGameFinal && !strings.EqualFold(last.Type.Text, "End Game")) ||
		(c.Status.Period > 0 && last.Period.Number != c.Status.Period) {
		return nil
	}
	if !validScore(last.AwayScore) || !validScore(last.HomeScore) || *last.AwayScore != finals["away"] || *last.HomeScore != finals["home"] {
		return nil
	}
	endClock, ok := scoreClock(last.Clock.DisplayValue)
	if !ok || (game.Status == SportsGameFinal && endClock != 0) {
		return nil
	}
	history := &SportsScoreHistory{
		EventID: game.ID, AwayTeamID: game.AwayTeam.ID, HomeTeamID: game.HomeTeam.ID, Complete: true,
	}
	seen := map[string]bool{}
	previousPeriod, previousClock := 1, math.Inf(1)
	periodLabel := ""
	for _, play := range p.Plays {
		sequence, err := strconv.ParseInt(play.Sequence, 10, 64)
		clock, ok := scoreClock(play.Clock.DisplayValue)
		if strings.TrimSpace(play.ID) == "" || seen[play.ID] || err != nil || sequence < 0 || !ok ||
			!validScore(play.AwayScore) || !validScore(play.HomeScore) || strings.TrimSpace(play.Period.DisplayValue) == "" {
			return nil
		}
		if play.Period.Number < previousPeriod || play.Period.Number > previousPeriod+1 {
			return nil
		}
		if play.Period.Number == previousPeriod && (clock > previousClock || (periodLabel != "" && periodLabel != play.Period.DisplayValue)) {
			return nil
		}
		seen[play.ID] = true
		history.Points = append(history.Points, SportsScorePoint{
			ID: play.ID, Sequence: play.Sequence, Period: play.Period.Number,
			PeriodLabel: play.Period.DisplayValue, Clock: play.Clock.DisplayValue,
			Away: *play.AwayScore, Home: *play.HomeScore,
		})
		previousPeriod, previousClock, periodLabel = play.Period.Number, clock, play.Period.DisplayValue
	}
	return history
}
