package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Addon is a Stremio addon queried for torrent streams.
type Addon struct {
	Name    string
	BaseURL string // manifest URL without /manifest.json
}

// Release is one torrent an addon offered for a title.
type Release struct {
	Name     string // release name
	Text     string // everything the addon said about it (for quality matching)
	InfoHash string
	FileIdx  int // file index inside the torrent, -1 if unknown
	FileName string
	Size     int64
	Seeders  int
	Addon    string
	trackers []string
}

// Magnet builds a magnet link with the trackers the addon listed.
func (r Release) Magnet() string {
	v := url.Values{"xt": {"urn:btih:" + r.InfoHash}}
	if r.Name != "" {
		v.Set("dn", r.Name)
	}
	for _, t := range r.trackers {
		v.Add("tr", t)
	}
	return "magnet:?" + v.Encode()
}

// ParseAddon turns a manifest URL into an Addon.
func ParseAddon(name, raw string) (Addon, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "stremio://"))
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return Addon{}, fmt.Errorf("invalid addon URL %q", raw)
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/manifest.json")
	u.RawQuery, u.Fragment = "", ""
	if name == "" {
		name = u.Host
	}
	return Addon{Name: name, BaseURL: strings.TrimRight(u.String(), "/")}, nil
}

// addonGuard shares cooldowns per addon: one 429 from Torrentio pauses every
// search against it instead of each lookup repeating the request.
var addonGuards = struct {
	sync.Mutex
	m map[string]*guard
}{m: map[string]*guard{}}

func addonGuard(name string) *guard {
	addonGuards.Lock()
	defer addonGuards.Unlock()
	g := addonGuards.m[name]
	if g == nil {
		g = newGuard(name, 200*time.Millisecond, 10*time.Second)
		addonGuards.m[name] = g
	}
	return g
}

var addonClient = &http.Client{Timeout: 20 * time.Second}

// Manifest fetches an addon's manifest and checks that it serves streams.
func Manifest(ctx context.Context, a Addon) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.BaseURL+"/manifest.json", nil)
	req.Header.Set("User-Agent", "WrenTV/1.0")
	resp, err := addonClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest: %s", resp.Status)
	}
	var m struct {
		Name      string `json:"name"`
		Resources []any  `json:"resources"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return "", fmt.Errorf("manifest: %w", err)
	}
	for _, r := range m.Resources {
		switch v := r.(type) {
		case string:
			if v == "stream" {
				return m.Name, nil
			}
		case map[string]any:
			if v["name"] == "stream" {
				return m.Name, nil
			}
		}
	}
	return "", errors.New("this addon doesn't provide streams")
}

// Search asks every addon for streams of a title (IMDb id; with season and
// episode for shows) and merges them by info hash.
func Search(ctx context.Context, addons []Addon, imdbID string, season, episode int) ([]Release, error) {
	if imdbID == "" {
		return nil, errors.New("title has no IMDb id")
	}
	kind, id := "movie", imdbID
	if season > 0 || episode > 0 {
		kind, id = "series", fmt.Sprintf("%s:%d:%d", imdbID, season, episode)
	}
	type result struct {
		rows []Release
		err  error
	}
	ch := make(chan result, len(addons))
	for _, a := range addons {
		go func(a Addon) {
			rows, err := searchAddon(ctx, a, kind, id)
			ch <- result{rows, err}
		}(a)
	}
	byHash := map[string]Release{}
	var errs []string
	for range addons {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err.Error())
			continue
		}
		for _, rel := range r.rows {
			if old, ok := byHash[rel.InfoHash]; !ok || rel.Seeders > old.Seeders || (old.FileIdx < 0 && rel.FileIdx >= 0) {
				byHash[rel.InfoHash] = rel
			}
		}
	}
	out := make([]Release, 0, len(byHash))
	for _, r := range byHash {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seeders > out[j].Seeders })
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.New("addons failed: " + strings.Join(errs, "; "))
	}
	return out, nil
}

type addonStream struct {
	Name          string   `json:"name"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	InfoHash      string   `json:"infoHash"`
	FileIdx       *int     `json:"fileIdx"`
	Sources       []string `json:"sources"`
	BehaviorHints struct {
		Filename  string `json:"filename"`
		VideoSize int64  `json:"videoSize"`
	} `json:"behaviorHints"`
}

var (
	sizeRE = regexp.MustCompile(`(?i)([0-9]+(?:[.,][0-9]+)?)\s*(tib|tb|gib|gb|mib|mb)\b`)
	seedRE = regexp.MustCompile(`(?i)(?:👤\s*|seeders?\D{0,3})([0-9]+)`)
)

func searchAddon(ctx context.Context, a Addon, kind, id string) ([]Release, error) {
	g := addonGuard(a.Name)
	u := a.BaseURL + "/stream/" + kind + "/" + url.PathEscape(id) + ".json"
	var payload struct {
		Streams []addonStream `json:"streams"`
	}
	for attempt := 0; ; attempt++ {
		if err := g.wait(ctx, "stream"); err != nil {
			return nil, err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "WrenTV/1.0")
		resp, err := addonClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			d := min(max(retryAfter(resp.Header.Get("Retry-After")), 2*time.Second), 30*time.Second)
			resp.Body.Close()
			err := g.block(d)
			if attempt > 0 {
				return nil, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d):
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("%s: %s", a.Name, resp.Status)
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, err)
		}
		break
	}
	out := make([]Release, 0, len(payload.Streams))
	for _, s := range payload.Streams {
		if r, ok := releaseFrom(s, a.Name); ok {
			out = append(out, r)
		}
	}
	slog.Debug("addon search", "addon", a.Name, "id", id, "streams", len(out))
	return out, nil
}

func releaseFrom(s addonStream, addon string) (Release, bool) {
	hash := strings.ToLower(strings.TrimSpace(s.InfoHash))
	if len(hash) != 40 {
		return Release{}, false
	}
	text := strings.TrimSpace(strings.Join([]string{s.Name, s.Title, s.Description, s.BehaviorHints.Filename}, "\n"))
	// Torrentio's title starts with the torrent name; Comet's description
	// starts with the file name behind an emoji.
	name := cleanName(firstLine(s.Title))
	if name == "" {
		name = cleanName(firstLine(s.Description))
	}
	if name == "" {
		name = s.BehaviorHints.Filename
	}
	r := Release{Name: name, Text: text, InfoHash: hash, FileIdx: -1, FileName: s.BehaviorHints.Filename, Size: s.BehaviorHints.VideoSize,
		Seeders: -1, Addon: addon}
	if s.FileIdx != nil {
		r.FileIdx = *s.FileIdx
	}
	if r.Size == 0 {
		r.Size = parseSize(text)
	}
	if m := seedRE.FindStringSubmatch(text); m != nil {
		r.Seeders, _ = strconv.Atoi(m[1])
	}
	for _, src := range s.Sources {
		if t, ok := strings.CutPrefix(src, "tracker:"); ok {
			r.trackers = append(r.trackers, t)
		}
	}
	return r, true
}

// cleanName drops leading emoji and symbols addons decorate names with.
func cleanName(s string) string {
	return strings.TrimLeftFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '[' && r != '('
	})
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(s)
}

func parseSize(s string) int64 {
	all := sizeRE.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return 0
	}
	m := all[len(all)-1]
	n, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
	switch strings.ToLower(m[2]) {
	case "tb", "tib":
		return int64(n * (1 << 40))
	case "gb", "gib":
		return int64(n * (1 << 30))
	}
	return int64(n * (1 << 20))
}
