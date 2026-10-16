package tmdb

import (
	"context"
	"fmt"
	"net/url"
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
