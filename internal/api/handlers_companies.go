package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// studioRow is one shelf on a studio's page: its first page of titles and
// how to ask for more (/api/discover/browse with the same type, sort and
// company).
type studioRow struct {
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	MediaType  string      `json:"mediaType"` // "movie" or "show"
	Sort       string      `json:"sort"`
	Items      []titleView `json:"items"`
	TotalPages int         `json:"totalPages"`
}

type companyView struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	LogoPath      string `json:"logoPath,omitempty"`
	Description   string `json:"description,omitempty"`
	Headquarters  string `json:"headquarters,omitempty"`
	OriginCountry string `json:"originCountry,omitempty"`
	// BackdropPath is borrowed from its most popular title.
	BackdropPath string      `json:"backdropPath,omitempty"`
	Rows         []studioRow `json:"rows"`
}

// studioShelves are a studio page's rows, in order.
var studioShelves = []struct{ id, title, mediaType, sort string }{
	{"movie-popular", "Popular Movies", "movie", "popular"},
	{"movie-top", "Top Rated Movies", "movie", "top_rated"},
	{"movie-new", "Recent Movies", "movie", "new"},
	{"tv-popular", "Popular Shows", "tv", "popular"},
	{"tv-top", "Top Rated Shows", "tv", "top_rated"},
}

// handleCompany returns a studio's details and its shelves of titles,
// linked to the library. Empty shelves are left out.
func (s *Server) handleCompany(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, 400, "bad company id")
		return
	}
	if !s.TMDB.Enabled() {
		writeErr(w, 404, "studio pages need TMDB")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	rows := make([]studioRow, len(studioShelves))
	var wg sync.WaitGroup
	for i, sh := range studioShelves {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ts, pages, err := s.TMDB.DiscoverWith(ctx, sh.mediaType, tmdb.DiscoverQuery{Sort: sh.sort, Companies: []int{id}, Page: 1})
			if err != nil {
				return
			}
			views := make([]titleView, 0, len(ts))
			for _, t := range ts {
				if t.PosterPath != "" {
					views = append(views, viewOf(t, ""))
				}
			}
			mt := "movie"
			if sh.mediaType == "tv" {
				mt = "show"
			}
			rows[i] = studioRow{ID: sh.id, Title: sh.title, MediaType: mt, Sort: sh.sort, Items: views, TotalPages: pages}
		}()
	}
	co, err := s.TMDB.Company(ctx, id)
	wg.Wait()
	if errors.Is(err, tmdb.ErrNotFound) {
		writeErr(w, 404, "studio not found")
		return
	}
	if err != nil {
		writeErr(w, 502, "studio unavailable: "+err.Error())
		return
	}
	out := companyView{ID: co.ID, Name: co.Name, LogoPath: co.LogoPath, Description: co.Description,
		Headquarters: co.Headquarters, OriginCountry: co.OriginCountry, Rows: []studioRow{}}
	for _, row := range rows {
		if len(row.Items) == 0 {
			continue
		}
		row.Items = s.linkLibrary(ctx, r, row.Items, false)
		if out.BackdropPath == "" {
			for _, t := range row.Items {
				if t.BackdropPath != "" {
					out.BackdropPath = t.BackdropPath
					break
				}
			}
		}
		out.Rows = append(out.Rows, row)
	}
	writeJSON(w, 200, out)
}
