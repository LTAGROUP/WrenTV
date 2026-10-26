package sports

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cricketSummary struct {
	Rosters []struct {
		Roster []struct {
			Athlete struct {
				ID       string `json:"id"`
				Headshot *struct {
					Href string `json:"href"`
				} `json:"headshot"`
			} `json:"athlete"`
		} `json:"roster"`
	} `json:"rosters"`
	Header struct {
		ID string `json:"id"`
	} `json:"header"`
	Matchcards []struct {
		Headline      string              `json:"headline"`
		InningsNumber string              `json:"inningsNumber"`
		TeamName      string              `json:"teamName"`
		Extras        string              `json:"extras"`
		PlayerDetails []map[string]string `json:"playerDetails"`
	} `json:"matchcards"`
}

func normalizeCricketDetail(game SportsGame, raw json.RawMessage, now time.Time) (SportsGame, error) {
	var payload cricketSummary
	if err := json.Unmarshal(raw, &payload); err != nil {
		return game, err
	}
	if payload.Header.ID != providerEventID(game) {
		return game, fmt.Errorf("cricket summary identity mismatch")
	}
	detail := SportsGameDetail{
		Source: "espn", UpdatedAt: now,
		Periods: []SportsPeriodScore{}, Plays: []SportsDetailPlay{}, Comparisons: []SportsComparison{},
	}
	if game.Detail != nil {
		detail = *game.Detail
	}
	detail.CricketScorecards = nil
	portraits := map[string]string{}
	for _, r := range payload.Rosters {
		for _, p := range r.Roster {
			if p.Athlete.Headshot != nil && strings.HasPrefix(p.Athlete.Headshot.Href, "https://") {
				portraits[p.Athlete.ID] = p.Athlete.Headshot.Href
			}
		}
	}
	for _, card := range payload.Matchcards {
		kind := strings.ToLower(strings.TrimSpace(card.Headline))
		n, err := strconv.Atoi(card.InningsNumber)
		if err != nil || n < 1 || (kind != "batting" && kind != "bowling") {
			continue
		}
		result := SportsCricketScorecard{Kind: kind, Innings: n, TeamName: card.TeamName, Extras: card.Extras}
		for _, team := range []SportsTeam{game.AwayTeam, game.HomeTeam} {
			if card.TeamName != "" && (strings.EqualFold(card.TeamName, team.Abbreviation) || strings.EqualFold(card.TeamName, team.Name)) {
				result.TeamID = team.ID
				result.TeamName = team.Name
			}
		}
		fields := []struct{ key, label string }{{"runs", "Runs"}, {"ballsFaced", "Balls"}, {"fours", "4s"}, {"sixes", "6s"}}
		if kind == "bowling" {
			fields = []struct{ key, label string }{{"overs", "Overs"}, {"maidens", "Maidens"}, {"conceded", "Runs"}, {"wickets", "Wickets"}, {"economyRate", "Economy"}}
		}
		for _, p := range card.PlayerDetails {
			if strings.TrimSpace(p["playerName"]) == "" {
				continue
			}
			player := SportsCricketPlayer{
				ID: p["playerID"], Name: p["playerName"],
				HeadshotURL: portraits[p["playerID"]], Dismissal: p["dismissal"],
			}
			for _, field := range fields {
				if value := strings.TrimSpace(p[field.key]); value != "" {
					player.Stats = append(player.Stats, SportsCricketStat{Label: field.label, Value: value})
				}
			}
			// Blank figures are not zeroes; keep unplayed batters out of
			// performance cards.
			if len(player.Stats) > 0 {
				result.Players = append(result.Players, player)
			}
		}
		if len(result.Players) > 0 {
			detail.CricketScorecards = append(detail.CricketScorecards, result)
		}
	}
	if len(detail.CricketScorecards) == 0 {
		return game, fmt.Errorf("cricket scorecards unavailable")
	}
	detail.Source = "espn"
	detail.UpdatedAt = now
	detail.Stale = false
	detail.Capabilities.Stats = true
	game.Detail = &detail
	return game, nil
}
