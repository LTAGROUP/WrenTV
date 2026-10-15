package stream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/haven/internal/media"
)

type Config struct {
	FFmpeg     string
	FFprobe    string
	ScratchDir string
	HW         HW
}

// Manager owns live playback sessions.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager(cfg Config) *Manager {
	_ = os.RemoveAll(cfg.ScratchDir) // stale segments from a previous run
	_ = os.MkdirAll(cfg.ScratchDir, 0o755)
	m := &Manager{cfg: cfg, sessions: map[string]*Session{}}
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
	lastAccess time.Time
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc

	remux *remuxer
	tx    *transcoder
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
		s.plan = planFixed(o.Info.Duration, 6)
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

// StopForFile ends every session reading a file (e.g. its torrent was replaced).
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
func (s *Session) ServeHTTP(w http.ResponseWriter, r *http.Request, file string) {
	s.touch()
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
		s.serveSegment(w, r, track, n)
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
		slog.Warn("init segment failed", "session", s.ID, "err", err)
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
			slog.Warn("segment failed", "session", s.ID, "track", track, "n", n, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "video/iso.segment")
	http.ServeFile(w, r, path)
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
