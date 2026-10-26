package sports

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type espnLineScore struct {
	Tiebreak     *int            `json:"tiebreak"`
	Winner       *bool           `json:"winner"`
	Period       int             `json:"period"`
	Value        json.RawMessage `json:"value"`
	DisplayValue json.RawMessage `json:"displayValue"`
	Linescores   []espnLineScore `json:"linescores"`
	ScoreType    struct {
		DisplayValue string `json:"displayValue"`
	} `json:"scoreType"`
	Runs        *int            `json:"runs"`
	Wickets     *int            `json:"wickets"`
	Overs       json.RawMessage `json:"overs"`
	IsBatting   bool            `json:"isBatting"`
	Description string          `json:"description"`
	Target      *int            `json:"target"`
	IsCurrent   json.RawMessage `json:"isCurrent"`
}

// Cricket uses quoted booleans while other ESPN sports use JSON booleans.
func (c *espnCompetitor) UnmarshalJSON(data []byte) error {
	type alias espnCompetitor
	var raw struct {
		*alias
		Winner json.RawMessage `json:"winner"`
	}
	raw.alias = (*alias)(c)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	winner, err := parseESPNBoolean(raw.Winner)
	if err != nil {
		return fmt.Errorf("competitor winner: %w", err)
	}
	c.Winner = winner != nil && *winner
	return nil
}

func parseESPNBoolean(raw json.RawMessage) (*bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var value bool
	switch string(raw) {
	case "true", `"true"`:
		value = true
	case "false", `"false"`:
		value = false
	default:
		return nil, fmt.Errorf("invalid boolean %s", raw)
	}
	return &value, nil
}

func applyScoreboardDetail(g *SportsGame, comp espnCompetition, league League) {
	if league.Sport != "golf" && league.Sport != "cricket" && league.Sport != "tennis" && league.Sport != "rugby" && league.Sport != "rugby-league" {
		return
	}
	d := &SportsGameDetail{
		Source: "espn", UpdatedAt: time.Now(),
		Periods: []SportsPeriodScore{}, Plays: []SportsDetailPlay{}, Comparisons: []SportsComparison{},
	}
	switch league.Sport {
	case "rugby", "rugby-league":
		for i, play := range comp.Details {
			row := SportsDetailPlay{ID: fmt.Sprintf("%s:%d", g.ID, i), TeamID: play.Team.ID, Title: play.Type.Text, Clock: play.Clock.DisplayValue}
			row.Scoring = play.Type.Text == "try" || play.Type.Text == "conversion" || play.Type.Text == "penalty goal" || play.Type.Text == "drop goal"
			for _, a := range play.Athletes {
				row.Participants = append(row.Participants, SportsPlayParticipant{ID: a.ID, Name: a.DisplayName})
				if row.Description != "" {
					row.Description += " · "
				}
				row.Description += a.DisplayName
			}
			d.Plays = append(d.Plays, row)
		}
		d.Capabilities.Plays = len(d.Plays) > 0
	case "golf":
		if league.Slug == "tgl" {
			return
		}
		g.HomeTeam = SportsTeam{}
		g.AwayTeam = SportsTeam{}
		g.EventKind = "tournament"
		for i := range g.Participants {
			g.Participants[i].Position = 0
			if g.Status == SportsGameScheduled {
				g.Participants[i].Score = ""
			}
		}
		for _, c := range comp.Competitors {
			team := competitorTeam(c)
			if team.ID == "" || team.Name == "" {
				continue
			}
			entry := SportsGolfEntry{ID: team.ID, Name: team.Name, Score: team.Score}
			if g.Status == SportsGameScheduled {
				entry.Score = ""
				d.Leaderboard = append(d.Leaderboard, entry)
				continue
			}
			for _, r := range c.Linescores {
				if r.Period < 1 || (len(r.Value) == 0 && len(r.Linescores) == 0) {
					continue
				}
				round := SportsGolfRound{Round: r.Period, Strokes: espnScore(r.Value), ToPar: espnScore(r.DisplayValue)}
				for _, h := range r.Linescores {
					if h.Period < 1 || h.Period > 18 || len(h.Value) == 0 {
						continue
					}
					round.Holes = append(round.Holes, SportsGolfHole{Hole: h.Period, Strokes: espnScore(h.Value), ToPar: h.ScoreType.DisplayValue})
				}
				sort.SliceStable(round.Holes, func(i, j int) bool { return round.Holes[i].Hole < round.Holes[j].Hole })
				entry.Rounds = append(entry.Rounds, round)
			}
			d.Leaderboard = append(d.Leaderboard, entry)
		}
		d.Capabilities.Stats = len(d.Leaderboard) > 0
	case "cricket":
		if g.Status == SportsGameScheduled {
			g.Detail = d
			return
		}
		g.Clock = ""
		if comp.Status.Summary != "" {
			g.StatusDetail = comp.Status.Summary
		}
		d.Innings = normalizeCricketInnings(comp)
		d.Capabilities.Stats = len(d.Innings) > 0
	case "tennis":
		if comp.Venue != nil {
			g.CourtName = comp.Venue.Court
		}
		if n := comp.Format.Regulation.Periods; n == 3 || n == 5 {
			g.BestOf = n
		}

		if g.Status == SportsGameScheduled {
			g.Detail = d
			return
		}
		rows := map[int]*SportsPeriodScore{}
		for _, c := range comp.Competitors {
			for i, set := range c.Linescores {
				value := espnScore(set.Value)
				if value == "" {
					continue
				}
				n := set.Period
				if n < 1 {
					n = i + 1
				}
				if rows[n] == nil {
					rows[n] = &SportsPeriodScore{Label: fmt.Sprintf("Set %d", n), Away: "–", Home: "–"}
				}
				if competitorTeam(c).ID == g.AwayTeam.ID {
					rows[n].Away = value
					rows[n].AwayWinner = set.Winner
					if set.Tiebreak != nil && *set.Tiebreak >= 0 {
						rows[n].AwayTiebreak = set.Tiebreak
					}
				} else {
					rows[n].Home = value
					rows[n].HomeWinner = set.Winner
					if set.Tiebreak != nil && *set.Tiebreak >= 0 {
						rows[n].HomeTiebreak = set.Tiebreak
					}
				}
			}
		}
		keys := []int{}
		for n := range rows {
			keys = append(keys, n)
		}
		sort.Ints(keys)
		for _, n := range keys {
			d.Periods = append(d.Periods, *rows[n])
		}
	}
	g.Detail = d
}

// Tennis groups matches beneath tournaments; MMA puts every bout in
// competitions. Keep their individual identities and start times instead of the
// tournament/card date.
func scoreboardEventGames(event espnEvent, league League) []SportsGame {
	if league.Sport != "tennis" && league.Sport != "mma" && league.Slug != "tgl" {
		if game, ok := espnEventToGame(event, league); ok {
			return []SportsGame{game}
		}
		return nil
	}
	competitions := append([]espnCompetition(nil), event.Competitions...)
	for _, group := range event.Groupings {
		competitions = append(competitions, group.Competitions...)
	}
	games := []SportsGame{}
	seen := map[string]bool{}
	for _, competition := range competitions {
		if competition.ID == "" || seen[competition.ID] || len(competition.Competitors) != 2 {
			continue
		}
		if league.Slug == "tgl" {
			// Athlete head-to-head holes belong inside the team match, not separate hub cards.
			if competition.Competitors[0].Athlete != nil || competition.Competitors[1].Athlete != nil {
				continue
			}
		}
		seen[competition.ID] = true
		// Unknown set results stay unknown; never invent a 0–0 result for a future match.
		if league.Sport == "tennis" && competition.Status.Type.State != "pre" {
			competition.Competitors = append([]espnCompetitor(nil), competition.Competitors...)
			for i := range competition.Competitors {
				c := &competition.Competitors[i]
				if espnScore(c.Score) != "" {
					continue
				}
				wins, known := 0, false
				for _, set := range c.Linescores {
					if set.Winner != nil {
						known = true
						if *set.Winner {
							wins++
						}
					}
				}
				if known {
					c.Score = json.RawMessage(strconv.Itoa(wins))
				}
			}
		}
		match := event
		match.ID = league.ID + ":" + event.ID + ":" + competition.ID
		match.Name = ""
		if competition.Date != "" {
			match.Date = competition.Date
		}
		match.Competitions = []espnCompetition{competition}
		if game, ok := espnEventToGame(match, league); ok {
			if league.Sport == "mma" {
				game.Combat = &SportsCombatContext{CardName: strings.TrimSpace(event.Name), Division: strings.TrimSpace(competition.Type.Abbreviation)}
				if rounds := competition.Format.Regulation.Periods; rounds == 3 || rounds == 5 {
					game.Combat.ScheduledRounds = rounds
				}
			}
			game.ParentEventID = event.ID
			game.ProviderEventID = competition.ID
			if league.Sport == "tennis" {
				parts := []string{}
				for _, value := range []string{event.Name, competition.Type.Text, competition.Round.DisplayName} {
					if value = strings.TrimSpace(value); value != "" {
						parts = append(parts, value)
					}
				}
				game.EventContext = strings.Join(parts, " · ")
				if competition.Status.Period > 0 {
					game.Period = "Set " + strconv.Itoa(competition.Status.Period)
				}
			}
			// Broadcasts often name the whole card/tournament, not each bout or
			// match. Keep that identity alongside the individual competitors.
			if parent := strings.TrimSpace(event.Name); parent != "" {
				game.Title = parent + ": " + game.Title
			}
			games = append(games, game)
		}
	}
	if league.Sport == "mma" {
		bouts := []SportsCombatBout{}
		for _, g := range games {
			bouts = append(bouts, SportsCombatBout{ID: g.ID, Away: g.AwayTeam, Home: g.HomeTeam, Division: g.Combat.Division, Status: g.Status, Period: g.Period, Clock: g.Clock})
		}
		for i := range games {
			games[i].Combat.Bouts = bouts
		}
	}
	return games
}

// New team sports use literal provider period/set values, without NFL drives,
// MLB runner diagrams or guesses about regulation period counts.
func applyGenericScoreboardPeriods(game *SportsGame, comp espnCompetition, league League) {
	switch league.Sport {
	case "australian-football", "lacrosse", "volleyball", "field-hockey", "water-polo", "baseball", "hockey", "basketball", "football", "soccer":
	default:
		return
	}
	if !strings.HasPrefix(league.ID, "espn:") || game.Status == SportsGameScheduled {
		return
	}
	sides := map[string][]espnLineScore{}
	for _, c := range comp.Competitors {
		sides[competitorTeam(c).ID] = c.Linescores
	}
	home, away := sides[game.HomeTeam.ID], sides[game.AwayTeam.ID]
	if len(home) == 0 && len(away) == 0 {
		return
	}
	detail := game.Detail
	if detail == nil {
		detail = &SportsGameDetail{Source: "espn", UpdatedAt: time.Now()}
	}
	detail.Periods = nil
	for i := 0; i < max(len(home), len(away)); i++ {
		values := []string{"", ""}
		for side, rows := range [][]espnLineScore{away, home} {
			if i < len(rows) {
				values[side] = espnScore(rows[i].DisplayValue)
				if values[side] == "" {
					values[side] = espnScore(rows[i].Value)
				}
			}
		}
		if values[0] == "" && values[1] == "" {
			continue
		}
		label := periodLabel(i+1, league.Sport)
		if league.Sport == "volleyball" {
			label = fmt.Sprintf("Set %d", i+1)
		}
		detail.Periods = append(detail.Periods, SportsPeriodScore{Label: label, Away: values[0], Home: values[1]})
	}
	detail.Capabilities.Stats = len(detail.Periods) > 0
	game.Detail = detail
}

var errPartialScoreboard = errors.New("provider scoreboard may be truncated; displaying available events")

func mergeCoverageGames(previous, fresh []SportsGame) []SportsGame {
	result := append([]SportsGame(nil), fresh...)
	seen := map[string]bool{}
	for _, game := range fresh {
		seen[game.ID] = true
	}
	for _, game := range previous {
		if !seen[game.ID] {
			result = append(result, game)
			seen[game.ID] = true
		}
	}
	return result
}

var cricketTargetPattern = regexp.MustCompile(`\btarget ([0-9]+)\b`)

func normalizeCricketInnings(comp espnCompetition) []SportsCricketInnings {
	var result []SportsCricketInnings
	for _, c := range comp.Competitors {
		for _, inn := range c.Linescores {
			// isBatting identifies whose innings this is, not who is batting now.
			// Opponent placeholders may contain overs and zeros, so do not infer ownership.
			if !inn.IsBatting || inn.Period < 1 {
				continue
			}
			row := SportsCricketInnings{
				TeamID: competitorTeam(c).ID, Number: inn.Period, Runs: inn.Runs,
				Wickets: inn.Wickets, Overs: espnScore(inn.Overs), Description: inn.Description, Target: inn.Target,
			}
			if row.Runs != nil {
				row.Score = strconv.Itoa(*row.Runs)
				if row.Wickets != nil {
					row.Score += fmt.Sprintf("/%d", *row.Wickets)
				}
			}
			// isCurrent is explicitly 0/1 in cricket scoreboards. Never infer it from order.
			switch string(bytes.TrimSpace(inn.IsCurrent)) {
			case "0", "false", `"false"`:
				v := false
				row.Batting = &v
			case "1", "true", `"true"`:
				v := comp.Status.Type.State == "in"
				row.Batting = &v
			}
			// The aggregate display score's target belongs only to the current innings.
			if row.Target == nil && (string(inn.IsCurrent) == "1" || (row.Batting != nil && *row.Batting)) {
				if match := cricketTargetPattern.FindStringSubmatch(espnScore(c.Score)); len(match) == 2 {
					n, err := strconv.Atoi(match[1])
					if err == nil {
						row.Target = &n
					}
				}
			}
			result = append(result, row)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Number < result[j].Number })
	return result
}
