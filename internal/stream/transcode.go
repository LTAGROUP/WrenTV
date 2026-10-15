package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/haven/internal/media"
)

// ---------- transcode: one continuous ffmpeg per playback position ----------

type transcoder struct {
	s      *Session
	mu     sync.Mutex
	cmd    *exec.Cmd
	cancel context.CancelFunc
	start  int // first segment of the running process
	done   chan struct{}
	err    error
	gen    int
}

func newTranscoder(s *Session) *transcoder { return &transcoder{s: s, start: -1} }

func (t *transcoder) dir() string { return filepath.Join(t.s.dir, "tx") }

func (t *transcoder) init(ctx context.Context) (string, error) {
	p := filepath.Join(t.dir(), "init.mp4")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	t.mu.Lock()
	if t.cmd == nil {
		t.launch(0)
	}
	first := filepath.Join(t.dir(), strconv.Itoa(t.start)+".m4s")
	runInit := filepath.Join(t.dir(), fmt.Sprintf("init_%d.mp4", t.gen))
	t.mu.Unlock()
	// ffmpeg writes its init file in place; once the run's first segment
	// exists it is complete. Publish one stable copy for all runs.
	if err := t.waitFor(ctx, first); err != nil {
		return "", err
	}
	b, err := os.ReadFile(runInit)
	if err != nil {
		return "", err
	}
	if err := writeAtomic(p, b); err != nil {
		return "", err
	}
	return p, nil
}

func (t *transcoder) segment(ctx context.Context, n int) (string, error) {
	p := filepath.Join(t.dir(), strconv.Itoa(n)+".m4s")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	t.mu.Lock()
	// Restart if the request is behind the running encode or too far ahead of it.
	if t.cmd == nil || n < t.start || n > t.produced()+8 {
		t.launch(n)
	}
	t.mu.Unlock()
	return p, t.waitFor(ctx, p)
}

// produced returns the highest finished segment of the running encode.
func (t *transcoder) produced() int {
	hi := t.start - 1
	for i := t.start; i < len(t.s.plan); i++ {
		if _, err := os.Stat(filepath.Join(t.dir(), strconv.Itoa(i)+".m4s")); err != nil {
			break
		}
		hi = i
	}
	return hi
}

func (t *transcoder) waitFor(ctx context.Context, p string) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
		t.mu.Lock()
		done := t.done
		t.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			if _, err := os.Stat(p); err == nil {
				return nil
			}
			t.mu.Lock()
			err := t.err
			t.mu.Unlock()
			if err != nil {
				return err
			}
			return errors.New("transcode ended before segment was produced")
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("timed out waiting for transcode")
}

func (t *transcoder) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	t.cmd = nil
}

// launch must be called with t.mu held.
func (t *transcoder) launch(n int) {
	if t.cancel != nil {
		t.cancel()
	}
	s := t.s
	hw := s.m.cfg.HW
	_ = os.MkdirAll(t.dir(), 0o755)
	startTS := s.plan[n].start
	segLen := s.segLen
	v := s.Info.PrimaryVideo()

	args := []string{"-nostdin", "-hide_banner", "-v", "error", "-y"}
	args = append(args, hw.DecodeArgs()...)
	args = append(args, "-copyts", "-ss", fmtTS(startTS), "-i", s.Src.Input())
	if v != nil {
		args = append(args, "-map", fmt.Sprintf("0:v:%d", v.TypeIdx))
	}
	if s.Decision.AudioIndex >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", s.Decision.AudioIndex))
	}
	if v != nil {
		hdr := v.HDR != ""
		args = append(args, "-vf", hw.VideoFilter(v.Width, v.Height, v.BitDepth, s.MaxWidth, hdr))
		args = append(args, hw.EncodeArgs(s.KbpsTarget)...)
		// Keyframes exactly on segment boundaries so the playlist is deterministic.
		if s.gop > 0 {
			g := strconv.Itoa(s.gop)
			args = append(args, "-g", g, "-keyint_min", g, "-sc_threshold", "0")
			if hw.Kind == "qsv" {
				args = append(args, "-idr_interval", "0")
			}
		} else {
			args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,%s+n_forced*%s)", fmtTS(startTS), fmtTS(segLen)),
				"-sc_threshold", "0")
		}
	}
	if s.Decision.AudioIndex >= 0 {
		if s.Decision.AudioCopy {
			args = append(args, "-c:a", "copy")
		} else {
			args = append(args, "-c:a", "aac", "-b:a", "256k", "-ac", "2")
		}
	}
	args = append(args, "-sn", "-dn", "-map_chapters", "-1", "-map_metadata", "-1",
		"-output_ts_offset", fmtTS(tsOffset),
		"-f", "hls", "-hls_time", fmtTS(segLen), "-hls_list_size", "0", "-hls_segment_type", "fmp4",
		"-hls_flags", "temp_file+independent_segments", "-start_number", strconv.Itoa(n),
		"-hls_fmp4_init_filename", fmt.Sprintf("init_%d.mp4", t.gen+1),
		"-hls_segment_filename", filepath.Join(t.dir(), "%d.m4s"),
		filepath.Join(t.dir(), "ffmpeg.m3u8"))

	ctx, cancel := context.WithCancel(s.ctx)
	cmd := exec.CommandContext(ctx, s.m.cfg.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	done := make(chan struct{})
	t.cmd, t.cancel, t.start, t.done, t.err = cmd, cancel, n, done, nil
	t.gen++
	gen := t.gen
	if err := cmd.Start(); err != nil {
		t.err = err
		close(done)
		return
	}
	go func() {
		err := cmd.Wait()
		t.mu.Lock()
		if gen == t.gen && err != nil && ctx.Err() == nil {
			t.err = fmt.Errorf("ffmpeg transcode: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
		t.mu.Unlock()
		close(done)
	}()
}

// ---------- subtitles ----------

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
	cache := filepath.Join(s.dir, fmt.Sprintf("sub%d.vtt", typeIdx))
	if _, err := os.Stat(cache); err != nil {
		cmd := exec.CommandContext(r.Context(), s.m.cfg.FFmpeg, "-nostdin", "-v", "error", "-y", "-i", s.Src.Input(),
			"-map", fmt.Sprintf("0:s:%d", typeIdx), "-f", "webvtt", cache+".part")
		if out, err := cmd.CombinedOutput(); err != nil {
			http.Error(w, "subtitle extraction failed: "+string(out), http.StatusInternalServerError)
			return
		}
		_ = os.Rename(cache+".part", cache)
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	http.ServeFile(w, r, cache)
}
