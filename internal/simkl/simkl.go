// Package simkl talks to the Simkl API (api.simkl.org): device sign-in,
// scrobbling, marking watched and reading a user's watch history.
package simkl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const appName = "wrentv"

var (
	// ErrPending means the user hasn't approved the device code yet.
	ErrPending = errors.New("authorization pending")
	// ErrSlowDown asks the poller to wait five seconds longer between polls.
	ErrSlowDown = errors.New("slow down")
	// ErrExpired means the device code is dead; start again.
	ErrExpired = errors.New("code expired")
	// ErrUnauthorized means the access token was rejected.
	ErrUnauthorized = errors.New("simkl rejected the token")
)

type Client struct {
	// ClientID is the app registered at simkl.com/settings/developer
	// (AUTH V2). Empty disables Simkl.
	ClientID   func() string
	AppVersion string
	HTTP       *http.Client
	// Base overrides https://api.simkl.com (tests).
	Base string
}

func New(clientID func() string, version string) *Client {
	return &Client{ClientID: clientID, AppVersion: version, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Enabled() bool { return c != nil && c.ClientID != nil && c.ClientID() != "" }

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return "https://api.simkl.com"
}

func (c *Client) userAgent() string { return appName + "/" + strings.TrimPrefix(c.version(), "v") }

func (c *Client) version() string {
	if c.AppVersion == "" {
		return "1.0"
	}
	return c.AppVersion
}

// ---------- sign-in (OAuth 2.0 device flow) ----------

type Device struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	URL        string `json:"verification_uri"`
	URLFull    string `json:"verification_uri_complete"`
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
	err := c.form(ctx, "/oauth2/device", url.Values{"client_id": {c.ClientID()}, "scope": {"media:read media:write"}}, &d)
	if d.Interval <= 0 {
		d.Interval = 5
	}
	return d, err
}

// PollDevice returns the tokens once the user approves, else ErrPending,
// ErrSlowDown or ErrExpired.
func (c *Client) PollDevice(ctx context.Context, deviceCode string) (Token, error) {
	return c.token(ctx, url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id": {c.ClientID()}, "device_code": {deviceCode}})
}

func (c *Client) Refresh(ctx context.Context, refresh string) (Token, error) {
	return c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID()}, "refresh_token": {refresh}})
}

func (c *Client) token(ctx context.Context, v url.Values) (Token, error) {
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := c.form(ctx, "/oauth2/token", v, &out); err != nil {
		return Token{}, err
	}
	if out.AccessToken == "" {
		return Token{}, errors.New("simkl returned no token")
	}
	exp := out.ExpiresIn
	if exp <= 0 {
		exp = 7 * 24 * 3600
	}
	return Token{Access: out.AccessToken, Refresh: out.RefreshToken, Expires: time.Now().Unix() + exp}, nil
}

func (c *Client) form(ctx context.Context, path string, v url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = json.Unmarshal(b, &e)
		switch e.Error {
		case "authorization_pending":
			return ErrPending
		case "slow_down":
			return ErrSlowDown
		case "expired_token":
			return ErrExpired
		}
		return fmt.Errorf("simkl %s: %s", resp.Status, firstOf(e.Desc, e.Error, string(b)))
	}
	return json.Unmarshal(b, out)
}

// ---------- API calls ----------

// Media identifies a movie or show; IDs keys are Simkl's (imdb, tmdb, tvdb…).
type Media struct {
	Title string         `json:"title,omitempty"`
	Year  int            `json:"year,omitempty"`
	IDs   map[string]any `json:"ids"`
}

// ID returns ids[key] as a string ("" when missing).
func (m *Media) ID(key string) string {
	if m == nil {
		return ""
	}
	switch v := m.IDs[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	}
	return ""
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
}

// Scrobble reports a player event. minProgress (50–100) is the share of the
// runtime at which a stop marks the title watched.
func (c *Client) Scrobble(ctx context.Context, access, action string, minProgress int, body Scrobble) error {
	q := url.Values{}
	if minProgress > 0 {
		q.Set("min_progress", strconv.Itoa(minProgress))
	}
	err := c.call(ctx, access, http.MethodPost, "/scrobble/"+action, q, body, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusConflict {
		return nil // already scrobbled within the hour
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

// ListItem is one entry of /sync/all-items.
type ListItem struct {
	Status        string `json:"status"`
	LastWatchedAt string `json:"last_watched_at"`
	Movie         *Media `json:"movie"`
	Show          *Media `json:"show"`
	Seasons       []struct {
		Number   int `json:"number"`
		Episodes []struct {
			Number    int    `json:"number"`
			WatchedAt string `json:"watched_at"`
		} `json:"episodes"`
	} `json:"seasons"`
}

type AllItems struct {
	Movies []ListItem `json:"movies"`
	Shows  []ListItem `json:"shows"`
	Anime  []ListItem `json:"anime"`
}

// AllItems lists one watchlist status of one type ("movies", "shows",
// "anime"), with every watched episode and when it was watched.
func (c *Client) AllItems(ctx context.Context, access, kind, status string) (AllItems, error) {
	var out AllItems
	q := url.Values{}
	if kind != "movies" {
		q.Set("extended", "full")
		q.Set("episode_watched_at", "yes")
		q.Set("include_all_episodes", "yes")
	}
	err := c.call(ctx, access, http.MethodGet, "/sync/all-items/"+kind+"/"+status, q, nil, &out)
	return out, err
}

// Playback is a session someone paused partway: progress is a percentage.
// Simkl's docs name the pause time and episode number two ways each.
type Playback struct {
	Progress  float64 `json:"progress"`
	PausedAt  string  `json:"paused_at"`
	WatchedAt string  `json:"watched_at"`
	Movie     *Media  `json:"movie"`
	Show      *Media  `json:"show"`
	Anime     *Media  `json:"anime"`
	Episode   *struct {
		Season  int `json:"season"`
		Number  int `json:"number"`
		Episode int `json:"episode"`
	} `json:"episode"`
}

// When is when the session was paused.
func (p *Playback) When() string { return firstOf(p.PausedAt, p.WatchedAt) }

// SeasonEpisode is the paused episode's season and number.
func (p *Playback) SeasonEpisode() (int, int) {
	if p.Episode == nil {
		return 0, 0
	}
	return p.Episode.Season, max(p.Episode.Number, p.Episode.Episode)
}

// Playbacks lists the user's paused sessions that haven't since been watched.
func (c *Client) Playbacks(ctx context.Context, access string) ([]Playback, error) {
	var out []Playback
	err := c.call(ctx, access, http.MethodGet, "/sync/playback", nil, nil, &out)
	return out, err
}

// Username returns the Simkl account's display name.
func (c *Client) Username(ctx context.Context, access string) (string, error) {
	var out struct {
		User struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	err := c.call(ctx, access, http.MethodGet, "/users/settings", nil, nil, &out)
	return out.User.Name, err
}

type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string { return fmt.Sprintf("simkl %d: %s", e.Code, e.Body) }

func (c *Client) call(ctx context.Context, access, method, path string, q url.Values, body, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("client_id", c.ClientID())
	q.Set("app-name", appName)
	q.Set("app-version", strings.TrimPrefix(c.version(), "v"))
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path+"?"+q.Encode(), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("simkl-api-key", c.ClientID())
	req.Header.Set("User-Agent", c.userAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
	return json.NewDecoder(resp.Body).Decode(out)
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// Watchlist lists one watchlist status of one type ("movies", "shows",
// "anime") without episodes.
func (c *Client) Watchlist(ctx context.Context, access, kind, status string) (AllItems, error) {
	var out AllItems
	err := c.call(ctx, access, http.MethodGet, "/sync/all-items/"+kind+"/"+status, nil, nil, &out)
	return out, err
}

// TrendingURL is where Simkl publishes its trending titles ("movies", "tv"
// or "anime"; "today", "week" or "month"), free to read without a client ID.
var TrendingURL = func(kind, interval string) string {
	return "https://data.simkl.in/discover/trending/" + kind + "/" + interval + "_100.json"
}

// Trending is one title of a trending chart.
type Trending struct {
	Title     string         `json:"title"`
	IDs       map[string]any `json:"ids"`
	AnimeType string         `json:"anime_type"`
}

// Trending fetches a trending chart.
func (c *Client) Trending(ctx context.Context, kind, interval string) ([]Trending, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TrendingURL(kind, interval), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("simkl trending: %s", resp.Status)
	}
	var out []Trending
	err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out)
	return out, err
}
