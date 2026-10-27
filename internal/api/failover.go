package api

import (
	"sync"
	"time"
)

// playLog remembers which streamed files each viewer was handed, so a
// "this version failed" report only counts from someone who played it, and
// once per play: one viewer can't drop a stream everyone shares.
type playLog struct {
	mu   sync.Mutex
	seen map[string]playNote // user id + file id
}

type playNote struct {
	at       time.Time
	reported bool
}

// playReportWindow is how long after a play its failure can be reported.
const playReportWindow = 12 * time.Hour

func (s *Server) plays() *playLog {
	s.playsOnce.Do(func() { s.playLog = &playLog{seen: map[string]playNote{}} })
	return s.playLog
}

// handed notes a play of a file; a new play can be reported again.
func (l *playLog) handed(userID, fileID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.seen) > 2000 {
		for k, n := range l.seen {
			if time.Since(n.at) > playReportWindow {
				delete(l.seen, k)
			}
		}
	}
	l.seen[userID+"\x00"+fileID] = playNote{at: time.Now()}
}

// claimFailure reports whether the viewer played the file lately and hasn't
// already reported that play.
func (l *playLog) claimFailure(userID, fileID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := userID + "\x00" + fileID
	n, ok := l.seen[k]
	if !ok || n.reported || time.Since(n.at) > playReportWindow {
		return false
	}
	n.reported = true
	l.seen[k] = n
	return true
}
