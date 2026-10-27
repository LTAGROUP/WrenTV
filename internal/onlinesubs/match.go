package onlinesubs

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
)

// Hash is the OpenSubtitles hash of a file: its size plus the first and last
// 64 KiB summed as little-endian uint64s. A subtitle with the same hash was
// timed against this exact file.
func Hash(r io.ReaderAt, size int64) (string, error) {
	const chunk = 64 << 10
	if size < chunk {
		return "", fmt.Errorf("file too small to hash")
	}
	h := uint64(size)
	buf := make([]byte, chunk)
	for _, off := range []int64{0, size - chunk} {
		if _, err := r.ReadAt(buf, off); err != nil && err != io.EOF {
			return "", err
		}
		for i := 0; i < chunk; i += 8 {
			h += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", h), nil
}

// Subtitles are timed to one release: a WEB-DL, a BluRay and a TV capture
// of the same episode are cut differently, so a subtitle for another source
// drifts by seconds. These say what a release name tells about its source.
var (
	sourceTags = map[string]string{
		"web": "web", "webdl": "web", "webrip": "web", "nf": "web", "netflix": "web", "amzn": "web", "amazon": "web",
		"dsnp": "web", "hmax": "web", "atvp": "web", "hulu": "web", "itunes": "web",
		"bluray": "bluray", "blu": "bluray", "bdrip": "bluray", "brrip": "bluray", "bd": "bluray", "remux": "bluray", "bdremux": "bluray",
		"hdtv": "hdtv", "pdtv": "hdtv", "dsr": "hdtv", "dvb": "hdtv", "tvrip": "hdtv",
		"dvd": "dvd", "dvdrip": "dvd", "dvdscr": "dvd",
	}
	serviceTags = map[string]string{
		"nf": "nf", "netflix": "nf", "amzn": "amzn", "amazon": "amzn", "dsnp": "dsnp", "disney": "dsnp",
		"hmax": "hmax", "atvp": "atvp", "hulu": "hulu",
	}
	resolutions = map[string]bool{"480p": true, "576p": true, "720p": true, "1080p": true, "2160p": true}
)

type release struct {
	source, service, group, resolution string
}

func parseRelease(name string) release {
	name = strings.ToLower(strings.TrimSpace(name))
	switch path.Ext(name) {
	case ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".srt", ".vtt", ".ass", ".ssa", ".sub":
		name = strings.TrimSuffix(name, path.Ext(name))
	}
	var r release
	for _, t := range strings.FieldsFunc(name, func(c rune) bool { return !('a' <= c && c <= 'z' || '0' <= c && c <= '9') }) {
		if s, ok := sourceTags[t]; ok && r.source == "" {
			r.source = s
		}
		if s, ok := serviceTags[t]; ok && r.service == "" {
			r.service = s
		}
		if resolutions[t] && r.resolution == "" {
			r.resolution = t
		}
	}
	// The group is what follows the last dash ("…x264-LOL.C" is LOL).
	if i := strings.LastIndexByte(name, '-'); i >= 0 {
		g := name[i+1:]
		if j := strings.IndexFunc(g, func(c rune) bool { return !('a' <= c && c <= 'z' || '0' <= c && c <= '9') }); j >= 0 {
			g = g[:j]
		}
		if g != "" && !resolutions[g] && sourceTags[g] == "" {
			r.group = g
		}
	}
	return r
}

// releaseScore is how likely a subtitle named sub is timed for the file:
// above zero is likely, below is likely another cut.
func releaseScore(file, sub string) int {
	if file == "" || sub == "" {
		return 0
	}
	f, s := parseRelease(file), parseRelease(sub)
	score := 0
	if f.source != "" && s.source != "" {
		if f.source == s.source {
			score += 4
		} else {
			score -= 4
		}
	}
	if f.service != "" && f.service == s.service {
		score += 2
	}
	if f.group != "" && f.group == s.group {
		score += 3
	}
	if f.resolution != "" && f.resolution == s.resolution {
		score++
	}
	return score
}
