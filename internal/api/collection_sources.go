package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/simkl"
)

// Lists from outside TMDB (MDBList, Simkl) name their titles by TMDB ID;
// posters come from TMDB. Only the first maxExternal titles are kept.
const maxExternal = 100

// mdblistBase is where public MDBList lists are read (tests override it).
var mdblistBase = "https://mdblist.com"

var mdblistRef = regexp.MustCompile(`^(?:https?://(?:www\.)?mdblist\.com)?/?(?:lists/)?([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/?(?:json/?)?$`)

// mdblistPath turns "user/list" or a list's URL into its path.
func mdblistPath(ref string) (string, error) {
	m := mdblistRef.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", errors.New(`an MDBList list is "user/list" or its mdblist.com/lists/… link`)
	}
	return "/lists/" + m[1] + "/" + m[2], nil
}

// mdblistTitles reads a public MDBList list; no API key is needed.
func mdblistTitles(ctx context.Context, ref string) (name string, out []db.CollectionTitle, err error) {
	p, err := mdblistPath(ref)
	if err != nil {
		return "", nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mdblistBase+p+"/json", nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("MDBList: %s (is the list public?)", resp.Status)
	}
	var rows []struct {
		ID        int    `json:"id"`
		Title     string `json:"title"`
		MediaType string `json:"mediatype"`
		Year      int    `json:"release_year"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return "", nil, fmt.Errorf("MDBList: %w", err)
	}
	for _, r := range rows {
		mt := r.MediaType
		if mt == "tv" {
			mt = "show"
		}
		if r.ID > 0 && (mt == "movie" || mt == "show") {
			out = append(out, db.CollectionTitle{TMDBID: r.ID, MediaType: mt, Title: r.Title, Year: r.Year})
		}
	}
	// "user/top-watched-movies" → "Top Watched Movies"
	slug := p[strings.LastIndex(p, "/")+1:]
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(slug))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " "), out, nil
}

// simklTitles reads a Simkl trending chart ("trending/movies/week") or
// the linked account's watchlist ("watchlist/plantowatch").
func (s *Server) simklTitles(ctx context.Context, ref, userID string) (name string, out []db.CollectionTitle, err error) {
	parts := strings.Split(strings.Trim(ref, "/"), "/")
	add := func(m *simkl.Media, mt string) {
		if id, _ := strconv.Atoi(m.ID("tmdb")); id > 0 {
			out = append(out, db.CollectionTitle{TMDBID: id, MediaType: mt, Title: m.Title, Year: m.Year})
		}
	}
	switch {
	case len(parts) == 3 && parts[0] == "trending":
		kind, interval := parts[1], parts[2]
		if (kind != "movies" && kind != "tv" && kind != "anime") || (interval != "today" && interval != "week" && interval != "month") {
			return "", nil, errors.New("unknown Simkl chart")
		}
		ts, err := s.simkl().Trending(ctx, kind, interval)
		if err != nil {
			return "", nil, err
		}
		for _, t := range ts {
			mt := "show"
			if kind == "movies" || t.AnimeType == "movie" {
				mt = "movie"
			}
			add(&simkl.Media{Title: t.Title, IDs: t.IDs}, mt)
		}
		label := map[string]string{"movies": "Movies", "tv": "Shows", "anime": "Anime"}[kind]
		when := map[string]string{"today": "Today", "week": "This Week", "month": "This Month"}[interval]
		return "Trending " + label + " " + when, out, nil
	case len(parts) == 2 && parts[0] == "watchlist":
		if s.Tracking == nil || userID == "" {
			return "", nil, errors.New("link a Simkl account first (Account settings)")
		}
		l, err := s.Tracking.SimklWatchlist(ctx, userID, parts[1])
		if err != nil {
			return "", nil, err
		}
		for _, it := range l.Movies {
			if it.Movie != nil {
				add(it.Movie, "movie")
			}
		}
		for _, it := range append(l.Shows, l.Anime...) {
			switch {
			case it.Show != nil:
				add(it.Show, "show")
			case it.Movie != nil:
				add(it.Movie, "movie")
			}
		}
		label := map[string]string{"plantowatch": "Plan to Watch", "watching": "Watching", "completed": "Completed",
			"hold": "On Hold", "dropped": "Dropped"}[parts[1]]
		if label == "" {
			return "", nil, errors.New("unknown Simkl watchlist")
		}
		return "Simkl " + label, out, nil
	}
	return "", nil, errors.New(`a Simkl source is "trending/<movies|tv|anime>/<today|week|month>" or "watchlist/<status>"`)
}

func (s *Server) simkl() *simkl.Client {
	if s.Tracking != nil && s.Tracking.Simkl != nil {
		return s.Tracking.Simkl
	}
	return simkl.New(nil, "")
}

// withPosters fills each title's poster from TMDB, a few at a time.
func (s *Server) withPosters(ctx context.Context, ts []db.CollectionTitle) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i := range ts {
		t := &ts[i]
		if t.PosterPath != "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if t.MediaType == "movie" {
				if m, err := s.TMDB.Movie(ctx, t.TMDBID); err == nil {
					t.PosterPath, t.Title = m.PosterPath, firstNonEmpty(m.Title, t.Title)
				}
			} else if sh, err := s.TMDB.Show(ctx, t.TMDBID); err == nil {
				t.PosterPath, t.Title = sh.PosterPath, firstNonEmpty(sh.Name, t.Title)
			}
		}()
	}
	wg.Wait()
}

// externalRef checks a source's reference before anything is saved.
func externalRef(ru db.CollectionRules) error {
	switch ru.Source {
	case "mdblist":
		_, err := mdblistPath(ru.SourceRef)
		return err
	case "simkl":
		if ru.SourceRef == "" {
			return errors.New("pick a Simkl chart or watchlist")
		}
	}
	return nil
}
