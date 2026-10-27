package onlinesubs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// openSubtitlesBase is the REST API. Tests point it elsewhere and keep the
// host a sign-in assigns from replacing it.
var (
	openSubtitlesBase = "https://api.opensubtitles.com/api/v1"
	followSignInHost  = true
)

// openSubtitles is the opensubtitles.com REST API. Without an account,
// downloads count against a small per-IP daily allowance.
type openSubtitles struct {
	cfg  config.OpenSubtitlesConfig
	http *http.Client
	ua   string

	mu      sync.Mutex
	token   string
	base    string // the API host a sign-in assigns
	tokenAt time.Time
}

func (o *openSubtitles) name() string { return "opensubtitles" }

func (o *openSubtitles) do(ctx context.Context, method, base, path string, body any, token string, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Api-Key", o.cfg.APIKey)
	req.Header.Set("User-Agent", o.ua)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
			Errors  []string
		}
		_ = json.Unmarshal(b, &e)
		msg := e.Message
		if msg == "" && len(e.Errors) > 0 {
			msg = e.Errors[0]
		}
		if msg == "" {
			msg = resp.Status
		}
		return &statusError{code: resp.StatusCode, msg: "OpenSubtitles: " + msg}
	}
	return json.Unmarshal(b, out)
}

type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string { return e.msg }

// signIn returns a token and API host when an account is set, signing in at
// most once a day.
func (o *openSubtitles) signIn(ctx context.Context, fresh bool) (token, base string, err error) {
	if o.cfg.Username == "" {
		return "", openSubtitlesBase, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !fresh && o.token != "" && time.Since(o.tokenAt) < 20*time.Hour {
		return o.token, o.base, nil
	}
	var out struct {
		Token   string `json:"token"`
		BaseURL string `json:"base_url"`
	}
	if err := o.do(ctx, http.MethodPost, openSubtitlesBase, "/login", map[string]string{"username": o.cfg.Username, "password": o.cfg.Password}, "", &out); err != nil {
		return "", "", err
	}
	o.token, o.base, o.tokenAt = out.Token, openSubtitlesBase, time.Now()
	if out.BaseURL != "" && followSignInHost {
		o.base = "https://" + strings.TrimSuffix(out.BaseURL, "/") + "/api/v1"
	}
	return o.token, o.base, nil
}

func (o *openSubtitles) search(ctx context.Context, q Query) ([]Result, error) {
	v := url.Values{}
	id := strings.TrimLeft(strings.TrimPrefix(q.IMDBID, "tt"), "0")
	if q.episode() {
		v.Set("parent_imdb_id", id)
		v.Set("season_number", strconv.Itoa(q.Season))
		v.Set("episode_number", strconv.Itoa(q.Episode))
		v.Set("type", "episode")
	} else {
		v.Set("imdb_id", id)
		v.Set("type", "movie")
	}
	if len(q.Languages) > 0 {
		v.Set("languages", strings.ToLower(strings.Join(sortedUnique(q.Languages), ",")))
	}
	if q.Hash != "" {
		v.Set("moviehash", q.Hash)
	}
	var out struct {
		Data []struct {
			Attributes struct {
				Language          string `json:"language"`
				DownloadCount     int    `json:"download_count"`
				HearingImpaired   bool   `json:"hearing_impaired"`
				MachineTranslated bool   `json:"machine_translated"`
				HashMatch         bool   `json:"moviehash_match"`
				AITranslated      bool   `json:"ai_translated"`
				Release           string `json:"release"`
				Files             []struct {
					FileID   int    `json:"file_id"`
					FileName string `json:"file_name"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	// The API redirects queries whose keys aren't sorted; Encode sorts them.
	if err := o.do(ctx, http.MethodGet, openSubtitlesBase, "/subtitles?"+v.Encode(), nil, "", &out); err != nil {
		return nil, err
	}
	var rs []Result
	for _, d := range out.Data {
		a := d.Attributes
		if len(a.Files) == 0 {
			continue
		}
		rs = append(rs, Result{
			Provider: o.name(), ID: strconv.Itoa(a.Files[0].FileID), Name: firstNonEmpty(a.Release, a.Files[0].FileName),
			Language: baseLang(a.Language), Downloads: a.DownloadCount, HearingImpaired: a.HearingImpaired,
			Machine: a.MachineTranslated || a.AITranslated, hashMatch: a.HashMatch,
		})
	}
	return rs, nil
}

func (o *openSubtitles) download(ctx context.Context, id string, _ Query) ([]byte, error) {
	fileID, err := strconv.Atoi(id)
	if err != nil {
		return nil, fmt.Errorf("bad subtitle id")
	}
	var out struct {
		Link string `json:"link"`
	}
	for try := 0; ; try++ {
		token, base, err := o.signIn(ctx, try > 0)
		if err != nil {
			return nil, err
		}
		err = o.do(ctx, http.MethodPost, base, "/download", map[string]any{"file_id": fileID, "sub_format": "webvtt"}, token, &out)
		var se *statusError
		// A token that expired early: sign in again once.
		if try == 0 && token != "" && errors.As(err, &se) && se.code == http.StatusUnauthorized {
			continue
		}
		if err != nil {
			return nil, err
		}
		break
	}
	if out.Link == "" {
		return nil, fmt.Errorf("OpenSubtitles gave no download link")
	}
	return get(ctx, o.http, out.Link, o.ua)
}

func get(ctx context.Context, c *http.Client, u, ua string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subtitle download: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 20<<20))
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// baseLang is the ISO 639-1 part of a code like "pt-BR" or "BR_PT".
func baseLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		if l[:i] == "br" { // SubDL's Brazilian Portuguese
			return "pt"
		}
		l = l[:i]
	}
	return l
}

func sortedUnique(vs []string) []string {
	out := slices.DeleteFunc(slices.Clone(vs), func(v string) bool { return v == "" })
	slices.Sort(out)
	return slices.Compact(out)
}
