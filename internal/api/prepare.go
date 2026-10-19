package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/media"
)

// playBudget is how long play works on a streamed title before telling
// the client to ask again. Apps give up on the request after 90 seconds;
// what's still being done carries on, so the next ask finds it ready.
const playBudget = 20 * time.Second

// preps are streams being read ahead of play, by file.
var preps = struct {
	mu sync.Mutex
	m  map[string]chan struct{}
}{m: map[string]chan struct{}{}}

// prepared reports whether f's probe, and its keyframes if the client
// would have it remuxed, are stored, so play needn't read for them.
func (s *Server) prepared(ctx context.Context, f *db.MediaFile, caps media.ClientCaps) bool {
	var info media.Info
	if f.Probe == "" || json.Unmarshal([]byte(f.Probe), &info) != nil || info.Duration <= 0 {
		return false
	}
	if media.Decide(&info, caps, -1).Method != media.MethodRemux {
		return true
	}
	b, err := s.DB.Keyframes(ctx, f.ID)
	return err == nil && len(b) > 2
}

// readAhead is how much of a Usenet stream's start prepare reads into the
// cache once it's probed: the first few segments of a 4K remux.
const readAhead = 128 << 20

// prepare probes a stream and indexes its keyframes in the background,
// or joins that already under way. The channel closes when it's done,
// whether or not it worked: play then opens the stream itself and
// handles what went wrong. The stream's start is read ahead after that.
func (s *Server) prepare(f *db.MediaFile, caps media.ClientCaps) <-chan struct{} {
	preps.mu.Lock()
	defer preps.mu.Unlock()
	if done := preps.m[f.ID]; done != nil {
		return done
	}
	done := make(chan struct{})
	preps.m[f.ID] = done
	fc := *f
	go func() {
		var once sync.Once
		finish := func() {
			once.Do(func() {
				preps.mu.Lock()
				delete(preps.m, fc.ID)
				preps.mu.Unlock()
				close(done)
			})
		}
		defer finish()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		src, err := s.openSource(ctx, &fc)
		if err != nil {
			slog.Debug("prepare stream", "file", fc.ID, "err", err)
			return
		}
		defer src.Close()
		info, err := s.ensureProbe(ctx, &fc, src)
		if err != nil {
			slog.Debug("prepare probe", "file", fc.ID, "err", err)
			return
		}
		if media.Decide(info, caps, -1).Method == media.MethodRemux {
			if _, err := s.ensureKeyframes(ctx, &fc, src); err != nil {
				slog.Debug("prepare keyframes", "file", fc.ID, "err", err)
			}
		}
		finish()
		if ra, ok := src.(interface {
			ReadAhead(context.Context, int64) error
		}); ok {
			t := time.Now()
			err := ra.ReadAhead(ctx, readAhead)
			slog.Debug("prepare read ahead", "file", fc.ID, "err", err, "took", time.Since(t).Round(time.Millisecond))
		}
	}()
	return done
}
