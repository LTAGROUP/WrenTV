package stream

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/media"
)

// ---------- subtitles ----------
//
// Subtitles inside a file are spread through it, so converting one to WebVTT
// means reading the whole file: minutes for a large file on a network mount.
// The conversion runs once per file and track into a cache shared by every
// session, and requests get whatever has been converted so far. The
// X-Subtitle-Complete header says whether the track is finished; players
// fetch it again until it is.

type subExtraction struct {
	done chan struct{}
	err  string
}

func (s *Session) serveSubtitle(w http.ResponseWriter, r *http.Request, typeIdx int) {
	var sub *media.Stream
	for i := range s.Info.Subtitles {
		if s.Info.Subtitles[i].TypeIdx == typeIdx {
			sub = &s.Info.Subtitles[i]
		}
	}
	if sub == nil || !sub.TextBased {
		http.Error(w, "subtitle not available as text", http.StatusNotFound)
		return
	}
	dir := s.m.cfg.SubtitleDir
	if dir == "" {
		dir = filepath.Join(s.dir, "subs")
	}
	cache := filepath.Join(dir, fmt.Sprintf("%s-%d-%d.vtt", safeName(s.FileID), s.Info.Size, typeIdx))
	complete := true
	if _, err := os.Stat(cache); err != nil {
		complete = false
		x := s.m.extractSubtitle(s, cache, typeIdx)
		// Short tracks and cached-on-disk files finish quickly; wait a moment
		// so most requests get the whole track in one go.
		select {
		case <-x.done:
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
			return
		}
		select {
		case <-x.done:
			if x.err != "" {
				http.Error(w, "subtitle extraction failed: "+x.err, http.StatusInternalServerError)
				return
			}
			complete = true
		default:
		}
	}
	name := cache
	if !complete {
		name = cache + ".part"
	}
	b, err := os.ReadFile(name)
	if err != nil || len(b) == 0 {
		b = []byte("WEBVTT\n\n")
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Subtitle-Complete", fmt.Sprint(complete))
	w.Write(b)
}

// extractSubtitle starts (or joins) converting one subtitle track to WebVTT.
// It runs for as long as the session that started it.
func (m *Manager) extractSubtitle(s *Session, cache string, typeIdx int) *subExtraction {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x, ok := m.subs[cache]; ok {
		select {
		case <-x.done:
			if x.err == "" {
				return x
			}
			// A failed or cancelled run: try again.
		default:
			return x
		}
	}
	x := &subExtraction{done: make(chan struct{})}
	m.subs[cache] = x
	go func(ctx context.Context) {
		defer close(x.done)
		_ = os.MkdirAll(filepath.Dir(cache), 0o755)
		part := cache + ".part"
		cmd := exec.CommandContext(ctx, m.cfg.FFmpeg, "-nostdin", "-v", "error", "-y", "-i", s.Src.Input(),
			"-map", fmt.Sprintf("0:s:%d", typeIdx), "-f", "webvtt", "-flush_packets", "1", part)
		if out, err := cmd.CombinedOutput(); err != nil {
			x.err = strings.TrimSpace(string(out))
			if x.err == "" {
				x.err = err.Error()
			}
			return
		}
		if err := os.Rename(part, cache); err != nil {
			x.err = err.Error()
			return
		}
		m.mu.Lock()
		delete(m.subs, cache) // the file on disk answers from now on
		m.mu.Unlock()
	}(s.ctx)
	return x
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '.' {
			return '_'
		}
		return r
	}, s)
}
