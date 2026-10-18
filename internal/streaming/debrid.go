package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider is a debrid service holding cached torrents (TorBox, AllDebrid,
// Real-Debrid). Wren TV only uses cached torrents: nothing is ever
// downloaded by the provider on our behalf.
type Provider interface {
	Name() string
	// Check reports which hashes are cached. Providers that can only tell by
	// adding the torrent return ok=false; callers then use Add, which fails
	// with ErrNotCached and leaves nothing behind.
	Check(ctx context.Context, hashes []string) (cached map[string]bool, ok bool, err error)
	// Add adds a cached torrent to the account and lists its files.
	Add(ctx context.Context, hash, magnet string) (*Torrent, error)
	// Link returns a direct URL for one file of an added torrent.
	Link(ctx context.Context, ref, fileID string) (string, error)
}

// UsenetProvider is a debrid service that also serves the Usenet posts it
// has cached (TorBox). Usenet streams come first: they start as fast as a
// cached torrent and don't depend on seeders.
type UsenetProvider interface {
	Provider
	// SearchUsenet lists the cached posts of a title (IMDb id; with season
	// and episode for shows).
	SearchUsenet(ctx context.Context, imdbID string, season, episode int) ([]Release, error)
	// AddUsenet adds a cached post to the account and lists its files.
	AddUsenet(ctx context.Context, hash, nzb, name string) (*Torrent, error)
	// LinkUsenet returns a direct URL for one file of an added post.
	LinkUsenet(ctx context.Context, ref, fileID string) (string, error)
}

// Torrent is a torrent (or Usenet download) on a provider's account.
type Torrent struct {
	Ref   string
	Files []RemoteFile
}

// RemoteFile is one file of an added torrent. ID is what Link takes.
type RemoteFile struct {
	ID   string
	Name string
	Size int64
}

var (
	ErrNotCached   = errors.New("not cached")
	ErrStale       = errors.New("no longer available on the debrid service")
	ErrRateLimited = errors.New("rate limited")
)

// CooldownError means a provider (or addon) asked us to back off.
type CooldownError struct {
	Who        string
	RetryAfter time.Duration
}

func (e *CooldownError) Error() string {
	return fmt.Sprintf("%s: rate limited (retry in %s)", e.Who, e.RetryAfter.Round(time.Second))
}

func (e *CooldownError) Unwrap() error { return ErrRateLimited }

// guard paces requests per endpoint and shares cooldowns across every
// caller, so one 429 stops all concurrent lookups from repeating it.
// Ported from WatchTower's provider guards.
type guard struct {
	who      string
	interval time.Duration // minimum gap between requests to one endpoint
	cooldown time.Duration // default back-off after a 429 without Retry-After

	mu      sync.Mutex
	next    map[string]time.Time
	blocked time.Time
}

func newGuard(who string, interval, cooldown time.Duration) *guard {
	return &guard{who: who, interval: interval, cooldown: cooldown, next: map[string]time.Time{}}
}

// wait reserves a slot for endpoint, or fails fast while cooling down.
func (g *guard) wait(ctx context.Context, endpoint string) error {
	g.mu.Lock()
	now := time.Now()
	if d := g.blocked.Sub(now); d > 0 {
		g.mu.Unlock()
		return &CooldownError{Who: g.who, RetryAfter: d}
	}
	at := now
	if n := g.next[endpoint]; n.After(at) {
		at = n
	}
	g.next[endpoint] = at.Add(g.interval)
	g.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (g *guard) block(d time.Duration) error {
	if d <= 0 {
		d = g.cooldown
	}
	g.mu.Lock()
	if until := time.Now().Add(d); until.After(g.blocked) {
		g.blocked = until
	}
	g.mu.Unlock()
	return &CooldownError{Who: g.who, RetryAfter: d}
}

// coolingDown reports the remaining cooldown, if any.
func (g *guard) coolingDown() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Until(g.blocked)
}

func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		return time.Until(at)
	}
	return 0
}

// apiCall runs one JSON API request through a guard and maps throttling
// and outages to cooldowns. It returns the body for 2xx responses.
func apiCall(ctx context.Context, g *guard, c *http.Client, endpoint string, req *http.Request) ([]byte, int, error) {
	if err := g.wait(ctx, endpoint); err != nil {
		return nil, 0, err
	}
	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, fmt.Errorf("%s %s: %w", g.who, endpoint, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, resp.StatusCode, g.block(retryAfter(resp.Header.Get("Retry-After")))
	case resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway:
		return nil, resp.StatusCode, g.block(max(retryAfter(resp.Header.Get("Retry-After")), 30*time.Second))
	case resp.StatusCode/100 != 2:
		return b, resp.StatusCode, fmt.Errorf("%s %s: %s: %s", g.who, endpoint, resp.Status, apiMessage(b))
	}
	return b, resp.StatusCode, nil
}

// apiMessage pulls a human-readable error out of a provider response.
func apiMessage(b []byte) string {
	var raw map[string]any
	if json.Unmarshal(b, &raw) == nil {
		for _, k := range []string{"detail", "error", "message"} {
			switch v := raw[k].(type) {
			case string:
				if v != "" {
					return v
				}
			case map[string]any:
				if m, _ := v["message"].(string); m != "" {
					return m
				}
			}
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func isVideoName(p string) bool {
	p = strings.ToLower(p)
	for _, e := range []string{".mkv", ".mp4", ".m4v", ".mov", ".avi", ".ts", ".m2ts", ".webm"} {
		if strings.HasSuffix(p, e) {
			return !strings.Contains(p, "sample")
		}
	}
	return false
}

// NewProviders builds the configured providers in order.
func NewProviders(order []string, keys map[string]string) []Provider {
	var out []Provider
	seen := map[string]bool{}
	for _, name := range append(order, "torbox", "alldebrid", "realdebrid") {
		name = strings.ToLower(strings.TrimSpace(name))
		if seen[name] || keys[name] == "" {
			continue
		}
		seen[name] = true
		switch name {
		case "torbox":
			out = append(out, newTorBox(keys[name]))
		case "alldebrid":
			out = append(out, newAllDebrid(keys[name]))
		case "realdebrid":
			out = append(out, newRealDebrid(keys[name]))
		}
	}
	return out
}

var apiClient = &http.Client{Timeout: 30 * time.Second}
