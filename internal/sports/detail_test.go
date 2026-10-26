package sports

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const nbaSummaryFixture = `{
  "header": {
    "id": "401000001",
    "season": {"name": "2026-27"},
    "competitions": [{
      "status": {"displayClock": "4:21", "period": 4, "type": {"state": "in", "detail": "4:21 - 4th"}},
      "competitors": [
        {"id": "2", "homeAway": "home", "score": "101", "winner": false, "linescores": [{"displayValue": "24"}, {"displayValue": "25"}, {"displayValue": "28"}, {"displayValue": "24"}]},
        {"id": "17", "homeAway": "away", "score": "99", "winner": false, "linescores": [{"displayValue": "22"}, {"displayValue": "27"}, {"displayValue": "23"}, {"displayValue": "27"}]}
      ]
    }]
  },
  "boxscore": {
    "players": [{
      "team": {"id": "2"},
      "statistics": [{
        "name": "offense",
        "labels": ["MIN", "PTS", "FG", "3PT", "FT", "REB", "AST", "TO", "STL", "BLK", "PF", "+/-"],
        "athletes": [{
          "athlete": {"id": "10", "displayName": "Jayson Tatum", "jersey": "0", "headshot": {"href": "https://a.espncdn.com/i/headshots/nba/players/full/10.png"}},
          "stats": ["34", "31", "10-20", "4-9", "7-8", "8", "4", "2", "1", "1", "3", "+2"]
        }]
      }]
    }],
    "teams": [{
      "team": {"id": "2"},
      "statistics": [{"name": "totalRebounds", "displayValue": "44"}, {"name": "fieldGoalPct", "displayValue": "48.1"}, {"name": "assists", "displayValue": "24"}]
    }, {
      "team": {"id": "17"},
      "statistics": [{"name": "totalRebounds", "displayValue": "41"}, {"name": "fieldGoalPct", "displayValue": "45.2"}, {"name": "assists", "displayValue": "22"}]
    }]
  },
  "plays": [
    {"id": "p1", "text": "Tatum makes 2-pt jump shot", "scoringPlay": true, "period": {"number": 1, "displayValue": "1st Quarter"}, "clock": {"displayValue": "11:42"}, "type": {"text": "Made Shot"}},
    {"id": "p2", "text": "Tatum makes 3-pt jump shot", "scoringPlay": true, "period": {"number": 4, "displayValue": "4th Quarter"}, "clock": {"displayValue": "4:21"}, "type": {"text": "Made Shot"}}
  ],
  "leaders": [{
    "team": {"id": "2"},
    "leaders": [{"name": "points", "displayName": "Points", "leaders": [{"displayValue": "31", "athlete": {"id": "10", "displayName": "Jayson Tatum"}}]}]
  }]
}`

func TestNormalizeTeamDetailPeriodsPlaysAndStats(t *testing.T) {
	var p teamSportSummary
	if err := json.Unmarshal([]byte(nbaSummaryFixture), &p); err != nil {
		t.Fatal(err)
	}
	game := SportsGame{
		ID: "401000001", League: "nba", Status: SportsGameLive,
		HomeTeam: SportsTeam{ID: "2", Name: "Boston Celtics"},
		AwayTeam: SportsTeam{ID: "17", Name: "New York Knicks"},
	}
	got, err := normalizeTeamDetail(game, p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d := got.Detail
	if d == nil || len(d.Periods) != 4 || d.Periods[3].Home != "24" || d.Periods[3].Away != "27" {
		t.Fatalf("periods = %+v", d)
	}
	if len(d.Plays) != 2 || d.Plays[0].ID != "p2" || d.Plays[1].ID != "p1" {
		t.Fatalf("plays = %+v, want newest first", d.Plays)
	}
	if len(d.Comparisons) != 3 || d.Comparisons[0].Label != "Field goal %" || d.Comparisons[0].Away != "45.2" {
		t.Fatalf("comparisons = %+v", d.Comparisons)
	}
	if len(d.PlayerStats) != 1 || d.PlayerStats[0].Name != "Jayson Tatum" || d.PlayerStats[0].Stats[1].Value != "31" {
		t.Fatalf("player stats = %+v", d.PlayerStats)
	}
	if len(d.Leaders) != 1 || d.Leaders[0].TeamAbbreviation != "" || d.Leaders[0].Value != "31" {
		t.Fatalf("leaders = %+v", d.Leaders)
	}
	if !d.Capabilities.Plays || !d.Capabilities.Stats {
		t.Fatalf("capabilities = %+v", d.Capabilities)
	}
	if got.HomeTeam.Score != "101" || got.AwayTeam.Score != "99" || got.Period != "Q4" {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestNormalizeTeamDetailRejectsIdentityMismatch(t *testing.T) {
	var p teamSportSummary
	if err := json.Unmarshal([]byte(nbaSummaryFixture), &p); err != nil {
		t.Fatal(err)
	}
	game := SportsGame{
		ID: "401000001", League: "nba",
		HomeTeam: SportsTeam{ID: "wrong"}, AwayTeam: SportsTeam{ID: "17"},
	}
	if _, err := normalizeTeamDetail(game, p, time.Now()); err == nil {
		t.Fatal("accepted a summary for another team")
	}
}

const mlbFinalFixture = `{
  "header": {
    "id": "401000002",
    "competitions": [{
      "status": {"displayClock": "0:00", "type": {"state": "post", "completed": true, "detail": "Final", "shortDetail": "Final"}},
      "competitors": [
        {"id": "1", "homeAway": "home", "score": "1", "hits": 6, "errors": 0, "linescores": [{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "1"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "x"}]},
        {"id": "5", "homeAway": "away", "score": "0", "hits": 4, "errors": 1, "linescores": [{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"},{"displayValue": "0"}]}
      ]
    }]
  },
  "plays": [
    {"id": "a", "text": "First out", "period": {"type": "Top", "number": 1}, "type": {"text": "Out"}},
    {"id": "b", "text": "RBI single", "scoringPlay": true, "period": {"type": "Bottom", "number": 3}, "type": {"text": "Single"}}
  ],
  "boxscore": {
    "teams": [
      {"team": {"id": "1"}, "statistics": [{"name": "batting", "stats": [{"name": "homeRuns", "displayValue": "1"}, {"name": "RBIs", "displayValue": "1"}, {"name": "walks", "displayValue": "2"}, {"name": "strikeouts", "displayValue": "8"}, {"name": "leftOnBase", "displayValue": "5"}]}]},
      {"team": {"id": "5"}, "statistics": [{"name": "batting", "stats": [{"name": "homeRuns", "displayValue": "0"}, {"name": "RBIs", "displayValue": "0"}, {"name": "walks", "displayValue": "3"}, {"name": "strikeouts", "displayValue": "9"}, {"name": "leftOnBase", "displayValue": "7"}]}]}
    ]
  }
}`

func TestMLBDetailKeepsZerosAndUnplayedInnings(t *testing.T) {
	var p mlbSummary
	if err := json.Unmarshal([]byte(mlbFinalFixture), &p); err != nil {
		t.Fatal(err)
	}
	game := SportsGame{
		ID: "401000002", League: "mlb", ProviderEventID: "401000002",
		HomeTeam: SportsTeam{ID: "1"}, AwayTeam: SportsTeam{ID: "5"},
	}
	got, err := normalizeMLBDetail(game, p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d := got.Detail
	if len(d.Periods) != 9 || d.Periods[8].Home != "x" {
		t.Fatalf("periods = %+v", d.Periods)
	}
	foundErrors := false
	for _, c := range d.Comparisons {
		if c.Label == "Errors" {
			foundErrors = true
			if c.Home != "0" || c.Away != "1" {
				t.Fatalf("errors comparison = %+v", c)
			}
		}
	}
	if !foundErrors {
		t.Fatal("missing Errors comparison")
	}
	if len(d.Plays) != 2 || d.Plays[0].ID != "b" || d.Plays[0].PeriodLabel != "Bottom 3" {
		t.Fatalf("plays = %+v", d.Plays)
	}
	if !d.Capabilities.Plays || !d.Capabilities.Stats {
		t.Fatalf("capabilities = %+v", d.Capabilities)
	}
}

func TestMLBPregameDoesNotExposeSeasonStats(t *testing.T) {
	var p mlbSummary
	if err := json.Unmarshal([]byte(mlbFinalFixture), &p); err != nil {
		t.Fatal(err)
	}
	p.Header.Competitions[0].Status.Type.State = "pre"
	game := SportsGame{
		ID: "401000002", League: "mlb", ProviderEventID: "401000002",
		HomeTeam: SportsTeam{ID: "1"}, AwayTeam: SportsTeam{ID: "5"},
	}
	got, err := normalizeMLBDetail(game, p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Detail.Capabilities.Stats || got.Detail.Capabilities.Plays || len(got.Detail.Periods) > 0 || len(got.Detail.Plays) > 0 {
		t.Fatalf("pregame leaked played statistics: %+v", got.Detail)
	}
}

const pregameFixture = `{
  "header": {
    "id": "401000003",
    "season": {"year": 2026, "type": 2},
    "competitions": [{
      "date": "2026-10-24T23:30:00Z",
      "competitors": [
        {"id": "2", "homeAway": "home", "record": [{"type": "total", "summary": "2-1"}]},
        {"id": "17", "homeAway": "away", "record": [{"type": "total", "summary": "1-2"}]}
      ]
    }]
  },
  "boxscore": {"teams": [
    {"team": {"id": "2"}, "statistics": [{"name": "avgPoints", "displayValue": "112.3"}, {"name": "gamesPlayed", "displayValue": "3"}]},
    {"team": {"id": "17"}, "statistics": [{"name": "avgPoints", "displayValue": "108.1"}, {"name": "gamesPlayed", "displayValue": "3"}]}
  ]},
  "lastFiveGames": [
    {"team": {"id": "2"}, "events": [{"id": "old", "gameDate": "2026-10-20T23:30:00Z", "gameResult": "W", "score": "110-100", "homeTeamId": "2", "awayTeamId": "9", "opponent": {"id": "9", "displayName": "Miami Heat"}}]}
  ],
  "seasonseries": []
}`

func TestPregameBuildsSeasonContextOnlyBeforeTipoff(t *testing.T) {
	game := SportsGame{
		ID: "401000003", League: "nba", Status: SportsGameScheduled,
		StartTime: time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC),
		HomeTeam:  SportsTeam{ID: "2", Name: "Boston Celtics", Record: "2-1"},
		AwayTeam:  SportsTeam{ID: "17", Name: "New York Knicks", Record: "1-2"},
	}
	pregame := normalizePregame(game, []byte(pregameFixture), time.Date(2026, 10, 24, 18, 0, 0, 0, time.UTC))
	if pregame == nil {
		t.Fatal("missing pregame")
	}
	if pregame.SeasonLabel != "2025–26 · Regular season" {
		t.Fatalf("season label = %q", pregame.SeasonLabel)
	}
	if len(pregame.Comparisons) != 1 || pregame.Comparisons[0].Label != "Points per game" || pregame.Comparisons[0].Home != "112.3" {
		t.Fatalf("comparisons = %+v", pregame.Comparisons)
	}
	if len(pregame.Teams) != 2 {
		t.Fatalf("teams = %+v", pregame.Teams)
	}
	found := false
	for _, team := range pregame.Teams {
		if team.TeamID == "2" && len(team.Recent) == 1 && team.Recent[0].Opponent == "Miami Heat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("recent results missing: %+v", pregame.Teams)
	}

	// Once the game is live, season comparisons must not masquerade as game stats.
	game.Status = SportsGameLive
	live := normalizePregame(game, []byte(pregameFixture), time.Date(2026, 10, 24, 23, 45, 0, 0, time.UTC))
	if live == nil || len(live.Comparisons) != 0 {
		t.Fatalf("live pregame comparisons = %+v", live)
	}
}

func TestPregameRejectsWrongEvent(t *testing.T) {
	game := SportsGame{
		ID: "other", League: "nba",
		HomeTeam: SportsTeam{ID: "2"}, AwayTeam: SportsTeam{ID: "17"},
	}
	if normalizePregame(game, []byte(pregameFixture), time.Now()) != nil {
		t.Fatal("accepted another event's summary")
	}
}

func TestDetailCacheIsBounded(t *testing.T) {
	s := NewWithClient(t.TempDir(), nil)
	now := time.Now()
	for i := 0; i < detailCacheSize+20; i++ {
		s.cacheDetail(strings.Repeat("x", i+1), detailCacheEntry{expires: now.Add(time.Duration(i) * time.Second)})
	}
	if len(s.details) > detailCacheSize {
		t.Fatalf("detail cache grew to %d", len(s.details))
	}
}
