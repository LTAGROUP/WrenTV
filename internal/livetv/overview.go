package livetv

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tile is a programme on a channel, for the overview's shelves.
type Tile struct {
	ChannelID string   `json:"channelId"`
	Program   *Program `json:"program,omitempty"`
	// Matchup is the game the programme shows, drawn with the teams' logos.
	Matchup *Matchup `json:"matchup,omitempty"`
	// Art is a picture for the programme: the guide's, or one found for it.
	Art string `json:"art,omitempty"`
}

// Shelf is a row of the overview.
type Shelf struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Tiles []Tile `json:"tiles"`
}

const shelfMax = 24

// Overview is Live TV at a glance, like YouTube TV's home: games on now
// and coming up, the viewer's channels, then what's on by kind. A
// programme on several channels shows once, on the one the viewer is
// likeliest to want ([favorites], then [recents], then lineup order).
func (s *Service) Overview(favorites, recents, shows []string) []Shelf {
	t := time.Now().Unix()
	s.mu.RLock()
	defer s.mu.RUnlock()

	rank := make(map[string]int, len(s.visible))
	for i, c := range s.visible {
		rank[c.ID] = len(favorites) + len(recents) + i
	}
	for i, id := range recents {
		if _, ok := rank[id]; ok {
			rank[id] = len(favorites) + i
		}
	}
	for i, id := range favorites {
		if _, ok := rank[id]; ok {
			rank[id] = i
		}
	}
	type entry struct {
		c     *Channel
		genre string
		now   *Program
	}
	lineup := make([]entry, 0, len(s.visible))
	for _, c := range s.visible {
		now, _ := s.guide.NowNext(c.ID, t)
		lineup = append(lineup, entry{c, genreOf(c, now), now})
	}
	sort.SliceStable(lineup, func(i, j int) bool { return rank[lineup[i].c.ID] < rank[lineup[j].c.ID] })

	seen := map[string]bool{} // programmes and games already on a shelf
	tile := func(c *Channel, p Program, m *Matchup) Tile {
		return Tile{ChannelID: c.ID, Program: &p, Matchup: m, Art: p.Icon}
	}
	once := func(key string) bool {
		if seen[key] {
			return false
		}
		seen[key] = true
		return true
	}
	gameKey := func(m *Matchup) string { return "game:" + teamKey(m.Away.Name) + "@" + teamKey(m.Home.Name) }
	titleKey := func(p *Program) string { return "title:" + teamKey(p.Title) }
	maybeGame := func(p *Program, c *Channel, genre string) *Matchup {
		if strings.HasSuffix(p.Title, "24/7") || !strings.Contains(p.Title, " at ") && !strings.Contains(p.Title, " vs") &&
			!strings.Contains(p.Title, " v ") && !strings.Contains(p.Title, " v. ") && !strings.Contains(p.Title, "@") {
			return nil
		}
		return s.matchup(p.Title, c.Name, genre == "sports")
	}

	// The shows the viewer follows: on now, then their next airings today.
	follow := map[string]bool{}
	for _, title := range shows {
		follow[teamKey(title)] = true
	}
	var mine []Tile
	if len(follow) > 0 {
		for _, e := range lineup {
			for _, p := range s.guide[e.c.ID] {
				if p.End > t && p.Start < t+24*3600 && follow[teamKey(p.Title)] && once(titleKey(&p)+"@"+strconv.FormatInt(p.Start, 10)) {
					mine = append(mine, tile(e.c, p, nil))
				}
			}
		}
		sort.SliceStable(mine, func(i, j int) bool { return max(mine[i].Program.Start, t) < max(mine[j].Program.Start, t) })
	}

	// Games on now, ones with logos first, and other live sport.
	var live []Tile
	for _, e := range lineup {
		if e.now == nil {
			continue
		}
		m := maybeGame(e.now, e.c, e.genre)
		if m == nil && !(e.genre == "sports" && e.now.Live) {
			continue
		}
		key := titleKey(e.now)
		if m != nil {
			key = gameKey(m)
		}
		if once(key) {
			live = append(live, tile(e.c, *e.now, m))
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return gameScore(live[i].Matchup) > gameScore(live[j].Matchup) })

	// Games later today and tomorrow, soonest first.
	var later []Tile
	for _, e := range lineup {
		if e.now != nil {
			if ev := nextEvent.FindStringSubmatch(e.now.Title); ev != nil {
				// An event channel's stand-in until its game starts.
				at, err := time.Parse("2006-01-02 03:04 PM -0700", ev[2])
				if m := s.matchup(ev[1], e.c.Name, true); err == nil && m != nil && at.Unix() > t-3600 && at.Unix() < t+36*3600 {
					if once(gameKey(m)) {
						later = append(later, tile(e.c, Program{Title: ev[1], Start: at.Unix(), End: at.Add(3 * time.Hour).Unix()}, m))
					}
				}
				continue
			}
		}
		for _, p := range s.guide[e.c.ID] {
			if p.Start <= t || p.Start > t+24*3600 {
				continue
			}
			if m := maybeGame(&p, e.c, e.genre); m != nil && (m.Away.Logo != "" || m.League != "") && once(gameKey(m)) {
				later = append(later, tile(e.c, p, m))
			}
		}
	}
	sort.SliceStable(later, func(i, j int) bool { return later[i].Program.Start < later[j].Program.Start })

	channels := func(ids []string) []Tile {
		var out []Tile
		for _, id := range ids {
			// Only channels still in the lineup.
			if _, visible := rank[id]; !visible {
				continue
			}
			tl := Tile{ChannelID: id}
			if now, _ := s.guide.NowNext(id, t); now != nil {
				tl.Program, tl.Art = now, now.Icon
			}
			out = append(out, tl)
		}
		return out
	}
	// What's on now on channels of a kind, live ones first.
	onNow := func(want func(e entry) bool) []Tile {
		var out []Tile
		for _, e := range lineup {
			if e.now != nil && want(e) && once(titleKey(e.now)) {
				out = append(out, tile(e.c, *e.now, nil))
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Program.Live && !out[j].Program.Live })
		return out
	}
	news := onNow(func(e entry) bool { return e.genre == "news" })
	fresh := onNow(func(e entry) bool { return e.now.New && e.genre != "sports" && e.genre != "news" })
	movies := onNow(func(e entry) bool { return e.genre == "movies" })
	kids := onNow(func(e entry) bool { return e.genre == "kids" })
	var soon []Tile
	for _, e := range lineup {
		if e.genre != "entertainment" && e.genre != "movies" {
			continue
		}
		_, next := s.guide.NowNext(e.c.ID, t)
		if next != nil && next.Start > t && next.Start <= t+30*60 && once(titleKey(next)) {
			soon = append(soon, tile(e.c, *next, nil))
		}
	}

	var out []Shelf
	for _, sh := range []Shelf{
		{"shows", "Your shows", mine},
		{"live-sports", "Live sports", live},
		{"recent", "Watched recently", channels(recents)},
		{"favorites", "Your favorites", channels(favorites)},
		{"upcoming-sports", "Coming up in sports", later},
		{"new", "New episodes", fresh},
		{"movies", "Movies on now", movies},
		{"news", "News", news},
		{"kids", "Kids", kids},
		{"soon", "Starting soon", soon},
	} {
		if len(sh.Tiles) == 0 {
			continue
		}
		if len(sh.Tiles) > shelfMax {
			sh.Tiles = sh.Tiles[:shelfMax]
		}
		out = append(out, sh)
	}
	return out
}

// gameScore puts games with both logos ahead of ones with names alone,
// and those ahead of live sport that isn't a game.
func gameScore(m *Matchup) int {
	switch {
	case m == nil:
		return 0
	case m.Away.Logo != "" && m.Home.Logo != "":
		return 3
	case m.League != "":
		return 2
	}
	return 1
}
