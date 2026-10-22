package stream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LTAGROUP/wrentv/internal/media"
)

type Config struct {
	FFmpeg     string
	FFprobe    string
	ScratchDir string
	// SubtitleDir caches text subtitles converted to WebVTT, across sessions.
	SubtitleDir string
	HW          HW
}

// Manager owns live playback sessions.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*Session
	subs     map[string]*subExtraction
}

func NewManager(cfg Config) *Manager {
	_ = os.RemoveAll(cfg.ScratchDir) // stale segments from a previous run
	_ = os.MkdirAll(cfg.ScratchDir, 0o755)
	m := &Manager{cfg: cfg, sessions: map[string]*Session{}, subs: map[string]*subExtraction{}}
	go m.reap()
	return m
}

func (m *Manager) HW() HW { return m.cfg.HW }

// Session is one playback of one file by one client.
type Session struct {
	ID       string         `json:"id"`
	UserID   string         `json:"userId"`
	ItemID   string         `json:"itemId"`
	FileID   string         `json:"fileId"`
	Decision media.Decision `json:"decision"`
	Info     *media.Info    `json:"-"`
	Started  time.Time      `json:"started"`
	Src      Source         `json:"-"`
	// KbpsTarget for transcodes.
	KbpsTarget int `json:"-"`
	MaxWidth   int `json:"-"`

	m          *Manager
	dir        string
	keyframes  []float64
	plan       []segment
	segLen     float64 // transcode segment length (a whole number of frames)
	gop        int
	lastAccess time.Time
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc

	remux *remuxer
	tx    *transcoder

	// Subtitle segments read straight from the file, for windows the
	// whole-track conversion hasn't reached yet.
	subMu  sync.Mutex
	subWin map[string][]vttCue

	// Delivery stats: bytes sent, per-second buckets for the current rate,
	// the last segment asked for, and the encode's progress for its speed.
	bytes    atomic.Int64
	rateMu   sync.Mutex
	buckets  [12]rateBucket
	lastSeg  atomic.Int64
	txSample struct {
		at  time.Time
		end float64
	}
	txSpeed float64
}

type rateBucket struct {
	sec   int64
	bytes int64
}

type segment struct {
	start, end float64
}

type NewSessionOpts struct {
	UserID, ItemID, FileID string
	Src                    Source
	Info                   *media.Info
	Decision               media.Decision
	Keyframes              []float64
	KbpsTarget             int
	MaxWidth               int
}

func (m *Manager) Create(o NewSessionOpts) (*Session, error) {
	id := randID()
	dir := filepath.Join(m.cfg.ScratchDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID: id, UserID: o.UserID, ItemID: o.ItemID, FileID: o.FileID, Decision: o.Decision, Info: o.Info, Src: o.Src,
		Started: time.Now(), KbpsTarget: o.KbpsTarget, MaxWidth: o.MaxWidth,
		m: m, dir: dir, keyframes: o.Keyframes, lastAccess: time.Now(), ctx: ctx, cancel: cancel,
	}
	switch o.Decision.Method {
	case media.MethodRemux:
		if len(o.Keyframes) < 2 {
			cancel()
			return nil, errors.New("no keyframe index for remux")
		}
		s.plan = planFromKeyframes(o.Keyframes, o.Info.Duration, 6)
		s.remux = newRemuxer(s)
	case media.MethodTranscode:
		s.segLen, s.gop = gopFor(o.Info.PrimaryVideo(), 6)
		s.plan = planFixed(o.Info.Duration, s.segLen)
		s.tx = newTranscoder(s)
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	slog.Info("playback session", "id", id, "engine", o.Decision.Engine, "method", o.Decision.Method, "reasons", o.Decision.Reasons)
	return s, nil
}

func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s != nil {
		s.touch()
	}
	return s
}

func (m *Manager) List() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

func (m *Manager) Stop(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if s != nil {
		s.close()
	}
}

// StopForFile ends every session reading a file (e.g. its stream was replaced).
func (m *Manager) StopForFile(fileID string) {
	for _, s := range m.List() {
		if s.FileID == fileID {
			m.Stop(s.ID)
		}
	}
}

func (m *Manager) reap() {
	t := time.NewTicker(time.Minute)
	for range t.C {
		for _, s := range m.List() {
			s.mu.Lock()
			idle := time.Since(s.lastAccess)
			s.mu.Unlock()
			// Paused players stop fetching; keep sessions long enough to resume.
			if idle > 4*time.Hour || (s.tx != nil && idle > 30*time.Minute) {
				m.Stop(s.ID)
			}
		}
	}
}

// Idle is how long since the client last fetched anything.
// Done closes when the session ends.
func (s *Session) Done() <-chan struct{} { return s.ctx.Done() }

func (s *Session) Idle() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastAccess)
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastAccess = time.Now()
	s.mu.Unlock()
}

func (s *Session) close() {
	s.cancel()
	if s.tx != nil {
		s.tx.stop()
	}
	if s.Src != nil {
		s.Src.Close()
	}
	_ = os.RemoveAll(s.dir)
}

// ServeHTTP handles /stream/{sid}/{file}.
//
//	file, file.<ext>          raw bytes (direct play)
//	main.m3u8                 master playlist
//	media.m3u8, init.mp4, N.m4s           transcode (muxed)
//	v/media.m3u8, v/init.mp4, v/N.m4s     remux video
//	a/media.m3u8, a/init.mp4, a/N.m4s     remux audio
//	subs/N.vtt                text subtitle as WebVTT
//	subs/N.m3u8, subs/N/K.vtt the same as an HLS subtitle rendition
func (s *Session) ServeHTTP(w http.ResponseWriter, r *http.Request, file string) {
	s.touch()
	// Stopping the session cuts transfers already under way, not just new
	// ones: a direct-play client holds one long range request.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stopWatch := context.AfterFunc(s.ctx, cancel)
	defer stopWatch()
	r = r.WithContext(ctx)
	w = &countingWriter{ResponseWriter: w, s: s}
	track := ""
	if s.remux != nil {
		if t, rest, ok := strings.Cut(file, "/"); ok && (t == "v" || t == "a") {
			track, file = t, rest
		}
	}
	switch {
	case file == "file" || strings.HasPrefix(file, "file."):
		s.Src.ServeHTTP(w, r)
	case file == "main.m3u8":
		s.serveMaster(w)
	case file == "media.m3u8":
		s.serveMediaPlaylist(w)
	case file == "init.mp4":
		s.serveInit(w, r, track)
	case strings.HasSuffix(file, ".m4s"):
		n, err := strconv.Atoi(strings.TrimSuffix(file, ".m4s"))
		if err != nil || n < 0 || n >= len(s.plan) {
			http.NotFound(w, r)
			return
		}
		if track != "a" {
			s.lastSeg.Store(int64(n))
		}
		s.serveSegment(w, r, track, n)
	case strings.HasPrefix(file, "subs/") && strings.HasSuffix(file, ".m3u8"):
		idx, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(file, "subs/"), ".m3u8"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.serveSubtitlePlaylist(w, r, idx)
	case strings.HasPrefix(file, "subs/") && strings.Count(file, "/") == 2 && strings.HasSuffix(file, ".vtt"):
		a, b, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(file, "subs/"), ".vtt"), "/")
		idx, err1 := strconv.Atoi(a)
		n, err2 := strconv.Atoi(b)
		if err1 != nil || err2 != nil {
			http.NotFound(w, r)
			return
		}
		s.serveSubtitleSegment(w, r, idx, n)
	case strings.HasPrefix(file, "subs/") && strings.HasSuffix(file, ".vtt"):
		idx, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(file, "subs/"), ".vtt"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.serveSubtitle(w, r, idx)
	default:
		http.NotFound(w, r)
	}
}

func (s *Session) serveInit(w http.ResponseWriter, r *http.Request, track string) {
	var path string
	var err error
	switch {
	case s.remux != nil && track != "":
		path, err = s.remux.init(r.Context(), track)
	case s.tx != nil:
		path, err = s.tx.init(r.Context())
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		// A stopped session kills its ffmpeg runs; that isn't a failure.
		if s.ctx.Err() == nil {
			slog.Warn("init segment failed", "session", s.ID, "err", err)
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeFile(w, r, path)
}

func (s *Session) serveSegment(w http.ResponseWriter, r *http.Request, track string, n int) {
	var path string
	var err error
	switch {
	case s.remux != nil && track != "":
		path, err = s.remux.segment(r.Context(), track, n)
	case s.tx != nil:
		path, err = s.tx.segment(r.Context(), n)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if r.Context().Err() == nil {
			if s.ctx.Err() == nil { // a stopped session kills its ffmpeg runs
				slog.Warn("segment failed", "session", s.ID, "track", track, "n", n, "err", err)
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "video/iso.segment")
	http.ServeFile(w, r, path)
}

var errStopped = errors.New("playback stopped")

// countingWriter tallies what a session sends and refuses to write once the
// session has been stopped, which ends the response.
type countingWriter struct {
	http.ResponseWriter
	s *Session
}

func (c *countingWriter) Write(b []byte) (int, error) {
	if c.s.ctx.Err() != nil {
		return 0, errStopped
	}
	n, err := c.ResponseWriter.Write(b)
	c.s.addBytes(int64(n))
	return n, err
}

func (c *countingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (s *Session) addBytes(n int64) {
	s.bytes.Add(n)
	now := time.Now().Unix()
	s.rateMu.Lock()
	b := &s.buckets[now%int64(len(s.buckets))]
	if b.sec != now {
		b.sec, b.bytes = now, 0
	}
	b.bytes += n
	s.rateMu.Unlock()
}

// Delivery is how a session is being sent right now.
type Delivery struct {
	Bytes int64 `json:"bytes"`
	// Kbps is the send rate over the last ten seconds.
	Kbps int `json:"kbps"`
	// For transcodes: how far the encode is ahead of the player, its speed
	// (2 = twice real time) and whether it has reached the end.
	BufferSeconds float64 `json:"bufferSeconds,omitempty"`
	Speed         float64 `json:"speed,omitempty"`
	Encoding      bool    `json:"encoding,omitempty"`
	EncodeDone    bool    `json:"encodeDone,omitempty"`
	// Output is what a transcode produces.
	OutWidth    int    `json:"outWidth,omitempty"`
	OutHeight   int    `json:"outHeight,omitempty"`
	OutKbps     int    `json:"outKbps,omitempty"`
	OutCodec    string `json:"outCodec,omitempty"`
	HWKind      string `json:"hw,omitempty"`
	HWDecode    bool   `json:"hwDecode,omitempty"`
	Tonemap     bool   `json:"tonemap,omitempty"`
	IdleSeconds int    `json:"idleSeconds"`
}

// Delivery reports the session's send rate and, for a transcode, its encode.
func (s *Session) Delivery() Delivery {
	now := time.Now().Unix()
	var sum int64
	s.rateMu.Lock()
	for _, b := range s.buckets {
		// The current second is still filling; average the ten before it.
		if b.sec < now && b.sec >= now-10 {
			sum += b.bytes
		}
	}
	s.rateMu.Unlock()
	d := Delivery{Bytes: s.bytes.Load(), Kbps: int(sum * 8 / 10 / 1000), IdleSeconds: int(s.Idle().Seconds())}
	if s.tx == nil {
		return d
	}
	hw := s.m.cfg.HW
	d.OutKbps, d.OutCodec, d.HWKind, d.HWDecode = s.KbpsTarget, "h264", hw.Kind, hw.Decode
	if v := s.Info.PrimaryVideo(); v != nil {
		d.OutWidth, d.OutHeight = v.Width, v.Height
		if s.MaxWidth > 0 && v.Width > s.MaxWidth {
			d.OutWidth, d.OutHeight = s.MaxWidth, v.Height*s.MaxWidth/v.Width
		}
		d.Tonemap = v.HDR != ""
	}
	s.tx.mu.Lock()
	running := s.tx.cmd != nil
	hi := s.tx.produced()
	var finished bool
	if s.tx.done != nil {
		select {
		case <-s.tx.done:
			finished = true
		default:
		}
	}
	s.tx.mu.Unlock()
	d.Encoding = running && !finished
	if hi < 0 || hi >= len(s.plan) {
		return d
	}
	end := s.plan[hi].end
	d.EncodeDone = hi == len(s.plan)-1
	if seg := int(s.lastSeg.Load()); seg >= 0 && seg < len(s.plan) {
		d.BufferSeconds = max(0, end-s.plan[seg].start)
	}
	s.rateMu.Lock()
	t := time.Now()
	if prev := s.txSample; !prev.at.IsZero() && t.Sub(prev.at) >= 2*time.Second {
		if dt := t.Sub(prev.at).Seconds(); end >= prev.end {
			s.txSpeed = (end - prev.end) / dt
		}
		s.txSample.at, s.txSample.end = t, end
	} else if prev.at.IsZero() {
		s.txSample.at, s.txSample.end = t, end
	}
	if d.Encoding {
		d.Speed = s.txSpeed
	}
	s.rateMu.Unlock()
	return d
}

func randID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// planFromKeyframes groups keyframes into segments of at least target seconds.
func planFromKeyframes(kf []float64, duration, target float64) []segment {
	var plan []segment
	start := kf[0]
	if start < 0.5 {
		start = 0
	}
	for _, t := range kf[1:] {
		if t-start >= target && (duration == 0 || duration-t > 1) {
			plan = append(plan, segment{start, t})
			start = t
		}
	}
	end := duration
	if end <= start {
		end = start + target
	}
	plan = append(plan, segment{start, end})
	return plan
}

// gopFor returns a segment length close to target that is a whole number of
// frames, and that frame count. Encoding with a fixed GOP of that size puts
// keyframes exactly on segment boundaries with any encoder or ffmpeg version
// (force_key_frames expressions evaluate t differently across versions).
// gop is 0 when the frame rate is unknown.
func gopFor(v *media.Stream, target float64) (segLen float64, gop int) {
	if v == nil || v.FrameRate <= 0 || v.FrameRate > 240 {
		return target, 0
	}
	gop = int(math.Round(target * v.FrameRate))
	return float64(gop) / v.FrameRate, gop
}

func planFixed(duration, target float64) []segment {
	var plan []segment
	for t := 0.0; t < duration; t += target {
		end := min(t+target, duration)
		// Fold a short tail into the previous segment (an encoder may never
		// produce a fraction-of-a-second segment).
		if len(plan) > 0 && end-t < 1 {
			plan[len(plan)-1].end = end
			break
		}
		plan = append(plan, segment{t, end})
	}
	if len(plan) == 0 {
		plan = append(plan, segment{0, target})
	}
	return plan
}
