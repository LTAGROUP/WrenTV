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

func TestSubtitleSegments(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager(Config{ScratchDir: filepath.Join(tmp, "scratch"), SubtitleDir: filepath.Join(tmp, "subs")})
	src, err := OpenFile(filepath.Join(tmp, "..", filepath.Base(tmp)))
	if err != nil {
		t.Fatal(err)
	}
	info := &media.Info{Size: 7, Duration: 75, Subtitles: []media.Stream{{TypeIdx: 0}, {TypeIdx: 1, Language: "eng", TextBased: true}}}
	s, err := m.Create(NewSessionOpts{FileID: "f2", Info: info, Src: src, Decision: media.Decision{Method: media.MethodDirect}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(s.ID)
	// Already converted.
	os.MkdirAll(filepath.Join(tmp, "subs"), 0o755)
	os.WriteFile(s.subtitleCache(1), []byte("WEBVTT\n\n00:00:05.000 --> 00:00:06.000\nfirst\n\n00:00:29.500 --> 00:00:31.000\nacross\n\n01:05.000 --> 01:06.000\nlast\n\n"), 0o644)

	get := func(p string) string {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/"+p, nil), p)
		return rec.Body.String()
	}
	var b strings.Builder
	if !s.subtitleRenditions(&b) || strings.Count(b.String(), "TYPE=SUBTITLES") != 1 || !strings.Contains(b.String(), `URI="subs/1.m3u8"`) {
		t.Fatalf("renditions: %s", b.String())
	}
	if pl := get("subs/1.m3u8"); strings.Count(pl, ".vtt") != 3 || !strings.Contains(pl, "#EXTINF:15.000,\n1/2.vtt") {
		t.Fatalf("playlist: %s", pl)
	}
	seg0, seg1, seg2 := get("subs/1/0.vtt"), get("subs/1/1.vtt"), get("subs/1/2.vtt")
	if !strings.Contains(seg0, "X-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000") || !strings.Contains(seg0, "first") || !strings.Contains(seg0, "across") {
		t.Fatalf("segment 0: %s", seg0)
	}
	if !strings.Contains(seg1, "00:00:29.500 --> 00:00:31.000\nacross") || strings.Contains(seg1, "first") || strings.Contains(seg1, "last") {
		t.Fatalf("segment 1: %s", seg1)
	}
	if !strings.Contains(seg2, "00:01:05.000 --> 00:01:06.000\nlast") {
		t.Fatalf("segment 2: %s", seg2)
	}
}
