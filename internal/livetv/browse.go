package livetv

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// ChannelView is a channel with what's on.
type ChannelView struct {
	Channel
	Now  *Program `json:"now,omitempty"`
	Next *Program `json:"next,omitempty"`
	// Genre is the kind of channel ("sports", "news", "movies", "kids",
	// "music" or "entertainment"), for the guide's filters.
	Genre string `json:"genre,omitempty"`
}

// Channels lists the visible channels with what's on now and next.
func (s *Service) Channels() []ChannelView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := time.Now().Unix()
	out := make([]ChannelView, 0, len(s.visible))
	for _, c := range s.visible {
		out = append(out, s.view(c, t))
	}
	return out
}

func (s *Service) view(c *Channel, t int64) ChannelView {
	v := ChannelView{Channel: *c}
	v.Now, v.Next = s.guide.NowNext(c.ID, t)
	v.Genre = genreOf(c, v.Now)
	return v
}

// GuideView is a channel's programmes in a window.
type GuideView struct {
	ID       string    `json:"id"`
	Programs []Program `json:"programs"`
}

// Schedule lists each visible channel's programmes from [from] (Unix
// seconds; now when 0) for [hours].
func (s *Service) Schedule(from int64, hours int) []GuideView {
	if from <= 0 {
		from = time.Now().Unix()
	}
	to := from + int64(hours)*3600
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]GuideView, 0, len(s.visible))
	for _, c := range s.visible {
		ps := []Program{}
		for _, p := range s.guide[c.ID] {
			if p.End > from && p.Start < to {
				ps = append(ps, p)
			}
		}
		out = append(out, GuideView{ID: c.ID, Programs: ps})
	}
	return out
}

// Airing is a programme found by a search, on its channel.
type Airing struct {
	ChannelID string  `json:"channelId"`
	Program   Program `json:"program"`
}

// SearchResult is the channels and programmes matching a search.
type SearchResult struct {
	Channels []ChannelView `json:"channels"`
	Airings  []Airing      `json:"airings"`
}

// Search finds channels by name, number or group, and programmes not yet
// over by title or episode: on now first, then soonest.
func (s *Service) Search(q string, limit int) SearchResult {
	out := SearchResult{Channels: []ChannelView{}, Airings: []Airing{}}
	q = strings.TrimSpace(q)
	if q == "" {
		return out
	}
	words := searchWords(q)
	match := func(fields ...string) bool {
		hay := strings.Join(searchWords(strings.Join(fields, " ")), " ")
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return false
			}
		}
		return true
	}
	t := time.Now().Unix()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.visible {
		if c.Number == q || match(c.Name, c.Group) {
			out.Channels = append(out.Channels, s.view(c, t))
		}
		for _, p := range s.guide[c.ID] {
			if p.End > t && match(p.Title, p.Episode) {
				out.Airings = append(out.Airings, Airing{ChannelID: c.ID, Program: p})
			}
		}
	}
	sort.SliceStable(out.Airings, func(i, j int) bool {
		a, b := out.Airings[i].Program, out.Airings[j].Program
		return max(a.Start, t) < max(b.Start, t)
	})
	if len(out.Channels) > limit {
		out.Channels = out.Channels[:limit]
	}
	if len(out.Airings) > limit {
		out.Airings = out.Airings[:limit]
	}
	return out
}

func searchWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// genreWords are the words that say what kind of channel it is, in a
// channel's group, its name, or what's on it.
var genreWords = []struct {
	genre string
	words []string
}{
	{"sports", []string{"sport", "sports", "espn", "fs1", "fs2", "nfl", "nba", "mlb", "nhl", "golf", "tennis", "football", "soccer",
		"racing", "f1", "ufc", "wwe", "boxing", "bein", "dazn", "tsn", "sportsnet", "cricket", "rugby", "olympics", "sec", "acc", "big ten"}},
	{"news", []string{"news", "cnn", "msnbc", "cnbc", "fox news", "bbc news", "sky news", "bloomberg", "newsmax", "weather", "c span", "cspan",
		"al jazeera", "euronews", "politics", "business"}},
	{"kids", []string{"kids", "kid", "children", "nick", "nickelodeon", "nick jr", "disney junior", "disney jr", "disney channel", "cartoon",
		"boomerang", "pbs kids", "baby", "animation", "family"}},
	{"movies", []string{"movie", "movies", "film", "films", "cinema", "cinemax", "hbo", "showtime", "starz", "tcm", "amc", "fx movie",
		"hallmark", "lifetime movie", "epix", "mgm", "sundance", "ifc"}},
	{"music", []string{"music", "mtv", "vh1", "cmt", "bet jams", "radio", "concert"}},
}

// genreOf is the kind of channel c is: from its group, else its name, else
// what's on it now.
func genreOf(c *Channel, now *Program) string {
	find := func(text string) string {
		hay := " " + strings.Join(searchWords(text), " ") + " "
		for _, g := range genreWords {
			for _, w := range g.words {
				if strings.Contains(hay, " "+w+" ") {
					return g.genre
				}
			}
		}
		return ""
	}
	if g := find(c.Group); g != "" {
		return g
	}
	if g := find(c.Name); g != "" {
		return g
	}
	if now != nil {
		if g := find(strings.Join(now.Categories, " ")); g != "" {
			return g
		}
	}
	return "entertainment"
}
