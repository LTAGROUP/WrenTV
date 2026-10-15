package library

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".mov": true, ".avi": true, ".ts": true, ".m2ts": true,
	".webm": true, ".wmv": true, ".mpg": true, ".mpeg": true, ".flv": true, ".iso": false,
}

func IsVideo(path string) bool {
	return videoExts[strings.ToLower(filepath.Ext(path))]
}

// IsIgnored skips samples, extras and hidden/system files.
func IsIgnored(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "._") {
		return true
	}
	if reSample.MatchString(base) {
		return true
	}
	for _, part := range strings.Split(strings.ToLower(filepath.ToSlash(filepath.Dir(path))), "/") {
		switch part {
		case "sample", "samples", "extras", "featurettes", "behind the scenes", "deleted scenes", "trailers", "@eadir", "#recycle", "$recycle.bin":
			return true
		}
	}
	return false
}

var (
	reSample = regexp.MustCompile(`(?i)(^|[\W_])sample([\W_]|$)`)
	// S01E02, S01E02E03, S01E02-E03, s1e2
	reSxE = regexp.MustCompile(`(?i)\bs(\d{1,4})[ ._-]*e(\d{1,4})(?:[ ._-]*-?[ ._-]*e?(\d{1,4}))?\b`)
	// 1x02
	reNxN = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{2,3})\b`)
	// "Season 1" folder, "Episode 3" or "Ep 3" file names
	reSeasonDir = regexp.MustCompile(`(?i)^(?:season|series|staffel|saison|s)[ ._-]*(\d{1,4})$`)
	reSpecials  = regexp.MustCompile(`(?i)^specials?$`)
	reEpisode   = regexp.MustCompile(`(?i)\b(?:episode|ep|e)[ ._-]*(\d{1,4})\b`)
	reYear      = regexp.MustCompile(`(?:^|[\s.(\[_-])((?:19|20)\d{2})(?:[\s.)\]_-]|$)`)
	// Release tags after which the title is over.
	reJunk = regexp.MustCompile(`(?i)[\s.\[(_-](?:2160p|1080p|1080i|720p|576p|480p|4k|uhd|hdr10?\+?|hdr|dv|dovi|dolby[ .]?vision|bluray|blu-ray|bdremux|remux|bdrip|brrip|web[ .-]?dl|webrip|web|hdtv|dvdrip|dvd|x264|x265|h\.?264|h\.?265|hevc|avc|av1|10bit|ddp?5\.1|dd\+|atmos|truehd|dts(?:-?hd)?|aac|ac3|eac3|flac|proper|repack|extended|unrated|imax|criterion|multi|dubbed|subbed|complete)(?:[\s.\])_-]|$)`)
	// Season-pack markers in torrent/folder names ("Show.Name.S01.1080p").
	reSeasonTag = regexp.MustCompile(`(?i)[\s.\[(_-](?:s\d{1,2}(?:[\s._-]*e\d{1,4})?|seasons?[\s._-]?\d{1,2}(?:-\d{1,2})?)(?:[\s.\])_-]|$)`)
	reBrackets  = regexp.MustCompile(`\[[^\]]*\]|\{[^}]*\}`)
	reTmdbTag   = regexp.MustCompile(`(?i)[\[{(]tmdb(?:id)?[-=](\d+)[\]})]`)
	reImdbTag   = regexp.MustCompile(`(?i)[\[{(]imdb(?:id)?[-=](tt\d{5,})[\]})]`)
)

// MovieInfo is what we can learn about a movie from its path alone.
type MovieInfo struct {
	Title  string
	Year   int
	TMDBID int
	IMDBID string // from {imdb-tt…} tags
}

// EpisodeInfo is what we can learn about an episode from its path alone.
type EpisodeInfo struct {
	ShowTitle string
	ShowYear  int
	ShowTMDB  int
	ShowIMDB  string
	Season    int
	Episode   int
	EndEp     int // multi-episode files
	OK        bool
}

// ParseMovie prefers the containing folder name when it looks like "Title (Year)",
// otherwise parses the file name.
func ParseMovie(libRoot, path string) MovieInfo {
	file := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	dir := filepath.Base(filepath.Dir(path))
	rel, _ := filepath.Rel(libRoot, filepath.Dir(path))
	fileInfo := parseTitleYear(file)
	if rel != "." && rel != "" {
		dirInfo := parseTitleYear(dir)
		if dirInfo.Year > 0 || dirInfo.TMDBID > 0 || dirInfo.IMDBID != "" || fileInfo.Year == 0 {
			if dirInfo.Title != "" {
				return dirInfo
			}
		}
		// IDs tagged on the folder still apply when the file name wins.
		if fileInfo.TMDBID == 0 {
			fileInfo.TMDBID = dirInfo.TMDBID
		}
		if fileInfo.IMDBID == "" {
			fileInfo.IMDBID = dirInfo.IMDBID
		}
	}
	return fileInfo
}

// ParseEpisode derives show/season/episode. The show title comes from the
// first folder under the library root when present ("Show (2019)/Season 1/x.mkv").
func ParseEpisode(libRoot, path string) EpisodeInfo {
	rel, err := filepath.Rel(libRoot, path)
	if err != nil {
		rel = path
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	file := strings.TrimSuffix(parts[len(parts)-1], filepath.Ext(path))
	var ep EpisodeInfo

	if m := reSxE.FindStringSubmatchIndex(file); m != nil {
		ep.Season = atoi(file[m[2]:m[3]])
		ep.Episode = atoi(file[m[4]:m[5]])
		if m[6] > 0 {
			ep.EndEp = atoi(file[m[6]:m[7]])
		}
		ep.OK = true
		if len(parts) == 1 {
			ep.ShowTitle = file[:m[0]]
		}
	} else if m := reNxN.FindStringSubmatchIndex(file); m != nil {
		ep.Season = atoi(file[m[2]:m[3]])
		ep.Episode = atoi(file[m[4]:m[5]])
		ep.OK = true
		if len(parts) == 1 {
			ep.ShowTitle = file[:m[0]]
		}
	}

	// Season from folder if the file name didn't carry it.
	seasonFromDir := -1
	if len(parts) >= 2 {
		d := parts[len(parts)-2]
		if m := reSeasonDir.FindStringSubmatch(strings.TrimSpace(d)); m != nil {
			seasonFromDir = atoi(m[1])
		} else if reSpecials.MatchString(d) {
			seasonFromDir = 0
		}
	}
	if !ep.OK && seasonFromDir >= 0 {
		if m := reEpisode.FindStringSubmatch(file); m != nil {
			ep.Season, ep.Episode, ep.OK = seasonFromDir, atoi(m[1]), true
		} else if n := leadingNumber(file); n > 0 {
			ep.Season, ep.Episode, ep.OK = seasonFromDir, n, true
		}
	}

	if len(parts) >= 2 {
		// Show folder is the first path component under the library root, unless
		// that component is itself a season folder (library pointed at a show).
		show := parts[0]
		if !reSeasonDir.MatchString(show) {
			ep.ShowTitle = show
		}
	}
	if ep.ShowTitle == "" && len(parts) >= 2 {
		ep.ShowTitle = parts[0]
	}
	ti := parseTitleYear(ep.ShowTitle)
	ep.ShowTitle, ep.ShowYear, ep.ShowTMDB, ep.ShowIMDB = ti.Title, ti.Year, ti.TMDBID, ti.IMDBID
	if ep.EndEp <= ep.Episode {
		ep.EndEp = 0
	}
	return ep
}

func parseTitleYear(s string) MovieInfo {
	var mi MovieInfo
	if m := reTmdbTag.FindStringSubmatch(s); m != nil {
		mi.TMDBID = atoi(m[1])
		s = reTmdbTag.ReplaceAllString(s, " ")
	}
	if m := reImdbTag.FindStringSubmatch(s); m != nil {
		mi.IMDBID = strings.ToLower(m[1])
		s = reImdbTag.ReplaceAllString(s, " ")
	}
	s = reBrackets.ReplaceAllString(s, " ")
	// Cut at season markers and release junk.
	if loc := reSeasonTag.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]]
	}
	if loc := reJunk.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]]
	}
	// Last plausible year wins ("2001 A Space Odyssey (1968)"), but never the whole title.
	locs := reYear.FindAllStringSubmatchIndex(s, -1)
	for i := len(locs) - 1; i >= 0; i-- {
		l := locs[i]
		if l[2] == 0 {
			continue
		}
		mi.Year = atoi(s[l[2]:l[3]])
		s = s[:l[2]]
		break
	}
	mi.Title = cleanTitle(s)
	return mi
}

func cleanTitle(s string) string {
	// Dots/underscores as separators unless it's an acronym like "S.H.I.E.L.D."
	if strings.Count(s, " ") == 0 {
		s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	} else {
		s = strings.ReplaceAll(s, "_", " ")
	}
	s = strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-' || r == '(' || r == '[' || r == '.' || r == ','
	})
	return strings.Join(strings.Fields(s), " ")
}

// MatchKey normalizes a title+year for de-duplication before we have a TMDB id.
func MatchKey(title string, year int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	if year > 0 {
		b.WriteString(":" + strconv.Itoa(year))
	}
	return b.String()
}

func leadingNumber(s string) int {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i > 4 {
		return 0
	}
	return atoi(s[:i])
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
