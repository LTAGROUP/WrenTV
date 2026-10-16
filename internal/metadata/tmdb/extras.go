package tmdb

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Title is a movie or show as TMDB lists it (recommendations, trending).
type Title struct {
	ID           int     `json:"id"`
	MediaType    string  `json:"media_type"` // "movie" or "tv" (trending only)
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
	VoteCount    int     `json:"vote_count"`
	Popularity   float64 `json:"popularity"`
}

func (t Title) DisplayTitle() string {
	if t.Title != "" {
		return t.Title
	}
	return t.Name
}

func (t Title) Year() int {
	d := t.ReleaseDate
	if d == "" {
		d = t.FirstAirDate
	}
	if len(d) >= 4 {
		y, _ := strconv.Atoi(d[:4])
		return y
	}
	return 0
}

type CastMember struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
	// TV aggregate credits list roles instead of one character.
	Roles []struct {
		Character    string `json:"character"`
		EpisodeCount int    `json:"episode_count"`
	} `json:"roles"`
}

type Video struct {
	Key         string `json:"key"`
	Site        string `json:"site"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Official    bool   `json:"official"`
	PublishedAt string `json:"published_at"`
}

// Extras is everything a detail page shows beyond the stored metadata.
type Extras struct {
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
	Credits     struct {
		Cast []CastMember `json:"cast"`
	} `json:"credits"`
	AggregateCredits struct {
		Cast []CastMember `json:"cast"`
	} `json:"aggregate_credits"`
	Videos struct {
		Results []Video `json:"results"`
	} `json:"videos"`
	Recommendations struct {
		Results []Title `json:"results"`
	} `json:"recommendations"`
	Similar struct {
		Results []Title `json:"results"`
	} `json:"similar"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
}

const extrasTTL = 3 * 24 * time.Hour

// MovieExtras fetches cast, videos, recommendations and similar titles.
func (c *Client) MovieExtras(ctx context.Context, id int) (*Extras, error) {
	var e Extras
	p := url.Values{"append_to_response": {"credits,videos,recommendations,similar,external_ids"}}
	err := c.get(ctx, "/movie/"+strconv.Itoa(id), p, extrasTTL, &e)
	return &e, err
}

// ShowExtras is MovieExtras for a show; cast comes from aggregate credits
// (everyone across all seasons, by episode count).
func (c *Client) ShowExtras(ctx context.Context, id int) (*Extras, error) {
	var e Extras
	p := url.Values{"append_to_response": {"aggregate_credits,videos,recommendations,similar,external_ids"}}
	err := c.get(ctx, "/tv/"+strconv.Itoa(id), p, extrasTTL, &e)
	if len(e.Credits.Cast) == 0 {
		e.Credits.Cast = e.AggregateCredits.Cast
	}
	for i := range e.Credits.Cast {
		m := &e.Credits.Cast[i]
		if m.Character == "" && len(m.Roles) > 0 {
			m.Character = m.Roles[0].Character
		}
	}
	return &e, err
}

// Trailer picks the best YouTube trailer: official trailers first, then
// teasers, newest first.
func (e *Extras) Trailer() *Video {
	rank := func(v Video) int {
		r := 0
		switch v.Type {
		case "Trailer":
			r += 4
		case "Teaser":
			r += 2
		default:
			return -1
		}
		if v.Official {
			r++
		}
		return r
	}
	var vids []Video
	for _, v := range e.Videos.Results {
		if v.Site == "YouTube" && rank(v) >= 0 {
			vids = append(vids, v)
		}
	}
	if len(vids) == 0 {
		return nil
	}
	sort.SliceStable(vids, func(i, j int) bool {
		if rank(vids[i]) != rank(vids[j]) {
			return rank(vids[i]) > rank(vids[j])
		}
		return vids[i].PublishedAt > vids[j].PublishedAt
	})
	return &vids[0]
}

// Trending returns this week's trending movies and shows.
func (c *Client) Trending(ctx context.Context) ([]Title, error) {
	var r struct {
		Results []Title `json:"results"`
	}
	err := c.get(ctx, "/trending/all/week", nil, 6*time.Hour, &r)
	out := r.Results[:0]
	for _, t := range r.Results {
		if t.MediaType == "movie" || t.MediaType == "tv" {
			out = append(out, t)
		}
	}
	return out, err
}

// SearchMulti finds movies and shows matching a query, most relevant first.
func (c *Client) SearchMulti(ctx context.Context, query string) ([]Title, error) {
	var r struct {
		Results []Title `json:"results"`
	}
	err := c.get(ctx, "/search/multi", url.Values{"query": {query}, "include_adult": {"false"}}, searchTTL, &r)
	out := r.Results[:0]
	for _, t := range r.Results {
		if t.MediaType == "movie" || t.MediaType == "tv" {
			out = append(out, t)
		}
	}
	return out, err
}
