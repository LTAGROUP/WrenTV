package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/haven/internal/db"
	"github.com/LTAGROUP/haven/internal/library"
	"github.com/LTAGROUP/haven/internal/media"
	"github.com/LTAGROUP/haven/internal/stream"
	"github.com/LTAGROUP/haven/internal/torrent"
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
	FileID    string          `json:"fileId,omitempty"`
}

// handlePlay picks a file, decides how the client should play it and opens a session.
// Torrent-backed items that are between sources return status "pending"; clients
// show a "finding a better source" state and retry.
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
	files, err := s.DB.FilesForItem(ctx, it.ID)
	if err != nil || len(files) == 0 {
		writeJSON(w, 202, playResponse{Status: "pending", Message: "Finding a source…"})
		return
	}
	f := pickFile(files, req.FileID)

	if f.TorrentID != "" {
		t, err := s.DB.GetTorrent(ctx, f.TorrentID)
		if err != nil {
			writeErr(w, 404, "torrent not found")
			return
		}
		switch t.Status {
		case torrent.StatusReady:
		case torrent.StatusFailed:
			writeErr(w, http.StatusFailedDependency, "No playable source could be found for this title.")
			return
		case torrent.StatusResolving:
			writeJSON(w, 202, playResponse{Status: "pending", Message: "Preparing stream…"})
			return
		default:
			writeJSON(w, 202, playResponse{Status: "pending", Message: "Finding a better source…"})
			return
		}
	}

	src, err := s.openSource(ctx, f)
	if errors.Is(err, torrent.ErrPending) {
		writeJSON(w, 202, playResponse{Status: "pending", Message: "Preparing stream…"})
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not open media: "+err.Error())
		return
	}

	info, err := s.ensureProbe(ctx, f, src)
	if err != nil {
		src.Close()
		writeErr(w, http.StatusBadGateway, "could not read media: "+err.Error())
		return
	}
	audioIdx := -1
	if req.AudioIndex != nil {
		audioIdx = *req.AudioIndex
	}
	dec := media.Decide(info, req.Caps, audioIdx)

	var kf []float64
	if dec.Method == media.MethodRemux {
		kf, err = s.ensureKeyframes(ctx, f, src)
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
	sess, err := s.Streams.Create(stream.NewSessionOpts{
		UserID: principal(r).User.ID, ItemID: it.ID, FileID: f.ID, Src: src, Info: info, Decision: dec,
		Keyframes: kf, KbpsTarget: kbps, MaxWidth: req.MaxWidth,
	})
	if err != nil {
		src.Close()
		writeErr(w, 500, err.Error())
		return
	}

	resp := playResponse{
		Status: "ready", SessionID: sess.ID, Engine: dec.Engine, Method: dec.Method, Decision: &dec,
		Duration: info.Duration, Info: info, FileID: f.ID,
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

func (s *Server) handleStopSession(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sid")
	if sess := s.Streams.Get(sid); sess != nil && (sess.UserID == principal(r).User.ID || principal(r).IsAdmin()) {
		s.Streams.Stop(sid)
	}
	w.WriteHeader(http.StatusNoContent)
}

func pickFile(files []*db.MediaFile, id string) *db.MediaFile {
	for _, f := range files {
		if f.ID == id {
			return f
		}
	}
	// Prefer a local file over a torrent, then the highest resolution.
	var best *db.MediaFile
	for _, f := range files {
		if best == nil || betterFile(f, best) {
			best = f
		}
	}
	return best
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

func betterFile(a, b *db.MediaFile) bool {
	if (a.TorrentID == "") != (b.TorrentID == "") {
		return a.TorrentID == ""
	}
	if ha, hb := fileHeight(a), fileHeight(b); ha != hb {
		return ha > hb
	}
	return a.Size > b.Size
}

func (s *Server) openSource(ctx context.Context, f *db.MediaFile) (stream.Source, error) {
	if f.TorrentID == "" {
		return stream.OpenFile(f.Path)
	}
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return s.Torrents.SourceFor(tctx, f)
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
