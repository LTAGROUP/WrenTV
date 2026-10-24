package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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
