package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
)

// Theme music, played behind a title's details like Plex does. A theme file
// next to the media wins (theme.mp3 in a movie's or show's folder, the
// Plex/Kodi convention); otherwise shows use Plex's public TV theme library,
// keyed by TVDB id. Movies have no public source, so only local files.

const themeLibrary = "https://tvthemes.plexapp.com/%d.mp3"

var themeNames = []string{"theme.mp3", "theme.m4a", "theme.aac", "theme.ogg", "theme.flac", "theme.opus"}

// themeMisses remembers shows the library has no theme for, so details pages
// don't ask it again each time.
var themeMisses sync.Map // tvdb id -> time.Time

func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if (it.Kind == "season" || it.Kind == "episode") && it.ShowID != "" {
		if sh, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
			it = sh
		}
	}
	if p := s.localTheme(ctx, it); p != "" {
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, p)
		return
	}
	if it.Kind == "show" {
		if p := s.libraryTheme(ctx, it); p != "" {
			w.Header().Set("Cache-Control", "private, max-age=86400")
			w.Header().Set("Content-Type", "audio/mpeg")
			http.ServeFile(w, r, p)
			return
		}
	}
	writeErr(w, 404, "no theme")
}

// localTheme finds a theme file in the folder of a movie, or of a show (its
// episodes' folder, or the one above a Season folder).
func (s *Server) localTheme(ctx context.Context, it *db.Item) string {
	var files []*db.MediaFile
	switch it.Kind {
	case "movie":
		files, _ = s.DB.FilesForItem(ctx, it.ID)
	case "show":
		eps, _ := s.DB.ItemsByShow(ctx, it.ID)
		for _, ep := range eps {
			if ep.Kind != "episode" {
				continue
			}
			if files, _ = s.DB.FilesForItem(ctx, ep.ID); len(files) > 0 {
				break
			}
		}
	}
	for _, f := range files {
		if f.Path == "" {
			continue
		}
		dirs := []string{filepath.Dir(f.Path)}
		if it.Kind == "show" {
			dirs = append(dirs, filepath.Dir(dirs[0]))
		}
		for _, d := range dirs {
			for _, n := range themeNames {
				if st, err := os.Stat(filepath.Join(d, n)); err == nil && st.Mode().IsRegular() {
					return filepath.Join(d, n)
				}
			}
		}
	}
	return ""
}

// libraryTheme downloads a show's theme from Plex's library once and keeps it.
func (s *Server) libraryTheme(ctx context.Context, it *db.Item) string {
	if !s.TMDB.Enabled() || it.TMDBID <= 0 {
		return ""
	}
	ex, err := s.TMDB.ShowExtras(ctx, it.TMDBID)
	if err != nil || ex.ExternalIDs.TVDBID <= 0 {
		return ""
	}
	id := ex.ExternalIDs.TVDBID
	path := filepath.Join(s.Cfg.DataDir, "cache", "themes", fmt.Sprintf("%d.mp3", id))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if t, ok := themeMisses.Load(id); ok && time.Since(t.(time.Time)) < 24*time.Hour {
		return ""
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf(themeLibrary, id), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Debug("theme", "tvdb", id, "err", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "audio/") {
		themeMisses.Store(id, time.Now())
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return ""
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 20<<20))
	f.Close()
	if err != nil || os.Rename(tmp, path) != nil {
		os.Remove(tmp)
		return ""
	}
	return path
}
