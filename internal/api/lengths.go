package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
)

// Versions of one movie or episode run the same length, give or take a few
// seconds. One that runs longer has something spliced in (an ad in front);
// one that runs shorter is cut. Guides only give whole minutes, so versions
// are measured against each other: any more than lengthSlack off the length
// most of them agree on is dropped, and another is looked up.
const lengthSlack = 15.0 // seconds

// fileLength is a probed file's running time in seconds, or 0.
func fileLength(f *db.MediaFile) float64 {
	if f.Duration > 0 {
		return f.Duration
	}
	var p struct {
		Duration float64 `json:"duration"`
	}
	if f.Probe != "" && json.Unmarshal([]byte(f.Probe), &p) == nil {
		return p.Duration
	}
	return 0
}

// agreedLength is the length most versions run: the median of three or
// more; of two that differ, the shorter, as what's added (ads) is more
// common than what's cut. 0 when there's nothing to compare.
func agreedLength(lengths []float64) float64 {
	var l []float64
	for _, x := range lengths {
		if x > 0 {
			l = append(l, x)
		}
	}
	if len(l) < 2 {
		return 0
	}
	sort.Float64s(l)
	return l[(len(l)-1)/2]
}

func offLength(length, agreed float64) bool {
	return length > 0 && agreed > 0 && (length-agreed > lengthSlack || agreed-length > lengthSlack)
}

// checkLengths drops an item's streamed versions whose length is off. With
// probe, versions not measured yet are probed first (those that can be read
// for free); otherwise only what's already measured is compared. It reports
// the streams dropped.
func (s *Server) checkLengths(ctx context.Context, it *db.Item, files []*db.MediaFile, probe bool) map[string]bool {
	if probe {
		var wg sync.WaitGroup
		slots := make(chan struct{}, 3)
		for _, f := range files {
			if f.StreamID == "" || fileLength(f) > 0 {
				continue
			}
			st, err := s.DB.GetStream(ctx, f.StreamID)
			if err != nil || !s.Streaming.Probeable(st) {
				continue
			}
			wg.Add(1)
			go func(f *db.MediaFile) {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				pctx, cancel := context.WithTimeout(ctx, time.Minute)
				defer cancel()
				src, err := s.openSource(pctx, f)
				if err != nil {
					return
				}
				defer src.Close()
				if _, err := s.ensureProbe(pctx, f, src); err != nil {
					slog.Debug("length probe", "file", f.ID, "err", err)
				}
			}(f)
		}
		wg.Wait()
	}
	lengths := make([]float64, len(files))
	for i, f := range files {
		lengths[i] = fileLength(f)
	}
	agreed := agreedLength(lengths)
	dropped := map[string]bool{}
	for i, f := range files {
		if f.StreamID != "" && offLength(lengths[i], agreed) {
			s.Streaming.Reject(f.StreamID, fmt.Sprintf("runs %s, the other versions %s", clock(lengths[i]), clock(agreed)))
			dropped[f.StreamID] = true
		}
	}
	return dropped
}

func clock(sec float64) string {
	n := int(sec + 0.5)
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}
