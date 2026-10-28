package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/livetv"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// ---------- viewers ----------

func (s *Server) handleLiveChannels(w http.ResponseWriter, r *http.Request) {
	uid := principal(r).User.ID
	favs, _ := s.DB.LiveFavorites(r.Context(), uid)
	recents, _ := s.DB.LiveRecents(r.Context(), uid)
	shows, _ := s.DB.LiveShows(r.Context(), uid)
	chs := []livetv.ChannelView{}
	if s.LiveTV.Active() {
		chs = s.LiveTV.Channels()
	}
	writeJSON(w, 200, map[string]any{"channels": chs, "favorites": nonNil(favs), "recents": nonNil(recents), "shows": nonNil(shows)})
}

// handleLiveGuide is every channel's programmes for a few hours, from now
// or from "from" (Unix seconds): the guide pages forward a few hours at a time.
func (s *Server) handleLiveGuide(w http.ResponseWriter, r *http.Request) {
	hours := min(max(queryInt(r, "hours", 6), 1), 36)
	from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	writeJSON(w, 200, map[string]any{"channels": s.LiveTV.Schedule(from, hours)})
}

// handleLiveSearch finds channels and the programmes coming up on them.
func (s *Server) handleLiveSearch(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.LiveTV.Search(r.URL.Query().Get("q"), min(max(queryInt(r, "limit", 40), 1), 200)))
}

// handleLiveOverview is Live TV's home: games on now and coming up, the
// viewer's channels, and what's on by kind, with pictures for programmes.
func (s *Server) handleLiveOverview(w http.ResponseWriter, r *http.Request) {
	shelves := []livetv.Shelf{}
	if s.LiveTV.Active() {
		uid := principal(r).User.ID
		favs, _ := s.DB.LiveFavorites(r.Context(), uid)
		recents, _ := s.DB.LiveRecents(r.Context(), uid)
		shows, _ := s.DB.LiveShows(r.Context(), uid)
		shelves = s.LiveTV.Overview(favs, recents, shows)
		s.liveArt(r.Context(), shelves)
	}
	writeJSON(w, 200, map[string]any{"shelves": shelves})
}

// liveArt gives programmes the guide has no picture for a backdrop from
// TMDB, when one has exactly their title. It waits a moment for TMDB, no
// longer: lookups still running finish into TMDB's cache for next time.
func (s *Server) liveArt(ctx context.Context, shelves []livetv.Shelf) {
	if s.TMDB == nil || !s.TMDB.Enabled() {
		return
	}
	type want struct {
		title string
		movie bool
	}
	need := map[want][]*livetv.Tile{}
	for i := range shelves {
		// News and sport titles name a broadcast, not a show TMDB has.
		if id := shelves[i].ID; id == "news" || id == "live-sports" || id == "upcoming-sports" {
			continue
		}
		for j := range shelves[i].Tiles {
			t := &shelves[i].Tiles[j]
			if t.Art == "" && t.Matchup == nil && t.Program != nil {
				k := want{t.Program.Title, shelves[i].ID == "movies"}
				need[k] = append(need[k], t)
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	gate := make(chan struct{}, 6)
	for k, tiles := range need {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case gate <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-gate }()
			if art := s.programArt(ctx, k.title, k.movie); art != "" {
				mu.Lock()
				for _, t := range tiles {
					t.Art = art
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

var titleYear = regexp.MustCompile(`\s*\((\d{4})\)$`)

// programArt is the TMDB backdrop of the show or movie titled [title].
func (s *Server) programArt(ctx context.Context, title string, movie bool) string {
	title = strings.TrimSpace(strings.TrimSuffix(title, "24/7"))
	year := 0
	if m := titleYear.FindStringSubmatch(title); m != nil {
		year, _ = strconv.Atoi(m[1])
		title = strings.TrimSpace(title[:len(title)-len(m[0])])
	}
	key := func(s string) string {
		return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
	}
	wanted := key(title)
	if wanted == "" {
		return ""
	}
	searches := []func(context.Context, string, int) ([]tmdb.SearchResult, error){s.TMDB.SearchTV, s.TMDB.SearchMovie}
	if movie {
		searches[0], searches[1] = searches[1], searches[0]
	}
	for _, search := range searches {
		results, err := search(ctx, title, year)
		if err != nil {
			return ""
		}
		best := tmdb.SearchResult{}
		for _, r := range results {
			if r.BackdropPath != "" && key(r.DisplayTitle()) == wanted && r.Popularity > best.Popularity {
				best = r
			}
		}
		if best.BackdropPath != "" {
			return "/api/images/w780" + best.BackdropPath
		}
	}
	return ""
}

// handleLiveShow follows ({"title": ..., "on": true}) or unfollows a show.
func (s *Server) handleLiveShow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
		On    bool   `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" {
		writeErr(w, 400, "title required")
		return
	}
	if err := s.DB.SetLiveShow(r.Context(), principal(r).User.ID, strings.TrimSpace(req.Title), req.On); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleLiveFavorite stars (PUT) or unstars (DELETE) a channel.
func (s *Server) handleLiveFavorite(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.SetLiveFavorite(r.Context(), principal(r).User.ID, chi.URLParam(r, "id"), r.Method == http.MethodPut); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLivePlay(w http.ResponseWriter, r *http.Request) {
	if !s.LiveTV.Active() {
		writeErr(w, 404, "live TV is off")
		return
	}
	// Apple's players ask for HLS: they can't read a plain TS stream.
	// A preview is the guide playing the picked channel muted: not watched.
	var req struct {
		HLS     bool `json:"hls"`
		Preview bool `json:"preview"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	// A player that retunes after an admin stopped it mustn't just start again.
	if msg, stopped := s.act().refused(principal(r).User.ID, "live:"+chi.URLParam(r, "id")); stopped {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	sess, err := s.LiveTV.Play(r.Context(), principal(r).User.ID, chi.URLParam(r, "id"), req.HLS)
	switch {
	case errors.Is(err, livetv.ErrBusy):
		writeErr(w, 429, err.Error())
		return
	case err != nil:
		writeErr(w, 404, err.Error())
		return
	}
	if !req.Preview {
		_ = s.DB.WatchedChannel(r.Context(), principal(r).User.ID, chi.URLParam(r, "id"))
	}
	writeJSON(w, 200, map[string]any{"url": sess.URL(), "type": sess.Type(), "sessionId": sess.ID})
}

// ---------- admins ----------

const maskedPassword = "••••••"

type liveTVView struct {
	Enabled      bool                  `json:"enabled"`
	Active       bool                  `json:"active"`
	Sources      []config.LiveTVSource `json:"sources"`
	HiddenGroups []string              `json:"hiddenGroups"`
	MaxStreams   int                   `json:"maxStreams"`
	Status       livetv.Status         `json:"status"`
	Playing      int                   `json:"playing"`
}

func (s *Server) liveTVView() liveTVView {
	c := s.Cfg.LiveTV
	v := liveTVView{Enabled: c.Enabled, Active: s.LiveTV.Active(), Sources: []config.LiveTVSource{}, HiddenGroups: nonNil(c.HiddenGroups),
		MaxStreams: c.MaxStreams, Status: s.LiveTV.Status(), Playing: s.LiveTV.Playing()}
	for _, src := range c.Sources {
		if src.Password != "" {
			src.Password = maskedPassword
		}
		v.Sources = append(v.Sources, src)
	}
	return v
}

type liveTVPatch struct {
	Enabled      *bool                  `json:"enabled"`
	Sources      *[]config.LiveTVSource `json:"sources"`
	HiddenGroups *[]string              `json:"hiddenGroups"`
	MaxStreams   *int                   `json:"maxStreams"`
}

// applyLiveTV validates and applies a live TV patch. It reports whether the
// playlists need fetching again, and whether only the groups changed.
func (s *Server) applyLiveTV(p *liveTVPatch) (refetch, regroup bool, err error) {
	c := &s.Cfg.LiveTV
	if p.Enabled != nil && *p.Enabled != c.Enabled {
		c.Enabled = *p.Enabled
		refetch = true
	}
	if p.MaxStreams != nil {
		if *p.MaxStreams < 0 {
			return false, false, errors.New("streams at once can't be negative")
		}
		c.MaxStreams = *p.MaxStreams
	}
	if p.HiddenGroups != nil && !slices.Equal(*p.HiddenGroups, c.HiddenGroups) {
		c.HiddenGroups = *p.HiddenGroups
		regroup = true
	}
	if p.Sources != nil {
		old := map[string]config.LiveTVSource{}
		for _, src := range c.Sources {
			old[src.ID] = src
		}
		var out []config.LiveTVSource
		for _, src := range *p.Sources {
			src, err := cleanSource(src, old[src.ID])
			if err != nil {
				return false, false, err
			}
			out = append(out, src)
		}
		if !slices.Equal(out, c.Sources) {
			refetch = true
		}
		c.Sources = out
	}
	return refetch, regroup, nil
}

func cleanSource(src, old config.LiveTVSource) (config.LiveTVSource, error) {
	src.Name = strings.TrimSpace(src.Name)
	src.URL = strings.TrimSpace(src.URL)
	src.Username = strings.TrimSpace(src.Username)
	src.EPGURL = strings.TrimSpace(src.EPGURL)
	src.UserAgent = strings.TrimSpace(src.UserAgent)
	if src.ID == "" {
		src.ID = db.NewID()[:8]
	}
	if src.Password == maskedPassword {
		src.Password = old.Password
	}
	switch src.Kind {
	case "", "m3u":
		src.Kind = "m3u"
		if !strings.HasPrefix(src.URL, "http://") && !strings.HasPrefix(src.URL, "https://") {
			return src, errors.New("a playlist link starts with http:// or https://")
		}
		src.Username, src.Password = "", ""
	case "stremio":
		if !strings.HasPrefix(src.URL, "http://") && !strings.HasPrefix(src.URL, "https://") && !strings.HasPrefix(src.URL, "stremio://") {
			return src, errors.New("an addon link starts with https:// or stremio://")
		}
		src.Username, src.Password = "", ""
	case "xtream":
		if src.URL == "" || src.Username == "" || src.Password == "" {
			return src, errors.New("an Xtream Codes login needs the server, a username and a password")
		}
	default:
		return src, errors.New("unknown source type")
	}
	if src.Name == "" {
		src.Name = "IPTV"
	}
	return src, nil
}

func (s *Server) handleLiveTVAdmin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"liveTv": s.liveTVView(), "groups": s.LiveTV.Groups()})
}

func (s *Server) handleLiveTVRefresh(w http.ResponseWriter, r *http.Request) {
	s.LiveTV.Refresh(true)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleLiveTVCheck(w http.ResponseWriter, r *http.Request) {
	var src config.LiveTVSource
	if err := decode(r, &src); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var old config.LiveTVSource
	for _, x := range s.Cfg.LiveTV.Sources {
		if x.ID == src.ID {
			old = x
		}
	}
	src, err := cleanSource(src, old)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	n, guides, err := s.LiveTV.Check(r.Context(), src)
	if err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"channels": n, "guide": len(guides) > 0})
}
