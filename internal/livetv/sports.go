package livetv

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Matchup is a game a programme shows, with the teams' logos and colours
// so the overview can draw it like a scoreboard.
type Matchup struct {
	League string `json:"league,omitempty"` // "NFL"
	Away   Team   `json:"away"`
	Home   Team   `json:"home"`
}

// Team is one side of a matchup. Logo and Color are empty when the team
// isn't one ESPN lists (cricket, say).
type Team struct {
	Name  string `json:"name"`
	Short string `json:"short,omitempty"`
	Logo  string `json:"logo,omitempty"`
	Color string `json:"color,omitempty"` // "203731", no '#'
}

// league is an ESPN league whose teams the overview knows, and the words in
// a programme's title or channel that say a game is in it.
type league struct {
	name, path string
	words      []string
	// Pro leagues are tried when nothing names the league.
	pro bool
}

var leagues = []league{
	{"NFL", "football/nfl", []string{"nfl"}, true},
	{"NBA", "basketball/nba", []string{"nba"}, true},
	{"MLB", "baseball/mlb", []string{"mlb", "world series", "baseball"}, true},
	{"NHL", "hockey/nhl", []string{"nhl", "hockey"}, true},
	{"WNBA", "basketball/wnba", []string{"wnba"}, false},
	{"College Football", "football/college-football", []string{"college football", "ncaa football", "ncaaf", "cfb"}, false},
	{"College Basketball", "basketball/mens-college-basketball", []string{"college basketball", "ncaa basketball", "ncaab", "march madness"}, false},
	{"MLS", "soccer/usa.1", []string{"mls", "major league soccer"}, false},
	{"Premier League", "soccer/eng.1", []string{"premier league", "epl"}, false},
	{"La Liga", "soccer/esp.1", []string{"la liga", "laliga"}, false},
	{"Bundesliga", "soccer/ger.1", []string{"bundesliga"}, false},
	{"Serie A", "soccer/ita.1", []string{"serie a"}, false},
	{"Ligue 1", "soccer/fra.1", []string{"ligue 1"}, false},
	{"Champions League", "soccer/uefa.champions", []string{"champions league", "ucl"}, false},
}

const teamsEvery = 24 * time.Hour

// teams is each league's teams by every name they go by, fetched from
// ESPN's public site API a day at a time.
type teams struct {
	client *http.Client

	mu      sync.Mutex
	byName  map[string]map[string]Team // league -> name -> team
	fetched map[string]time.Time
	loading map[string]bool
}

func (t *teams) lookup(lg league) map[string]Team {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byName == nil {
		t.byName, t.fetched, t.loading = map[string]map[string]Team{}, map[string]time.Time{}, map[string]bool{}
	}
	// A stale or missing league is fetched in the background: the overview
	// shows names alone this time, logos the next.
	if time.Since(t.fetched[lg.name]) > teamsEvery && !t.loading[lg.name] {
		t.loading[lg.name] = true
		go t.fetch(lg)
	}
	return t.byName[lg.name]
}

func (t *teams) fetch(lg league) {
	names, err := t.download(lg)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.loading[lg.name] = false
	t.fetched[lg.name] = time.Now()
	if err != nil {
		// Try again in an hour, not a day.
		t.fetched[lg.name] = time.Now().Add(-teamsEvery + time.Hour)
		slog.Warn("live tv: team logos", "league", lg.name, "err", err)
		return
	}
	t.byName[lg.name] = names
}

func (t *teams) download(lg league) (map[string]Team, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://site.api.espn.com/apis/site/v2/sports/"+lg.path+"/teams?limit=1000", nil)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ESPN said %s", resp.Status)
	}
	var body struct {
		Sports []struct {
			Leagues []struct {
				Teams []struct {
					Team struct {
						DisplayName      string `json:"displayName"`
						ShortDisplayName string `json:"shortDisplayName"`
						Name             string `json:"name"`
						Nickname         string `json:"nickname"`
						Location         string `json:"location"`
						Abbreviation     string `json:"abbreviation"`
						Color            string `json:"color"`
						Logos            []struct {
							Href string `json:"href"`
						} `json:"logos"`
					} `json:"team"`
				} `json:"teams"`
			} `json:"leagues"`
		} `json:"sports"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := map[string]Team{}
	for _, sp := range body.Sports {
		for _, l := range sp.Leagues {
			for _, e := range l.Teams {
				x := e.Team
				tm := Team{Name: x.DisplayName, Short: x.ShortDisplayName, Color: x.Color}
				if len(x.Logos) > 0 {
					tm.Logo = x.Logos[0].Href
				}
				// "Green Bay Packers", "Packers", "Green Bay", "GB": guides use all of them.
				for _, n := range []string{x.DisplayName, x.ShortDisplayName, x.Name, x.Nickname, x.Location, x.Location + " " + x.Name, x.Abbreviation} {
					if k := teamKey(n); k != "" {
						if _, taken := out[k]; !taken {
							out[k] = tm
						}
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no teams")
	}
	return out, nil
}

func teamKey(s string) string { return strings.Join(searchWords(s), " ") }

var (
	// "NFL Football : Carolina Panthers at Green Bay Packers", "Arsenal v Chelsea".
	versus = regexp.MustCompile(`^(.+?)\s+(?:at|@|vs\.?|v\.?)\s+(.+)$`)
	// "Next Event Wizards at Cavaliers on 2026-10-28 07:00 PM -0400": event
	// channels fill the time before a game with this.
	nextEvent = regexp.MustCompile(`^Next Event (.+) on (\d{4}-\d{2}-\d{2} \d{2}:\d{2} [AP]M [+-]\d{4})$`)
)

// matchup reads the game a programme on channel [ch] shows, if it's one.
// [sporty] says the channel shows sport, so a title like "Red vs Blue" on
// it is taken as a game even when no league or team is recognised.
func (s *Service) matchup(title, ch string, sporty bool) *Matchup {
	prefix, game := "", title
	if i := strings.LastIndex(title, ":"); i >= 0 {
		prefix, game = title[:i], title[i+1:]
	}
	if i := strings.Index(game, ";"); i >= 0 {
		game = game[:i]
	}
	m := versus.FindStringSubmatch(strings.TrimSpace(game))
	if m == nil {
		return nil
	}
	away, home := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	if away == "" || home == "" || len(away) > 40 || len(home) > 40 {
		return nil
	}

	hint := " " + teamKey(prefix+" "+ch) + " "
	var named []league
	for _, lg := range leagues {
		for _, w := range lg.words {
			if strings.Contains(hint, " "+w+" ") {
				named = append(named, lg)
				break
			}
		}
	}
	try := named
	if len(try) == 0 {
		for _, lg := range leagues {
			if lg.pro {
				try = append(try, lg)
			}
		}
	}
	for _, lg := range try {
		byName := s.teams.lookup(lg)
		a, okA := byName[teamKey(away)]
		h, okH := byName[teamKey(home)]
		// Both teams in one league, or the league named and one of them found.
		if okA && okH || len(named) > 0 && (okA || okH) {
			if !okA {
				a = Team{Name: away}
			}
			if !okH {
				h = Team{Name: home}
			}
			return &Matchup{League: lg.name, Away: a, Home: h}
		}
	}
	if len(named) > 0 || sporty {
		mu := &Matchup{Away: Team{Name: away}, Home: Team{Name: home}}
		if len(named) > 0 {
			mu.League = named[0].name
		}
		return mu
	}
	return nil
}
