// Package tmdb is a small TMDB v3 client with rate limiting and a
// persistent response cache.
package tmdb

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
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

// base is the TMDB API; WRENTV_TMDB_URL points it at a proxy or a test server.
var base = func() string {
	if v := os.Getenv("WRENTV_TMDB_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return "https://api.themoviedb.org/3"
}()

// Cache persists raw responses (the server's metadata_cache table).
type Cache interface {
	CacheGet(ctx context.Context, key string, maxAge int64) ([]byte, bool)
	CachePut(ctx context.Context, key string, body []byte)
}

type Client struct {
	key      string
	language string
	http     *http.Client
	limiter  *rate.Limiter
	// quick is for requests someone is waiting on, so they don't queue
	// behind a library scan; the two together stay under TMDB's limit.
	quick *rate.Limiter
	cache Cache
	sf    singleflight.Group

	mu  sync.Mutex
	mem map[string]memEntry // hot cache for the current scan (seasons shared by many episodes)
}

func New(apiKey, language string, cache Cache) *Client {
	return &Client{
		key:      apiKey,
		language: language,
		http:     &http.Client{Timeout: 15 * time.Second},
		// TMDB allows ~50 req/s; stay comfortably under it.
		limiter: rate.NewLimiter(rate.Limit(35), 20),
		quick:   rate.NewLimiter(rate.Limit(10), 10),
		cache:   cache,
		mem:     map[string]memEntry{},
	}
}

func (c *Client) Enabled() bool { return c.key != "" }

var ErrNotFound = errors.New("tmdb: not found")

type interactiveKey struct{}

// Interactive marks ctx as a request someone is waiting on.
func Interactive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, true)
}

type prefetchKey struct{}

// Prefetch marks ctx as warming for something the user may open next. It
// takes the background budget even inside a signed-in request, so warming a
// shelf never queues ahead of the title someone actually picked.
func Prefetch(ctx context.Context) context.Context {
	return context.WithValue(ctx, prefetchKey{}, true)
}

// waitedOn reports whether someone is waiting on ctx's lookups.
func waitedOn(ctx context.Context) bool {
	return ctx.Value(interactiveKey{}) != nil && ctx.Value(prefetchKey{}) == nil
}

// pause waits d, or less if ctx ends first.
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// get fetches path with params, served from cache when fresh.
func (c *Client) get(ctx context.Context, path string, params url.Values, maxAge time.Duration, out any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("language", c.language)
	key := path + "?" + params.Encode()

	c.mu.Lock()
	if m, ok := c.mem[key]; ok && time.Since(m.at) < maxAge {
		c.mu.Unlock()
		return json.Unmarshal(m.body, out)
	}
	c.mu.Unlock()
	if b, ok := c.cache.CacheGet(ctx, "tmdb:"+key, int64(maxAge.Seconds())); ok {
		c.remember(key, b)
		return json.Unmarshal(b, out)
	}

	// Callers asking for the same thing share one fetch. It runs on its own
	// clock, not the first caller's: a shelf's warming giving up must not fail
	// the open that joined it. Each caller still stops waiting with its own ctx.
	quick := waitedOn(ctx)
	ch := c.sf.DoChan(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		params.Set("api_key", c.key)
		var body []byte
		limiter, perTry := c.limiter, 15*time.Second
		if quick {
			// A stalled connection is retried sooner than the client's timeout.
			limiter, perTry = c.quick, 6*time.Second
		}
		for attempt := 0; attempt < 4; attempt++ {
			if err := limiter.Wait(ctx); err != nil {
				return nil, err
			}
			tryCtx, cancel := context.WithTimeout(ctx, perTry)
			req, _ := http.NewRequestWithContext(tryCtx, http.MethodGet, base+path+"?"+params.Encode(), nil)
			req.Header.Set("Accept", "application/json")
			resp, err := c.http.Do(req)
			if err != nil {
				cancel()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if attempt == 3 {
					return nil, err
				}
				if err := pause(ctx, time.Duration(attempt+1)*500*time.Millisecond); err != nil {
					return nil, err
				}
				continue
			}
			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			if err != nil {
				if attempt == 3 || ctx.Err() != nil {
					return nil, err
				}
				continue
			}
			switch {
			case resp.StatusCode == http.StatusTooManyRequests:
				wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
				if err := pause(ctx, time.Duration(max(wait, 1))*time.Second); err != nil {
					return nil, err
				}
				continue
			case resp.StatusCode == http.StatusNotFound:
				return nil, ErrNotFound
			case resp.StatusCode >= 300:
				return nil, fmt.Errorf("tmdb %s: %s", path, resp.Status)
			}
			c.remember(key, body)
			if quick {
				// The memory copy serves it from now on, so the page doesn't
				// also wait on the database's write lock to persist it.
				go c.cache.CachePut(context.WithoutCancel(ctx), "tmdb:"+key, body)
			} else {
				c.cache.CachePut(ctx, "tmdb:"+key, body)
			}
			return body, nil
		}
		return nil, fmt.Errorf("tmdb %s: rate limited", path)
	})
	var v any
	select {
	case r := <-ch:
		if r.Err != nil {
			return r.Err
		}
		v = r.Val
	case <-ctx.Done():
		return ctx.Err()
	}
	return json.Unmarshal(v.([]byte), out)
}

// memEntry is a response held in memory, and when it was put there.
type memEntry struct {
	body []byte
	at   time.Time
}

func (c *Client) remember(key string, b []byte) {
	c.mu.Lock()
	if len(c.mem) > 5000 {
		c.mem = map[string]memEntry{}
	}
	c.mem[key] = memEntry{b, time.Now()}
	c.mu.Unlock()
}

// ---------- types ----------

type SearchResult struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	Popularity    float64 `json:"popularity"`
	VoteCount     int     `json:"vote_count"`
	BackdropPath  string  `json:"backdrop_path"`
}

func (r SearchResult) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

func (r SearchResult) Year() int {
	d := r.ReleaseDate
	if d == "" {
		d = r.FirstAirDate
	}
	if len(d) >= 4 {
		y, _ := strconv.Atoi(d[:4])
		return y
	}
	return 0
}

type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Movie struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Overview      string  `json:"overview"`
	Tagline       string  `json:"tagline"`
	ReleaseDate   string  `json:"release_date"`
	Runtime       int     `json:"runtime"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	VoteAverage   float64 `json:"vote_average"`
	IMDBID        string  `json:"imdb_id"`
	Genres        []Genre `json:"genres"`
	ReleaseDates  struct {
		Results []struct {
			ISO31661     string `json:"iso_3166_1"`
			ReleaseDates []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	Images Images `json:"images"`
	// BelongsToCollection is the franchise (e.g. "The Dark Knight Collection").
	BelongsToCollection *CollectionRef `json:"belongs_to_collection"`
}

type CollectionRef struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

type Images struct {
	Logos []struct {
		FilePath string  `json:"file_path"`
		ISO6391  string  `json:"iso_639_1"`
		Vote     float64 `json:"vote_average"`
	} `json:"logos"`
}

// BestLogo picks the top-voted English (or language-neutral) logo.
func (im Images) BestLogo() string {
	best, bestVote := "", -1.0
	for _, l := range im.Logos {
		if (l.ISO6391 == "en" || l.ISO6391 == "") && l.Vote > bestVote {
			best, bestVote = l.FilePath, l.Vote
		}
	}
	return best
}

type Show struct {
	ID             int     `json:"id"`
	Name           string  `json:"name"`
	OriginalName   string  `json:"original_name"`
	Overview       string  `json:"overview"`
	Tagline        string  `json:"tagline"`
	FirstAirDate   string  `json:"first_air_date"`
	PosterPath     string  `json:"poster_path"`
	BackdropPath   string  `json:"backdrop_path"`
	VoteAverage    float64 `json:"vote_average"`
	Genres         []Genre `json:"genres"`
	EpisodeRunTime []int   `json:"episode_run_time"`
	Seasons        []struct {
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
		Overview     string `json:"overview"`
		PosterPath   string `json:"poster_path"`
		AirDate      string `json:"air_date"`
	} `json:"seasons"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
	ContentRatings struct {
		Results []struct {
			ISO31661 string `json:"iso_3166_1"`
			Rating   string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
	Images Images `json:"images"`
}

type Season struct {
	SeasonNumber int       `json:"season_number"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	PosterPath   string    `json:"poster_path"`
	AirDate      string    `json:"air_date"`
	Episodes     []Episode `json:"episodes"`
}

type Episode struct {
	EpisodeNumber int     `json:"episode_number"`
	SeasonNumber  int     `json:"season_number"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	StillPath     string  `json:"still_path"`
	Runtime       int     `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
}

// ---------- calls ----------

const (
	searchTTL = 7 * 24 * time.Hour
	detailTTL = 3 * 24 * time.Hour
	seasonTTL = 12 * time.Hour // new episodes air; keep this fresher
)

func (c *Client) SearchMovie(ctx context.Context, query string, year int) ([]SearchResult, error) {
	p := url.Values{"query": {query}, "include_adult": {"false"}}
	if year > 0 {
		p.Set("year", strconv.Itoa(year))
	}
	var r struct {
		Results []SearchResult `json:"results"`
	}
	err := c.get(ctx, "/search/movie", p, searchTTL, &r)
	return r.Results, err
}

func (c *Client) SearchTV(ctx context.Context, query string, year int) ([]SearchResult, error) {
	p := url.Values{"query": {query}, "include_adult": {"false"}}
	if year > 0 {
		p.Set("first_air_date_year", strconv.Itoa(year))
	}
	var r struct {
		Results []SearchResult `json:"results"`
	}
	err := c.get(ctx, "/search/tv", p, searchTTL, &r)
	return r.Results, err
}

// FindIMDB resolves an IMDb ID to TMDB movie and TV IDs (0 when absent).
func (c *Client) FindIMDB(ctx context.Context, imdbID string) (movieID, tvID int, err error) {
	var r struct {
		Movies []SearchResult `json:"movie_results"`
		TV     []SearchResult `json:"tv_results"`
	}
	err = c.get(ctx, "/find/"+url.PathEscape(imdbID), url.Values{"external_source": {"imdb_id"}}, detailTTL, &r)
	if len(r.Movies) > 0 {
		movieID = r.Movies[0].ID
	}
	if len(r.TV) > 0 {
		tvID = r.TV[0].ID
	}
	return
}

func (c *Client) Movie(ctx context.Context, id int) (*Movie, error) {
	var m Movie
	p := url.Values{"append_to_response": {"release_dates,images"}, "include_image_language": {"en,null"}}
	err := c.get(ctx, "/movie/"+strconv.Itoa(id), p, detailTTL, &m)
	return &m, err
}

func (c *Client) Show(ctx context.Context, id int) (*Show, error) {
	var s Show
	p := url.Values{"append_to_response": {"external_ids,content_ratings,images"}, "include_image_language": {"en,null"}}
	err := c.get(ctx, "/tv/"+strconv.Itoa(id), p, detailTTL, &s)
	return &s, err
}

// Airing is a show's schedule: whether it's still running, and the
// episodes either side of today. It's kept fresher than Show.
type Airing struct {
	Status      string `json:"status"`
	LastAirDate string `json:"last_air_date"`
	LastEpisode *struct {
		SeasonNumber int `json:"season_number"`
	} `json:"last_episode_to_air"`
	NextEpisode *struct {
		SeasonNumber int    `json:"season_number"`
		AirDate      string `json:"air_date"`
	} `json:"next_episode_to_air"`
}

func (c *Client) Airing(ctx context.Context, showID int) (*Airing, error) {
	var a Airing
	err := c.get(ctx, "/tv/"+strconv.Itoa(showID), nil, seasonTTL, &a)
	return &a, err
}

func (c *Client) Season(ctx context.Context, showID, season int) (*Season, error) {
	var s Season
	err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d", showID, season), nil, seasonTTL, &s)
	return &s, err
}

// BestMatch scores search results by title similarity, year and popularity.
func BestMatch(results []SearchResult, title string, year int) *SearchResult {
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	want := norm(title)
	var best *SearchResult
	bestScore := -1.0
	for i := range results {
		r := &results[i]
		score := 0.0
		for _, t := range []string{r.Title, r.Name, r.OriginalTitle, r.OriginalName} {
			if t != "" && norm(t) == want {
				score = 10
				break
			}
		}
		if score == 0 && strings.Contains(norm(r.DisplayTitle()), want) {
			score = 4
		}
		if year > 0 {
			switch d := r.Year() - year; {
			case d == 0:
				score += 5
			case d == 1 || d == -1:
				score += 2
			default:
				score -= 3
			}
		}
		// Popularity breaks ties (remakes, same-name shows), never dominates.
		score += min(float64(r.VoteCount)/2000, 2)
		score -= float64(i) * 0.3
		if score > bestScore {
			best, bestScore = r, score
		}
	}
	if bestScore < 2 {
		return nil
	}
	return best
}

func ContentRatingUS(m *Movie) string {
	for _, r := range m.ReleaseDates.Results {
		if r.ISO31661 == "US" {
			for _, d := range r.ReleaseDates {
				if d.Certification != "" {
					return d.Certification
				}
			}
		}
	}
	return ""
}

func ShowRatingUS(s *Show) string {
	for _, r := range s.ContentRatings.Results {
		if r.ISO31661 == "US" {
			return r.Rating
		}
	}
	return ""
}
