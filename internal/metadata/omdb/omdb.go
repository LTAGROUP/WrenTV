// Package omdb fetches IMDb, Rotten Tomatoes and Metacritic ratings from the
// OMDb API (https://www.omdbapi.com). TMDB doesn't carry them.
package omdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

// base is the OMDb API; WRENTV_OMDB_URL points it at a proxy or a test server.
var base = func() string {
	if v := os.Getenv("WRENTV_OMDB_URL"); v != "" {
		return v
	}
	return "https://www.omdbapi.com/"
}()

// Cache persists responses (the server's metadata_cache table).
type Cache interface {
	CacheGet(ctx context.Context, key string, maxAge int64) ([]byte, bool)
	CachePut(ctx context.Context, key string, body []byte)
}

type Client struct {
	key   func() string // read on each call, so a key set in Settings applies at once
	http  *http.Client
	cache Cache
	sf    singleflight.Group
}

func New(key func() string, cache Cache) *Client {
	return &Client{key: key, http: &http.Client{Timeout: 10 * time.Second}, cache: cache}
}

func (c *Client) Enabled() bool { return c.key() != "" }

// Ratings are normalised scores; zero values mean "not available".
type Ratings struct {
	IMDb           float64 `json:"imdb,omitempty"`      // 0–10
	IMDbVotes      int     `json:"imdbVotes,omitempty"` // number of votes
	RottenTomatoes int     `json:"rottenTomatoes,omitempty"`
	Metacritic     int     `json:"metacritic,omitempty"`
}

var ErrNotFound = errors.New("omdb: not found")

const ttl = 7 * 24 * time.Hour

// ByIMDB returns ratings for an IMDb ID (tt…).
func (c *Client) ByIMDB(ctx context.Context, imdbID string) (*Ratings, error) {
	if !c.Enabled() {
		return nil, errors.New("omdb: no API key")
	}
	if !strings.HasPrefix(imdbID, "tt") {
		return nil, ErrNotFound
	}
	key := "omdb:" + imdbID
	body, ok := c.cache.CacheGet(ctx, key, int64(ttl.Seconds()))
	if !ok {
		v, err, _ := c.sf.Do(imdbID, func() (any, error) {
			u := base + "?" + url.Values{"apikey": {c.key()}, "i": {imdbID}}.Encode()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			resp, err := c.http.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusUnauthorized {
				return nil, errors.New("omdb: invalid API key")
			}
			if resp.StatusCode >= 300 {
				return nil, fmt.Errorf("omdb: %s", resp.Status)
			}
			var probe struct{ Response, Error string }
			if json.Unmarshal(b, &probe) == nil && probe.Response == "False" && !strings.Contains(probe.Error, "not found") {
				return nil, fmt.Errorf("omdb: %s", probe.Error) // don't cache key/limit errors
			}
			c.cache.CachePut(ctx, key, b)
			return b, nil
		})
		if err != nil {
			return nil, err
		}
		body = v.([]byte)
	}
	return parse(body)
}

func parse(b []byte) (*Ratings, error) {
	var r struct {
		Response   string
		IMDBRating string `json:"imdbRating"`
		IMDBVotes  string `json:"imdbVotes"`
		Metascore  string
		Ratings    []struct{ Source, Value string }
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Response == "False" {
		return nil, ErrNotFound
	}
	out := &Ratings{}
	out.IMDb, _ = strconv.ParseFloat(r.IMDBRating, 64)
	out.IMDbVotes, _ = strconv.Atoi(strings.ReplaceAll(r.IMDBVotes, ",", ""))
	out.Metacritic, _ = strconv.Atoi(r.Metascore)
	for _, x := range r.Ratings {
		switch x.Source {
		case "Rotten Tomatoes":
			out.RottenTomatoes, _ = strconv.Atoi(strings.TrimSuffix(x.Value, "%"))
		case "Metacritic":
			if out.Metacritic == 0 {
				v, _, _ := strings.Cut(x.Value, "/")
				out.Metacritic, _ = strconv.Atoi(v)
			}
		case "Internet Movie Database":
			if out.IMDb == 0 {
				v, _, _ := strings.Cut(x.Value, "/")
				out.IMDb, _ = strconv.ParseFloat(v, 64)
			}
		}
	}
	return out, nil
}
