package sports

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func normalizeMLBDetail(game SportsGame, payload mlbSummary, now time.Time) (SportsGame, error) {
	if payload.Header.ID != providerEventID(game) || len(payload.Header.Competitions) != 1 {
		return game, fmt.Errorf("MLB summary identity mismatch")
	}
	competition := payload.Header.Competitions[0]
	sides := make(map[string][]string)
	counts := map[string]map[string]string{}
	for _, team := range competition.Competitors {
		expected := game.HomeTeam.ID
		if team.HomeAway == "away" {
			expected = game.AwayTeam.ID
		} else if team.HomeAway != "home" {
			return game, fmt.Errorf("invalid MLB side")
		}
		if team.ID != expected {
			return game, fmt.Errorf("MLB team identity mismatch")
		}
		if _, exists := sides[team.HomeAway]; exists {
			return game, fmt.Errorf("duplicate MLB side")
		}
		line := make([]string, len(team.Linescores))
		for i, inning := range team.Linescores {
			line[i] = inning.DisplayValue
		}
		sides[team.HomeAway] = line
		counts[team.HomeAway] = map[string]string{"Runs": espnScore(team.Score)}
		if team.Hits != nil {
			counts[team.HomeAway]["Hits"] = strconv.Itoa(*team.Hits)
		}
		if team.Errors != nil {
			counts[team.HomeAway]["Errors"] = strconv.Itoa(*team.Errors)
		}
		if team.HomeAway == "away" {
			game.AwayTeam.Score = espnScore(team.Score)
		} else {
			game.HomeTeam.Score = espnScore(team.Score)
		}
	}
	if len(sides) != 2 {
		return game, fmt.Errorf("incomplete MLB matchup")
	}
	if competition.Status.Type.State == "" {
		return game, fmt.Errorf("missing MLB lifecycle")
	}
	game.Status = espnStatusToGameStatus(competition.Status.Type)
	game.StatusDetail = competition.Status.Type.Detail
	game.Clock = competition.Status.DisplayClock
	game.Period = competition.Status.Type.ShortDetail
	detail := &SportsGameDetail{
		Source: "espn", UpdatedAt: now,
		Periods: []SportsPeriodScore{}, Plays: []SportsDetailPlay{}, Comparisons: []SportsComparison{},
	}
	// Pregame payloads can contain season statistics: never present these as
	// game stats.
	if game.Status != SportsGameScheduled {
		detail.PlayerStats = normalizePlayerGameStats(game, payload.Boxscore.Players)
		for _, label := range []string{"Runs", "Hits", "Errors"} {
			a, h := counts["away"][label], counts["home"][label]
			if a != "" && h != "" {
				detail.Comparisons = append(detail.Comparisons, SportsComparison{Label: label, Away: a, Home: h})
			}
		}
		n := max(len(sides["away"]), len(sides["home"]))
		for i := 0; i < n; i++ {
			a, h := "–", "–"
			if i < len(sides["away"]) && sides["away"][i] != "" {
				a = sides["away"][i]
			}
			if i < len(sides["home"]) && sides["home"][i] != "" {
				h = sides["home"][i]
			}
			detail.Periods = append(detail.Periods, SportsPeriodScore{Label: strconv.Itoa(i + 1), Away: a, Home: h})
		}
		seen := map[string]bool{}
		// ESPN's chronological plays become newest-first for Latest and the
		// timeline.
		for i := len(payload.Plays) - 1; i >= 0; i-- {
			p := payload.Plays[i]
			if p.ID == "" || p.Text == "" || seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			detail.Plays = append(detail.Plays, SportsDetailPlay{
				ID: p.ID, PeriodLabel: fmt.Sprintf("%s %d", p.Period.Type, p.Period.Number),
				Title: p.Type.Text, Description: p.Text, Scoring: p.Scoring,
			})
			if len(detail.Plays) == 200 {
				break
			}
		}
		values := map[string]map[string]string{}
		for _, team := range payload.Boxscore.Teams {
			stats := map[string]string{}
			for _, group := range team.Statistics {
				if group.Name != "batting" {
					continue
				}
				for _, stat := range group.Stats {
					stats[stat.Name] = stat.DisplayValue
				}
			}
			values[team.Team.ID] = stats
		}
		for _, field := range []struct{ key, label string }{
			{"homeRuns", "Home runs"}, {"RBIs", "RBI"}, {"walks", "Walks"},
			{"strikeouts", "Strikeouts"}, {"leftOnBase", "Left on base"},
		} {
			a := values[game.AwayTeam.ID][field.key]
			h := values[game.HomeTeam.ID][field.key]
			if a != "" && h != "" {
				detail.Comparisons = append(detail.Comparisons, SportsComparison{Label: field.label, Away: a, Home: h})
			}
		}
	}
	if game.Status == SportsGameLive && payload.Situation != nil {
		sit := payload.Situation
		pitcherID := espnPlayerID(sit.Pitcher.PlayerID)
		var pitcherPitchCount *int
		names := map[string]string{}
		for _, roster := range payload.Rosters {
			if roster.Team.ID != game.AwayTeam.ID && roster.Team.ID != game.HomeTeam.ID {
				continue
			}
			for _, player := range roster.Roster {
				names[player.Athlete.ID] = player.Athlete.DisplayName
			}
		}
		for _, team := range payload.Boxscore.Players {
			if team.Team.ID != game.AwayTeam.ID && team.Team.ID != game.HomeTeam.ID {
				continue
			}
			for _, group := range team.Statistics {
				pcIndex := -1
				for i, name := range group.Names {
					if name == "PC" {
						pcIndex = i
						break
					}
				}
				for _, player := range group.Athletes {
					names[player.Athlete.ID] = player.Athlete.DisplayName
					if pitcherID != "" && pitcherID != "0" && player.Athlete.ID == pitcherID && pcIndex >= 0 && pcIndex < len(player.Stats) {
						if count, err := strconv.Atoi(player.Stats[pcIndex]); err == nil && count >= 0 {
							pitcherPitchCount = &count
						}
					}
				}
			}
		}
		occupied := []string{}
		for _, base := range []struct {
			label  string
			runner *mlbRunner
		}{{"1st", sit.OnFirst}, {"2nd", sit.OnSecond}, {"3rd", sit.OnThird}} {
			if base.runner != nil && espnPlayerID(base.runner.PlayerID) != "" && espnPlayerID(base.runner.PlayerID) != "0" {
				occupied = append(occupied, base.label)
			}
		}
		bases := ""
		if len(occupied) > 0 {
			bases = "Runner on " + strings.Join(occupied, " & ")
			if len(occupied) > 1 {
				bases = "Runners on " + strings.Join(occupied, " & ")
			}
		}
		batterID := espnPlayerID(sit.Batter.PlayerID)
		detail.Sport = &SportsMLBSituation{
			BatterID: batterID, PitcherID: pitcherID, Kind: "mlb", Bases: bases,
			Inning: game.Period, Balls: validSummaryCount(sit.Balls, 3),
			Strikes: validSummaryCount(sit.Strikes, 2), Outs: validSummaryCount(sit.Outs, 3),
			Pitcher: names[pitcherID], PitcherPitchCount: pitcherPitchCount, Batter: names[batterID],
		}
	}
	detail.Capabilities.Plays = len(detail.Plays) > 0
	detail.Capabilities.Stats = len(detail.Comparisons) > 0 || len(detail.PlayerStats) > 0
	game.Detail = detail
	return game, nil
}
