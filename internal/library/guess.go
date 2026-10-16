package library

import (
	"context"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/db"
)

// File names usually say what's inside ("Movie (2022) [2160p].mkv",
// "Show.S01E02.1080p.WEB-DL.DV.HDR.x265.mkv"). Until a file is probed, its
// resolution, codec, HDR format and container are guessed from its name and
// extension, so stats, quality badges and filters work without reading the
// file. A probe replaces the guess.
var (
	reGuessRes   = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])(2160|1440|1080|720|576|480)[pi](?:[\s.\])_-]|$)`)
	reGuess4K    = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])(?:4k|uhd)(?:[\s.\])_-]|$)`)
	reGuessHEVC  = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])(?:x265|h\.?265|hevc)(?:[\s.\])_-]|$)`)
	reGuessH264  = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])(?:x264|h\.?264|avc)(?:[\s.\])_-]|$)`)
	reGuessAV1   = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])av1(?:[\s.\])_-]|$)`)
	reGuessMPEG4 = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])(?:xvid|divx)(?:[\s.\])_-]|$)`)
	reGuessDV    = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])(?:dv|dovi|dolby[\s.]?vision)(?:[\s.\])_-]|$)`)
	reGuessHDR   = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])hdr(?:10)?\+?(?:[\s.\])_-]|$)`)
	reGuessHLG   = regexp.MustCompile(`(?i)(?:^|[\s.\[(_-])hlg(?:[\s.\])_-]|$)`)
)

// GuessFromName fills f's empty media fields from its file and folder names.
// It reports whether it changed anything.
func GuessFromName(f *db.MediaFile) bool {
	name := filepath.Base(f.Path)
	text := strings.TrimSuffix(name, filepath.Ext(name)) + " " + filepath.Base(filepath.Dir(f.Path))
	changed := false
	if f.Container == "" {
		switch ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")); ext {
		case "":
		case "m4v", "mov":
			f.Container, changed = "mp4", true
		case "m2ts":
			f.Container, changed = "ts", true
		case "mpeg":
			f.Container, changed = "mpg", true
		default:
			f.Container, changed = ext, true
		}
	}
	if f.Width == 0 && f.Height == 0 {
		h := 0
		if m := reGuessRes.FindStringSubmatch(text); m != nil {
			h = atoi(m[1])
		} else if reGuess4K.MatchString(text) {
			h = 2160
		}
		if h > 0 {
			f.Width, f.Height, changed = widthFor(h), h, true
		}
	}
	if f.VideoCodec == "" {
		codec := ""
		switch {
		case reGuessHEVC.MatchString(text):
			codec = "hevc"
		case reGuessAV1.MatchString(text):
			codec = "av1"
		case reGuessH264.MatchString(text):
			codec = "h264"
		case reGuessMPEG4.MatchString(text):
			codec = "mpeg4"
		}
		if codec != "" {
			f.VideoCodec, changed = codec, true
		}
	}
	if f.HDR == "" {
		dv, hdr := reGuessDV.MatchString(text), reGuessHDR.MatchString(text)
		switch {
		case dv && hdr:
			f.HDR = "DV+HDR10"
		case dv:
			f.HDR = "DV"
		case hdr:
			f.HDR = "HDR10"
		case reGuessHLG.MatchString(text):
			f.HDR = "HLG"
		}
		changed = changed || f.HDR != ""
	}
	return changed
}

// widthFor is the usual 16:9 width for a resolution tag.
func widthFor(h int) int {
	switch h {
	case 2160:
		return 3840
	case 1440:
		return 2560
	case 1080:
		return 1920
	case 720:
		return 1280
	case 576:
		return 720
	default:
		return 640
	}
}

// guessUnprobed fills in name-based guesses for files scanned before guessing
// existed. Files that already have media info are left alone.
func (s *Scanner) guessUnprobed(ctx context.Context) {
	files, err := s.db.UnguessedFiles(ctx)
	if err != nil || len(files) == 0 {
		return
	}
	var changed []*db.MediaFile
	for _, f := range files {
		if GuessFromName(f) {
			changed = append(changed, f)
		}
	}
	if err := s.db.UpdateFileGuesses(ctx, changed); err != nil {
		slog.Warn("saving media guesses", "err", err)
		return
	}
	if len(changed) > 0 {
		slog.Info("guessed media info from file names", "files", len(changed))
	}
}
