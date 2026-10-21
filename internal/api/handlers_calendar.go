package api

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
)

// calendarEpisode is one episode on the calendar, with the show it's from.
type calendarEpisode struct {
	Date          string   `json:"date"` // YYYY-MM-DD
	Show          *db.Item `json:"show"`
	SeasonNumber  int      `json:"seasonNumber"`
	EpisodeNumber int      `json:"episodeNumber"`
	Title         string   `json:"title"`
	Overview      string   `json:"overview,omitempty"`
	StillPath     string   `json:"stillPath,omitempty"`
	// ItemID is the episode in the library, when it's there.
	ItemID  string `json:"itemId,omitempty"`
	Watched bool   `json:"watched"`
	// Watching: the caller has watched some of this show. UpNext: this is
	// the episode they're up to (as on Continue Watching).
	Watching bool `json:"watching"`
	UpNext   bool `json:"upNext"`
}

// handleCalendar lists episodes airing between ?from and ?to (YYYY-MM-DD;
// a week back to a month ahead by default) of shows in the caller's
// libraries that are still airing, and of shows they're watching.
func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	today := time.Now()
	day := func(key string, def time.Time) time.Time {
		if t, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get(key), time.Local); err == nil {
			return t
		}
		return def
	}
	from := day("from", today.AddDate(0, 0, -7))
	to := day("to", today.AddDate(0, 0, 30))
	if to.Before(from) || to.Sub(from) > 120*24*time.Hour {
		writeErr(w, 400, "from must be before to, at most 120 days apart")
		return
	}
	fromS, toS := from.Format(time.DateOnly), to.Format(time.DateOnly)
	out := map[string]any{"from": fromS, "to": toS, "episodes": []calendarEpisode{}}
	if !s.TMDB.Enabled() {
		writeJSON(w, 200, out)
		return
	}
	_, ids, err := s.visibleLibraries(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid := principal(r).User.ID
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	// A show that's aired in the last year and a half may still be running.
	shows, watching, err := s.DB.CalendarShows(ctx, uid, ids, from.AddDate(0, -18, 0).Format(time.DateOnly))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	var mu sync.Mutex
	var eps []calendarEpisode
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, sh := range shows {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			found := s.showCalendar(ctx, uid, sh, watching[sh.ID], fromS, toS)
			mu.Lock()
			eps = append(eps, found...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.SliceStable(eps, func(i, j int) bool {
		a, b := eps[i], eps[j]
		switch {
		case a.Date != b.Date:
			return a.Date < b.Date
		case a.Show.SortTitle != b.Show.SortTitle:
			return a.Show.SortTitle < b.Show.SortTitle
		case a.SeasonNumber != b.SeasonNumber:
			return a.SeasonNumber < b.SeasonNumber
		}
		return a.EpisodeNumber < b.EpisodeNumber
	})
	if eps != nil {
		out["episodes"] = eps
	}
	writeJSON(w, 200, out)
}

// showCalendar is one show's episodes airing from..to (inclusive).
func (s *Server) showCalendar(ctx context.Context, uid string, sh *db.Item, watching bool, from, to string) []calendarEpisode {
	a, err := s.TMDB.Airing(ctx, sh.TMDBID)
	if err != nil {
		return nil
	}
	// Over before the window opens: nothing to show.
	if a.NextEpisode == nil && a.LastAirDate != "" && a.LastAirDate < from {
		return nil
	}
	seasons := map[int]bool{}
	if a.LastEpisode != nil {
		seasons[a.LastEpisode.SeasonNumber] = true
	}
	if a.NextEpisode != nil {
		seasons[a.NextEpisode.SeasonNumber] = true
	}

	// The library's episodes, for what's there and what's been watched.
	have := map[[2]int]*db.Item{}
	if items, _, err := s.DB.QueryItems(ctx, db.ItemQuery{ShowID: sh.ID, Kind: "episode", UserID: uid}); err == nil {
		for _, e := range items {
			have[[2]int{e.SeasonNumber, e.EpisodeNumber}] = e
		}
	}
	var next [2]int
	if watching {
		if e, err := s.DB.NextEpisode(ctx, uid, sh.ID); err == nil {
			next = [2]int{e.SeasonNumber, e.EpisodeNumber}
		}
	}

	var out []calendarEpisode
	for n := range seasons {
		if n <= 0 {
			continue
		}
		season, err := s.TMDB.Season(ctx, sh.TMDBID, n)
		if err != nil {
			continue
		}
		for _, e := range season.Episodes {
			if e.AirDate < from || e.AirDate > to {
				continue
			}
			key := [2]int{e.SeasonNumber, e.EpisodeNumber}
			ce := calendarEpisode{Date: e.AirDate, Show: sh, SeasonNumber: e.SeasonNumber, EpisodeNumber: e.EpisodeNumber,
				Title: e.Name, Overview: e.Overview, StillPath: e.StillPath, Watching: watching, UpNext: watching && key == next}
			if it := have[key]; it != nil {
				ce.ItemID = it.ID
				ce.Watched = it.Progress != nil && it.Progress.Watched
				ce.StillPath = firstNonEmpty(it.StillPath, ce.StillPath)
			}
			out = append(out, ce)
		}
	}
	return out
}
