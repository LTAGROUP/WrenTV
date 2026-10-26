package livetv

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/media"
)

func TestQualityFromInfoUsesMeasuredStreams(t *testing.T) {
	q := qualityFromInfo(&media.Info{
		Container: "applehttp",
		Video: []media.Stream{
			{Width: 1280, Height: 720, Codec: "h264", FrameRate: 60, Bitrate: 3000000},
			{Width: 1920, Height: 1080, Codec: "hevc", FrameRate: 50, Bitrate: 6000000},
		},
		Audio: []media.Stream{{Codec: "aac", Channels: 2, ChannelLayout: "stereo", Language: "en", Bitrate: 128000, SampleRate: 48000}},
	})
	if q.Width != 1920 || q.Height != 1080 || q.Codec != "hevc" || q.FrameRate != 50 || q.Bitrate != 6000000 {
		t.Fatalf("video = %+v", q)
	}
	if !q.Adaptive {
		t.Fatal("HLS container must be reported as adaptive")
	}
	if len(q.Audio) != 1 || q.Audio[0].Layout != "stereo" || q.Audio[0].SampleRate != 48000 {
		t.Fatalf("audio = %+v", q.Audio)
	}
	empty := qualityFromInfo(&media.Info{})
	if empty.Audio == nil || len(empty.Audio) != 0 || empty.Adaptive || empty.Height != 0 {
		t.Fatalf("empty quality = %+v", empty)
	}
}

func TestSearchProgramsSplitsAndBoundsResults(t *testing.T) {
	now := time.Now().Unix()
	svc := &Service{
		visible: []*Channel{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}},
		guide: Guide{
			"a": {
				{Title: "Celtics Live", Start: now - 600, End: now + 600},
				{Title: "Heat Tonight", Start: now + 3600, End: now + 7200},
			},
			"b": {
				{Title: "Celtics Replay", Start: now - 7200, End: now - 3600},
				{Title: "Celtics Upcoming", Start: now + 1800, End: now + 3600},
			},
		},
	}
	match := func(title, _ string) bool { return strings.Contains(strings.ToLower(title), "celtics") }
	live, upcoming := svc.SearchPrograms(match, 10)
	if len(live) != 1 || live[0].ChannelID != "a" || live[0].ChannelName != "A" {
		t.Fatalf("live = %+v", live)
	}
	if len(upcoming) != 1 || upcoming[0].ChannelID != "b" {
		t.Fatalf("upcoming = %+v", upcoming)
	}
	// Ended programmes are not offered and the cap applies independently.
	live, upcoming = svc.SearchPrograms(func(string, string) bool { return true }, 1)
	if len(live) != 1 || len(upcoming) != 1 || live[0].Title != "Celtics Live" || upcoming[0].Title != "Heat Tonight" {
		t.Fatalf("bounded results: live=%+v upcoming=%+v", live, upcoming)
	}
}

func TestProbeChannelRejectsUnknownBusyAndUnavailable(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg, dir)
	svc.mu.Lock()
	channel := &Channel{ID: "c", Name: "C"}
	svc.visible = []*Channel{channel}
	svc.byID = map[string]*Channel{"c": channel}
	svc.mu.Unlock()

	if _, err := svc.ProbeChannel(context.Background(), "missing"); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("unknown channel err = %v", err)
	}
	if _, err := svc.ProbeChannel(context.Background(), "c"); !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("missing ffprobe err = %v", err)
	}

	svc.FFprobe = "ffprobe"
	channel.URL = "http://example.invalid/stream.ts"
	svc.probeSem <- struct{}{}
	svc.probeSem <- struct{}{}
	if _, err := svc.ProbeChannel(context.Background(), "c"); !errors.Is(err, ErrProbeBusy) {
		t.Fatalf("busy err = %v", err)
	}
	<-svc.probeSem
	<-svc.probeSem

	cfg.LiveTV.MaxStreams = 1
	svc.sessions.byID = map[string]*Session{"s": {ID: "s"}}
	svc.sessions.kinds = map[string]string{}
	if _, err := svc.ProbeChannel(context.Background(), "c"); !errors.Is(err, ErrProbeLimit) {
		t.Fatalf("limit err = %v", err)
	}
}
