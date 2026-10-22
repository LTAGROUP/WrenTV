package stream

import (
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
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

	get := func(path string) (string, string) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/"+path, nil), "subs/0.vtt")
		b, _ := io.ReadAll(rec.Body)
		return string(b), rec.Header().Get("X-Subtitle-Complete")
	}
	body, complete := get("subs/0.vtt?partial=1")
	if complete != "false" || !strings.Contains(body, "first") || strings.Contains(body, "last") {
		t.Fatalf("first fetch: complete=%s body=%q", complete, body)
	}
	time.Sleep(2 * time.Second)
	body, complete = get("subs/0.vtt?partial=1")
	if complete != "true" || !strings.Contains(body, "last") {
		t.Fatalf("second fetch: complete=%s body=%q", complete, body)
	}
}

// Players that load a subtitle file once get the whole track, even when the
// conversion is still running when they ask.
func TestSubtitleWaitsForConversion(t *testing.T) {
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
	info := &media.Info{Size: 43, Duration: 700, Subtitles: []media.Stream{{TypeIdx: 0, TextBased: true}}}
	s, err := m.Create(NewSessionOpts{FileID: "f3", Info: info, Src: src, Decision: media.Decision{Method: media.MethodDirect}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(s.ID)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/subs/0.vtt", nil), "subs/0.vtt")
	cues := parseVTT(rec.Body.String(), true)
	if len(cues) != 2 || cues[0].text != "first" || cues[1].text != "last" {
		t.Fatalf("body %q", rec.Body.String())
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

// A segment the whole-track conversion hasn't reached is read from the file
// by seeking to its window, with real ffmpeg on a small MKV.
func TestSubtitleSegmentAheadOfConversion(t *testing.T) {
	real, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	tmp := t.TempDir()
	var srt strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&srt, "%d\n00:%02d:%02d,000 --> 00:%02d:%02d,000\ncue at %d\n\n", i+1, i*5/60, i*5%60, (i*5+3)/60, (i*5+3)%60, i*5)
	}
	os.WriteFile(filepath.Join(tmp, "a.srt"), []byte(srt.String()), 0o644)
	mkv := filepath.Join(tmp, "t.mkv")
	if out, err := exec.Command(real, "-v", "error", "-f", "lavfi", "-t", "200", "-i", "testsrc=size=160x120:rate=10",
		"-i", filepath.Join(tmp, "a.srt"), "-map", "0", "-map", "1", "-c:v", "mpeg4", "-g", "10", "-c:s", "srt", mkv).CombinedOutput(); err != nil {
		t.Fatalf("make mkv: %v %s", err, out)
	}
	// The whole-track conversion (no -ss) never finishes; windows run for real.
	ff := filepath.Join(tmp, "ffmpeg")
	os.WriteFile(ff, []byte("#!/bin/sh\ncase \" $* \" in *\" -ss \"*) exec "+real+" \"$@\";; esac\nsleep 60\n"), 0o755)
	m := NewManager(Config{FFmpeg: ff, ScratchDir: filepath.Join(tmp, "scratch"), SubtitleDir: filepath.Join(tmp, "subs")})
	src, err := OpenFile(mkv)
	if err != nil {
		t.Fatal(err)
	}
	info := &media.Info{Size: 1, Duration: 200, Subtitles: []media.Stream{{TypeIdx: 0, Language: "eng", TextBased: true}}}
	s, err := m.Create(NewSessionOpts{FileID: "f3", Info: info, Src: src, Decision: media.Decision{Method: media.MethodDirect}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(s.ID)

	rec := httptest.NewRecorder()
	began := time.Now()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/subs/0/4.vtt", nil), "subs/0/4.vtt")
	seg := rec.Body.String()
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("segment took %s", took)
	}
	// Segment 4 is 2:00 to 2:30: cues 120 through 145, nothing either side.
	if !strings.Contains(seg, "00:02:00.000 --> 00:02:03.000\ncue at 120") || !strings.Contains(seg, "cue at 145") ||
		strings.Contains(seg, "cue at 115") || strings.Contains(seg, "cue at 150") {
		t.Fatalf("segment 4: %s", seg)
	}
}
