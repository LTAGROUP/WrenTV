package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Franchise is a TMDB collection: the movies of one series.
type Franchise struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	Parts        []Title `json:"parts"`
}

func (c *Client) Collection(ctx context.Context, id int) (*Franchise, error) {
	var f Franchise
	if err := c.get(ctx, fmt.Sprintf("/collection/%d", id), nil, 24*time.Hour, &f); err != nil {
		return nil, err
	}
	for i := range f.Parts {
		f.Parts[i].MediaType = "movie"
	}
	return &f, nil
}

// SearchCollections finds franchises by name.
func (c *Client) SearchCollections(ctx context.Context, query string) ([]CollectionRef, error) {
	var r struct {
		Results []CollectionRef `json:"results"`
	}
	err := c.get(ctx, "/search/collection", url.Values{"query": {query}}, 24*time.Hour, &r)
	return r.Results, err
}

// UserList is a public TMDB list.
type UserList struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	PosterPath  string  `json:"poster_path"`
	Items       []Title `json:"items"`
}

func (c *Client) List(ctx context.Context, id int) (*UserList, error) {
	var l UserList
	if err := c.get(ctx, fmt.Sprintf("/list/%d", id), nil, 6*time.Hour, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// Chart returns a TMDB chart: "trending", "popular", "top_rated",
// "now_playing", "upcoming" (movies) or "airing" (shows). mediaType is
// "movie" or "tv". Up to pages×20 titles.
func (c *Client) Chart(ctx context.Context, chart, mediaType string, pages int) ([]Title, error) {
	var path string
	switch chart {
	case "trending":
		path = "/trending/" + mediaType + "/week"
	case "popular", "top_rated":
		path = "/" + mediaType + "/" + chart
	case "now_playing", "upcoming":
		path = "/movie/" + chart
		mediaType = "movie"
	case "airing":
		path = "/tv/on_the_air"
		mediaType = "tv"
	default:
		return nil, fmt.Errorf("unknown chart %q", chart)
	}
	var out []Title
	for p := 1; p <= pages; p++ {
		var r struct {
			Results []Title `json:"results"`
		}
		if err := c.get(ctx, path, url.Values{"page": {fmt.Sprint(p)}}, 6*time.Hour, &r); err != nil {
			if p == 1 {
				return nil, err
			}
			break
		}
		for _, t := range r.Results {
			t.MediaType = mediaType
			out = append(out, t)
		}
	}
	return out, nil
}

// ByGenre returns popular titles in a TMDB genre. mediaType is "movie" or
// "tv". Only titles with enough votes to be worth showing.
func (c *Client) ByGenre(ctx context.Context, mediaType string, genreID int) ([]Title, error) {
	var r struct {
		Results []Title `json:"results"`
	}
	params := url.Values{"with_genres": {fmt.Sprint(genreID)}, "sort_by": {"popularity.desc"}, "vote_count.gte": {"200"}}
	if err := c.get(ctx, "/discover/"+mediaType, params, 12*time.Hour, &r); err != nil {
		return nil, err
	}
	for i := range r.Results {
		r.Results[i].MediaType = mediaType
	}
	return r.Results, nil
}

// Genres lists TMDB's genres for "movie" or "tv".
func (c *Client) Genres(ctx context.Context, mediaType string) ([]Genre, error) {
	var r struct {
		Genres []Genre `json:"genres"`
	}
	err := c.get(ctx, "/genre/"+mediaType+"/list", nil, 7*24*time.Hour, &r)
	return r.Genres, err
}

// Discover pages through TMDB's whole catalog for "movie" or "tv". sort is
// "popular", "top_rated" or "new"; genreID 0 means any genre. Returns the
// page's titles and the number of pages TMDB will serve.
func (c *Client) Discover(ctx context.Context, mediaType, sort string, genreID, page int) ([]Title, int, error) {
	q := DiscoverQuery{Sort: sort, Page: page}
	if genreID > 0 {
		q.Genres = []int{genreID}
	}
	return c.DiscoverWith(ctx, mediaType, q)
}

// DiscoverQuery narrows the catalog. Genres and Providers match any of
// theirs; Providers are watch providers in the client's region.
type DiscoverQuery struct {
	Sort      string
	Genres    []int
	Providers []int
	Page      int
}

func joinInts(v []int, sep string) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprint(n)
	}
	return strings.Join(parts, sep)
}

func (c *Client) DiscoverWith(ctx context.Context, mediaType string, q DiscoverQuery) ([]Title, int, error) {
	date := "primary_release_date"
	if mediaType == "tv" {
		date = "first_air_date"
	}
	params := url.Values{"page": {fmt.Sprint(max(1, q.Page))}, "include_adult": {"false"}}
	switch q.Sort {
	case "top_rated":
		params.Set("sort_by", "vote_average.desc")
		params.Set("vote_count.gte", "500")
		if len(q.Providers) > 0 || len(q.Genres) > 0 {
			params.Set("vote_count.gte", "200")
		}
	case "new":
		params.Set("sort_by", date+".desc")
		params.Set(date+".lte", time.Now().Format("2006-01-02"))
		params.Set("vote_count.gte", "20")
	default:
		params.Set("sort_by", "popularity.desc")
	}
	if len(q.Genres) > 0 {
		params.Set("with_genres", joinInts(q.Genres, "|"))
	}
	if len(q.Providers) > 0 {
		params.Set("with_watch_providers", joinInts(q.Providers, "|"))
		params.Set("watch_region", c.Region())
		params.Set("with_watch_monetization_types", "flatrate")
	}
	var r struct {
		Results    []Title `json:"results"`
		TotalPages int     `json:"total_pages"`
	}
	if err := c.get(ctx, "/discover/"+mediaType, params, 6*time.Hour, &r); err != nil {
		return nil, 0, err
	}
	for i := range r.Results {
		r.Results[i].MediaType = mediaType
	}
	// TMDB never serves past page 500.
	return r.Results, min(r.TotalPages, 500), nil
}

// Region is the country streaming availability is looked up in, from the
// metadata language ("en-GB" → "GB"); "US" when it names none.
func (c *Client) Region() string {
	if i := strings.LastIndex(c.language, "-"); i > 0 && len(c.language)-i == 3 {
		return strings.ToUpper(c.language[i+1:])
	}
	return "US"
}

// WatchProvider is a streaming service TMDB tracks.
type WatchProvider struct {
	ID       int    `json:"provider_id"`
	Name     string `json:"provider_name"`
	LogoPath string `json:"logo_path"`
	Priority int    `json:"display_priority"`
}

// WatchProviders lists the movie streaming services in the client's region,
// most prominent first.
func (c *Client) WatchProviders(ctx context.Context) ([]WatchProvider, error) {
	var r struct {
		Results []WatchProvider `json:"results"`
	}
	err := c.get(ctx, "/watch/providers/movie", url.Values{"watch_region": {c.Region()}}, 7*24*time.Hour, &r)
	sort.SliceStable(r.Results, func(i, j int) bool { return r.Results[i].Priority < r.Results[j].Priority })
	return r.Results, err
}
