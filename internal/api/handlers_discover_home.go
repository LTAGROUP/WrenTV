package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// discoverRow is one shelf of the streaming home page.
type discoverRow struct {
	ID    string      `json:"id"`
	Title string      `json:"title"`
	Items []titleView `json:"items"`
}

// discoverSource fetches one row's titles from TMDB.
type discoverSource struct {
	id, title string
	fetch     func(ctx context.Context) ([]tmdb.Title, error)
}

// handleDiscoverHome is the home page when the server streams: the whole
// TMDB catalog in rows, not just what's in the libraries. Titles the server
// has are linked to their items; the rest open through /api/streaming/titles.
func (s *Server) handleDiscoverHome(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	_, ids, err := s.visibleLibraries(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out := map[string]any{
		"enabled":          s.TMDB.Enabled(),
		"streaming":        s.Streaming.Active() && s.limitsFor(p.User).CanStream,
		"continueWatching": s.continueWatching(ctx, r, ids),
		"recentlyWatched":  s.recentlyWatched(ctx, p.User.ID, ids, 20),
		"hero":             []titleView{},
		"rows":             []discoverRow{},
	}
	if !s.TMDB.Enabled() {
		writeJSON(w, 200, out)
		return
	}

	chart := func(name, mediaType string) func(context.Context) ([]tmdb.Title, error) {
		return func(ctx context.Context) ([]tmdb.Title, error) { return s.TMDB.Chart(ctx, name, mediaType, 1) }
	}
	genre := func(mediaType string, id int) func(context.Context) ([]tmdb.Title, error) {
		return func(ctx context.Context) ([]tmdb.Title, error) { return s.TMDB.ByGenre(ctx, mediaType, id) }
	}
	sources := []discoverSource{{"trending", "Trending Now", s.TMDB.Trending}}
	// "Because you watched": recommendations for the user's latest titles.
	for _, it := range s.recentTitles(ctx, p.User.ID, ids, 3) {
		it := it
		sources = append(sources, discoverSource{"because-" + it.ID, "Because you watched " + it.Title, func(ctx context.Context) ([]tmdb.Title, error) {
			var ex *tmdb.Extras
			var err error
			if it.Kind == "show" {
				ex, err = s.TMDB.ShowExtras(ctx, it.TMDBID)
			} else {
				ex, err = s.TMDB.MovieExtras(ctx, it.TMDBID)
			}
			if err != nil {
				return nil, err
			}
			mt := map[bool]string{true: "tv", false: "movie"}[it.Kind == "show"]
			list := append(ex.Recommendations.Results, ex.Similar.Results...)
			for i := range list {
				list[i].MediaType = mt
			}
			return list, nil
		}})
	}
	sources = append(sources,
		discoverSource{"popular-movies", "Popular Movies", chart("popular", "movie")},
		discoverSource{"popular-shows", "Popular Shows", chart("popular", "tv")},
		discoverSource{"now-playing", "New Releases", chart("now_playing", "movie")},
		discoverSource{"airing", "New Episodes", chart("airing", "tv")},
		discoverSource{"top-movies", "Top Rated Movies", chart("top_rated", "movie")},
		discoverSource{"top-shows", "Top Rated Shows", chart("top_rated", "tv")},
		discoverSource{"action", "Action & Adventure", genre("movie", 28)},
		discoverSource{"comedy", "Comedies", genre("movie", 35)},
		discoverSource{"scifi", "Sci-Fi & Fantasy", genre("tv", 10765)},
		discoverSource{"crime", "Crime Shows", genre("tv", 80)},
		discoverSource{"horror", "Horror", genre("movie", 27)},
		discoverSource{"animation", "Animation", genre("movie", 16)},
		discoverSource{"documentary", "Documentaries", genre("movie", 99)},
	)

	rows := make([]discoverRow, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ts, err := src.fetch(ctx)
			if err != nil {
				slog.Debug("discover row", "row", src.id, "err", err)
				return
			}
			seen := map[string]bool{}
			views := []titleView{}
			for _, t := range ts {
				v := viewOf(t, "")
				key := fmt.Sprint(v.MediaType, v.TMDBID)
				if t.PosterPath == "" || seen[key] || (v.MediaType != "movie" && v.MediaType != "show") {
					continue
				}
				seen[key] = true
				views = append(views, v)
				if len(views) == 20 {
					break
				}
			}
			rows[i] = discoverRow{ID: src.id, Title: src.title, Items: s.linkLibrary(ctx, r, views, false)}
		}()
	}
	wg.Wait()

	kept := []discoverRow{}
	for _, row := range rows {
		if len(row.Items) >= 4 {
			kept = append(kept, row)
		}
	}
	out["rows"] = kept
	// The hero rotates through this week's trending titles with artwork.
	if len(kept) > 0 && kept[0].ID == "trending" {
		hero := []titleView{}
		for _, v := range kept[0].Items {
			if v.BackdropPath != "" && v.Overview != "" {
				hero = append(hero, v)
			}
			if len(hero) == 6 {
				break
			}
		}
		out["hero"] = hero
	}
	writeJSON(w, 200, out)
}

// recentlyWatched is the movies and shows the user played most recently,
// newest first.
func (s *Server) recentlyWatched(ctx context.Context, userID string, ids []string, limit int) []*db.Item {
	out := []*db.Item{}
	if len(ids) == 0 {
		return out
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT CASE WHEN i.kind='episode' THEN i.show_id ELSE i.id END t, MAX(w.updated_at) m
		FROM watch_state w JOIN items i ON i.id=w.item_id
		WHERE w.user_id=? AND i.kind IN ('movie','episode') AND (w.watched=1 OR w.position>0)
		GROUP BY t ORDER BY m DESC LIMIT ?`, userID, limit*2)
	if err != nil {
		return out
	}
	var titleIDs []string
	for rows.Next() {
		var id string
		var m int64
		if rows.Scan(&id, &m) == nil && id != "" {
			titleIDs = append(titleIDs, id)
		}
	}
	rows.Close()
	visible := map[string]bool{}
	for _, id := range ids {
		visible[id] = true
	}
	for _, id := range titleIDs {
		if it, err := s.DB.GetItem(ctx, id); err == nil && visible[it.LibraryID] {
			out = append(out, it)
			if len(out) == limit {
				break
			}
		}
	}
	return out
}

// recentTitles is the latest recently watched titles that TMDB knows.
func (s *Server) recentTitles(ctx context.Context, userID string, ids []string, n int) []*db.Item {
	var out []*db.Item
	for _, it := range s.recentlyWatched(ctx, userID, ids, n*2) {
		if it.TMDBID > 0 {
			out = append(out, it)
			if len(out) == n {
				break
			}
		}
	}
	return out
}
