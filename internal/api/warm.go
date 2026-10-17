package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/media"
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
	done chan struct{}
	at   time.Time
}

type warmups struct {
	mu sync.Mutex
	m  map[string]*warmup
}

var warms = warmups{m: map[string]*warmup{}}

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
	w := &warmup{done: make(chan struct{}), at: time.Now()}
	warms.m[it.ID] = w
	warms.mu.Unlock()

	go func() {
		defer close(w.done)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if ready, _ := s.Streaming.Ensure(ctx, it, time.Minute); !ready {
			return
		}
		files, err := s.playableFiles(ctx, it)
		if err != nil || len(files) == 0 {
			return
		}
		f := chooseFile(files, playRequest{Caps: warmCaps})
		if f.StreamID == "" {
			return // a file on disk opens at once
		}
		src, err := s.openSource(ctx, f)
		if err != nil {
			slog.Debug("warm stream", "item", it.ID, "err", err)
			return
		}
		defer src.Close()
		info, err := s.ensureProbe(ctx, f, src)
		if err != nil {
			slog.Debug("warm probe", "item", it.ID, "err", err)
			return
		}
		if media.Decide(info, warmCaps, -1).Method == media.MethodRemux {
			if _, err := s.ensureKeyframes(ctx, f, src); err != nil {
				slog.Debug("warm keyframes", "item", it.ID, "err", err)
			}
		}
	}()
}

// waitWarm lets play wait on a warm-up already under way rather than
// repeating its work alongside it.
func (s *Server) waitWarm(ctx context.Context, itemID string, max time.Duration) {
	warms.mu.Lock()
	w := warms.m[itemID]
	warms.mu.Unlock()
	if w == nil {
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
