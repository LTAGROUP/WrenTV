package onlinesubs

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

var (
	subDLBase     = "https://api.subdl.com/api/v1"
	subDLDownload = "https://dl.subdl.com"
)

// subDL is subdl.com: subtitles come as zip files, sometimes a whole season
// in one.
type subDL struct {
	key  string
	http *http.Client
	ua   string
}

func (s *subDL) name() string { return "subdl" }

func (s *subDL) search(ctx context.Context, q Query) ([]Result, error) {
	v := url.Values{"api_key": {s.key}, "imdb_id": {q.IMDBID}, "subs_per_page": {"30"}}
	if q.episode() {
		v.Set("type", "tv")
		v.Set("season_number", strconv.Itoa(q.Season))
		v.Set("episode_number", strconv.Itoa(q.Episode))
	} else {
		v.Set("type", "movie")
	}
	if len(q.Languages) > 0 {
		v.Set("languages", strings.ToUpper(strings.Join(sortedUnique(q.Languages), ",")))
	}
	b, err := get(ctx, s.http, subDLBase+"/subtitles?"+v.Encode(), s.ua)
	if err != nil {
		return nil, fmt.Errorf("SubDL: %w", err)
	}
	var out struct {
		Status    bool   `json:"status"`
		Error     string `json:"error"`
		Subtitles []struct {
			ReleaseName string `json:"release_name"`
			Name        string `json:"name"`
			Language    string `json:"language"`
			URL         string `json:"url"`
			HI          bool   `json:"hi"`
		} `json:"subtitles"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("SubDL: %w", err)
	}
	// "Can't find" is a status false with a message: nothing, not a failure.
	var rs []Result
	for _, x := range out.Subtitles {
		if x.URL == "" {
			continue
		}
		rs = append(rs, Result{Provider: s.name(), ID: x.URL, Name: firstNonEmpty(x.ReleaseName, x.Name), Language: baseLang(x.Language), HearingImpaired: x.HI})
	}
	return rs, nil
}

func (s *subDL) download(ctx context.Context, id string, q Query) ([]byte, error) {
	if !strings.HasPrefix(id, "/") || strings.Contains(id, "..") {
		return nil, fmt.Errorf("bad subtitle id")
	}
	b, err := get(ctx, s.http, subDLDownload+id, s.ua)
	if err != nil {
		return nil, err
	}
	return pickFromZip(b, q)
}

// pickFromZip takes the subtitle file out of an archive: the one named for
// the episode when it holds several.
func pickFromZip(b []byte, q Query) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		// Not an archive: the file itself.
		return b, nil
	}
	var subs []*zip.File
	for _, f := range zr.File {
		switch strings.ToLower(path.Ext(f.Name)) {
		case ".srt", ".vtt", ".ass", ".ssa":
			subs = append(subs, f)
		}
	}
	if len(subs) == 0 {
		return nil, fmt.Errorf("the download has no subtitle file")
	}
	pick := subs[0]
	if q.episode() && len(subs) > 1 {
		tags := []string{fmt.Sprintf("s%02de%02d", q.Season, q.Episode), fmt.Sprintf("%dx%02d", q.Season, q.Episode)}
		for _, f := range subs {
			n := strings.ToLower(path.Base(f.Name))
			if strings.Contains(n, tags[0]) || strings.Contains(n, tags[1]) {
				pick = f
				break
			}
		}
	}
	rc, err := pick.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 20<<20))
}
