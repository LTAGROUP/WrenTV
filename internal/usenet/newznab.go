package usenet

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Indexer is a Newznab indexer account (NZBGeek, DrunkenSlug, …).
type Indexer struct {
	Name   string
	URL    string // the API endpoint, …/api
	APIKey string
}

// Query is what to search for. IMDb ids are used where the indexer
// supports them; the title otherwise.
type Query struct {
	IMDB    string // tt…
	Title   string
	Year    int
	Season  int // 0 for movies
	Episode int
}

// Result is one post an indexer found.
type Result struct {
	Indexer string
	Title   string
	Size    int64
	// NZB is the download link without the API key, safe to store.
	NZB     string
	PubDate time.Time
	Grabs   int
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// Endpoint returns the API URL for what an owner typed: the site or …/api.
func Endpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("enter the indexer's https:// address")
	}
	u.RawQuery, u.Fragment = "", ""
	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/api") {
		p += "/api"
	}
	u.Path = p
	return u.String(), nil
}

// caps is which search parameters an indexer takes.
type caps struct {
	movieIMDB, tvIMDB, tvSearch, movieSearch bool
}

var (
	capsMu    sync.Mutex
	capsCache = map[string]caps{}
)

func (ix Indexer) get(ctx context.Context, params url.Values) ([]byte, error) {
	params.Set("apikey", ix.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ix.URL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "WrenTV/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, ix.redact(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, ix.redact(err)
	}
	if err := apiError(b); err != nil {
		return nil, fmt.Errorf("%s: %w", ix.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", ix.Name, resp.Status)
	}
	return b, nil
}

// redact keeps the API key out of errors.
func (ix Indexer) redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		host := ue.URL
		if u, perr := url.Parse(ue.URL); perr == nil {
			host = u.Host
		}
		return fmt.Errorf("%s: %s %s: %w", ix.Name, ue.Op, host, ue.Err)
	}
	return err
}

// apiError reads Newznab's <error code="…" description="…"/> reply.
func apiError(b []byte) error {
	head := b[:min(len(b), 512)]
	if !strings.Contains(string(head), "<error") {
		return nil
	}
	var e struct {
		XMLName     xml.Name `xml:"error"`
		Code        string   `xml:"code,attr"`
		Description string   `xml:"description,attr"`
	}
	if xml.Unmarshal(b, &e) != nil || e.XMLName.Local != "error" {
		return nil
	}
	return fmt.Errorf("indexer error %s: %s", e.Code, e.Description)
}

func (ix Indexer) caps(ctx context.Context) (caps, error) {
	capsMu.Lock()
	c, ok := capsCache[ix.URL]
	capsMu.Unlock()
	if ok {
		return c, nil
	}
	b, err := ix.get(ctx, url.Values{"t": {"caps"}})
	if err != nil {
		return caps{}, err
	}
	var doc struct {
		Searching struct {
			Movie struct {
				Available string `xml:"available,attr"`
				Params    string `xml:"supportedParams,attr"`
			} `xml:"movie-search"`
			TV struct {
				Available string `xml:"available,attr"`
				Params    string `xml:"supportedParams,attr"`
			} `xml:"tv-search"`
		} `xml:"searching"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return caps{}, fmt.Errorf("%s: couldn't read its capabilities", ix.Name)
	}
	has := func(params, p string) bool {
		for _, x := range strings.Split(params, ",") {
			if strings.TrimSpace(x) == p {
				return true
			}
		}
		return false
	}
	m, t := doc.Searching.Movie, doc.Searching.TV
	c = caps{movieSearch: m.Available == "yes", tvSearch: t.Available == "yes",
		movieIMDB: has(m.Params, "imdbid"), tvIMDB: has(t.Params, "imdbid")}
	capsMu.Lock()
	capsCache[ix.URL] = c
	capsMu.Unlock()
	return c, nil
}

// Check makes sure the address and key work, for the settings page.
func (ix Indexer) Check(ctx context.Context) error {
	capsMu.Lock()
	delete(capsCache, ix.URL)
	capsMu.Unlock()
	if _, err := ix.caps(ctx); err != nil {
		return err
	}
	_, err := ix.get(ctx, url.Values{"t": {"search"}, "q": {"test"}, "limit": {"1"}})
	return err
}

// Search asks the indexer for posts of a movie or episode.
func (ix Indexer) Search(ctx context.Context, q Query) ([]Result, error) {
	c, err := ix.caps(ctx)
	if err != nil {
		return nil, err
	}
	p := url.Values{"extended": {"1"}, "limit": {"100"}}
	imdb := strings.TrimPrefix(q.IMDB, "tt")
	if q.Season > 0 {
		p.Set("t", "tvsearch")
		if !c.tvSearch {
			p.Set("t", "search")
		}
		p.Set("season", strconv.Itoa(q.Season))
		if q.Episode > 0 {
			p.Set("ep", strconv.Itoa(q.Episode))
		}
		if c.tvIMDB && imdb != "" {
			p.Set("imdbid", imdb)
		} else {
			p.Set("q", q.Title)
		}
		p.Set("cat", "5000")
	} else {
		p.Set("t", "movie")
		if !c.movieSearch {
			p.Set("t", "search")
		}
		if c.movieIMDB && imdb != "" {
			p.Set("imdbid", imdb)
		} else {
			p.Set("q", strings.TrimSpace(fmt.Sprintf("%s %d", q.Title, q.Year)))
		}
		p.Set("cat", "2000")
	}
	if p.Get("imdbid") == "" && strings.TrimSpace(p.Get("q")) == "" {
		return nil, nil
	}
	b, err := ix.get(ctx, p)
	if err != nil {
		return nil, err
	}
	return ix.parse(b)
}

func (ix Indexer) parse(b []byte) ([]Result, error) {
	var doc struct {
		Items []struct {
			Title     string `xml:"title"`
			Link      string `xml:"link"`
			PubDate   string `xml:"pubDate"`
			Enclosure struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
			Attrs []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value,attr"`
			} `xml:"attr"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: couldn't read the results", ix.Name)
	}
	var out []Result
	for _, it := range doc.Items {
		r := Result{Indexer: ix.Name, Title: strings.TrimSpace(it.Title), Size: it.Enclosure.Length}
		link := it.Enclosure.URL
		if link == "" {
			link = it.Link
		}
		for _, a := range it.Attrs {
			switch a.Name {
			case "size":
				if n, err := strconv.ParseInt(a.Value, 10, 64); err == nil && n > 0 {
					r.Size = n
				}
			case "grabs":
				r.Grabs, _ = strconv.Atoi(a.Value)
			}
		}
		r.PubDate, _ = time.Parse(time.RFC1123Z, it.PubDate)
		r.NZB = stripKey(link)
		if r.Title == "" || r.NZB == "" {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// stripKey drops the API key from an NZB link; FetchNZB puts it back.
func stripKey(link string) string {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Host == "" {
		return ""
	}
	q := u.Query()
	q.Del("apikey")
	q.Del("r")
	u.RawQuery = q.Encode()
	return u.String()
}

// FetchNZB downloads a post's NZB.
func (ix Indexer) FetchNZB(ctx context.Context, link string) ([]byte, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("apikey", ix.APIKey)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "WrenTV/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, ix.redact(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, ix.redact(err)
	}
	if err := apiError(b); err != nil {
		return nil, fmt.Errorf("%s: %w", ix.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: NZB download: %s", ix.Name, resp.Status)
	}
	return b, nil
}

// Owns reports whether an NZB link came from this indexer.
func (ix Indexer) Owns(link string) bool {
	a, err1 := url.Parse(ix.URL)
	b, err2 := url.Parse(link)
	if err1 != nil || err2 != nil {
		return false
	}
	return baseDomain(a.Hostname()) == baseDomain(b.Hostname())
}

// baseDomain is the last two labels: api.nzbgeek.info → nzbgeek.info.
func baseDomain(host string) string {
	parts := strings.Split(strings.ToLower(host), ".")
	if len(parts) <= 2 {
		return strings.Join(parts, ".")
	}
	return strings.Join(parts[len(parts)-2:], ".")
}
