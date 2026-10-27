package markers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/LTAGROUP/wrentv/internal/media"
)

// communityBase is IntroDB's public API; WRENTV_INTRODB_URL points it at a
// test server.
var communityBase = func() string {
	if v := os.Getenv("WRENTV_INTRODB_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return "https://api.introdb.app"
}()

// communityTTL is how long an answer, including "nothing timed yet", is kept.
const communityTTL = 3 * 24 * time.Hour

// Cache persists answers (the server's metadata_cache table).
type Cache interface {
	CacheGet(ctx context.Context, key string, maxAge int64) ([]byte, bool)
	CachePut(ctx context.Context, key string, body []byte)
}

// Community looks up intro and credits timestamps that viewers submitted to
// IntroDB, by IMDb id. It covers what neither chapters nor fingerprinting
// can: streamed files, which the server never has on disk to compare, and
// lone episodes with no season peers. Reads are anonymous.
type Community struct {
	Cache   Cache
	Enabled func() bool
	http    *http.Client
	sf      singleflight.Group
}

func NewCommunity(cache Cache, enabled func() bool) *Community {
	return &Community{Cache: cache, Enabled: enabled, http: &http.Client{Timeout: 10 * time.Second}}
}

type segment struct {
	Start float64 `json:"start_sec"`
	End   float64 `json:"end_sec"`
}

type segments struct {
	Intro *segment `json:"intro"`
	Outro *segment `json:"outro"`
}

// Lookup returns the community markers for a movie (season and episode < 0)
// or an episode, by its show's IMDb id. Nothing timed is an empty result,
// not an error.
func (c *Community) Lookup(ctx context.Context, imdbID string, season, episode int) (media.Markers, error) {
	if c == nil || (c.Enabled != nil && !c.Enabled()) || !strings.HasPrefix(imdbID, "tt") {
		return media.Markers{}, nil
	}
	q := url.Values{"imdb_id": {imdbID}}
	if season >= 0 && episode >= 0 {
		q.Set("season", strconv.Itoa(season))
		q.Set("episode", strconv.Itoa(episode))
	} else {
		q.Set("is_movie", "true")
	}
	key := "introdb:" + q.Encode()
	if b, ok := c.Cache.CacheGet(ctx, key, int64(communityTTL.Seconds())); ok {
		return parseSegments(b), nil
	}
	// The fetch runs on its own clock, so a play that gave up waiting still
	// leaves the answer cached for the next one.
	ch := c.sf.DoChan(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, communityBase+"/segments?"+q.Encode(), nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "WrenTV")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var body []byte
		switch {
		case resp.StatusCode == http.StatusNotFound:
			body = []byte("{}")
		case resp.StatusCode != http.StatusOK:
			// Rate limited or down: not remembered, asked again next time.
			return nil, fmt.Errorf("introdb: %s", resp.Status)
		default:
			var s segments
			if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
				return nil, err
			}
			body, _ = json.Marshal(s)
		}
		c.Cache.CachePut(ctx, key, body)
		return body, nil
	})
	select {
	case r := <-ch:
		if r.Err != nil {
			return media.Markers{}, r.Err
		}
		return parseSegments(r.Val.([]byte)), nil
	case <-ctx.Done():
		return media.Markers{}, ctx.Err()
	}
}

func parseSegments(b []byte) media.Markers {
	var s segments
	_ = json.Unmarshal(b, &s)
	var m media.Markers
	if s.Intro != nil && s.Intro.End > s.Intro.Start {
		m.Intro = &media.Range{Start: s.Intro.Start, End: s.Intro.End}
	}
	if s.Outro != nil && s.Outro.Start > 0 {
		m.Credits = &media.Range{Start: s.Outro.Start, End: s.Outro.End}
	}
	if !m.Empty() {
		m.Source = "community"
	}
	return m
}

// Fit drops community markers that can't belong to a file this long: they
// were timed on another cut. An open-ended outro runs to the end.
func Fit(m media.Markers, duration float64) media.Markers {
	if duration <= 0 {
		return m
	}
	if m.Intro != nil {
		if l := m.Intro.End - m.Intro.Start; l < 5 || l > 300 || m.Intro.Start >= duration/2 {
			m.Intro = nil
		}
	}
	if m.Credits != nil {
		if m.Credits.End <= m.Credits.Start || m.Credits.End > duration {
			m.Credits = &media.Range{Start: m.Credits.Start, End: duration}
		}
		if m.Credits.Start < duration/2 || m.Credits.Start >= duration-1 {
			m.Credits = nil
		}
	}
	if m.Empty() {
		m.Source = ""
	}
	return m
}
