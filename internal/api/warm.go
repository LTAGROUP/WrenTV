package api

import (
	"context"
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
	w := &warmup{done: make(chan struct{}), at: time.Now()}
	warms.m[it.ID] = w
	warms.mu.Unlock()

	go func() {
		defer close(w.done)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		// A stored stream found gone is dropped, so the next try finds
		// another rather than leaving play to start the search.
		for try := 0; try < 3; try++ {
			if !s.warm(ctx, it) {
				break
			}
		}
	}()
}

// warm warms an item once, reporting whether its chosen stream had gone
// and another should be tried.
func (s *Server) warm(ctx context.Context, it *db.Item) (again bool) {
	if ready, _ := s.Streaming.Ensure(ctx, it, time.Minute); !ready {
		return false
	}
	files, err := s.playableFiles(ctx, it)
	if err != nil || len(files) == 0 {
		return false
	}
	// The version play would pick is got ready first, so play can join
	// that rather than wait on the rest; then the others are measured
	// against it.
	f := chooseFile(files, playRequest{Caps: warmCaps})
	if f.StreamID != "" {
		select {
		case <-s.prepare(f, warmCaps):
		case <-ctx.Done():
			return false
		}
		if _, err := s.DB.GetStream(ctx, f.StreamID); err != nil {
			return ctx.Err() == nil // gone: find another
		}
		if fresh, err := s.DB.GetFile(ctx, f.ID); err == nil {
			for i := range files {
				if files[i].ID == f.ID {
					files[i] = fresh
				}
			}
		}
	}
	// Off-length ones are dropped; if that's the one got ready, another is.
	return s.checkLengths(ctx, it, files, true)[f.StreamID]
}
