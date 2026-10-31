package tracking

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// SyncEvery is how often linked trackers are checked for what their users
// watched or paused in other apps. Linking imports everything once; this
// keeps it current afterwards.
var SyncEvery = 10 * time.Minute

// historyEvery re-reads the whole watch history even when the tracker's
// activity time hasn't moved (or it gave none).
const historyEvery = 6 * time.Hour

// syncState is what the last sync saw of one account.
type syncState struct {
	activity string    // the tracker's last activity time
	paused   string    // the paused list, as pausedKey
	history  time.Time // when the history was last read
}

// syncLoop syncs every linked account a minute after start, then every
// SyncEvery.
func (s *Service) syncLoop(ctx context.Context) {
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = SyncEvery
		for _, t := range []tracker{simklTracker{s.Simkl}, traktTracker{s.Trakt}} {
			if !t.on() {
				continue
			}
			users, err := s.DB.TrackerUsers(ctx, t.id())
			if err != nil {
				slog.Warn(t.id()+" sync", "err", err)
				continue
			}
			for _, u := range users {
				s.syncOne(ctx, t, u)
			}
		}
	}
}

// syncOne brings in one account's changes: its paused titles when that
// list changed, and its watch history when the account's activity time
// moved. It skips an account whose import is already running.
func (s *Service) syncOne(ctx context.Context, t tracker, userID string) {
	k := linkKey(t.id(), userID)
	s.mu.Lock()
	if s.imports[k] {
		s.mu.Unlock()
		return
	}
	s.imports[k] = true
	last := s.synced[k]
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.imports, k)
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	r, err := s.newRun(ctx, t, userID)
	if err != nil {
		return // unlinked meanwhile
	}
	next := last
	activity, err := t.activity(ctx, r.call)
	if err != nil {
		slog.Warn(t.id()+" sync", "user", userID, "err", err)
		return
	}
	readHistory := activity == "" && s.now().Sub(last.history) >= historyEvery ||
		activity != "" && (activity != last.activity || s.now().Sub(last.history) >= historyEvery)
	if readHistory {
		movies, episodes, err := r.history(ctx)
		if err != nil {
			slog.Warn(t.id()+" sync", "user", userID, "err", err)
			return
		}
		next.activity, next.history = activity, s.now()
		if movies+episodes > 0 {
			slog.Info(t.id()+" sync", "user", userID, "movies", movies, "episodes", episodes)
		}
	}
	paused, err := t.paused(ctx, r.call)
	if err != nil {
		slog.Warn(t.id()+" sync", "user", userID, "err", err)
	} else if key := pausedKey(paused); key != last.paused {
		if err := r.paused(ctx, paused); err != nil {
			slog.Warn(t.id()+" sync", "user", userID, "err", err)
		} else {
			next.paused = key
		}
	}
	s.mu.Lock()
	s.synced[k] = next
	s.mu.Unlock()
}

// pausedKey sums up a paused list, so an unchanged one is passed over.
func pausedKey(ps []pausedTitle) string {
	lines := make([]string, len(ps))
	for i, p := range ps {
		lines[i] = fmt.Sprintf("%v %d %s %d %d %.1f %d", p.movie, p.ref.tmdb, p.ref.imdb, p.season, p.number, p.percent, p.at)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
