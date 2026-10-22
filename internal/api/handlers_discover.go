package api

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// titleView is a TMDB title (recommendation, trending), linked to the
// library item when the server has it.
type titleView struct {
	TMDBID       int      `json:"tmdbId"`
	MediaType    string   `json:"mediaType"` // "movie" or "show"
	Title        string   `json:"title"`
	Year         int      `json:"year,omitempty"`
	Overview     string   `json:"overview,omitempty"`
	PosterPath   string   `json:"posterPath,omitempty"`
	BackdropPath string   `json:"backdropPath,omitempty"`
	Rating       float64  `json:"rating,omitempty"`
	Item         *db.Item `json:"item,omitempty"`
	// Watched: the user watched it elsewhere (a Simkl import) and the
	// server doesn't have it. Titles it has carry Item.Progress instead.
	Watched bool `json:"watched,omitempty"`
}

func viewOf(t tmdb.Title, mediaType string) titleView {
	if mediaType == "" {
		mediaType = t.MediaType
	}
	if mediaType == "tv" {
		mediaType = "show"
	}
	return titleView{TMDBID: t.ID, MediaType: mediaType, Title: t.DisplayTitle(), Year: t.Year(), Overview: t.Overview,
		PosterPath: t.PosterPath, BackdropPath: t.BackdropPath, Rating: t.VoteAverage}
}

// linkLibrary fills Item on titles the user can access, then orders in-library
// titles first when inLibraryFirst is set.
func (s *Server) linkLibrary(ctx context.Context, r *http.Request, views []titleView, inLibraryFirst bool) []titleView {
	_, ids, err := s.visibleLibraries(r)
	if err != nil || len(views) == 0 {
		return views
	}
	byKind := map[string][]int{}
	for _, v := range views {
		byKind[v.MediaType] = append(byKind[v.MediaType], v.TMDBID)
	}
	streamLibs, _ := s.DB.LibraryIDsBySource(ctx, "stream")
	found := map[string]*db.Item{}
	for kind, tids := range byKind {
		items, _, err := s.DB.QueryItems(ctx, db.ItemQuery{LibraryIDs: ids, Kind: kind, TMDBIDs: tids, UserID: principal(r).User.ID, Limit: 200})
		if err != nil {
			continue
		}
		for _, it := range items {
			key := kind + ":" + strconv.Itoa(it.TMDBID)
			// A title in a real library beats its streamed copy.
			if old := found[key]; old != nil && !slices.Contains(streamLibs, old.LibraryID) {
				continue
			}
			found[key] = it
		}
	}
	elsewhere := map[string]map[int]bool{}
	for kind, tids := range byKind {
		elsewhere[kind], _ = s.DB.ExternalWatched(ctx, principal(r).User.ID, kind, tids)
	}
	var in, out []titleView
	for _, v := range views {
		v.Item = found[v.MediaType+":"+strconv.Itoa(v.TMDBID)]
		v.Watched = v.Item == nil && elsewhere[v.MediaType][v.TMDBID]
		if v.Item != nil || !inLibraryFirst {
			in = append(in, v)
		} else {
			out = append(out, v)
		}
	}
	return append(in, out...)
}

type castView struct {
	// ID is the TMDB person, for their page.
	ID          int    `json:"id,omitempty"`
	Name        string `json:"name"`
	Character   string `json:"character,omitempty"`
	ProfilePath string `json:"profilePath,omitempty"`
}

type ratingsView struct {
	TMDB           float64 `json:"tmdb,omitempty"`
	TMDBVotes      int     `json:"tmdbVotes,omitempty"`
	IMDb           float64 `json:"imdb,omitempty"`
	IMDbVotes      int     `json:"imdbVotes,omitempty"`
	RottenTomatoes int     `json:"rottenTomatoes,omitempty"`
	Metacritic     int     `json:"metacritic,omitempty"`
}

type extrasView struct {
	Ratings ratingsView `json:"ratings"`
	// OMDb says whether IMDb/Rotten Tomatoes/Metacritic ratings are
	// configured (an admin can add a key in Settings).
	OMDb         bool        `json:"omdb"`
	TMDB         bool        `json:"tmdb"`
	Cast         []castView  `json:"cast"`
	Trailer      *trailer    `json:"trailer,omitempty"`
	MoreLikeThis []titleView `json:"moreLikeThis"`
}

type trailer struct {
	YouTubeKey string `json:"youtubeKey"`
	Name       string `json:"name"`
}

// handleExtras returns ratings, cast, trailer and "more like this" for a
// movie or show (seasons and episodes use their show).
func (s *Server) handleExtras(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if (it.Kind == "season" || it.Kind == "episode") && it.ShowID != "" {
		if sh, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
			it = sh
		}
	}
	out := extrasView{Ratings: ratingsView{TMDB: it.Rating}, OMDb: s.OMDb.Enabled(), TMDB: s.TMDB.Enabled(),
		Cast: []castView{}, MoreLikeThis: []titleView{}}
	imdbID := it.IMDBID

	if s.TMDB.Enabled() && it.TMDBID > 0 {
		var ex *tmdb.Extras
		var err error
		mediaType := "movie"
		if it.Kind == "show" {
			mediaType = "show"
			ex, err = s.TMDB.ShowExtras(ctx, it.TMDBID)
		} else {
			ex, err = s.TMDB.MovieExtras(ctx, it.TMDBID)
		}
		if err != nil {
			slog.Debug("tmdb extras", "item", it.ID, "err", err)
		} else {
			if ex.VoteAverage > 0 {
				out.Ratings.TMDB, out.Ratings.TMDBVotes = ex.VoteAverage, ex.VoteCount
			}
			if imdbID == "" {
				imdbID = ex.ExternalIDs.IMDBID
			}
			for i, c := range ex.Credits.Cast {
				if i >= 20 {
					break
				}
				out.Cast = append(out.Cast, castView{ID: c.ID, Name: c.Name, Character: c.Character, ProfilePath: c.ProfilePath})
			}
			if t := ex.Trailer(); t != nil {
				out.Trailer = &trailer{YouTubeKey: t.Key, Name: t.Name}
			}
			seen := map[int]bool{it.TMDBID: true}
			var views []titleView
			for _, list := range [][]tmdb.Title{ex.Recommendations.Results, ex.Similar.Results} {
				for _, t := range list {
					if !seen[t.ID] && t.PosterPath != "" {
						seen[t.ID] = true
						views = append(views, viewOf(t, mediaType))
					}
				}
			}
			if len(views) > 24 {
				views = views[:24]
			}
			out.MoreLikeThis = nonNil(s.linkLibrary(ctx, r, views, true))
		}
	}
	if s.OMDb.Enabled() && imdbID != "" {
		if rt, err := s.OMDb.ByIMDB(ctx, imdbID); err == nil {
			out.Ratings.IMDb, out.Ratings.IMDbVotes = rt.IMDb, rt.IMDbVotes
			out.Ratings.RottenTomatoes, out.Ratings.Metacritic = rt.RottenTomatoes, rt.Metacritic
		} else {
			slog.Debug("omdb", "imdb", imdbID, "err", err)
		}
	}
	writeJSON(w, 200, out)
}

// handleTrending lists this week's TMDB trending titles, linked to the
// library. Titles the server doesn't have are included and marked.
func (s *Server) handleTrending(w http.ResponseWriter, r *http.Request) {
	if !s.TMDB.Enabled() {
		writeJSON(w, 200, map[string]any{"enabled": false, "items": []titleView{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ts, err := s.TMDB.Trending(ctx)
	if err != nil {
		writeErr(w, 502, "trending unavailable: "+err.Error())
		return
	}
	views := make([]titleView, 0, len(ts))
	for _, t := range ts {
		views = append(views, viewOf(t, ""))
	}
	// TMDB's order, so the rank shown is the real trending rank.
	writeJSON(w, 200, map[string]any{"enabled": true, "items": s.linkLibrary(ctx, r, views, false)})
}

// homeRows are the library-only rows on the home page beyond continue
// watching and recently added.
func (s *Server) homeRows(ctx context.Context, userID string, ids []string) (topRated []*db.Item, genres []genreRow) {
	if len(ids) == 0 {
		return []*db.Item{}, []genreRow{}
	}
	topRated, _, _ = s.DB.QueryItems(ctx, db.ItemQuery{LibraryIDs: ids, Kind: "movie,show", Sort: "rating", MinRating: 7, Limit: 20, UserID: userID})
	names, _ := s.DB.TopGenres(ctx, ids, 4)
	for _, g := range names {
		items, _, _ := s.DB.QueryItems(ctx, db.ItemQuery{LibraryIDs: ids, Kind: "movie,show", Genre: g, Sort: "rating", Limit: 20, UserID: userID})
		if len(items) >= 4 {
			genres = append(genres, genreRow{Genre: g, Items: items})
		}
	}
	return nonNil(topRated), nonNil(genres)
}

type genreRow struct {
	Genre string     `json:"genre"`
	Items []*db.Item `json:"items"`
}
