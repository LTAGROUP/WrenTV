package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/media"
)

func (s *Server) visibleLibraries(r *http.Request) ([]*db.Library, []string, error) {
	all, err := s.DB.ListLibraries(r.Context())
	if err != nil {
		return nil, nil, err
	}
	p := principal(r)
	var libs []*db.Library
	for _, l := range all {
		if p.CanAccess(l.ID) {
			libs = append(libs, l)
		}
	}
	ids := make([]string, 0, len(libs))
	for _, l := range libs {
		ids = append(ids, l.ID)
	}
	return libs, ids, nil
}

func (s *Server) handleLibraries(w http.ResponseWriter, r *http.Request) {
	libs, _, err := s.visibleLibraries(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if libs == nil {
		libs = []*db.Library{}
	}
	if !principal(r).IsAdmin() {
		for _, l := range libs {
			l.Paths = nil // don't reveal server paths to shared users
		}
	}
	writeJSON(w, 200, libs)
}

func (s *Server) handleCreateLibrary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string   `json:"name"`
		Type  string   `json:"type"`
		Paths []string `json:"paths"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Type != "movies" && req.Type != "shows" {
		writeErr(w, 400, `type must be "movies" or "shows"`)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, 400, "name required")
		return
	}
	paths, err := cleanPaths(req.Paths, false)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	lib := &db.Library{Name: strings.TrimSpace(req.Name), Type: req.Type, Paths: paths}
	if err := s.DB.CreateLibrary(r.Context(), lib); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.afterLibraryChange(lib)
	writeJSON(w, 201, lib)
}

// cleanPaths validates folders.
func cleanPaths(in []string, _ bool) ([]string, error) {
	out := []string{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("folder %q is not accessible from the server: %v", p, err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("%q is not a folder", p)
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Server) afterLibraryChange(lib *db.Library) {
	go func() {
		ctx := context.Background()
		if _, err := s.Scanner.ScanLibrary(ctx, lib); err != nil {
			slog.Warn("scan failed", "library", lib.Name, "err", err)
		}
		s.Watcher.Refresh(ctx)
		s.heartbeat()
		s.Markers.Kick()
	}()
}

func (s *Server) handleUpdateLibrary(w http.ResponseWriter, r *http.Request) {
	lib, err := s.DB.GetLibrary(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "library not found")
		return
	}
	var req struct {
		Name  *string   `json:"name"`
		Paths *[]string `json:"paths"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		lib.Name = strings.TrimSpace(*req.Name)
	}
	if req.Paths != nil && lib.Source == "stream" {
		writeErr(w, 400, "streaming libraries have no folders")
		return
	}
	if req.Paths != nil {
		paths, err := cleanPaths(*req.Paths, false)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		lib.Paths = paths
	}
	if err := s.DB.UpdateLibrary(r.Context(), lib); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.afterLibraryChange(lib)
	writeJSON(w, 200, lib)
}

func (s *Server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.DeleteLibrary(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	go func() {
		s.Watcher.Refresh(context.Background())
		s.heartbeat()
	}()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleScanLibrary(w http.ResponseWriter, r *http.Request) {
	lib, err := s.DB.GetLibrary(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "library not found")
		return
	}
	go func() {
		if _, err := s.Scanner.ScanLibrary(context.Background(), lib); err != nil {
			slog.Warn("scan failed", "library", lib.Name, "err", err)
		}
		s.Markers.Kick()
	}()
	writeJSON(w, 202, map[string]string{"status": "scanning"})
}

func (s *Server) handleLibraryItems(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p := principal(r)
	if !p.CanAccess(id) {
		writeErr(w, 404, "library not found")
		return
	}
	lib, err := s.DB.GetLibrary(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "library not found")
		return
	}
	kind := "movie"
	if lib.Type == "shows" {
		kind = "show"
	}
	items, total, err := s.DB.QueryItems(r.Context(), db.ItemQuery{
		LibraryID: id, Kind: kind, Sort: r.URL.Query().Get("sort"), Search: r.URL.Query().Get("q"), Genre: r.URL.Query().Get("genre"),
		Unwatched: r.URL.Query().Get("unwatched") == "1",
		Limit:     queryInt(r, "limit", 500), Offset: queryInt(r, "offset", 0), UserID: p.User.ID,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": nonNil(items), "total": total})
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	libs, ids, err := s.visibleLibraries(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ctx := r.Context()
	cont := s.continueWatching(ctx, r, ids)

	type shelf struct {
		Library *db.Library `json:"library"`
		Items   []*db.Item  `json:"items"`
	}
	shelves := []shelf{}
	for _, l := range libs {
		kind := "movie"
		if l.Type == "shows" {
			kind = "show"
		}
		items, _, _ := s.DB.QueryItems(ctx, db.ItemQuery{LibraryID: l.ID, Kind: kind, Sort: "added", Limit: 20, UserID: p.User.ID})
		if len(items) > 0 {
			shelves = append(shelves, shelf{l, items})
		}
	}
	topRated, genres := s.homeRows(ctx, p.User.ID, ids)
	writeJSON(w, 200, map[string]any{"continueWatching": nonNil(cont), "recentlyAdded": shelves, "topRated": topRated, "genres": genres,
		"collections": s.homeCollections(ctx, r), "watchlist": s.watchlistRow(ctx, p.User.ID, ids)})
}

// watchlistRow is the home row: what's still to watch, newest added first.
func (s *Server) watchlistRow(ctx context.Context, userID string, ids []string) []*db.Item {
	items, _, _ := s.DB.QueryItems(ctx, db.ItemQuery{LibraryIDs: ids, Kind: "movie,show", WatchlistOf: userID, Unwatched: true,
		Sort: "watchlist", Limit: 20, UserID: userID})
	return nonNil(items)
}

func (s *Server) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	_, ids, err := s.visibleLibraries(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid := principal(r).User.ID
	items, _, err := s.DB.QueryItems(r.Context(), db.ItemQuery{LibraryIDs: ids, Kind: "movie,show", WatchlistOf: uid, Sort: "watchlist", UserID: uid})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, nonNil(items))
}

func (s *Server) handleWatchlistAdd(w http.ResponseWriter, r *http.Request) {
	s.setWatchlist(w, r, true)
}
func (s *Server) handleWatchlistRemove(w http.ResponseWriter, r *http.Request) {
	s.setWatchlist(w, r, false)
}

func (s *Server) setWatchlist(w http.ResponseWriter, r *http.Request, on bool) {
	it, ok := s.itemFor(r)
	if !ok || (it.Kind != "movie" && it.Kind != "show") {
		writeErr(w, 404, "movie or show not found")
		return
	}
	if err := s.DB.SetWatchlist(r.Context(), principal(r).User.ID, it.ID, on); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if on && s.Streaming != nil {
		s.Streaming.PrefetchWatchlist(principal(r).User.ID, it)
	}
	w.WriteHeader(http.StatusNoContent)
}

// continueWatching is in-progress movies and episodes, then the next
// episode of shows watched recently.
func (s *Server) continueWatching(ctx context.Context, r *http.Request, ids []string) []*db.Item {
	p := principal(r)
	prefs, _ := s.DB.GetPrefs(ctx, p.User.ID)
	cont, _ := s.DB.ContinueWatching(ctx, p.User.ID, ids, prefs.ContinueAfterSeconds(), 20)
	// "Up next": the next episode for shows watched recently.
	upNext := []*db.Item{}
	if len(ids) > 0 {
		rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT i.show_id, MAX(w.updated_at) m FROM watch_state w JOIN items i ON i.id=w.item_id
			WHERE w.user_id=? AND i.kind='episode' AND w.watched=1 GROUP BY i.show_id ORDER BY m DESC LIMIT 200`, p.User.ID)
		if err == nil {
			var showIDs []string
			for rows.Next() {
				var sid string
				var m int64
				if rows.Scan(&sid, &m) == nil {
					showIDs = append(showIDs, sid)
				}
			}
			rows.Close()
			inProgress := map[string]bool{}
			for _, c := range cont {
				if c.ShowID != "" {
					inProgress[c.ShowID] = true
				}
			}
			// Finished shows have no next episode, so look past them.
			for _, sid := range showIDs {
				if len(upNext) == 20 {
					break
				}
				if inProgress[sid] {
					continue
				}
				if ep, err := s.DB.NextEpisode(ctx, p.User.ID, sid); err == nil && p.CanAccess(ep.LibraryID) {
					upNext = append(upNext, ep)
				}
			}
		}
	}
	cont = append(cont, upNext...)
	s.decorateEpisodes(ctx, cont)
	return nonNil(cont)
}

// decorateEpisodes fills show artwork onto episodes lacking stills.
func (s *Server) decorateEpisodes(ctx context.Context, items []*db.Item) {
	cache := map[string]*db.Item{}
	for _, it := range items {
		if it.Kind != "episode" || it.ShowID == "" {
			continue
		}
		sh, ok := cache[it.ShowID]
		if !ok {
			sh, _ = s.DB.GetItem(ctx, it.ShowID)
			cache[it.ShowID] = sh
		}
		if sh == nil {
			continue
		}
		it.ShowTitle = sh.Title
		if it.BackdropPath == "" {
			it.BackdropPath = sh.BackdropPath
		}
		if it.PosterPath == "" {
			it.PosterPath = sh.PosterPath
		}
		if it.LogoPath == "" {
			it.LogoPath = sh.LogoPath
		}
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, 200, []any{})
		return
	}
	_, ids, err := s.visibleLibraries(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	items, _, err := s.DB.QueryItems(r.Context(), db.ItemQuery{LibraryIDs: ids, Kind: "movie,show,episode", Search: q, Limit: 60, UserID: principal(r).User.ID})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.decorateEpisodes(r.Context(), items)
	writeJSON(w, 200, nonNil(items))
}

func (s *Server) itemFor(r *http.Request) (*db.Item, bool) {
	it, err := s.DB.GetItem(r.Context(), chi.URLParam(r, "id"))
	if err != nil || !principal(r).CanAccess(it.LibraryID) {
		return nil, false
	}
	return it, true
}

// FileView is a media file with its parsed streams for clients.
type FileView struct {
	*db.MediaFile
	Info     *media.Info `json:"info,omitempty"`
	FileName string      `json:"fileName"`
	Label    string      `json:"label"`
	// Release is the torrent a stream comes from.
	Release string `json:"release,omitempty"`
}

func (s *Server) handleItem(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	ctx := r.Context()
	p := principal(r)
	if ws, _ := s.DB.GetWatchState(ctx, p.User.ID, it.ID); ws != nil {
		if it.Kind == "show" || it.Kind == "season" {
			ws.Watched = s.DB.EpisodesWatched(ctx, p.User.ID, it.ID)
		}
		it.Progress = ws
	}
	resp := map[string]any{"item": it}
	if it.Kind == "movie" || it.Kind == "show" {
		resp["onWatchlist"] = s.DB.OnWatchlist(ctx, p.User.ID, it.ID)
	}
	streamed := s.isStreamLibrary(ctx, it.LibraryID)
	// The owner can turn off looking streams up as soon as a page opens.
	warm := streamed && s.Cfg.Streaming.LookupOnOpen
	resp["streaming"] = streamed
	resp["canDownload"] = s.limitsFor(p.User).CanDownload
	switch it.Kind {
	case "show":
		if streamed {
			s.Streaming.MaybeRefresh(it)
		}
		seasons, _, _ := s.DB.QueryItems(ctx, db.ItemQuery{ParentID: it.ID, Kind: "season", Sort: "episode", UserID: p.User.ID})
		resp["children"] = nonNil(seasons)
		if next, err := s.DB.NextEpisode(ctx, p.User.ID, it.ID); err == nil {
			if ws, _ := s.DB.GetWatchState(ctx, p.User.ID, next.ID); ws != nil {
				next.Progress = ws
			}
			resp["nextEpisode"] = next
			if warm {
				s.warmItem(next, p.User)
			}
		}
	case "season":
		eps, _, _ := s.DB.QueryItems(ctx, db.ItemQuery{ParentID: it.ID, Kind: "episode", Sort: "episode", UserID: p.User.ID})
		resp["children"] = nonNil(eps)
		if sh, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
			resp["show"] = sh
		}
	case "episode":
		if sh, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
			resp["show"] = sh
		}
		fallthrough
	case "movie":
		if warm {
			s.warmItem(it, p.User)
		}
		files, _ := s.playableFiles(ctx, it)
		views := []FileView{}
		for _, f := range files {
			v := FileView{MediaFile: f, FileName: displayFileName(f.Path), Label: versionLabel(f)}
			if f.Probe != "" {
				var info media.Info
				if json.Unmarshal([]byte(f.Probe), &info) == nil {
					v.Info = &info
				}
			}
			if f.StreamID != "" {
				if st, err := s.DB.GetStream(ctx, f.StreamID); err == nil {
					v.FileName, v.Release = filepath.Base(st.FileName), st.Release
				}
			}
			views = append(views, v)
		}
		resp["files"] = views
	}
	writeJSON(w, 200, resp)
}

func displayFileName(p string) string {
	return filepath.Base(p)
}

func (s *Server) handleChildren(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	kind := "season"
	if it.Kind == "season" {
		kind = "episode"
	}
	items, _, err := s.DB.QueryItems(r.Context(), db.ItemQuery{ParentID: it.ID, Kind: kind, Sort: "episode", UserID: principal(r).User.ID})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, nonNil(items))
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	var req struct {
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		// State is "playing", "paused" or "buffering"; players that don't
		// say are taken to be paused once their reports stop.
		State string `json:"state"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	u := principal(r).User
	if msg, stopped := s.act().stoppedFor(u.ID, it.ID); stopped {
		// An admin stopped this playback: the player ends it and says why.
		writeJSON(w, http.StatusGone, map[string]any{"error": msg, "stopped": true})
		return
	}
	s.act().progress(u.ID, it.ID, req.Position, req.Duration, req.State)
	prefs, _ := s.DB.GetPrefs(r.Context(), u.ID)
	at := prefs.WatchedAt()
	justWatched, err := s.DB.SetProgress(r.Context(), u.ID, it.ID, req.Position, req.Duration, at)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	_ = s.DB.TrackPlay(r.Context(), u.ID, it.ID, req.Position, req.Duration, at)
	if s.Tracking != nil {
		s.Tracking.Progress(r.Context(), u, it, req.Position, req.Duration, at, justWatched, clientName(r))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWatched(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	var req struct {
		Watched bool `json:"watched"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.DB.SetWatched(r.Context(), principal(r).User.ID, it.ID, req.Watched); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if s.Tracking != nil {
		s.Tracking.Marked(r.Context(), principal(r).User, it, req.Watched)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRematch(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok || (it.Kind != "movie" && it.Kind != "show") {
		writeErr(w, 404, "movie or show not found")
		return
	}
	var req struct {
		TMDBID int `json:"tmdbId"`
	}
	if err := decode(r, &req); err != nil || req.TMDBID <= 0 {
		writeErr(w, 400, "tmdbId required")
		return
	}
	if err := s.Scanner.Rematch(r.Context(), it, req.TMDBID); err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, it)
}

// handleEvents streams server-sent events so clients refresh instantly.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsub := s.Hub.Subscribe()
	defer unsub()
	p := principal(r)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case ev := <-ch:
			if ev.LibraryID != "" && !p.CanAccess(ev.LibraryID) {
				continue
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			fl.Flush()
		}
	}
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
