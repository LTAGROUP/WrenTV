package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/onlinesubs"
	"github.com/LTAGROUP/wrentv/internal/stream"
)

// onlineSubIndex is where fetched subtitles' track numbers start, clear of
// any file's own.
const onlineSubIndex = 10000

// syncedSuffix ends a subtitle's copy lined up with a file. It changes with
// how lines are matched, so copies a worse matcher made are made again.
const syncedSuffix = ".a2.vtt"

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
	release, hash := s.subtitleFile(ctx, r, it.ID)
	rs, err := s.Subtitles.Search(ctx, onlinesubs.Query{IMDBID: imdb, Season: season, Episode: episode, Languages: compactLangs(langs),
		Release: release, Hash: hash})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "results": nonNil(rs)})
}

// subtitleFile is the release name and OpenSubtitles hash of the file the
// viewer is playing for an item (the session sid, else their latest one), or
// of its first file when they aren't. Search ranks subtitles timed for it first.
func (s *Server) subtitleFile(ctx context.Context, r *http.Request, itemID string) (release, hash string) {
	var sess *stream.Session
	if sid := r.URL.Query().Get("sid"); sid != "" {
		sess = s.Streams.Get(sid)
	}
	if sess == nil || sess.ItemID != itemID {
		sess = nil
		uid := principal(r).User.ID
		for _, x := range s.Streams.List() {
			if x.ItemID == itemID && x.UserID == uid && (sess == nil || x.Started.After(sess.Started)) {
				sess = x
			}
		}
	}
	var f *db.MediaFile
	if sess != nil {
		f, _ = s.DB.GetFile(ctx, sess.FileID)
	}
	if f == nil {
		if fs, _ := s.DB.FilesForItem(ctx, itemID); len(fs) > 0 {
			f = fs[0]
		}
	}
	if f == nil {
		return "", ""
	}
	if f.StreamID != "" {
		if st, err := s.DB.GetStream(ctx, f.StreamID); err == nil && st != nil {
			release = firstNonEmpty(st.FileName, st.Release)
		}
	} else {
		release = filepath.Base(f.Path)
	}
	if sess != nil && sess.FileID == f.ID {
		hash = fileHash(ctx, f.ID, sess.Src)
	}
	return release, hash
}

// fileHashes keeps each file's OpenSubtitles hash: reading a streamed file's
// ends can take a few seconds.
var fileHashes sync.Map // file id -> hash

func fileHash(ctx context.Context, fileID string, src stream.Source) string {
	if h, ok := fileHashes.Load(fileID); ok {
		return h.(string)
	}
	ra := src.ReaderAt()
	if ra == nil {
		return ""
	}
	done := make(chan string, 1)
	go func() {
		h, err := onlinesubs.Hash(ra, src.Size())
		if err != nil {
			slog.Debug("subtitle hash", "file", fileID, "err", err)
		}
		done <- h
	}()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case h := <-done:
		if h != "" {
			fileHashes.Store(fileID, h)
		}
		return h
	case <-ctx.Done():
		return ""
	}
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
// item it was fetched for, moved onto the file's timing when the file has a
// text subtitle of its own to line it up with.
func (s *Server) serveOnlineSubtitle(w http.ResponseWriter, r *http.Request, sess *stream.Session, name string) {
	key := strings.TrimSuffix(name, ".vtt")
	if !onlineSubKey.MatchString(key) {
		http.NotFound(w, r)
		return
	}
	subs, _ := s.DB.OnlineSubtitles(r.Context(), sess.ItemID)
	for _, o := range subs {
		if o.Key != key {
			continue
		}
		raw := filepath.Join(s.onlineSubsDir(), key+".vtt")
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		synced := filepath.Join(s.onlineSubsDir(), "synced", key+"-"+sess.FileID+syncedSuffix)
		if _, err := os.Stat(synced); err == nil {
			w.Header().Set("Cache-Control", "private, max-age=86400")
			http.ServeFile(w, r, synced)
			return
		}
		ref, sameLang, ok := syncReference(sess, o.Language)
		b, err := os.ReadFile(raw)
		if !ok || err != nil {
			w.Header().Set("Cache-Control", "private, max-age=86400")
			http.ServeFile(w, r, raw)
			return
		}
		// Lining it up needs the file's own track, which is read along with
		// the file, and players wait for a side-loaded subtitle before they
		// play. So the answer is never held: every cue, moved by what the
		// track read so far says (the stretch reached last carries on past
		// it), and X-Subtitle-Complete false until the track is all read, for
		// players to ask again.
		s.syncInBackground(sess, raw, ref, sameLang, synced)
		cues := onlinesubs.ParseVTT(string(b), true)
		var al onlinesubs.Alignment
		complete := true
		if text, done, _, err := sess.SubtitleText(ref); err == nil {
			refCues := onlinesubs.ParseVTT(text, done)
			refEnd := math.Inf(1)
			if complete = done; !done {
				refEnd = 0
				if n := len(refCues); n > 0 {
					refEnd = refCues[n-1].Start
				}
			}
			al = onlinesubs.Align(cues, refCues, sameLang, refEnd)
		}
		var out strings.Builder
		out.WriteString("WEBVTT\n\n")
		for _, c := range al.Apply(cues) {
			out.WriteString(onlinesubs.WriteCue(c))
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Subtitle-Complete", fmt.Sprint(complete))
		io.WriteString(w, out.String())
		return
	}
	http.NotFound(w, r)
}

// syncReference picks the file's own text subtitle to line a fetched one up
// with: one in the same language (matched by its words), else any. Forced
// tracks are too sparse to go by.
func syncReference(sess *stream.Session, lang string) (typeIdx int, sameLang, ok bool) {
	if sess.Info == nil {
		return 0, false, false
	}
	lang = db.NormalizeLang(lang)
	typeIdx = -1
	for _, st := range sess.Info.Subtitles {
		if !st.TextBased || st.Forced {
			continue
		}
		if same := lang != "" && db.NormalizeLang(st.Language) == lang; typeIdx < 0 || same && !sameLang {
			typeIdx, sameLang = st.TypeIdx, same
		}
	}
	return typeIdx, sameLang, typeIdx >= 0
}

// syncing holds the lined-up copies being made, by path.
var syncing sync.Map

// syncInBackground lines the fetched subtitle at raw up with the file's track
// ref once the session has converted it, and keeps the result at cache. A
// session that ends first stops the conversion; the next play tries again.
func (s *Server) syncInBackground(sess *stream.Session, raw string, ref int, sameLang bool, cache string) {
	if _, busy := syncing.LoadOrStore(cache, true); busy {
		return
	}
	go func() {
		defer syncing.Delete(cache)
		b, err := os.ReadFile(raw)
		if err != nil {
			return
		}
		for {
			text, complete, done, err := sess.SubtitleText(ref)
			if err != nil {
				slog.Debug("subtitle sync: reference track not read", "file", sess.FileID, "err", err)
				return
			}
			if complete {
				cues := onlinesubs.ParseVTT(string(b), true)
				s.keepSynced(cache, sess, cues, onlinesubs.Align(cues, onlinesubs.ParseVTT(text, true), sameLang, math.Inf(1)))
				return
			}
			<-done
		}
	}()
}

// keepSynced saves a fetched subtitle moved onto a file's timing, so the
// next play of the file sends it straight away.
func (s *Server) keepSynced(path string, sess *stream.Session, cues []onlinesubs.Cue, al onlinesubs.Alignment) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, c := range al.Apply(cues) {
		b.WriteString(onlinesubs.WriteCue(c))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path+".tmp", []byte(b.String()), 0o644); err == nil {
		os.Rename(path+".tmp", path)
	}
	slog.Info("synced online subtitle", "file", sess.FileID, "subtitle", filepath.Base(path), "aligned", al.OK(), "stretches", al.String())
}

// RunOnlineSubtitleSweep deletes fetched subtitle files no item lists any
// more (their titles were removed), at start and then daily.
func (s *Server) RunOnlineSubtitleSweep(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		s.sweepOnlineSubtitles(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) sweepOnlineSubtitles(ctx context.Context) {
	entries, err := os.ReadDir(s.onlineSubsDir())
	if err != nil {
		return
	}
	keep, err := s.DB.OnlineSubtitleKeys(ctx)
	if err != nil {
		return
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		key := strings.TrimSuffix(strings.TrimSuffix(name, ".tmp"), ".vtt")
		if e.IsDir() || !onlineSubKey.MatchString(key) || keep[key] {
			continue
		}
		// A download in progress is saved before it's listed; leave new files.
		if info, err := e.Info(); err != nil || time.Since(info.ModTime()) < time.Hour {
			continue
		}
		if os.Remove(filepath.Join(s.onlineSubsDir(), name)) == nil {
			removed++
		}
	}
	// Copies moved onto a file's timing go with their subtitle.
	synced := filepath.Join(s.onlineSubsDir(), "synced")
	if entries, err := os.ReadDir(synced); err == nil {
		for _, e := range entries {
			key, _, _ := strings.Cut(e.Name(), "-")
			old := !strings.HasSuffix(e.Name(), syncedSuffix) // lined up by an older matcher
			if info, err := e.Info(); err != nil || !old && (keep[key] || time.Since(info.ModTime()) < time.Hour) {
				continue
			}
			if os.Remove(filepath.Join(synced, e.Name())) == nil {
				removed++
			}
		}
	}
	if removed > 0 {
		slog.Info("removed unused online subtitles", "files", removed)
	}
}
