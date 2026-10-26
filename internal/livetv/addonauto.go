package livetv

// Automatic feed selection. An addon can offer one item with several feeds: a
// paid plan's placeholder, an external-player tip, a 720p backup and a 1080p
// main stream. The viewer should not have to guess which one works, so the
// server ranks the playable feeds and proves a feed starts before a session is
// created for it. Nothing here caches a link: addon URLs are short-lived and
// IP-bound, so every call resolves the addon again.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// addonStreamCheckWait bounds the request that proves a feed starts. A dead
// host, a refused token or a stream that never sends a byte answers (or hangs)
// well inside this, so one broken feed can't stall automatic playback.
const addonStreamCheckWait = 8 * time.Second

// addonStreamCheckBytes is enough for a playlist header or a TS packet.
const addonStreamCheckBytes = 4096

// addonHeightPattern reads the resolutions addons put in their stream titles:
// "1080p", "720i" and "1920x1080". A bare number is not a quality claim.
var addonHeightPattern = regexp.MustCompile(`(?:(\d{3,4})x(\d{3,4}))|(?:(\d{3,4})[pi])`)

// addonHeightFromText reads a nominal height from a feed's own labels. It only
// repeats what the addon said; an unlabelled feed stays at 0 rather than being
// guessed at.
func addonHeightFromText(text string) int {
	text = strings.ToLower(text)
	height := 0
	raise := func(candidate int) {
		if candidate >= 240 && candidate <= 4320 && candidate > height {
			height = candidate
		}
	}
	for _, match := range addonHeightPattern.FindAllStringSubmatch(text, -1) {
		value := match[2]
		if match[3] != "" {
			value = match[3]
		}
		if n, err := strconv.Atoi(value); err == nil {
			raise(n)
		}
	}
	words := " " + strings.Map(func(r rune) rune {
		switch r {
		case '/', '|', '·', ',', ')', '(', '[', ']', '-', '_':
			return ' '
		}
		return r
	}, text) + " "
	fields := strings.Fields(words)
	has := func(word string) bool {
		for _, field := range fields {
			if field == word {
				return true
			}
		}
		return false
	}
	switch {
	case has("4k") || has("uhd") || has("2160p"):
		raise(2160)
	case has("fhd") || (has("full") && has("hd")):
		raise(1080)
	case has("hd"):
		raise(720)
	case has("sd"):
		raise(480)
	}
	return height
}

// AddonStreamQuality is the height a feed advertises. A parsed feed carries it;
// a feed built by hand is read from its labels.
func AddonStreamQuality(stream AddonStream) int {
	if stream.Quality > 0 {
		return stream.Quality
	}
	return addonHeightFromText(stream.Name + " " + stream.Title)
}

// addonStreamIsHLS reports whether a link is an HLS playlist. HLS is what live
// addon feeds usually publish, and it is the form the browser player handles
// best, so it wins a tie against a progressive file of the same quality.
func addonStreamIsHLS(stream AddonStream) bool {
	path := strings.ToLower(strings.SplitN(stream.URL, "?", 2)[0])
	return strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".m3u")
}

// RankAddonStreams orders feeds best-first without dropping any: the caller
// decides how many to try. The highest advertised quality wins, then HLS, and
// otherwise the addon's own order is kept, so an addon that publishes a
// preferred feed first is respected.
func RankAddonStreams(streams []AddonStream) []AddonStream {
	out := append([]AddonStream(nil), streams...)
	sort.SliceStable(out, func(i, j int) bool {
		left, right := AddonStreamQuality(out[i]), AddonStreamQuality(out[j])
		if left != right {
			return left > right
		}
		if hlsLeft, hlsRight := addonStreamIsHLS(out[i]), addonStreamIsHLS(out[j]); hlsLeft != hlsRight {
			return hlsLeft
		}
		return false
	})
	return out
}

// AddonStreamError is a feed that answered, but not with media. It says what
// happened without repeating the link: addon URLs carry the viewer's tokens.
type AddonStreamError struct{ Reason string }

func (e *AddonStreamError) Error() string { return e.Reason }

// CheckAddonStream asks a provider for its first bytes and reports whether the
// feed starts. A playlist must actually be a playlist: an expired token often
// answers 200 with a web page, which the player would only discover later.
func (s *Service) CheckAddonStream(ctx context.Context, stream AddonStream) error {
	link := strings.TrimSpace(stream.URL)
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		return &AddonStreamError{Reason: "it isn't an HTTP stream"}
	}
	wait := s.AddonCheckWait
	if wait <= 0 {
		wait = addonStreamCheckWait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	start := time.Now()
	resp, err := get(ctx, link, stream.UserAgent, SanitizeRequestHeaders(stream.Headers))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &AddonStreamError{Reason: "it didn't answer in time"}
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		return &AddonStreamError{Reason: "it didn't answer"}
	}
	defer resp.Body.Close()
	slog.Debug("addon feed checked", "endpoint", "feed", "status", resp.StatusCode,
		"ms", time.Since(start).Milliseconds(), "retry", 0, "sessions", s.Playing())
	if resp.StatusCode >= 400 {
		// A provider refusing this server for now is not a dead feed. The caller
		// stops asking and says when to come back, instead of walking the rest
		// of the feed list into the same limit.
		if limit := addonRateLimit(resp); limit != nil {
			slog.Warn("addon feed rate limited", "endpoint", "feed", "status", resp.StatusCode,
				"retryAfter", limit.RetryAfter.String(), "ms", time.Since(start).Milliseconds(),
				"sessions", s.Playing())
			return limit
		}
		return &AddonStreamError{Reason: fmt.Sprintf("it answered %s", resp.Status)}
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		return &AddonStreamError{Reason: "it answered with a web page, not media"}
	}
	head := make([]byte, addonStreamCheckBytes)
	n, _ := io.ReadFull(resp.Body, head)
	body := strings.TrimSpace(string(head[:n]))
	playlist := strings.Contains(contentType, "mpegurl") || strings.HasPrefix(body, "#EXTM3U") || addonStreamIsHLS(stream)
	if playlist {
		if !strings.HasPrefix(body, "#EXTM3U") {
			return &AddonStreamError{Reason: "it didn't send a playlist"}
		}
		return nil
	}
	if n == 0 {
		return &AddonStreamError{Reason: "it sent no data"}
	}
	return nil
}
