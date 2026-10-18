package streaming

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var (
	q2160RE = regexp.MustCompile(`(?i)\b(2160p|4k|uhd)\b`)
	q1080RE = regexp.MustCompile(`(?i)\b(1080p|fhd)\b`)
	q720RE  = regexp.MustCompile(`(?i)\b720p\b`)
	// Releases nobody wants to watch: camcorder/telesync copies, screeners, samples.
	junkRE = regexp.MustCompile(`(?i)\b(cam|camrip|hdcam|ts|hdts|telesync|tc|telecine|scr|screener|dvdscr|workprint|sample|trailer|upscaled?|ai[ ._-]upscale[d]?)\b`)
	// S01E02, s1e2, 1x02
	episodeRE     = regexp.MustCompile(`(?i)\bs(\d{1,2})[ ._-]*e(\d{1,3})\b|\b(\d{1,2})x(\d{2,3})\b`)
	multiSeasonRE = regexp.MustCompile(`(?i)\bs\d{1,2}\s*-\s*s?\d{1,2}\b|\bseasons?[ ._-]*\d{1,2}\s*-\s*\d{1,2}\b`)
	// Season 01 Episode 02
	episodeWordsRE = regexp.MustCompile(`(?i)\bseason[ ._-]*(\d{1,2})\W*episode[ ._-]*(\d{1,3})\b`)
	seasonRE       = regexp.MustCompile(`(?i)\b(?:s(\d{1,2})|season[ ._-]*(\d{1,2}))\b`)
	completeRE     = regexp.MustCompile(`(?i)\b(complete|season[ ._-]*pack|all[ ._-]*seasons)\b`)
	// Releases players can't handle: H.266 video, and Blu-ray disc folders
	// whose numbered clips may not be the film.
	unplayableRE = regexp.MustCompile(`(?i)\b(vvc|vvenc|h\.?266|x266)\b|(^|/)bdmv/|\.iso$`)
	// Subtitles burned into the picture.
	hardsubRE = regexp.MustCompile(`(?i)\b(hc|hardsub(bed|s)?|hard[ ._-]?coded|[a-z]{2,3}sub(bed|s)?)\b`)
	// Dubbed or foreign-language releases.
	foreignRE = regexp.MustCompile(`(?i)\b(\d+x[ ._-]?dub|dub(bed)?|mvo|dvo|multi|dual[ ._-]?audio|vf[fiq]?|vostfr|truefrench|french|german|deutsch|ita|italian|spanish|castellano|latino|rus|russian|ukr|hindi|tamil|telugu|korean|kor|japanese|chinese|chs|cht|polish|pl|turkish|portuguese|dublado|nordic|czech|hun)\b`)
)

// foreignPenalty pushes releases that aren't plainly in English below the
// ones that are: burned-in subtitles first, then dubs, language tags and
// titles written in another script.
func foreignPenalty(text string) int {
	if hardsubRE.MatchString(text) {
		return 5000
	}
	if foreignRE.MatchString(text) {
		return 2500
	}
	for _, r := range text {
		if r > unicode.MaxLatin1 && unicode.IsLetter(r) && !unicode.In(r, unicode.Latin) {
			return 2500
		}
	}
	return 0
}

// QualityOf reads the resolution tag of a release ("2160p", "1080p", "720p" or "").
func QualityOf(text string) string {
	switch {
	case q2160RE.MatchString(text):
		return "2160p"
	case q1080RE.MatchString(text):
		return "1080p"
	case q720RE.MatchString(text):
		return "720p"
	}
	return ""
}

// HeightOf is the frame height for a quality tag.
func HeightOf(quality string) int {
	switch quality {
	case "2160p":
		return 2160
	case "1080p":
		return 1080
	case "720p":
		return 720
	}
	return 0
}

// EpisodeOf finds a season/episode marker in a file name, preferring the
// file over its folders (a pack folder can itself say S01E01-E10).
func EpisodeOf(name string) (season, episode int, ok bool) {
	parts := strings.Split(strings.ReplaceAll(name, `\`, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if m := episodeRE.FindStringSubmatch(parts[i]); m != nil {
			if m[1] != "" {
				season, _ = strconv.Atoi(m[1])
				episode, _ = strconv.Atoi(m[2])
			} else {
				season, _ = strconv.Atoi(m[3])
				episode, _ = strconv.Atoi(m[4])
			}
			return season, episode, true
		}
		if m := episodeWordsRE.FindStringSubmatch(parts[i]); m != nil {
			season, _ = strconv.Atoi(m[1])
			episode, _ = strconv.Atoi(m[2])
			return season, episode, true
		}
	}
	return 0, 0, false
}

// isJunk checks a release or file name (a .ts extension is a container, not a telesync).
func isJunk(name string) bool {
	if isVideoName(name) {
		name = strings.TrimSuffix(name, path.Ext(name))
	}
	return junkRE.MatchString(name)
}

// isPack reports whether a release name looks like a whole season (or more).
func isPack(name string, season int) bool {
	if _, _, ok := EpisodeOf(name); ok {
		return false
	}
	if completeRE.MatchString(name) {
		return true
	}
	if m := seasonRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1] + m[2])
		return n == season
	}
	return false
}

// Rank orders an addon's releases for one quality: matching quality only,
// junk, unplayable and oversized releases dropped, then (for episodes) season packs first
// so the rest of the season can come from the same torrent, English releases
// before dubbed or hard-subbed ones, then quality markers and seeders.
func Rank(rels []Release, quality string, maxSize int64, season int) []Release {
	type scored struct {
		r     Release
		score int
	}
	var list []scored
	for _, r := range rels {
		if QualityOf(r.Text) != quality || isJunk(r.Name) || isJunk(r.FileName) || unplayableRE.MatchString(r.Name) ||
			unplayableRE.MatchString(r.FileName) {
			continue
		}
		// Packs report the whole torrent's size; their file is checked once picked.
		if maxSize > 0 && r.Size > maxSize && !isPack(r.Name, season) && !multiSeasonRE.MatchString(r.Name) {
			continue
		}
		t := strings.ToLower(r.Text)
		score := min(max(r.Seeders, 0), 500) * 4
		if season > 0 && isPack(r.Name, season) {
			score += 3000
		}
		for _, b := range []struct {
			tag string
			pts int
		}{{"remux", 400}, {"bluray", 250}, {"blu-ray", 250}, {"web-dl", 300}, {"webrip", 150}, {"x265", 100}, {"hevc", 100},
			{"dolby vision", 300}, {" dv ", 300}, {"hdr10", 200}, {"hdr", 150}, {"atmos", 150}} {
			if strings.Contains(t, b.tag) {
				score += b.pts
			}
		}
		score -= foreignPenalty(r.Text)
		if quality != "2160p" && (strings.Contains(t, "hdr") || strings.Contains(t, "dolby vision")) {
			score -= 300 // HDR at 1080p is rarer to play back well
		}
		list = append(list, scored{r, score})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	out := make([]Release, len(list))
	for i, s := range list {
		out[i] = s.r
	}
	return out
}

// PickFile finds the video for a movie (season 0) or one episode in a
// provider's file list, using the addon's file name when it gave one.
func PickFile(files []RemoteFile, fileName string, season, episode int) (RemoteFile, bool) {
	if fileName != "" {
		want := strings.ToLower(path.Base(fileName))
		for _, f := range files {
			if strings.ToLower(path.Base(f.Name)) == want && isVideoName(f.Name) {
				return f, true
			}
		}
	}
	var best RemoteFile
	found := false
	for _, f := range files {
		if !isVideoName(f.Name) || isJunk(path.Base(f.Name)) {
			continue
		}
		if season > 0 {
			s, e, ok := EpisodeOf(f.Name)
			if !ok || s != season || e != episode {
				continue
			}
		}
		if !found || f.Size > best.Size {
			best, found = f, true
		}
	}
	return best, found
}

// EpisodeFiles maps each episode of a season found in a pack to its file.
func EpisodeFiles(files []RemoteFile, season int) map[int]RemoteFile {
	out := map[int]RemoteFile{}
	for _, f := range files {
		if !isVideoName(f.Name) {
			continue
		}
		if s, e, ok := EpisodeOf(f.Name); ok && s == season {
			if old, dup := out[e]; !dup || f.Size > old.Size {
				out[e] = f
			}
		}
	}
	return out
}

var nonAlnumRE = regexp.MustCompile(`[^a-z0-9]+`)

// sameRelease normalizes a release name, so one release found both on
// Usenet and as a torrent is offered once.
func sameRelease(name string) string {
	if n := nonAlnumRE.ReplaceAllString(strings.ToLower(name), ""); n != "" {
		return "name:" + n
	}
	return ""
}

// ReleaseTag describes where a stream comes from for version pickers:
// "Usenet · Remux", "Torrent · WEB-DL".
func ReleaseTag(kind, name string) string {
	tag := "Torrent"
	if kind == "usenet" {
		tag = "Usenet"
	}
	t := strings.ToLower(name)
	for _, s := range []struct{ marker, label string }{{"remux", "Remux"}, {"bluray", "BluRay"}, {"blu-ray", "BluRay"},
		{"web-dl", "WEB-DL"}, {"webdl", "WEB-DL"}, {"webrip", "WEBRip"}, {"hdtv", "HDTV"}} {
		if strings.Contains(t, s.marker) {
			return tag + " · " + s.label
		}
	}
	return tag
}
