package stream

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/media"
)

// A slow "ffmpeg" that writes one cue, waits, then writes the rest, like a
// conversion reading a large file over a network mount.
const fakeFFmpeg = `#!/bin/sh
for out; do :; done
printf 'WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nfirst\n\n' > "$out"
sleep 4
printf '00:10:00.000 --> 00:10:01.000\nlast\n\n' >> "$out"
`

func TestSubtitlesServedWhileConverting(t *testing.T) {
	tmp := t.TempDir()
	ff := filepath.Join(tmp, "ffmpeg")
	if err := os.WriteFile(ff, []byte(fakeFFmpeg), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{FFmpeg: ff, ScratchDir: filepath.Join(tmp, "scratch"), SubtitleDir: filepath.Join(tmp, "subs")})
	src, err := OpenFile(ff)
	if err != nil {
		t.Fatal(err)
	}
	info := &media.Info{Size: 42, Duration: 700, Subtitles: []media.Stream{{TypeIdx: 0, TextBased: true}}}
	s, err := m.Create(NewSessionOpts{FileID: "f1", Info: info, Src: src, Decision: media.Decision{Method: media.MethodDirect}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(s.ID)

	get := func() (string, string) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/subs/0.vtt", nil), "subs/0.vtt")
		b, _ := io.ReadAll(rec.Body)
		return string(b), rec.Header().Get("X-Subtitle-Complete")
	}
	body, complete := get()
	if complete != "false" || !strings.Contains(body, "first") || strings.Contains(body, "last") {
		t.Fatalf("first fetch: complete=%s body=%q", complete, body)
	}
	time.Sleep(2 * time.Second)
	body, complete = get()
	if complete != "true" || !strings.Contains(body, "last") {
		t.Fatalf("second fetch: complete=%s body=%q", complete, body)
	}
}
