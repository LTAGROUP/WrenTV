package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/onlinesubs"
)

// onlineSubIndex is where fetched subtitles' track numbers start, clear of
// any file's own.
const onlineSubIndex = 10000

var onlineSubKey = regexp.MustCompile(`^[0-9a-f]{20}$`)

// imdbTarget is the IMDb id a movie is known by, or an episode's show's, with
// its season and episode (-1 for a movie).
func (s *Server) imdbTarget(ctx context.Context, it *db.Item) (imdb string, season, episode int) {
	switch it.Kind {
	case "movie":
		return it.IMDBID, -1, -1
	case "episode":
		show, err := s.DB.GetItem(ctx, it.ShowID)
		if err != nil || show == nil {
			return "", -1, -1
		}
		return show.IMDBID, it.SeasonNumber, it.EpisodeNumber
	}
	return "", -1, -1
}

func (s *Server) onlineSubsDir() string { return filepath.Join(s.Cfg.DataDir, "subtitles") }

// handleSearchOnlineSubtitles searches the subtitle sites for an item, in the
// languages asked for (lang=en,fr) or else the viewer's subtitle language
// and English.
func (s *Server) handleSearchOnlineSubtitles(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	if !s.Subtitles.Enabled() {
		writeJSON(w, 200, map[string]any{"enabled": false, "results": []onlinesubs.Result{}})
		return
	}
	var langs []string
	if l := r.URL.Query().Get("lang"); l != "" {
		langs = strings.Split(strings.ToLower(l), ",")
	} else {
		prefs, _ := s.DB.GetPrefs(r.Context(), principal(r).User.ID)
		langs = []string{prefs.SubtitleLanguage, prefs.AudioLanguage, "en"}
	}
	imdb, season, episode := s.imdbTarget(r.Context(), it)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	rs, err := s.Subtitles.Search(ctx, onlinesubs.Query{IMDBID: imdb, Season: season, Episode: episode, Languages: compactLangs(langs)})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "results": nonNil(rs)})
}

func compactLangs(ls []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range ls {
		if l = strings.TrimSpace(l); l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// handleFetchOnlineSubtitle downloads a search result, keeps it with the
// item and returns it as a track for the session it's wanted in.
func (s *Server) handleFetchOnlineSubtitle(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	var req struct {
		Provider  string `json:"provider"`
		ID        string `json:"id"`
		Language  string `json:"language"`
		Name      string `json:"name"`
		SessionID string `json:"sessionId"`
	}
	if err := decode(r, &req); err != nil || req.Provider == "" || req.ID == "" {
		writeErr(w, 400, "provider and id are required")
		return
	}
	sum := sha256.Sum256([]byte(req.Provider + "\x00" + req.ID))
	key := hex.EncodeToString(sum[:10])
	file := filepath.Join(s.onlineSubsDir(), key+".vtt")
	if _, err := os.Stat(file); err != nil {
		imdb, season, episode := s.imdbTarget(r.Context(), it)
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		vtt, err := s.Subtitles.Fetch(ctx, req.Provider, req.ID, onlinesubs.Query{IMDBID: imdb, Season: season, Episode: episode})
		if err != nil {
			code := 502
			if errors.Is(err, onlinesubs.ErrNone) {
				code = 400
			}
			writeErr(w, code, err.Error())
			return
		}
		if err := os.MkdirAll(s.onlineSubsDir(), 0o755); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		tmp := file + ".tmp"
		if err := os.WriteFile(tmp, vtt, 0o644); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if err := os.Rename(tmp, file); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 200 {
		name = name[:200]
	}
	if err := s.DB.AddOnlineSubtitle(r.Context(), db.OnlineSubtitle{ItemID: it.ID, Key: key, Provider: req.Provider, Language: req.Language, Name: name}); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, t := range s.onlineTracks(r.Context(), it.ID, req.SessionID) {
		if strings.HasSuffix(t.URL, "/online/"+key+".vtt") {
			writeJSON(w, 200, t)
			return
		}
	}
	writeErr(w, 500, "subtitle saved but not listed")
}

// onlineTracks are an item's fetched subtitles as tracks served through a
// playback session.
func (s *Server) onlineTracks(ctx context.Context, itemID, sid string) []subtitleTrack {
	subs, _ := s.DB.OnlineSubtitles(ctx, itemID)
	var out []subtitleTrack
	for i, o := range subs {
		title := o.Name
		if title == "" {
			title = o.Provider
		}
		out = append(out, subtitleTrack{Index: onlineSubIndex + i, Language: o.Language, Title: title + " (online)", Codec: "webvtt",
			URL: fmt.Sprintf("/stream/%s/online/%s.vtt", sid, o.Key)})
	}
	return out
}

// serveOnlineSubtitle serves a fetched subtitle to a session playing the
// item it was fetched for.
func (s *Server) serveOnlineSubtitle(w http.ResponseWriter, r *http.Request, itemID, name string) {
	key := strings.TrimSuffix(name, ".vtt")
	if !onlineSubKey.MatchString(key) {
		http.NotFound(w, r)
		return
	}
	subs, _ := s.DB.OnlineSubtitles(r.Context(), itemID)
	for _, o := range subs {
		if o.Key == key {
			w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
			w.Header().Set("Cache-Control", "private, max-age=86400")
			http.ServeFile(w, r, filepath.Join(s.onlineSubsDir(), key+".vtt"))
			return
		}
	}
	http.NotFound(w, r)
}
