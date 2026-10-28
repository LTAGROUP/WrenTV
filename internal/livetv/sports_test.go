package livetv

import (
	"testing"
	"time"
)

func TestCleanTitle(t *testing.T) {
	for _, c := range []struct {
		in, title string
		live, new bool
	}{
		{"NBA Basketball : Detroit Pistons at New York Knicks ᴸᶦᵛᵉ", "NBA Basketball : Detroit Pistons at New York Knicks", true, false},
		{"MLB on FOX Pregame  ᴸᶦᵛᵉ", "MLB on FOX Pregame", true, false},
		{"College Football Live  ᴺᵉʷ", "College Football Live", false, true},
		{"Inside Story", "Inside Story", false, false},
	} {
		title, live, isNew := cleanTitle(c.in)
		if title != c.title || live != c.live || isNew != c.new {
			t.Errorf("cleanTitle(%q) = %q %v %v", c.in, title, live, isNew)
		}
	}
}

func TestMatchup(t *testing.T) {
	s := &Service{}
	packers := Team{Name: "Green Bay Packers", Short: "Packers", Logo: "gb.png"}
	panthers := Team{Name: "Carolina Panthers", Short: "Panthers", Logo: "car.png"}
	nhlPanthers := Team{Name: "Florida Panthers", Short: "Panthers", Logo: "fla.png"}
	lightning := Team{Name: "Tampa Bay Lightning", Short: "Lightning", Logo: "tb.png"}
	jets := Team{Name: "Winnipeg Jets", Short: "Jets", Logo: "wpg.png"}
	s.teams = teams{
		byName: map[string]map[string]Team{
			"NFL": {"green bay packers": packers, "packers": packers, "carolina panthers": panthers, "panthers": panthers, "jets": {Name: "New York Jets"}},
			"NHL": {"panthers": nhlPanthers, "lightning": lightning, "jets": jets},
		},
		fetched: map[string]time.Time{},
		loading: map[string]bool{},
	}
	for _, l := range leagues {
		s.teams.fetched[l.name] = time.Now()
	}
	m := s.matchup("NFL Football : Carolina Panthers at Green Bay Packers", "ESPN", true)
	if m == nil || m.League != "NFL" || m.Away.Logo != "car.png" || m.Home.Logo != "gb.png" {
		t.Fatalf("NFL game: %+v", m)
	}
	if m := s.matchup("Jets at Lightning", "Events 04", true); m == nil || m.League != "NHL" || m.Away.Logo != "wpg.png" {
		t.Fatalf("both teams pick the league: %+v", m)
	}
	if m := s.matchup("3rd T20: New Zealand vs. India; T20 Cricket", "Sky Sports Cricket", true); m == nil || m.Away.Name != "New Zealand" || m.Home.Name != "India" || m.Away.Logo != "" {
		t.Fatalf("cricket: %+v", m)
	}
	if m := s.matchup("BBC News at Ten", "BBC News", false); m != nil {
		t.Fatalf("news isn't a game: %+v", m)
	}
}
