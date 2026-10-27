package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// maxLogoBatch caps one request: a shelf's visible cards and a few past them.
const maxLogoBatch = 30

// handleTitleLogos returns the clear logos of TMDB titles, for a home hero
// that shows a title's logo before the server has it. ids is a comma list of
// "movie:<tmdbId>" or "show:<tmdbId>"; the reply maps each to its logo path,
// "" when it has none. The lookups are the ones adding a title makes, so
// they also ready a title the user may open next.
func (s *Server) handleTitleLogos(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	if !s.TMDB.Enabled() {
		writeJSON(w, 200, out)
		return
	}
	var keys []string
	seen := map[string]bool{}
	for _, k := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if k = strings.TrimSpace(k); k != "" && !seen[k] && len(keys) < maxLogoBatch {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	// Warming, not something the user picked: it mustn't queue ahead of an open.
	ctx, cancel := context.WithTimeout(tmdb.Prefetch(r.Context()), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, key := range keys {
		kind, num, ok := strings.Cut(key, ":")
		id, err := strconv.Atoi(num)
		if !ok || err != nil || id <= 0 || (kind != "movie" && kind != "show") {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			logo := ""
			if kind == "movie" {
				m, err := s.TMDB.Movie(ctx, id)
				if err != nil {
					return
				}
				logo = m.Images.BestLogo()
			} else {
				sh, err := s.TMDB.Show(ctx, id)
				if err != nil {
					return
				}
				logo = sh.Images.BestLogo()
			}
			mu.Lock()
			out[key] = logo
			mu.Unlock()
		}()
	}
	wg.Wait()
	writeJSON(w, 200, out)
}

// maxDetailWarm caps one warm request: the focused title and its neighbours.
const maxDetailWarm = 3

// detailWarms bounds detail warming server-wide, so fast shelf scrolling on
// several screens can't build a queue in front of real lookups.
var detailWarms = make(chan struct{}, 4)

// handleWarmTitleDetails readies what a TMDB title's detail page shows first
// (its details, logo and artwork, then cast, trailer and more like this) for
// a poster the user has settled on, so opening it finds them cached. It
// answers at once; the lookups run in the background on the warming budget
// and are dropped, not queued, when enough are already running.
func (s *Server) handleWarmTitleDetails(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
	if !s.TMDB.Enabled() {
		return
	}
	n := 0
	for _, key := range strings.Split(r.URL.Query().Get("ids"), ",") {
		kind, num, ok := strings.Cut(strings.TrimSpace(key), ":")
		id, err := strconv.Atoi(num)
		if !ok || err != nil || id <= 0 || (kind != "movie" && kind != "show") {
			continue
		}
		if n++; n > maxDetailWarm {
			break
		}
		select {
		case detailWarms <- struct{}{}:
		default:
			return
		}
		go func() {
			defer func() { <-detailWarms }()
			ctx, cancel := context.WithTimeout(tmdb.Prefetch(context.Background()), 20*time.Second)
			defer cancel()
			if kind == "movie" {
				if _, err := s.TMDB.Movie(ctx, id); err == nil {
					_, _ = s.TMDB.MovieExtras(ctx, id)
				}
			} else if _, err := s.TMDB.Show(ctx, id); err == nil {
				_, _ = s.TMDB.ShowExtras(ctx, id)
			}
		}()
	}
}
