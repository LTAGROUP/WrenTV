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
	// CollectionID is set on a collection's row, for "See all".
	CollectionID string `json:"collectionId,omitempty"`
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
		"watchlist":        s.watchlistRow(ctx, p.User.ID, ids),
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
	// Collections the admin put on the home screen go after the first
	// personal rows.
	if cols := s.homeCollectionRows(ctx, r); len(cols) > 0 {
		at := min(len(kept), 2)
		kept = append(kept[:at], append(cols, kept[at:]...)...)
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
// newest first. Like Continue Watching, a play only counts once it's past
// the user's Continue Watching threshold (or finished), so a title opened
// for a few seconds doesn't drive Because You Watched.
func (s *Server) recentlyWatched(ctx context.Context, userID string, ids []string, limit int) []*db.Item {
	out := []*db.Item{}
	if len(ids) == 0 {
		return out
	}
	prefs, _ := s.DB.GetPrefs(ctx, userID)
	rows, err := s.DB.QueryContext(ctx, `SELECT CASE WHEN i.kind='episode' THEN i.show_id ELSE i.id END t, MAX(w.updated_at) m
		FROM watch_state w JOIN items i ON i.id=w.item_id
		WHERE w.user_id=? AND i.kind IN ('movie','episode') AND (w.watched=1 OR w.position > ?)
		GROUP BY t ORDER BY m DESC LIMIT ?`, userID, prefs.ContinueAfterSeconds(), limit*2)
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

// handleDiscoverBrowse is the Movies and TV Shows pages when the server
// streams: TMDB's whole catalog, a page at a time, linked to the library.
// It also pages a studio's shelves (company).
func (s *Server) handleDiscoverBrowse(w http.ResponseWriter, r *http.Request) {
	if !s.TMDB.Enabled() {
		writeJSON(w, 200, map[string]any{"enabled": false, "items": []titleView{}, "genres": []tmdb.Genre{}, "page": 1, "totalPages": 0})
		return
	}
	mediaType := "movie"
	if t := r.URL.Query().Get("type"); t == "show" || t == "tv" {
		mediaType = "tv"
	}
	page := max(1, min(queryInt(r, "page", 1), 500))
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	q := tmdb.DiscoverQuery{Sort: r.URL.Query().Get("sort"), Page: page}
	if g := queryInt(r, "genre", 0); g > 0 {
		q.Genres = []int{g}
	}
	// A studio's shelf asks for its next page here.
	if c := queryInt(r, "company", 0); c > 0 {
		q.Companies = []int{c}
	}
	ts, pages, err := s.TMDB.DiscoverWith(ctx, mediaType, q)
	if err != nil {
		writeErr(w, 502, "TMDB unavailable: "+err.Error())
		return
	}
	views := make([]titleView, 0, len(ts))
	for _, t := range ts {
		if t.PosterPath != "" {
			views = append(views, viewOf(t, ""))
		}
	}
	genres, _ := s.TMDB.Genres(ctx, mediaType)
	writeJSON(w, 200, map[string]any{"enabled": true, "items": s.linkLibrary(ctx, r, views, false), "genres": nonNil(genres),
		"page": page, "totalPages": pages})
}

// homeCollectionRows are the collections promoted to the home screen, as
// rows of titles. A TMDB collection holds every title in it.
func (s *Server) homeCollectionRows(ctx context.Context, r *http.Request) []discoverRow {
	cs, err := s.DB.ListCollections(ctx)
	if err != nil {
		return nil
	}
	s.syncStaleCollections()
	var out []discoverRow
	now := time.Now()
	for _, c := range cs {
		if !c.ShowOnHome || !c.ActiveOn(now) || !visibleCollection(r, c) {
			continue
		}
		var views []titleView
		if c.Kind == "tmdb" {
			views = s.collectionTitles(ctx, r, c)
		} else {
			items, _, err := s.collectionItems(ctx, r, c, 24)
			if err != nil {
				continue
			}
			for _, it := range items {
				views = append(views, titleView{TMDBID: it.TMDBID, MediaType: it.Kind, Title: it.Title, Year: it.Year,
					PosterPath: it.PosterPath, BackdropPath: it.BackdropPath, Item: it})
			}
		}
		if len(views) > 24 {
			views = views[:24]
		}
		if len(views) > 0 {
			out = append(out, discoverRow{ID: "collection-" + c.ID, Title: c.Name, Items: views, CollectionID: c.ID})
		}
	}
	return out
}
