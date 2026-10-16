package stream

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	cache := s.subtitleCache(typeIdx)
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

// ---------- subtitles inside HLS ----------
//
// Text subtitles are also offered as HLS subtitle renditions, so players with
// their own subtitle menus (AVKit's, Safari's) list them alongside any
// captions in the video. Each segment is a window of the converted track;
// a segment past the conversion so far waits for it to catch up.

const subSegLen = 30.0

// subtitleRenditions writes EXT-X-MEDIA lines for the text subtitles and
// reports whether there were any. None are on by default: clients pick.
func (s *Session) subtitleRenditions(b *strings.Builder) bool {
	any := false
	for _, st := range s.Info.Subtitles {
		if !st.TextBased {
			continue
		}
		name := subtitleName(st)
		lang := ""
		if l := st.Language; l != "" && l != "und" {
			lang = fmt.Sprintf(",LANGUAGE=%q", l)
		}
		forced := ""
		if st.Forced {
			forced = ",FORCED=YES"
		}
		fmt.Fprintf(b, "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=%q%s,DEFAULT=NO,AUTOSELECT=NO%s,URI=\"subs/%d.m3u8\"\n",
			name, lang, forced, st.TypeIdx)
		any = true
	}
	return any
}

// subtitleName is unique per track, so clients can find a track by name.
func subtitleName(st media.Stream) string {
	name := st.Title
	if name == "" {
		name = strings.ToUpper(st.Language)
	}
	if name == "" || name == "UND" {
		name = "Subtitles"
	}
	return fmt.Sprintf("%s (%d)", name, st.TypeIdx+1)
}

func (s *Session) textSubtitle(typeIdx int) *media.Stream {
	for i := range s.Info.Subtitles {
		if st := &s.Info.Subtitles[i]; st.TypeIdx == typeIdx && st.TextBased {
			return st
		}
	}
	return nil
}

func (s *Session) serveSubtitlePlaylist(w http.ResponseWriter, r *http.Request, typeIdx int) {
	if s.textSubtitle(typeIdx) == nil {
		http.NotFound(w, r)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n", int(subSegLen))
	for n, at := 0, 0.0; at < s.Info.Duration; n, at = n+1, at+subSegLen {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%d/%d.vtt\n", math.Min(subSegLen, s.Info.Duration-at), typeIdx, n)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	io.WriteString(w, b.String())
}

func (s *Session) serveSubtitleSegment(w http.ResponseWriter, r *http.Request, typeIdx, n int) {
	if s.textSubtitle(typeIdx) == nil || n < 0 {
		http.NotFound(w, r)
		return
	}
	start, end := float64(n)*subSegLen, float64(n+1)*subSegLen
	cache := s.subtitleCache(typeIdx)
	var cues []vttCue
	deadline := time.Now().Add(20 * time.Second)
	for {
		complete := true
		if _, err := os.Stat(cache); err != nil {
			complete = false
			if x := s.m.extractSubtitle(s, cache, typeIdx); isDone(x) && x.err == "" {
				complete = true
			}
		}
		name := cache
		if !complete {
			name += ".part"
		}
		b, _ := os.ReadFile(name)
		cues = parseVTT(string(b), complete)
		// Done when the track is, or when it has passed this window.
		if complete || (len(cues) > 0 && cues[len(cues)-1].start >= end) || time.Now().After(deadline) {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	var b strings.Builder
	// Media timestamps start at tsOffset (90 kHz clock); cue times count from 0.
	fmt.Fprintf(&b, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:%d,LOCAL:00:00:00.000\n\n", int64(tsOffset*90000))
	for _, c := range cues {
		if c.end > start && c.start < end {
			fmt.Fprintf(&b, "%s --> %s\n%s\n\n", vttTime(c.start), vttTime(c.end), c.text)
		}
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	io.WriteString(w, b.String())
}

func (s *Session) subtitleCache(typeIdx int) string {
	dir := s.m.cfg.SubtitleDir
	if dir == "" {
		dir = filepath.Join(s.dir, "subs")
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%d-%d.vtt", safeName(s.FileID), s.Info.Size, typeIdx))
}

func isDone(x *subExtraction) bool {
	select {
	case <-x.done:
		return true
	default:
		return false
	}
}

type vttCue struct {
	start, end float64
	text       string
}

// parseVTT reads cues; a file still being written may end mid-cue, so its
// last block waits for the next read unless the file is complete.
func parseVTT(text string, complete bool) []vttCue {
	blocks := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n\n")
	if !complete && len(blocks) > 0 {
		blocks = blocks[:len(blocks)-1]
	}
	var out []vttCue
	for _, blk := range blocks {
		lines := strings.Split(strings.Trim(blk, "\n"), "\n")
		for i, l := range lines {
			a, z, ok := strings.Cut(l, "-->")
			if !ok {
				continue
			}
			start, ok1 := parseVTTTime(a)
			end, ok2 := parseVTTTime(strings.Fields(z + " x")[0])
			body := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			if ok1 && ok2 && body != "" && end > start {
				out = append(out, vttCue{start, end, body})
			}
			break
		}
	}
	return out
}

func parseVTTTime(s string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	t := 0.0
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.Replace(p, ",", ".", 1), 64)
		if err != nil {
			return 0, false
		}
		t = t*60 + v
	}
	return t, true
}

func vttTime(t float64) string {
	ms := int64(math.Round(t * 1000))
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
