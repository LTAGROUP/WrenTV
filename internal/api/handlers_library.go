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

// cleanPaths validates folders. A library may also have no paths (torrent-only).
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
		LibraryID: id, Kind: kind, Sort: r.URL.Query().Get("sort"), Search: r.URL.Query().Get("q"),
		Limit: queryInt(r, "limit", 500), Offset: queryInt(r, "offset", 0), UserID: p.User.ID,
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
	cont, _ := s.DB.ContinueWatching(ctx, p.User.ID, ids, 20)
	// "Up next": the next episode for shows watched recently.
	upNext := []*db.Item{}
	if len(ids) > 0 {
		rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT i.show_id, MAX(w.updated_at) m FROM watch_state w JOIN items i ON i.id=w.item_id
			WHERE w.user_id=? AND i.kind='episode' AND w.watched=1 GROUP BY i.show_id ORDER BY m DESC LIMIT 20`, p.User.ID)
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
			for _, sid := range showIDs {
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
	writeJSON(w, 200, map[string]any{"continueWatching": nonNil(cont), "recentlyAdded": shelves, "topRated": topRated, "genres": genres})
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
	Info          *media.Info `json:"info,omitempty"`
	TorrentStatus string      `json:"torrentStatus,omitempty"`
	FileName      string      `json:"fileName"`
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
		it.Progress = ws
	}
	resp := map[string]any{"item": it}
	switch it.Kind {
	case "show":
		seasons, _, _ := s.DB.QueryItems(ctx, db.ItemQuery{ParentID: it.ID, Kind: "season", Sort: "episode", UserID: p.User.ID})
		resp["children"] = nonNil(seasons)
		if next, err := s.DB.NextEpisode(ctx, p.User.ID, it.ID); err == nil {
			if ws, _ := s.DB.GetWatchState(ctx, p.User.ID, next.ID); ws != nil {
				next.Progress = ws
			}
			resp["nextEpisode"] = next
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
		files, _ := s.DB.FilesForItem(ctx, it.ID)
		views := []FileView{}
		for _, f := range files {
			v := FileView{MediaFile: f, FileName: displayFileName(f.Path)}
			if f.Probe != "" {
				var info media.Info
				if json.Unmarshal([]byte(f.Probe), &info) == nil {
					v.Info = &info
				}
			}
			if f.TorrentID != "" {
				if t, err := s.DB.GetTorrent(ctx, f.TorrentID); err == nil {
					v.TorrentStatus = t.Status
				}
			}
			views = append(views, v)
		}
		resp["files"] = views
	}
	writeJSON(w, 200, resp)
}

func displayFileName(p string) string {
	if strings.HasPrefix(p, "torrent://") {
		parts := strings.SplitN(strings.TrimPrefix(p, "torrent://"), "/", 3)
		if len(parts) == 3 {
			return filepath.Base(parts[2])
		}
	}
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
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.DB.SetProgress(r.Context(), principal(r).User.ID, it.ID, req.Position, req.Duration); err != nil {
		writeErr(w, 500, err.Error())
		return
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
