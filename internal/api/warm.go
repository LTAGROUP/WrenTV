package api

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/media"
	"github.com/LTAGROUP/wrentv/internal/streaming"
)

// Opening a streamed title's page starts what pressing play would otherwise
// wait on: finding streams, getting the debrid link and checking its CDN,
// then probing the file and indexing its keyframes. Play then starts from
// what's stored.

// warmCaps stand in for the player, which hasn't said what it plays yet, so
// the version warmed is the one most clients would pick.
var warmCaps = media.ClientCaps{
	Engines:          []string{"avplayer"},
	NativeVideo:      []string{"h264", "hevc"},
	NativeAudio:      []string{"aac", "ac3", "eac3", "alac", "mp3"},
	NativeContainers: []string{"mp4", "mov"},
}

type warmup struct {
	// looked is closed once the warm-up's stream lookup is over, done once
	// all of it is.
	looked, done chan struct{}
	at           time.Time
}

type warmups struct {
	mu sync.Mutex
	m  map[string]*warmup
}

var warms = warmups{m: map[string]*warmup{}}

// forget lets an item be warmed again at once.
func (ws *warmups) forget(itemID string) {
	ws.mu.Lock()
	delete(ws.m, itemID)
	ws.mu.Unlock()
}

// warmItem starts warming a streamed movie or episode in the background.
// A title is warmed at most every few minutes, however often it's opened.
func (s *Server) warmItem(it *db.Item, u *db.User) {
	if (it.Kind != "movie" && it.Kind != "episode") || s.Streaming == nil || !s.Streaming.Active() || !s.limitsFor(u).CanStream {
		return
	}
	warms.mu.Lock()
	if w := warms.m[it.ID]; w != nil && time.Since(w.at) < 5*time.Minute {
		warms.mu.Unlock()
		return
	}
	for id, w := range warms.m {
		if time.Since(w.at) > 10*time.Minute {
			delete(warms.m, id)
		}
	}
	w := &warmup{looked: make(chan struct{}), done: make(chan struct{}), at: time.Now()}
	warms.m[it.ID] = w
	warms.mu.Unlock()

	go func() {
		defer close(w.done)
		var once sync.Once
		looked := func() { once.Do(func() { close(w.looked) }) }
		defer looked()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		// A stored stream found gone is dropped, so the next try finds
		// another rather than leaving play to start the search.
		for try := 0; try < 3; try++ {
			if !s.warm(ctx, it, looked) {
				break
			}
		}
	}()
}

// warm warms an item once, reporting whether its chosen stream had gone
// and another should be tried.
// looked is called once its lookup is over.
func (s *Server) warm(ctx context.Context, it *db.Item, looked func()) (again bool) {
	ready, _ := s.Streaming.Ensure(ctx, it, time.Minute)
	looked()
	if !ready {
		return false
	}
	files, err := s.playableFiles(ctx, it)
	if err != nil || len(files) == 0 {
		return false
	}
	if dropped := s.checkLengths(ctx, it, files, true); len(dropped) > 0 {
		kept := files[:0]
		for _, f := range files {
			if !dropped[f.StreamID] {
				kept = append(kept, f)
			}
		}
		if files = kept; len(files) == 0 {
			return true
		}
	}
	f := chooseFile(files, playRequest{Caps: warmCaps})
	if f.StreamID == "" {
		return false // a file on disk opens at once
	}
	src, err := s.openSource(ctx, f)
	if err != nil {
		slog.Debug("warm stream", "item", it.ID, "err", err)
		return errors.Is(err, streaming.ErrUnavailable) && ctx.Err() == nil
	}
	defer src.Close()
	info, err := s.ensureProbe(ctx, f, src)
	if err != nil {
		slog.Debug("warm probe", "item", it.ID, "err", err)
		return false
	}
	if media.Decide(info, warmCaps, -1).Method == media.MethodRemux {
		if _, err := s.ensureKeyframes(ctx, f, src); err != nil {
			slog.Debug("warm keyframes", "item", it.ID, "err", err)
		}
	}
	return false
}

// waitWarm lets play wait on a warm-up already reading its stream rather
// than repeating that alongside it. One still looking streams up isn't
// waited on: play's own wait on the lookup ends the moment one is found,
// where the warm-up goes on to check every version found.
func (s *Server) waitWarm(ctx context.Context, itemID string, max time.Duration) {
	warms.mu.Lock()
	w := warms.m[itemID]
	warms.mu.Unlock()
	if w == nil {
		return
	}
	select {
	case <-w.looked:
	default:
		return
	}
	t := time.NewTimer(max)
	defer t.Stop()
	select {
	case <-w.done:
	case <-t.C:
	case <-ctx.Done():
	}
}
