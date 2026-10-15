package markers

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
)

// Analyzer is the background job that fingerprints seasons and stores the
// intro and credits it finds for each episode file.
type Analyzer struct {
	DB      *db.DB
	FFmpeg  string
	Enabled func() bool

	mu      sync.Mutex
	running bool
	wake    chan struct{}
}

const (
	introWindow   = 600.0 // look for the intro in the first 10 min…
	creditsWindow = 360.0 // …and the credits in the last 6 min
	minIntro      = 15.0
	maxIntro      = 180.0
	minCredits    = 15.0
	comparePeers  = 3 // compare each episode with up to this many neighbours
)

func New(d *db.DB, ffmpeg string, enabled func() bool) *Analyzer {
	return &Analyzer{DB: d, FFmpeg: ffmpeg, Enabled: enabled, wake: make(chan struct{}, 1)}
}

// Kick asks for a pass soon (after a scan adds episodes).
func (a *Analyzer) Kick() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// Run analyzes pending seasons shortly after start, then whenever kicked and
// every half hour.
func (a *Analyzer) Run(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.wake:
		}
		if a.Enabled() {
			if err := a.Pass(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("marker analysis", "err", err)
			}
		}
		t.Reset(30 * time.Minute)
	}
}

// Pass analyzes every season that has new, changed or under-compared episodes.
func (a *Analyzer) Pass(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.running = false; a.mu.Unlock() }()

	files, err := a.DB.EpisodeFilesForMarkers(ctx)
	if err != nil {
		return err
	}
	for i := 0; i < len(files); {
		j := i
		for j < len(files) && files[j].SeasonID == files[i].SeasonID {
			j++
		}
		season := files[i:j]
		i = j
		if !needsWork(season) {
			continue
		}
		start := time.Now()
		found := a.analyzeSeason(ctx, season)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Info("analyzed season for intro/credits", "season", season[0].SeasonID, "episodes", len(season),
			"found", found, "took", time.Since(start).Round(time.Millisecond))
	}
	return nil
}

func needsWork(season []*db.EpisodeFile) bool {
	if len(season) < 2 {
		return false
	}
	for _, f := range season {
		m := f.Markers
		if m == nil || m.Mtime != f.Mtime {
			return true
		}
		// Nothing found yet, but there are episodes it wasn't compared with.
		if (m.Intro == nil || m.Credits == nil) && m.Peers < min(comparePeers, len(season)-1) {
			return true
		}
	}
	return false
}

type prints struct {
	head, tail []uint32
	tailStart  float64
	err        error
}

// analyzeSeason fingerprints the start and end of each episode and compares
// each with its nearest neighbours. Returns how many files got a marker.
func (a *Analyzer) analyzeSeason(ctx context.Context, season []*db.EpisodeFile) int {
	fp := make([]*prints, len(season))
	get := func(i int) *prints {
		if fp[i] != nil {
			return fp[i]
		}
		f := season[i]
		p := &prints{}
		headLen := math.Min(introWindow, f.Duration/2)
		p.head, p.err = Extract(ctx, a.FFmpeg, f.Path, 0, headLen)
		if p.err == nil {
			tailLen := math.Min(creditsWindow, f.Duration/3)
			p.tailStart = f.Duration - tailLen
			p.tail, p.err = Extract(ctx, a.FFmpeg, f.Path, p.tailStart, tailLen)
		}
		if p.err != nil {
			slog.Warn("fingerprint failed", "file", f.Path, "err", p.err)
		}
		fp[i] = p
		return p
	}
	found := 0
	for i, f := range season {
		if ctx.Err() != nil {
			return found
		}
		if m := f.Markers; m != nil && m.Mtime == f.Mtime && m.Intro != nil && m.Credits != nil {
			continue // complete
		}
		me := get(i)
		if me.err != nil {
			continue
		}
		var intro, credits *[2]float64
		peers := 0
		for _, k := range neighbours(i, len(season), comparePeers) {
			other := get(k)
			if other.err != nil {
				continue
			}
			peers++
			if m, ok := Common(me.head, other.head, minIntro); ok && m.Len() <= maxIntro {
				if intro == nil || m.Len() > intro[1]-intro[0] {
					intro = &[2]float64{m.AStart, m.AEnd}
				}
			}
			if m, ok := Common(me.tail, other.tail, minCredits); ok {
				r := [2]float64{me.tailStart + m.AStart, me.tailStart + m.AEnd}
				// Credits run to (nearly) the end; prefer the earliest start.
				if r[1] > f.Duration-90 && (credits == nil || r[0] < credits[0]) {
					credits = &r
				}
			}
		}
		if credits != nil && credits[1] > f.Duration-5 {
			credits[1] = f.Duration
		}
		if err := a.DB.SetFileMarkers(ctx, &db.FileMarkers{FileID: f.FileID, Mtime: f.Mtime, Intro: intro, Credits: credits, Peers: peers}); err != nil {
			slog.Warn("save markers", "file", f.FileID, "err", err)
		}
		if intro != nil || credits != nil {
			found++
		}
	}
	return found
}

// neighbours returns up to n indexes closest to i (i+1, i-1, i+2, …).
func neighbours(i, total, n int) []int {
	var out []int
	for d := 1; len(out) < n && (i-d >= 0 || i+d < total); d++ {
		if i+d < total {
			out = append(out, i+d)
		}
		if i-d >= 0 && len(out) < n {
			out = append(out, i-d)
		}
	}
	return out
}
