// Package thumbs makes the small frames players show while seeking.
//
// Frames are cut one at a time by seeking to a keyframe near each point, so
// a streamed file is read a few megabytes per frame rather than end to end.
// They go out coarse to fine (every 8th frame first), and asking for a frame
// that isn't made yet gets the nearest one that is, so a partly done set is
// already useful.
package thumbs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Width of each frame; height follows the video.
const Width = 320

type Manager struct {
	Dir    string
	FFmpeg string

	mu      sync.Mutex
	running map[string]bool
	// One set at a time keeps the CPU (and a stream's bandwidth) for playback.
	slot chan struct{}
}

func New(dir, ffmpeg string) *Manager {
	return &Manager{Dir: dir, FFmpeg: ffmpeg, running: map[string]bool{}, slot: make(chan struct{}, 1)}
}

// Job describes one file's frames.
type Job struct {
	FileID   string
	Input    string  // path or URL ffmpeg reads
	Duration float64 // seconds
	Remote   bool    // read over the network: fewer frames
	// Filter turns the decoded frame into SDR at Width (scaling, tone mapping).
	Filter string
}

type manifest struct {
	Interval float64 `json:"interval"`
	Count    int     `json:"count"`
	Done     bool    `json:"done"`
}

// Interval is the spacing between frames for a file.
func Interval(duration float64, remote bool) float64 {
	min, most := 10.0, 400.0
	if remote {
		min, most = 20.0, 180.0
	}
	return math.Max(min, math.Ceil(duration/most))
}

// Start makes a file's frames in the background unless they're done or
// underway. It stops with ctx and picks up where it left off next time.
func (m *Manager) Start(ctx context.Context, j Job) (interval float64) {
	if j.Duration <= 0 || m.FFmpeg == "" {
		return 0
	}
	interval = Interval(j.Duration, j.Remote)
	dir := m.dir(j.FileID)
	if mf, ok := m.read(dir); ok {
		if mf.Done {
			return mf.Interval
		}
		interval = mf.Interval
	}
	m.mu.Lock()
	if m.running[j.FileID] {
		m.mu.Unlock()
		return interval
	}
	m.running[j.FileID] = true
	m.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.finish(j.FileID)
		return 0
	}
	count := int(j.Duration/interval) + 1
	m.write(dir, manifest{Interval: interval, Count: count})
	go func() {
		defer m.finish(j.FileID)
		select {
		case m.slot <- struct{}{}:
			defer func() { <-m.slot }()
		case <-ctx.Done():
			return
		}
		start := time.Now()
		made := 0
		for _, n := range order(count) {
			if ctx.Err() != nil {
				return
			}
			out := m.Frame(j.FileID, n)
			if _, err := os.Stat(out); err == nil {
				continue
			}
			t := float64(n) * interval
			if n == 0 {
				t = math.Min(2, j.Duration/2) // the very first frame is often black
			}
			t = math.Min(t, math.Max(0, j.Duration-5)) // the last point can be the very end, past the last keyframe
			if err := m.cut(ctx, j, t, out); err != nil {
				if ctx.Err() == nil {
					slog.Debug("thumbnail", "file", j.FileID, "t", t, "err", err)
				}
				continue
			}
			made++
		}
		m.write(dir, manifest{Interval: interval, Count: count, Done: true})
		slog.Info("seek thumbnails ready", "file", j.FileID, "frames", made, "took", time.Since(start).Round(time.Second))
	}()
	return interval
}

// Frame is where frame n lives (it may not exist yet).
func (m *Manager) Frame(fileID string, n int) string {
	return filepath.Join(m.dir(fileID), fmt.Sprintf("%05d.jpg", n))
}

// Nearest is the closest made frame to n, searching outward.
func (m *Manager) Nearest(fileID string, n int) (string, bool) {
	mf, ok := m.read(m.dir(fileID))
	if !ok || n < 0 || n >= mf.Count {
		return "", false
	}
	for d := 0; d < 16; d++ {
		for _, k := range []int{n - d, n + d} {
			if k < 0 || k >= mf.Count {
				continue
			}
			p := m.Frame(fileID, k)
			if _, err := os.Stat(p); err == nil {
				return p, true
			}
		}
	}
	return "", false
}

func (m *Manager) cut(ctx context.Context, j Job, t float64, out string) error {
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	vf := j.Filter
	if vf == "" {
		vf = "scale=" + strconv.Itoa(Width) + ":-2,format=yuvj420p"
	}
	tmp := out + ".tmp.jpg"
	cmd := exec.CommandContext(cctx, m.FFmpeg, "-nostdin", "-v", "error", "-skip_frame", "nokey", "-noaccurate_seek",
		"-ss", strconv.FormatFloat(t, 'f', 2, 64), "-i", j.Input,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1", "-vf", vf, "-q:v", "6", "-y", tmp)
	if b, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%w: %s", err, b)
	}
	return os.Rename(tmp, out)
}

// order is every 8th frame, then every 4th, 2nd and the rest.
func order(count int) []int {
	out := make([]int, 0, count)
	seen := make([]bool, count)
	for _, step := range []int{8, 4, 2, 1} {
		for n := 0; n < count; n += step {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func (m *Manager) dir(fileID string) string { return filepath.Join(m.Dir, filepath.Base(fileID)) }

func (m *Manager) finish(fileID string) {
	m.mu.Lock()
	delete(m.running, fileID)
	m.mu.Unlock()
}

func (m *Manager) read(dir string) (manifest, bool) {
	var mf manifest
	b, err := os.ReadFile(filepath.Join(dir, "info.json"))
	if err != nil || json.Unmarshal(b, &mf) != nil || mf.Interval <= 0 {
		return mf, false
	}
	return mf, true
}

func (m *Manager) write(dir string, mf manifest) {
	b, _ := json.Marshal(mf)
	tmp := filepath.Join(dir, "info.json.tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, "info.json"))
	}
}
