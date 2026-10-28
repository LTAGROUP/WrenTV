// Package trakt talks to the Trakt API (api.trakt.tv): device sign-in,
// scrobbling, marking watched and reading a user's watch history.
package trakt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const appName = "wrentv"

var (
	// ErrPending means the user hasn't approved the device code yet.
	ErrPending = errors.New("authorization pending")
	// ErrSlowDown asks the poller to wait longer between polls.
	ErrSlowDown = errors.New("slow down")
	// ErrExpired means the device code is dead; start again.
	ErrExpired = errors.New("code expired")
	// ErrDenied means the user said no to the app.
	ErrDenied = errors.New("the request was denied on trakt.tv")
	// ErrUnauthorized means the access token was rejected.
	ErrUnauthorized = errors.New("trakt rejected the token")
)

type Client struct {
	// ClientID and ClientSecret are an app registered at
	// trakt.tv/oauth/applications. An empty ID or secret disables Trakt.
	ClientID     func() string
	ClientSecret func() string
	AppVersion   string
	HTTP         *http.Client
	// Base overrides https://api.trakt.tv (tests).
	Base string
}

func New(clientID, secret func() string, version string) *Client {
	return &Client{ClientID: clientID, ClientSecret: secret, AppVersion: version, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Enabled() bool {
	return c != nil && c.ClientID != nil && c.ClientID() != "" && c.ClientSecret != nil && c.ClientSecret() != ""
}

// Public reports whether public endpoints (trending, popular) can be read:
// they only need the client ID.
func (c *Client) Public() bool { return c != nil && c.ClientID != nil && c.ClientID() != "" }

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return "https://api.trakt.tv"
}

func (c *Client) userAgent() string {
	v := strings.TrimPrefix(c.AppVersion, "v")
	if v == "" {
		v = "1.0"
	}
	return appName + "/" + v
}

// ---------- sign-in (OAuth 2.0 device flow) ----------

type Device struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	URL        string `json:"verification_url"`
	ExpiresIn  int    `json:"expires_in"`
	Interval   int    `json:"interval"`
}

type Token struct {
	Access  string
	Refresh string
	Expires int64 // unix seconds
}

func (c *Client) StartDevice(ctx context.Context) (Device, error) {
	var d Device
	_, err := c.post(ctx, "/oauth/device/code", map[string]string{"client_id": c.ClientID()}, &d)
	if d.Interval <= 0 {
		d.Interval = 5
	}
	if d.URL == "" {
		d.URL = "https://trakt.tv/activate"
	}
	return d, err
}

// PollDevice returns the tokens once the user approves, else ErrPending,
// ErrSlowDown, ErrExpired or ErrDenied.
func (c *Client) PollDevice(ctx context.Context, deviceCode string) (Token, error) {
	var out tokenBody
	code, err := c.post(ctx, "/oauth/device/token", map[string]string{"code": deviceCode,
		"client_id": c.ClientID(), "client_secret": c.ClientSecret()}, &out)
	switch code {
	case http.StatusBadRequest:
		return Token{}, ErrPending
	case http.StatusTooManyRequests:
		return Token{}, ErrSlowDown
	case http.StatusNotFound, http.StatusConflict, http.StatusGone:
		return Token{}, ErrExpired
	case 418:
		return Token{}, ErrDenied
	}
	if err != nil {
		return Token{}, err
	}
	return out.token()
}

func (c *Client) Refresh(ctx context.Context, refresh string) (Token, error) {
	var out tokenBody
	if _, err := c.post(ctx, "/oauth/token", map[string]string{"refresh_token": refresh, "client_id": c.ClientID(),
		"client_secret": c.ClientSecret(), "redirect_uri": "urn:ietf:wg:oauth:2.0:oob", "grant_type": "refresh_token"}, &out); err != nil {
		return Token{}, err
	}
	return out.token()
}

type tokenBody struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	CreatedAt    int64  `json:"created_at"`
}

func (b tokenBody) token() (Token, error) {
	if b.AccessToken == "" {
		return Token{}, errors.New("trakt returned no token")
	}
	start := b.CreatedAt
	if start <= 0 {
		start = time.Now().Unix()
	}
	exp := b.ExpiresIn
	if exp <= 0 {
		exp = 24 * 3600
	}
	return Token{Access: b.AccessToken, Refresh: b.RefreshToken, Expires: start + exp}, nil
}

// post sends an unauthenticated JSON body (the sign-in calls) and returns
// the status code alongside any error.
func (c *Client) post(ctx context.Context, path string, body, out any) (int, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = json.Unmarshal(rb, &e)
		return resp.StatusCode, fmt.Errorf("trakt %s: %s", resp.Status, firstOf(e.Desc, e.Error, strings.TrimSpace(string(rb))))
	}
	return resp.StatusCode, json.Unmarshal(rb, out)
}

// ---------- API calls ----------

// IDs are the ids Trakt knows a title by.
type IDs struct {
	Trakt int    `json:"trakt,omitempty"`
	Slug  string `json:"slug,omitempty"`
	IMDB  string `json:"imdb,omitempty"`
	TMDB  int    `json:"tmdb,omitempty"`
	TVDB  int    `json:"tvdb,omitempty"`
}

// Media identifies a movie or show.
type Media struct {
	Title string `json:"title,omitempty"`
	Year  int    `json:"year,omitempty"`
	IDs   IDs    `json:"ids"`
}

type Episode struct {
	Season int `json:"season"`
	Number int `json:"number"`
}

// Scrobble is the body of /scrobble/{start,pause,stop}.
type Scrobble struct {
	Progress float64  `json:"progress"`
	Movie    *Media   `json:"movie,omitempty"`
	Show     *Media   `json:"show,omitempty"`
	Episode  *Episode `json:"episode,omitempty"`
	AppVer   string   `json:"app_version,omitempty"`
}

// Scrobble reports a player event ("start", "pause" or "stop"). Trakt marks
// the title watched on a stop at 80% or more, and keeps a stop under that
// as a paused playback.
func (c *Client) Scrobble(ctx context.Context, access, action string, body Scrobble) error {
	body.AppVer = strings.TrimPrefix(c.AppVersion, "v")
	err := c.call(ctx, access, http.MethodPost, "/scrobble/"+action, nil, body, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusConflict {
		return nil // already scrobbled a moment ago
	}
	return err
}

// History is the body of /sync/history and /sync/history/remove.
type History struct {
	Movies []HistoryItem `json:"movies,omitempty"`
	Shows  []HistoryItem `json:"shows,omitempty"`
}

type HistoryItem struct {
	Media
	WatchedAt string          `json:"watched_at,omitempty"`
	Seasons   []HistorySeason `json:"seasons,omitempty"`
}

type HistorySeason struct {
	Number   int              `json:"number"`
	Episodes []HistoryEpisode `json:"episodes,omitempty"`
}

type HistoryEpisode struct {
	Number    int    `json:"number"`
	WatchedAt string `json:"watched_at,omitempty"`
}

func (c *Client) AddHistory(ctx context.Context, access string, h History) error {
	return c.call(ctx, access, http.MethodPost, "/sync/history", nil, h, nil)
}

func (c *Client) RemoveHistory(ctx context.Context, access string, h History) error {
	return c.call(ctx, access, http.MethodPost, "/sync/history/remove", nil, h, nil)
}

// WatchedMovie is one entry of /sync/watched/movies.
type WatchedMovie struct {
	LastWatchedAt string `json:"last_watched_at"`
	Movie         Media  `json:"movie"`
}

// WatchedShow is one entry of /sync/watched/shows, with every episode watched.
type WatchedShow struct {
	LastWatchedAt string `json:"last_watched_at"`
	Show          Media  `json:"show"`
	Seasons       []struct {
		Number   int `json:"number"`
		Episodes []struct {
			Number        int    `json:"number"`
			LastWatchedAt string `json:"last_watched_at"`
		} `json:"episodes"`
	} `json:"seasons"`
}

func (c *Client) WatchedMovies(ctx context.Context, access string) ([]WatchedMovie, error) {
	var out []WatchedMovie
	err := c.call(ctx, access, http.MethodGet, "/sync/watched/movies", nil, nil, &out)
	return out, err
}

func (c *Client) WatchedShows(ctx context.Context, access string) ([]WatchedShow, error) {
	var out []WatchedShow
	err := c.call(ctx, access, http.MethodGet, "/sync/watched/shows", nil, nil, &out)
	return out, err
}

// Playback is a session someone paused partway: progress is a percentage.
type Playback struct {
	Progress float64  `json:"progress"`
	PausedAt string   `json:"paused_at"`
	Type     string   `json:"type"` // "movie" or "episode"
	Movie    *Media   `json:"movie"`
	Show     *Media   `json:"show"`
	Episode  *Episode `json:"episode"`
}

// Playbacks lists the user's paused sessions.
func (c *Client) Playbacks(ctx context.Context, access string) ([]Playback, error) {
	var out []Playback
	err := c.call(ctx, access, http.MethodGet, "/sync/playback", nil, nil, &out)
	return out, err
}

// ListItem is one entry of a watchlist or a chart.
type ListItem struct {
	Type  string `json:"type"`
	Movie *Media `json:"movie"`
	Show  *Media `json:"show"`
}

// Watchlist lists the user's watchlist, movies and shows.
func (c *Client) Watchlist(ctx context.Context, access string) ([]ListItem, error) {
	var out []ListItem
	err := c.call(ctx, access, http.MethodGet, "/sync/watchlist", url.Values{"limit": {"1000"}}, nil, &out)
	return out, err
}

// Chart reads a public chart: kind is "movies" or "shows", chart is
// "trending", "popular", "anticipated" or "watched/weekly".
func (c *Client) Chart(ctx context.Context, kind, chart string, limit int) ([]ListItem, error) {
	var raw []json.RawMessage
	if err := c.call(ctx, "", http.MethodGet, "/"+kind+"/"+chart, url.Values{"limit": {fmt.Sprint(limit)}}, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]ListItem, 0, len(raw))
	for _, r := range raw {
		// "popular" lists bare titles; the others wrap them with counts.
		var wrapped ListItem
		if json.Unmarshal(r, &wrapped) == nil && (wrapped.Movie != nil || wrapped.Show != nil) {
			out = append(out, wrapped)
			continue
		}
		var m Media
		if json.Unmarshal(r, &m) == nil && m.IDs.Trakt > 0 {
			if kind == "movies" {
				out = append(out, ListItem{Type: "movie", Movie: &m})
			} else {
				out = append(out, ListItem{Type: "show", Show: &m})
			}
		}
	}
	return out, nil
}

// Username returns the Trakt account's display name.
func (c *Client) Username(ctx context.Context, access string) (string, error) {
	var out struct {
		User struct {
			Username string `json:"username"`
			Name     string `json:"name"`
		} `json:"user"`
	}
	err := c.call(ctx, access, http.MethodGet, "/users/settings", nil, nil, &out)
	return firstOf(out.User.Username, out.User.Name), err
}

type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string { return fmt.Sprintf("trakt %d: %s", e.Code, e.Body) }

func (c *Client) call(ctx context.Context, access, method, path string, q url.Values, body, out any) error {
	u := c.base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.ClientID())
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
