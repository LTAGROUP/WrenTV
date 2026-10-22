package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/media"
	"github.com/LTAGROUP/wrentv/internal/stream"
	"github.com/LTAGROUP/wrentv/internal/streaming"
	"github.com/LTAGROUP/wrentv/internal/thumbs"
)

type playRequest struct {
	FileID     string           `json:"fileId"`
	AudioIndex *int             `json:"audioIndex"`
	Caps       media.ClientCaps `json:"caps"`
	MaxWidth   int              `json:"maxWidth"`
}

type subtitleTrack struct {
	Index    int    `json:"index"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Codec    string `json:"codec"`
	Forced   bool   `json:"forced"`
	Default  bool   `json:"default"`
	URL      string `json:"url,omitempty"` // WebVTT, for text subtitles
}

type playResponse struct {
	Status    string          `json:"status"` // "ready" or "pending"
	Message   string          `json:"message,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Engine    string          `json:"engine,omitempty"`
	Method    media.Method    `json:"method,omitempty"`
	URL       string          `json:"url,omitempty"`
	Decision  *media.Decision `json:"decision,omitempty"`
	Duration  float64         `json:"duration,omitempty"`
	Resume    float64         `json:"resumePosition,omitempty"`
	Info      *media.Info     `json:"info,omitempty"`
	Subtitles []subtitleTrack `json:"subtitles,omitempty"`
	// SubtitleIndex is the track to show from the viewer's preferences, or -1 for off.
	SubtitleIndex int    `json:"subtitleIndex"`
	FileID        string `json:"fileId,omitempty"`
	// Chapters and the skippable intro/credits of the file being played.
	Chapters []media.Chapter `json:"chapters"`
	Markers  media.Markers   `json:"markers"`
	// Versions are the item's files; clients offer them as a picker.
	Versions []versionView `json:"versions,omitempty"`
	// Thumbs are frames for the seek bar, when the server can make them.
	Thumbs *thumbsView `json:"thumbs,omitempty"`
}

// thumbsView: frame n (url + n + ".jpg") shows the picture at n × interval seconds.
type thumbsView struct {
	URL      string  `json:"url"`
	Interval float64 `json:"interval"`
	Width    int     `json:"width"`
}

type versionView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Height int    `json:"height"`
}

func versionViews(files []*db.MediaFile) []versionView {
	out := []versionView{}
	for _, v := range files {
		out = append(out, versionView{ID: v.ID, Label: versionLabel(v), Height: fileHeight(v)})
	}
	return out
}

// handleVersions lists an item's versions as the player offers them. A
// streamed title's lookup goes on after play starts, so players ask again
// while "looking" is true, and when their picker opens.
func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok || (it.Kind != "movie" && it.Kind != "episode") {
		writeErr(w, 404, "playable item not found")
		return
	}
	files, err := s.playableFiles(r.Context(), it)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	looking := s.Streaming != nil && s.Streaming.Looking(it.ID)
	writeJSON(w, 200, map[string]any{"versions": versionViews(files), "looking": looking})
}

// handlePlay picks a file, decides how the client should play it and opens a session.
// Streamed titles whose streams are still being looked up return status
// "pending"; clients show the message and retry.
func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok || (it.Kind != "movie" && it.Kind != "episode") {
		writeErr(w, 404, "playable item not found")
		return
	}
	var req playRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(req.Caps.Engines) == 0 {
		req.Caps.Engines = []string{"avplayer"}
	}
	ctx := r.Context()
	p := principal(r)
	if err := s.admitPlayback(p.User, it.ID); err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	// A player that retries after an admin stopped it mustn't just start again.
	if msg, stopped := s.act().refused(p.User.ID, it.ID); stopped {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	streamed := s.isStreamLibrary(ctx, it.LibraryID)
	// steps times each part of starting playback, for the debug log.
	t0, last, steps := time.Now(), time.Now(), []any{}
	left := func() time.Duration { return max(time.Until(t0.Add(playBudget)), 0) }
	step := func(name string) {
		steps = append(steps, name, time.Since(last).Round(time.Millisecond))
		last = time.Now()
	}
	if streamed {
		if !s.limitsFor(p.User).CanStream {
			writeErr(w, http.StatusForbidden, "streaming isn't available on this server")
			return
		}
		// Look up streams on first play; wait a little so a quick lookup
		// starts playback at once, else the client retries.
		ready, err := s.Streaming.Ensure(ctx, it, left())
		step("lookup")
		if !ready {
			if err != nil {
				if local, _ := s.DB.FileEquivalent(ctx, it); local == nil {
					writeErr(w, http.StatusFailedDependency, streamErrMessage(err))
					return
				}
			} else {
				writeJSON(w, 202, playResponse{Status: "pending", Message: "Finding a stream…"})
				return
			}
		}
	}
	files, err := s.playableFiles(ctx, it)
	if err != nil || len(files) == 0 {
		writeJSON(w, 202, playResponse{Status: "pending", Message: "Finding a source…"})
		return
	}
	f := chooseFile(files, req)
	if f.StreamID != "" && !s.prepared(ctx, f, req.Caps) {
		// Probing a stream and indexing its keyframes can take a while:
		// it's done apart from this request, which hands back to the
		// client rather than run past its timeout. The title's page may
		// already be doing it, and this joins that.
		t := time.NewTimer(left())
		select {
		case <-s.prepare(f, req.Caps):
			t.Stop()
			if fresh, err := s.DB.GetFile(ctx, f.ID); err == nil {
				f = fresh
			}
		case <-t.C:
			writeJSON(w, 202, playResponse{Status: "pending", Message: "Getting the stream ready…"})
			return
		case <-ctx.Done():
			t.Stop()
			return
		}
		step("prepare")
	}

	src, err := s.openSource(ctx, f)
	step("open")
	if f.StreamID != "" && errors.Is(err, streaming.ErrUnavailable) {
		// The stored stream went away: find another.
		_, _ = s.Streaming.Ensure(ctx, it, 0)
		writeJSON(w, 202, playResponse{Status: "pending", Message: "Finding another stream…"})
		return
	}
	if errors.Is(err, streaming.ErrBusy) {
		writeErr(w, http.StatusServiceUnavailable, "The server is playing as many streams as it allows. Try again shortly.")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not open media: "+err.Error())
		return
	}
	if streamed {
		s.Streaming.Prefetch(it)
		if s.Cfg.Streaming.Mode == "download" && f.StreamID != "" {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if _, err := s.Streaming.QueueDownload(ctx, it, p.User.ID); err != nil {
					slog.Debug("auto download", "item", it.ID, "err", err)
				}
			}()
		}
	}

	info, err := s.ensureProbe(ctx, f, src)
	step("probe")
	if err != nil {
		src.Close()
		writeErr(w, http.StatusBadGateway, "could not read media: "+err.Error())
		return
	}
	if f.StreamID != "" && s.checkLengths(ctx, it, files, false)[f.StreamID] {
		// Longer or shorter than the other versions: an ad spliced in, or cut.
		src.Close()
		writeJSON(w, 202, playResponse{Status: "pending", Message: "Finding another stream…"})
		return
	}
	if why := notTheTitle(it, info); why != "" && f.StreamID != "" {
		// A trailer, a promo or another film: find the real one.
		src.Close()
		s.Streaming.Reject(f.StreamID, why)
		writeJSON(w, 202, playResponse{Status: "pending", Message: "Finding another stream…"})
		return
	}
	prefs, _ := s.DB.GetPrefs(ctx, principal(r).User.ID)
	audioIdx := preferredAudio(info, prefs.AudioLanguage)
	if req.AudioIndex != nil {
		audioIdx = *req.AudioIndex
	}
	dec := media.Decide(info, req.Caps, audioIdx)

	var kf []float64
	if dec.Method == media.MethodRemux {
		kf, err = s.ensureKeyframes(ctx, f, src)
		step("keyframes")
		if err != nil || len(kf) < 2 {
			slog.Warn("keyframe index unavailable, falling back", "file", f.ID, "err", err)
			req.Caps.ForceEngine = ""
			if contains(req.Caps.Engines, "mpv") {
				dec = media.Decision{Engine: "mpv", Method: media.MethodDirect, VideoCopy: true, AudioCopy: true, AudioIndex: dec.AudioIndex,
					Reasons: []string{"no keyframe index for remux; direct play in mpv"}}
			} else {
				dec.Method = media.MethodTranscode
				dec.AudioCodec = "aac"
				dec.AudioCopy = false
				dec.Reasons = append(dec.Reasons, "no keyframe index; transcoding")
			}
		}
	}

	kbps := req.Caps.MaxKbps
	if kbps <= 0 {
		kbps = 20000
	}
	slog.Debug("play start", append([]any{"item", it.ID, "total", time.Since(t0).Round(time.Millisecond)}, steps...)...)
	sess, err := s.Streams.Create(stream.NewSessionOpts{
		UserID: p.User.ID, ItemID: it.ID, FileID: f.ID, Src: src, Info: info, Decision: dec,
		Keyframes: kf, KbpsTarget: kbps, MaxWidth: req.MaxWidth,
	})
	if err != nil {
		src.Close()
		writeErr(w, 500, err.Error())
		return
	}
	// The player was closed while this was set up: it will never learn the
	// session's id to stop it, so don't leave it reading the source.
	if ctx.Err() != nil {
		s.Streams.Stop(sess.ID)
		return
	}

	resp := playResponse{
		Status: "ready", SessionID: sess.ID, Engine: dec.Engine, Method: dec.Method, Decision: &dec,
		Duration: info.Duration, Info: info, FileID: f.ID,
		Chapters: nonNil(info.Chapters), Markers: s.markersFor(ctx, f, info),
		SubtitleIndex: preferredSubtitle(info, prefs.SubtitleLanguage),
	}
	resp.Versions = versionViews(files)
	s.recordPlay(r, it, f, sess, dec, info)
	if iv := s.startThumbs(f, src, info, sess); iv > 0 {
		resp.Thumbs = &thumbsView{URL: "/stream/" + sess.ID + "/thumbs/", Interval: iv, Width: thumbs.Width}
	}
	base := "/stream/" + sess.ID + "/"
	if dec.Method == media.MethodDirect {
		resp.URL = base + "file" + extFor(info.Container)
	} else {
		resp.URL = base + "main.m3u8"
	}
	if ws, _ := s.DB.GetWatchState(ctx, principal(r).User.ID, it.ID); ws != nil && !ws.Watched {
		resp.Resume = ws.Position
	}
	for _, st := range info.Subtitles {
		t := subtitleTrack{Index: st.TypeIdx, Language: st.Language, Title: st.Title, Codec: st.Codec, Forced: st.Forced, Default: st.Default}
		if st.TextBased {
			t.URL = fmt.Sprintf("%ssubs/%d.vtt", base, st.TypeIdx)
		}
		resp.Subtitles = append(resp.Subtitles, t)
	}
	writeJSON(w, 200, resp)
}

// markersFor combines chapter-named markers with fingerprint results;
// chapters win where both exist.
func (s *Server) markersFor(ctx context.Context, f *db.MediaFile, info *media.Info) media.Markers {
	m := media.MarkersFromChapters(info.Chapters, info.Duration)
	if m.Intro != nil && m.Credits != nil {
		return m
	}
	fm, err := s.DB.GetFileMarkers(ctx, f.ID)
	if err != nil || fm.Mtime != f.MTime {
		return m
	}
	used := false
	if m.Intro == nil && fm.Intro != nil {
		m.Intro, used = &media.Range{Start: fm.Intro[0], End: fm.Intro[1]}, true
	}
	if m.Credits == nil && fm.Credits != nil {
		m.Credits, used = &media.Range{Start: fm.Credits[0], End: fm.Credits[1]}, true
	}
	if used {
		if m.Source == "" {
			m.Source = "fingerprint"
		} else {
			m.Source += "+fingerprint"
		}
	}
	return m
}

// handleStopSession ends a playback. A player closing calls it for its own
// session; an admin stopping someone's stream from the dashboard passes
// kill=1 and an optional message, which the player is told instead of the
// stream just failing, and which keeps it from starting straight back up.
func (s *Server) handleStopSession(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sid")
	kill := r.URL.Query().Get("kill") == "1" && principal(r).IsAdmin()
	if sess := s.Streams.Get(sid); sess != nil && (sess.UserID == principal(r).User.ID || principal(r).IsAdmin()) {
		if kill {
			s.act().kill(sid, sess.UserID, sess.ItemID, r.URL.Query().Get("message"))
			slog.Info("stream stopped by admin", "session", sid, "by", principal(r).User.ID)
		}
		s.Streams.Stop(sid)
		if s.Tracking != nil {
			s.Tracking.Stopped(sess.UserID, sess.ItemID)
		}
	} else if s.Tracking != nil {
		if itemID := s.DB.PlayItemBySession(r.Context(), sid, principal(r).User.ID); itemID != "" {
			s.Tracking.Stopped(principal(r).User.ID, itemID)
		}
	}
	if ls := s.LiveTV.Get(sid); ls != nil && kill {
		s.act().kill(sid, ls.UserID, "live:"+ls.ChannelID, r.URL.Query().Get("message"))
		slog.Info("live stream stopped by admin", "session", sid, "by", principal(r).User.ID)
	}
	s.LiveTV.Stop(sid, principal(r).User.ID, principal(r).IsAdmin())
	w.WriteHeader(http.StatusNoContent)
}

// chooseFile returns the requested version, else the one this client plays
// best: a version it can play without transcoding beats one that needs it
// (a 1080p file is served instead of transcoding the 4K one down), then a
// version that fits the requested quality, then a file over a stream.
func chooseFile(files []*db.MediaFile, req playRequest) *db.MediaFile {
	for _, f := range files {
		if f.ID == req.FileID {
			return f
		}
	}
	type rank struct {
		tier, streamed, fit, height, srank int
		size                               int64
	}
	rankOf := func(f *db.MediaFile) rank {
		rk := rank{tier: 1, size: -f.Size, srank: f.StreamRank}
		h, w := fileHeight(f), f.Width
		fits := req.MaxWidth <= 0 || (w > 0 && w <= req.MaxWidth*11/10) || (w == 0 && h > 0 && h*16/9 <= req.MaxWidth*11/10)
		if f.Probe != "" {
			var info media.Info
			if json.Unmarshal([]byte(f.Probe), &info) == nil && info.Duration > 0 {
				if media.Decide(&info, req.Caps, -1).Method == media.MethodTranscode || !fits {
					rk.tier = 2
				} else {
					rk.tier = 0
				}
			}
		} else if !fits {
			rk.tier = 2
		}
		if f.StreamID != "" {
			rk.streamed = 1
		}
		if fits {
			rk.height = -h // the best version within the limit
		} else {
			rk.fit, rk.height = 1, h // else the smallest version above it: least work to transcode
		}
		return rk
	}
	less := func(a, b rank) bool {
		switch {
		// A quality's streams keep their order (best first) unless one is
		// known to need transcoding: playing another once doesn't make it
		// the default.
		case a.streamed == 1 && b.streamed == 1 && a.fit == b.fit && a.height == b.height && a.tier < 2 && b.tier < 2:
			if a.srank != b.srank {
				return a.srank < b.srank
			}
		case a.tier != b.tier:
			return a.tier < b.tier
		case a.streamed != b.streamed:
			return a.streamed < b.streamed
		case a.fit != b.fit:
			return a.fit < b.fit
		case a.height != b.height:
			return a.height < b.height
		}
		return a.size < b.size
	}
	var best *db.MediaFile
	var bestRank rank
	for _, f := range files {
		if rk := rankOf(f); best == nil || less(rk, bestRank) {
			best, bestRank = f, rk
		}
	}
	return best
}

// preferredAudio is the typeIndex of the best track in the user's language
// (its default track if that matches, else the first non-commentary one), or
// -1 to keep the file's default.
func preferredAudio(info *media.Info, lang string) int {
	if lang == "" {
		return -1
	}
	best := -1
	for _, a := range info.Audio {
		if db.NormalizeLang(a.Language) != lang || strings.Contains(strings.ToLower(a.Title), "commentary") {
			continue
		}
		if a.Default {
			return a.TypeIdx
		}
		if best < 0 {
			best = a.TypeIdx
		}
	}
	return best
}

// preferredSubtitle is the subtitle track to show for a language, or -1.
// Full tracks beat forced ones (which only cover foreign-language parts),
// text beats pictures (browsers only show text), and default-flagged and
// plain tracks beat SDH and commentary.
func preferredSubtitle(info *media.Info, lang string) int {
	if lang == "" {
		return -1
	}
	best, bestScore := -1, -1
	for _, st := range info.Subtitles {
		if db.NormalizeLang(st.Language) != lang {
			continue
		}
		title := strings.ToLower(st.Title)
		score := 0
		if !st.Forced {
			score += 8
		}
		if st.TextBased {
			score += 4
		}
		if !strings.Contains(title, "sdh") && !strings.Contains(title, "commentary") {
			score += 2
		}
		if st.Default {
			score++
		}
		if score > bestScore {
			best, bestScore = st.TypeIdx, score
		}
	}
	return best
}

// versionLabel names a file for version pickers, e.g. "4K · Dolby Vision · 58.2 GB".
func versionLabel(f *db.MediaFile) string {
	var parts []string
	switch h := fileHeight(f); {
	case f.Width >= 3200 || h >= 2000:
		parts = append(parts, "4K")
	case f.Width >= 1800 || h >= 1000:
		parts = append(parts, "1080p")
	case f.Width >= 1200 || h >= 700:
		parts = append(parts, "720p")
	case h > 0:
		parts = append(parts, fmt.Sprintf("%dp", h))
	}
	switch f.HDR {
	case "DV", "DV+HDR10":
		parts = append(parts, "Dolby Vision")
	case "":
	default:
		parts = append(parts, f.HDR)
	}
	if f.VideoCodec != "" {
		parts = append(parts, strings.ToUpper(f.VideoCodec))
	}
	switch {
	case f.Size >= 1<<30:
		parts = append(parts, fmt.Sprintf("%.1f GB", float64(f.Size)/(1<<30)))
	case f.Size > 0:
		parts = append(parts, fmt.Sprintf("%d MB", max(1, f.Size>>20)))
	}
	if f.StreamID != "" {
		parts = append(parts, cmp.Or(f.Tag, "Stream"))
	}
	if len(parts) == 0 {
		return displayFileName(f.Path)
	}
	return strings.Join(parts, " · ")
}

// recordPlay starts a play-history entry for stats.
func (s *Server) recordPlay(r *http.Request, it *db.Item, f *db.MediaFile, sess *stream.Session, dec media.Decision, info *media.Info) {
	ctx := r.Context()
	p := principal(r)
	ip := clientIP(r)
	s.act().started(sess.ID, ip, clientName(r))
	out := fileHeight(f)
	if d := sess.Delivery(); d.OutHeight > 0 && !dec.VideoCopy {
		out = d.OutHeight
	}
	ps := db.PlayStart{SessionID: sess.ID, UserID: p.User.ID, FileID: f.ID, Method: string(dec.Method), Engine: dec.Engine,
		Client: clientName(r), Item: it, VideoCopy: dec.VideoCopy, Height: fileHeight(f), Duration: info.Duration,
		IP: ip, Quality: versionLabel(f), StreamHeight: out, Reasons: strings.Join(dec.Reasons, "; ")}
	if ws, _ := s.DB.GetWatchState(ctx, p.User.ID, it.ID); ws != nil && !ws.Watched {
		ps.Position = ws.Position
	}
	if it.Kind == "episode" {
		if sh, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
			ps.ShowTitle = sh.Title
			if it.PosterPath == "" {
				c := *it
				c.PosterPath = sh.PosterPath
				ps.Item = &c
			}
		}
	}
	if err := s.DB.StartPlay(ctx, ps); err != nil {
		slog.Warn("play history", "err", err)
	}
}

// clientName describes the player from its User-Agent, e.g. "Chrome on macOS".
func clientName(r *http.Request) string {
	if c := strings.TrimSpace(r.Header.Get("X-WrenTV-Client")); c != "" {
		return c
	}
	ua := r.UserAgent()
	if strings.Contains(ua, "CFNetwork") || strings.Contains(ua, "Wren%20TV") {
		return "Wren TV for Mac"
	}
	browser := "Browser"
	for _, b := range [][2]string{{"Edg/", "Edge"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"}} {
		if strings.Contains(ua, b[0]) {
			browser = b[1]
			break
		}
	}
	os := ""
	for _, o := range [][2]string{{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Android", "Android"}, {"Mac OS X", "macOS"}, {"Windows", "Windows"}, {"CrOS", "ChromeOS"}, {"Linux", "Linux"}} {
		if strings.Contains(ua, o[0]) {
			os = o[1]
			break
		}
	}
	if os == "" {
		return browser
	}
	return browser + " on " + os
}

var reResTag = regexp.MustCompile(`(?i)\b(2160|1440|1080|720|576|480)p\b|\b(4k|uhd)\b`)

// fileHeight is the probed height, else a resolution tag in the file name.
func fileHeight(f *db.MediaFile) int {
	if f.Height > 0 {
		return f.Height
	}
	m := reResTag.FindStringSubmatch(filepath.Base(f.Path))
	switch {
	case m == nil:
		return 0
	case m[1] != "":
		n, _ := strconv.Atoi(m[1])
		return n
	default:
		return 2160
	}
}

func (s *Server) openSource(ctx context.Context, f *db.MediaFile) (stream.Source, error) {
	if f.StreamID == "" {
		return stream.OpenFile(f.Path)
	}
	sctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return s.Streaming.Source(sctx, f)
}

// playableFiles are an item's versions, best first. A streamed title also
// offers the real file of the same movie or episode (which wins), so files
// and streams mix.
func (s *Server) playableFiles(ctx context.Context, it *db.Item) ([]*db.MediaFile, error) {
	files, err := s.DB.FilesForItem(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	if s.isStreamLibrary(ctx, it.LibraryID) {
		if eq, err := s.DB.FileEquivalent(ctx, it); err == nil {
			local, _ := s.DB.FilesForItem(ctx, eq.ID)
			files = append(local, files...)
		}
	}
	return files, nil
}

// admitPlayback enforces a user's limit on simultaneous playbacks. Playing
// the same title again (another version, a restart) doesn't count, and a
// session the user left idle (closed without saying so) makes room.
func (s *Server) admitPlayback(u *db.User, itemID string) error {
	max := s.limitsFor(u).MaxStreams
	if max <= 0 {
		return nil
	}
	var mine []*stream.Session
	for _, x := range s.Streams.List() {
		if x.UserID == u.ID && x.ItemID != itemID {
			mine = append(mine, x)
		}
	}
	if len(mine) < max {
		return nil
	}
	idlest := mine[0]
	for _, x := range mine[1:] {
		if x.Idle() > idlest.Idle() {
			idlest = x
		}
	}
	if idlest.Idle() > 2*time.Minute {
		s.Streams.Stop(idlest.ID)
		return nil
	}
	if max == 1 {
		return errors.New("You're already watching something. Stop it to play this.")
	}
	return fmt.Errorf("You're already watching on %d screens, the most your account allows.", max)
}

func streamErrMessage(err error) string {
	switch {
	case errors.Is(err, streaming.ErrNoStreams):
		return "No stream was found for this title right now."
	case errors.Is(err, streaming.ErrDisabled):
		return "Streaming isn't set up on this server."
	}
	return "Couldn't find a stream: " + err.Error()
}

func (s *Server) ensureProbe(ctx context.Context, f *db.MediaFile, src stream.Source) (*media.Info, error) {
	if f.Probe != "" {
		var info media.Info
		if err := json.Unmarshal([]byte(f.Probe), &info); err == nil && info.Duration > 0 {
			return &info, nil
		}
	}
	info, err := media.Probe(ctx, s.Cfg.Transcode.FFprobePath, src.Input())
	if err != nil {
		return nil, err
	}
	library.ApplyProbe(f, info)
	_ = s.DB.UpdateFileProbe(ctx, f)
	return info, nil
}

func (s *Server) ensureKeyframes(ctx context.Context, f *db.MediaFile, src stream.Source) ([]float64, error) {
	if b, err := s.DB.Keyframes(ctx, f.ID); err == nil && len(b) > 0 {
		var kf []float64
		if json.Unmarshal(b, &kf) == nil && len(kf) > 1 {
			return kf, nil
		}
	}
	kctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	kf, err := media.Keyframes(kctx, s.Cfg.Transcode.FFprobePath, src.Input(), src.ReaderAt(), src.Size())
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(kf)
	_ = s.DB.SetKeyframes(ctx, f.ID, b)
	return kf, nil
}

func extFor(container string) string {
	switch container {
	case "mp4":
		return ".mp4"
	case "mkv":
		return ".mkv"
	case "ts":
		return ".ts"
	case "avi":
		return ".avi"
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// startThumbs makes the file's seek thumbnails in the background. A file on
// disk is done whether or not anyone keeps watching; a streamed one only
// while it plays, after a head start for the player's buffer, so it never
// takes a stream slot of its own.
func (s *Server) startThumbs(f *db.MediaFile, src stream.Source, info *media.Info, sess *stream.Session) float64 {
	v := info.PrimaryVideo()
	if s.Thumbs == nil || v == nil || info.Duration <= 0 {
		return 0
	}
	hw := s.Streams.HW()
	hdr := v.HDR != ""
	// Profile 5 Dolby Vision has no HDR10 picture to fall back on, and HDR
	// without tone mapping comes out grey; no thumbnails beats wrong colours.
	if (v.HDR == "DV" && v.DVProfile == 5) || (hdr && hw.Tonemap != "zscale") {
		return 0
	}
	sw := stream.HW{Kind: "software", Tonemap: hw.Tonemap}
	j := thumbs.Job{FileID: f.ID, Duration: info.Duration, Remote: f.StreamID != "",
		Filter: sw.VideoFilter(v.Width, v.Height, v.BitDepth, thumbs.Width, hdr) + ",format=yuvj420p"}
	if !j.Remote {
		j.Input = f.Path
		return s.Thumbs.Start(context.Background(), j)
	}
	// Reading a streamed file costs bandwidth playback needs, so the frames
	// wait until a player asks for one (most never do), and read at low
	// priority when the source offers it.
	j.Input = src.Input()
	if bg, ok := src.(interface{ BackgroundInput() string }); ok {
		j.Input = bg.BackgroundInput()
	}
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	s.thumbJobs.Store(sess.ID, func() { once.Do(func() { s.Thumbs.Start(ctx, j) }) })
	go func() {
		<-sess.Done()
		cancel()
		s.thumbJobs.Delete(sess.ID)
	}()
	return thumbs.Interval(info.Duration, true)
}

func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request) {
	sess := s.Streams.Get(chi.URLParam(r, "sid"))
	if sess == nil || s.Thumbs == nil {
		http.Error(w, "session expired", http.StatusGone)
		return
	}
	if start, ok := s.thumbJobs.Load(sess.ID); ok {
		start.(func())()
	}
	n, err := strconv.Atoi(strings.TrimSuffix(chi.URLParam(r, "n"), ".jpg"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, ok := s.Thumbs.Nearest(sess.FileID, n)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=600")
	http.ServeFile(w, r, p)
}

// notTheTitle says why a streamed file can't be the item: it has no
// picture, or it is far shorter than the film. "" when it may be.
func notTheTitle(it *db.Item, info *media.Info) string {
	if len(info.Video) == 0 {
		return "no video"
	}
	if it.Kind == "movie" && it.RuntimeMin > 0 && info.Duration > 0 && info.Duration < 0.6*float64(it.RuntimeMin*60) {
		return fmt.Sprintf("runs %.0f min, the film %d min", info.Duration/60, it.RuntimeMin)
	}
	return ""
}
