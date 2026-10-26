package sports

import (
	"math"
	"strconv"
	"strings"
)

// ESPN supplies parallel labels and values. Never assign values by a guessed
// index.
type playerBoxscoreTeam struct {
	Team struct {
		ID string `json:"id"`
	} `json:"team"`
	Statistics []struct {
		Name     string   `json:"name"`
		Names    []string `json:"names"`
		Labels   []string `json:"labels"`
		Athletes []struct {
			Stats      []string `json:"stats"`
			DidNotPlay bool     `json:"didNotPlay"`
			Athlete    struct {
				ID          string         `json:"id"`
				DisplayName string         `json:"displayName"`
				Jersey      string         `json:"jersey"`
				Headshot    playerHeadshot `json:"headshot"`
			} `json:"athlete"`
		} `json:"athletes"`
	} `json:"statistics"`
}

func normalizePlayerGameStats(game SportsGame, teams []playerBoxscoreTeam) []SportsPlayerGameStats {
	if game.Status == SportsGameScheduled {
		return nil
	}
	var rows []SportsPlayerGameStats
	seen := map[string]bool{}
	for _, team := range teams {
		if team.Team.ID != game.AwayTeam.ID && team.Team.ID != game.HomeTeam.ID {
			continue
		}
		for _, group := range team.Statistics {
			labels := group.Labels
			if len(labels) == 0 {
				labels = group.Names
			}
			category := ""
			var selected string
			switch {
			case game.League == "mlb":
				for _, label := range labels {
					if label == "IP" {
						category, selected = "Pitching", "|IP|H|R|ER|BB|K|HR|PC-ST|"
						break
					}
					if label == "AB" {
						category, selected = "Batting", "|AB|H|R|RBI|HR|BB|K|"
					}
				}
			case game.League == "nba" || game.League == "wnba" || strings.Contains(game.League, "college-basketball"):
				category, selected = "Game stats", "|MIN|PTS|FG|3PT|FT|REB|AST|TO|STL|BLK|PF|+/-|"
			case game.League == "nhl":
				switch group.Name {
				// ESPN labels shootout goals SOG; ordinary shots are S.
				case "forwards", "defenses", "skaters":
					category, selected = "Skater", "|G|A|S|+/-|TOI|PIM|BS|HT|FW|FL|"
				case "goalies":
					category, selected = "Goalie", "|SV|SA|GA|SV%|TOI|"
				}
			case game.League == "nfl" || game.League == "college-football":
				switch group.Name {
				case "passing":
					category, selected = "Passing", "|C/ATT|YDS|TD|INT|SACKS|RTG|"
				case "rushing":
					category, selected = "Rushing", "|CAR|YDS|AVG|TD|LONG|"
				case "defensive":
					category, selected = "Defense", "|TOT|SOLO|SACKS|TFL|PD|QB HTS|TD|"
				case "interceptions":
					category, selected = "Interceptions", "|INT|YDS|TD|"
				case "fumbles":
					category, selected = "Fumbles", "|FUM|LOST|REC|"
				case "kicking":
					category, selected = "Kicking", "|FG|LONG|XP|PTS|"
				case "punting":
					category, selected = "Punting", "|NO|YDS|AVG|TB|In 20|LONG|"
				case "kickReturns":
					category, selected = "Kick returns", "|NO|YDS|AVG|LONG|TD|"
				case "puntReturns":
					category, selected = "Punt returns", "|NO|YDS|AVG|LONG|TD|"
				case "receiving":
					category, selected = "Receiving", "|REC|YDS|AVG|TD|TGTS|LONG|"
				}
			}
			if category == "" {
				continue
			}
			for _, player := range group.Athletes {
				key := team.Team.ID + ":" + player.Athlete.ID + ":" + category
				if player.Athlete.ID == "" || player.Athlete.DisplayName == "" || player.DidNotPlay || seen[key] || len(labels) != len(player.Stats) {
					continue
				}
				row := SportsPlayerGameStats{
					ID: player.Athlete.ID, TeamID: team.Team.ID, Name: player.Athlete.DisplayName,
					Jersey: strings.TrimSpace(player.Athlete.Jersey), HeadshotURL: string(player.Athlete.Headshot),
					Category: category,
				}
				for i, label := range labels {
					value := strings.TrimSpace(player.Stats[i])
					if label != "" && strings.Contains(selected, "|"+label+"|") && value != "" && value != "--" && value != "—" && value != "-" {
						displayLabel := label
						if category == "Passing" && label == "SACKS" {
							displayLabel = "SACK-YDS"
						}
						row.Stats = append(row.Stats, SportsPlayerStatistic{Label: displayLabel, Value: value})
					}
				}
				if len(row.Stats) > 0 {
					rows = append(rows, row)
					seen[key] = true
				}
			}
		}
	}
	return rows
}

// scoreClock validates a countdown clock without converting it to elapsed time.
func scoreClock(value string) (float64, bool) {
	parts := strings.Split(value, ":")
	if len(parts) < 1 || len(parts) > 2 {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds >= 60 {
		return 0, false
	}
	if len(parts) == 2 {
		minutes, err := strconv.Atoi(parts[0])
		if err != nil || minutes < 0 {
			return 0, false
		}
		seconds += float64(minutes) * 60
	}
	return seconds, true
}

func validScore(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && math.Trunc(*value) == *value
}
