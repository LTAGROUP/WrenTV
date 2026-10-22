package stream

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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
// session. Most players load a subtitle file once, so a request waits for
// the whole track, with the cues sent as they're converted. Players that
// fetch again (the web player) ask with ?partial=1 and get whatever has been
// converted so far; the X-Subtitle-Complete header says whether that's all.

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
			if r.URL.Query().Get("partial") != "1" {
				streamSubtitle(w, r, cache+".part", x)
				return
			}
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

// streamSubtitle sends a track as its conversion writes it, whole cues at a
// time, and ends when the conversion does. Blank lines (harmless between
// cues) keep the connection alive while it's between cues, so players don't
// time out the read.
func streamSubtitle(w http.ResponseWriter, r *http.Request, part string, x *subExtraction) {
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Subtitle-Complete", "true")
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	io.WriteString(w, "WEBVTT\n\n")
	flush()
	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	var pending []byte
	header := true // the file's own WEBVTT header, already sent
	buf := make([]byte, 64<<10)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	idle := 0
	for {
		finished := isDone(x)
		if f == nil {
			// The open handle keeps reading after the finished file is renamed.
			f, _ = os.Open(part)
			if f == nil && finished {
				f, _ = os.Open(strings.TrimSuffix(part, ".part"))
			}
		}
		if f != nil {
			for {
				n, err := f.Read(buf)
				pending = append(pending, buf[:n]...)
				if n == 0 || err != nil {
					break
				}
			}
		}
		if header {
			if i := strings.Index(string(pending), "\n\n"); i >= 0 {
				pending, header = pending[i+2:], false
			}
		}
		sent := false
		if !header {
			// Whole cues only: a cue cut short would show cut short.
			end := len(pending)
			if !finished {
				end = strings.LastIndex(string(pending), "\n\n") + 2
			}
			if end > 1 {
				w.Write(pending[:end])
				pending, sent = pending[end:], true
			}
		}
		if finished {
			flush()
			return
		}
		if sent {
			idle = 0
			flush()
		} else if idle++; idle >= 4 {
			idle = 0
			io.WriteString(w, "\n")
			flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-x.done:
		case <-tick.C:
		}
	}
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
	// Give the whole-track conversion a moment; past that, read just this
	// window from the file. Players don't fetch a segment twice, so an empty
	// answer would leave these 30 seconds without subtitles for good.
	cues, covered := s.convertedCues(r.Context(), typeIdx, end, time.Now().Add(2*time.Second))
	if !covered {
		if win, err := s.windowCues(r.Context(), typeIdx, n); err == nil {
			cues = win
		} else if r.Context().Err() != nil {
			return
		} else {
			slog.Debug("subtitle window", "session", s.ID, "track", typeIdx, "segment", n, "err", err)
			cues, _ = s.convertedCues(r.Context(), typeIdx, end, time.Now().Add(18*time.Second))
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

// convertedCues waits until the whole-track conversion has finished or got
// past end, or the deadline passes, and returns its cues so far.
func (s *Session) convertedCues(ctx context.Context, typeIdx int, end float64, deadline time.Time) ([]vttCue, bool) {
	cache := s.subtitleCache(typeIdx)
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
		cues := parseVTT(string(b), complete)
		if complete || (len(cues) > 0 && cues[len(cues)-1].start >= end) {
			return cues, true
		}
		if time.Now().After(deadline) {
			return cues, false
		}
		select {
		case <-ctx.Done():
			return cues, false
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// windowCues converts one segment's window of a subtitle track by seeking
// to it, which reads about that much of the file instead of all of it up
// to there. It starts a little early to catch cues already on screen.
func (s *Session) windowCues(ctx context.Context, typeIdx, n int) ([]vttCue, error) {
	key := fmt.Sprintf("%d/%d", typeIdx, n)
	s.subMu.Lock()
	cues, ok := s.subWin[key]
	s.subMu.Unlock()
	if ok {
		return cues, nil
	}
	start := float64(n) * subSegLen
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.m.cfg.FFmpeg, "-nostdin", "-v", "error", "-ss", fmtTS(math.Max(0, start-10)), "-i", s.Src.Input(),
		"-copyts", "-map", fmt.Sprintf("0:s:%d", typeIdx), "-to", fmtTS(start+subSegLen), "-f", "webvtt", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	cues = parseVTT(string(out), true)
	s.subMu.Lock()
	if s.subWin == nil {
		s.subWin = map[string][]vttCue{}
	}
	s.subWin[key] = cues
	s.subMu.Unlock()
	return cues, nil
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
