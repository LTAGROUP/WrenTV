package streaming

import (
	"cmp"
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

// Addon is a Stremio addon queried for streams: torrents, or play links
// from addons that add releases to a debrid account themselves.
type Addon struct {
	Name    string
	BaseURL string // manifest URL without /manifest.json
}

// Release is one torrent an addon offered for a title, or a play link
// (Link set, InfoHash only when the addon said which torrent it is).
type Release struct {
	Name     string // release name
	Text     string // everything the addon said about it (for quality matching)
	InfoHash string
	NZB      string
	Link     string // addon URL that redirects to the file on the debrid CDN
	Usenet   bool   // a Usenet post (an addon's link, or an indexer's NZB)
	Indexer  string // the Newznab indexer that found the post
	FileIdx  int    // file index inside the torrent, -1 if unknown
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
		return "", redactURL(err)
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
// episode for shows) and merges torrents by info hash.
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
	var order []string // first seen first, so ties keep the addon's order
	var errs []string
	for range addons {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err.Error())
			continue
		}
		for _, rel := range r.rows {
			key := rel.InfoHash
			if rel.Link != "" {
				key = "link:" + rel.Link
			}
			old, ok := byHash[key]
			if !ok {
				order = append(order, key)
			}
			if !ok || rel.Link != "" || rel.Seeders > old.Seeders || (old.FileIdx < 0 && rel.FileIdx >= 0) {
				byHash[key] = rel
			}
		}
	}
	out := make([]Release, 0, len(order))
	for _, k := range order {
		out = append(out, byHash[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seeders > out[j].Seeders })
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
	URL           string   `json:"url"`
	Sources       []string `json:"sources"`
	BehaviorHints struct {
		Filename  string `json:"filename"`
		VideoSize int64  `json:"videoSize"`
	} `json:"behaviorHints"`
	// AIOStreams describes each stream here for clients it recognises.
	StreamData *struct {
		Type       string `json:"type"`
		Filename   string `json:"filename"`
		FolderName string `json:"folderName"`
		Size       int64  `json:"size"`
		Service    *struct {
			ID     string `json:"id"`
			Cached bool   `json:"cached"`
		} `json:"service"`
		Torrent *struct {
			InfoHash string `json:"infoHash"`
		} `json:"torrent"`
	} `json:"streamData"`
}

// addonUA starts with "AIO" because that is how AIOStreams decides to
// include streamData (stream type, cached or not) in its responses.
const addonUA = "AIOStreams-compatible WrenTV/1.0"

var (
	sizeRE = regexp.MustCompile(`(?i)([0-9]+(?:[.,][0-9]+)?)\s*(tib|tb|gib|gb|mib|mb)\b`)
	seedRE = regexp.MustCompile(`(?i)(?:[👤👥]\s*|seeders?\D{0,3})([0-9]+)`)
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
		req.Header.Set("User-Agent", addonUA)
		resp, err := addonClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, redactURL(err)) // the URL can hold the addon's config
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
	text := strings.TrimSpace(strings.Join([]string{s.Name, s.Title, s.Description, s.BehaviorHints.Filename}, "\n"))
	var link string
	if len(hash) != 40 {
		var ok bool
		if link, ok = playLink(s, text); !ok {
			return Release{}, false
		}
		hash = ""
	}
	// Torrentio's title starts with the torrent name; Comet's description
	// starts with the file name behind an emoji.
	name := cleanName(firstLine(s.Title))
	if name == "" {
		name = cleanName(firstLine(s.Description))
	}
	if name == "" || link != "" {
		name = cmp.Or(s.BehaviorHints.Filename, name)
	}
	r := Release{Name: name, Text: text, InfoHash: hash, Link: link, FileIdx: -1, FileName: s.BehaviorHints.Filename, Size: s.BehaviorHints.VideoSize,
		Seeders: -1, Addon: addon}
	if d := s.StreamData; d != nil && link != "" {
		r.Usenet = d.Type == "usenet"
		r.FileName = cmp.Or(r.FileName, d.Filename)
		r.Name = cmp.Or(d.FolderName, r.FileName, r.Name)
		r.Size = cmp.Or(r.Size, d.Size)
		if d.Torrent != nil && len(d.Torrent.InfoHash) == 40 {
			r.InfoHash = strings.ToLower(d.Torrent.InfoHash)
		}
	} else if link != "" {
		r.Usenet = usenetRE.MatchString(text)
	}
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

var usenetRE = regexp.MustCompile(`(?i)\b(usenet|nzb|easynews)\b|📰`)

// playLink returns the link of a stream an addon serves from a debrid
// account. Links that would start a download (not cached yet), errors and
// notices dressed up as streams are skipped: they'd keep the player waiting.
func playLink(s addonStream, text string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(s.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", false
	}
	if d := s.StreamData; d != nil {
		switch d.Type {
		case "usenet", "debrid", "http", "":
		default:
			return "", false // error, statistic, info, live, ...
		}
		if d.Service != nil && !d.Service.Cached {
			return "", false
		}
	} else if strings.Contains(text, "⏳") || strings.Contains(strings.ToLower(text), "uncached") {
		return "", false
	}
	return u.String(), true
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
