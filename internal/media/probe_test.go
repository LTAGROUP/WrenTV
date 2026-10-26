package media

import (
	"strings"
	"testing"
)

// A provider that refuses an anonymous probe gets the same allowlisted identity
// ffmpeg and ffprobe expect, and a User-Agent in that set is never sent twice.
func TestProbeURLArgsSendsHeaders(t *testing.T) {
	plain := strings.Join(probeURLArgs("http://example/stream.m3u8", "", nil), " ")
	if !strings.Contains(plain, "-i http://example/stream.m3u8") || strings.Contains(plain, "-headers") {
		t.Fatalf("plain args = %q", plain)
	}

	args := probeURLArgs("http://example/stream.m3u8", "Player/1", map[string]string{
		"User-Agent": "Player/2",
		"Referer":    "https://provider.example/watch",
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-user_agent Player/2") {
		t.Fatalf("agent = %q", joined)
	}
	if strings.Contains(joined, "-headers Referer: https://provider.example/watch\r\nUser-Agent") {
		t.Fatalf("agent duplicated in -headers: %q", joined)
	}
	if !strings.Contains(joined, "-headers Referer: https://provider.example/watch\r\n") {
		t.Fatalf("headers = %q", joined)
	}
	if !strings.HasSuffix(joined, "-i http://example/stream.m3u8") {
		t.Fatalf("input position = %q", joined)
	}

	withAgent := strings.Join(probeURLArgs("http://example/stream.m3u8", "Player/1", map[string]string{
		"Referer": "https://provider.example/watch",
	}), " ")
	if !strings.Contains(withAgent, "-user_agent Player/1") {
		t.Fatalf("agent missing: %q", withAgent)
	}
	if !strings.Contains(withAgent, "-headers Referer: https://provider.example/watch\r\n") {
		t.Fatalf("referer missing: %q", withAgent)
	}
}
