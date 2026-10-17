package thumbs

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestOrderCoversEveryFrameOnce(t *testing.T) {
	o := order(21)
	if len(o) != 21 || o[0] != 0 || o[1] != 8 || o[2] != 16 {
		t.Fatalf("order = %v", o)
	}
	seen := map[int]bool{}
	for _, n := range o {
		if seen[n] {
			t.Fatalf("frame %d twice", n)
		}
		seen[n] = true
	}
}

func TestInterval(t *testing.T) {
	if got := Interval(45*60, false); got != 10 {
		t.Errorf("episode on disk = %v, want 10", got)
	}
	if got := Interval(3*3600, false); got != 27 {
		t.Errorf("long film on disk = %v, want 27", got)
	}
	if got := Interval(45*60, true); got != 20 {
		t.Errorf("streamed episode = %v, want 20", got)
	}
}

// Needs ffmpeg and WRENTV_THUMB_SAMPLE pointing at a short video.
func TestCutsFrames(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	sample := os.Getenv("WRENTV_THUMB_SAMPLE")
	if err != nil || sample == "" {
		t.Skip("no ffmpeg or sample")
	}
	m := New(t.TempDir(), ff)
	iv := m.Start(context.Background(), Job{FileID: "f1", Input: sample, Duration: 120})
	if iv != 10 {
		t.Fatalf("interval = %v", iv)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if mf, ok := m.read(m.dir("f1")); ok && mf.Done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("frames not done in time")
		}
		time.Sleep(200 * time.Millisecond)
	}
	for n := 0; n <= 12; n++ {
		if _, err := os.Stat(m.Frame("f1", n)); err != nil {
			t.Errorf("frame %d missing: %v", n, err)
		}
	}
	if p, ok := m.Nearest("f1", 99); ok {
		t.Errorf("frame past the end found: %s", p)
	}
}
