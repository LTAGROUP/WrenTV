package sports

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
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

// providerBodyLimit bounds every sports provider response; the byte cap keeps a
// hostile or broken upstream from exhausting memory.
const providerBodyLimit = 32 << 20

// providerBlockBase is the first backoff after a provider refuses a league, and
// providerBlockMax caps it. A refusal (401/403/429) is not a request that may
// succeed on the next cycle: re-asking every 90 seconds is exactly what turns a
// rate limit into an IP-level block, and then every league fails at once.
const (
	providerBlockBase = 5 * time.Minute
	providerBlockMax  = 6 * time.Hour
)

// providerStatusError is a provider response that is not a usable answer. The
// message is unchanged from the plain fmt.Errorf this replaces, so callers and
// tests that read it keep working; Status is what lets the cycle tell a refusal
// apart from a transient failure.
type providerStatusError struct {
	msg    string
	status int
}

func (e *providerStatusError) Error() string { return e.msg }

func providerStatus(status int, format string, args ...any) error {
	return &providerStatusError{msg: fmt.Sprintf(format, args...), status: status}
}

// providerRefusal reports whether an error is the provider refusing to serve
// this client, rather than failing to serve it.
func providerRefusal(err error) bool {
	var refused *providerStatusError
	if !errors.As(err, &refused) {
		return false
	}
	switch refused.status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}
	return false
}

type providerBlock struct {
	until    time.Time
	refusals int
}

// leagueBlocked reports whether the provider recently refused this league, so
// the cycle must not ask again yet. The last known data stays in place.
func (s *Service) leagueBlocked(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	block, ok := s.providerBlocks[id]
	return ok && time.Now().Before(block.until)
}

// noteLeagueRefusal records a refusal and reports whether the error was one. A
// backoff doubles up to providerBlockMax. It is deliberately silent: a refusal
// usually covers every league at once, so logging here is one line per league
// per cycle, which buries the single fact that the provider refused this
// client. The cycle reports that once, and the status carries the count. A
// transient failure is not recorded here and is retried normally.
func (s *Service) noteLeagueRefusal(id string, err error) bool {
	if !providerRefusal(err) {
		return false
	}
	s.mu.Lock()
	block := s.providerBlocks[id]
	block.refusals++
	delay := providerBlockBase << min(block.refusals-1, 6)
	if delay > providerBlockMax {
		delay = providerBlockMax
	}
	block.until = time.Now().Add(delay + providerBlockJitter(id, delay))
	s.providerBlocks[id] = block
	s.mu.Unlock()
	return true
}

// clearLeagueRefusal forgets a refusal streak once the provider answers again.
func (s *Service) clearLeagueRefusal(id string) {
	s.mu.Lock()
	delete(s.providerBlocks, id)
	s.mu.Unlock()
}

// providerBlockJitter spreads the retry of hundreds of leagues that were
// refused together, so recovery does not become another burst.
func providerBlockJitter(id string, delay time.Duration) time.Duration {
	sum := sha256.Sum256([]byte(id))
	return time.Duration(sum[0]%10) * delay / 100
}

type sportsHTTPEntry struct {
	body     []byte
	header   http.Header
	status   int
	expires  time.Time
	failures int
}

type sportsHTTPFlight struct {
	done  chan struct{}
	entry *sportsHTTPEntry
	err   error
}

// providerRefusalBurst is how many refusals inside providerRefusalWindow mean
// the block is about this client rather than one league; providerRefusalCooldown
// is then how long the host stays quiet. Every league is refused together when
// the block is about this client's IP, so continuing the cycle would keep
// feeding the block with requests that were already answered.
const (
	providerRefusalCooldown = time.Minute
	providerRefusalBurst    = 5
	providerRefusalWindow   = 30 * time.Second
)

// providerRequestInterval paces requests to one provider. The catalog polls more
// than 200 leagues, so a cold cache or a refresh after an outage would otherwise
// emit hundreds of requests in a burst, which is what earns a provider-wide
// block. Steady state is far below this rate because cached scoreboards are
// reused, so the pacing only shapes startup and recovery.
const providerRequestInterval = 200 * time.Millisecond

type sportsHTTPHost struct {
	slots    chan struct{}
	until    time.Time
	status   int
	refusals int
	window   time.Time
	next     time.Time
}

// reserveSend claims the next send slot for this provider and returns when it
// may be used. Callers hold the pool lock.
func (h *sportsHTTPHost) reserveSend(now time.Time) time.Time {
	if h.next.Before(now) {
		h.next = now
	}
	send := h.next
	h.next = send.Add(providerRequestInterval)
	return send
}

// noteRefusal counts a refusal and silences the host once several arrive inside
// the window. A single refused league must not stop the other leagues; a
// provider that refuses everything must not be asked 90 more times.
func (h *sportsHTTPHost) noteRefusal(status int, now time.Time) {
	if now.After(h.window) {
		h.refusals = 0
		h.window = now.Add(providerRefusalWindow)
	}
	h.refusals++
	if h.refusals < providerRefusalBurst {
		return
	}
	until := now.Add(providerRefusalCooldown)
	if until.After(h.until) {
		h.until = until
		h.status = status
	}
}

type sportsHTTPPool struct {
	mu      sync.Mutex
	entries map[string]*sportsHTTPEntry
	flights map[string]*sportsHTTPFlight
	hosts   map[string]*sportsHTTPHost
	bytes   int
}

type sportsHTTPTransport struct {
	next http.RoundTripper
	pool *sportsHTTPPool
}

var sharedSportsHTTP = newSportsHTTPPool()

func newSportsHTTPPool() *sportsHTTPPool {
	return &sportsHTTPPool{entries: map[string]*sportsHTTPEntry{}, flights: map[string]*sportsHTTPFlight{}, hosts: map[string]*sportsHTTPHost{}}
}

// boundedSportsClient wraps a client in the shared, cached, single-flight
// transport used by every sports provider.
func boundedSportsClient(client *http.Client) *http.Client {
	clone := *client
	next := client.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	clone.Transport = &sportsHTTPTransport{next: next, pool: sharedSportsHTTP}
	return &clone
}

func sportsHTTPResponse(req *http.Request, e *sportsHTTPEntry) *http.Response {
	return &http.Response{StatusCode: e.status, Header: e.header.Clone(), Body: io.NopCloser(bytes.NewReader(e.body)), ContentLength: int64(len(e.body)), Request: req}
}

func providerRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 {
		return time.Duration(min(seconds, int64((1<<63-1)/int64(time.Second)))) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return time.Minute
}

func providerCooldown(until time.Time, status int) *sportsHTTPEntry {
	if status == 0 {
		status = http.StatusTooManyRequests
	}
	return &sportsHTTPEntry{status: status, header: http.Header{"Retry-After": []string{strconv.Itoa(max(1, int(time.Until(until).Seconds())+1))}}}
}

func (t *sportsHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.next.RoundTrip(req)
	}
	headers, _ := json.Marshal(req.Header)
	key := fmt.Sprintf("%x", sha256.Sum256(append([]byte(req.URL.String()), headers...)))
	p := t.pool
	p.mu.Lock()
	old := p.entries[key]
	if old != nil && time.Now().Before(old.expires) {
		p.mu.Unlock()
		return sportsHTTPResponse(req, old), nil
	}
	provider := sportsProviderKey(req.URL.Hostname())
	host := p.hosts[provider]
	if host == nil {
		host = &sportsHTTPHost{slots: make(chan struct{}, 4)}
		p.hosts[provider] = host
	}
	if time.Now().Before(host.until) {
		entry := providerCooldown(host.until, host.status)
		p.mu.Unlock()
		return sportsHTTPResponse(req, entry), nil
	}
	flight := p.flights[key]
	if flight == nil {
		flight = &sportsHTTPFlight{done: make(chan struct{})}
		p.flights[key] = flight
		go t.fetch(req, key, host, old, flight)
	}
	p.mu.Unlock()
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-flight.done:
	}
	if flight.err != nil {
		return nil, flight.err
	}
	return sportsHTTPResponse(req, flight.entry), nil
}

func (t *sportsHTTPTransport) fetch(original *http.Request, key string, host *sportsHTTPHost, old *sportsHTTPEntry, f *sportsHTTPFlight) {
	p := t.pool
	defer func() { p.mu.Lock(); delete(p.flights, key); close(f.done); p.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(original.Context()), 20*time.Second)
	defer cancel()
	select {
	case host.slots <- struct{}{}:
		defer func() { <-host.slots }()
	case <-ctx.Done():
		f.err = ctx.Err()
		return
	}
	// Wait for this request's send slot so concurrent leagues leave as a paced
	// stream rather than a burst the provider answers with 403.
	p.mu.Lock()
	send := host.reserveSend(time.Now())
	p.mu.Unlock()
	if wait := time.Until(send); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.err = ctx.Err()
			return
		case <-timer.C:
		}
	}
	p.mu.Lock()
	until := host.until
	coolingStatus := host.status
	p.mu.Unlock()
	if time.Now().Before(until) {
		f.entry = providerCooldown(until, coolingStatus)
		return
	}
	req := original.Clone(ctx)
	response, err := t.next.RoundTrip(req)
	if err != nil {
		response = &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
	}
	defer response.Body.Close()
	var reader io.Reader = response.Body
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		zipped, e := gzip.NewReader(response.Body)
		if e != nil {
			f.err = e
			return
		}
		defer zipped.Close()
		reader = zipped
	}
	body, err := io.ReadAll(io.LimitReader(reader, providerBodyLimit+1))
	if err != nil || len(body) > providerBodyLimit {
		f.err = fmt.Errorf("sports provider response incomplete or exceeds %d bytes", providerBodyLimit)
		return
	}
	header := response.Header.Clone()
	header.Del("Content-Encoding")
	header.Del("Content-Length")
	e := &sportsHTTPEntry{body: body, header: header, status: response.StatusCode}
	ttl := sportsMetadataTTL(req.URL.Path, body)
	switch {
	case e.status == 429:
		p.mu.Lock()
		until := time.Now().Add(providerRetryAfter(header.Get("Retry-After"), time.Now()))
		if until.After(host.until) {
			host.until = until
			host.status = e.status
		}
		p.mu.Unlock()
		f.entry = e
		return
	case e.status == 404:
		ttl = 24 * time.Hour
	case e.status == 401 || e.status == 403:
		// A refused client is refused for every league, so stop asking the host
		// for a while instead of spending the rest of the cycle on requests that
		// will be answered the same way. The status is kept so the log says what
		// actually happened rather than reporting a rate limit.
		ttl = 5 * time.Minute
		p.mu.Lock()
		host.noteRefusal(e.status, time.Now())
		p.mu.Unlock()
	case e.status >= 500:
		e.failures = 1
		if old != nil {
			e.failures = old.failures + 1
		}
		ttl = time.Duration(15<<min(e.failures-1, 4)) * time.Second
	case e.status != 200:
		ttl = time.Minute
	}
	// Stable per-key jitter spreads refreshes without changing fixture semantics.
	jitter := time.Duration(sha256.Sum256([]byte(key))[0]%10) * ttl / 100
	e.expires = time.Now().Add(ttl + jitter)
	p.mu.Lock()
	if previous := p.entries[key]; previous != nil {
		p.bytes -= len(previous.body)
		delete(p.entries, key)
	}
	for len(p.entries) >= 1024 || p.bytes+len(e.body) > 96<<20 {
		for k, v := range p.entries {
			p.bytes -= len(v.body)
			delete(p.entries, k)
			break
		}
	}
	p.entries[key] = e
	p.bytes += len(e.body)
	p.mu.Unlock()
	f.entry = e
}

func sportsMetadataTTL(path string, body []byte) time.Duration {
	switch {
	case path == "/seasons" || strings.Contains(path, "/teams") || strings.Contains(path, "/leagues/dropdown"):
		return 24 * time.Hour
	case strings.Contains(path, "/fixtures/"):
		return 15 * time.Minute
	case strings.Contains(path, "/standings"):
		return 30 * time.Minute
	case strings.Contains(path, "/scoreboard"):
		var payload struct {
			Events []struct {
				Date         string     `json:"date"`
				Status       espnStatus `json:"status"`
				Competitions []struct {
					Status espnStatus `json:"status"`
				} `json:"competitions"`
			} `json:"events"`
		}
		if json.Unmarshal(body, &payload) != nil {
			return 30 * time.Second
		}
		for _, event := range payload.Events {
			if event.Status.Type.State == "in" {
				return 30 * time.Second
			}
			for _, c := range event.Competitions {
				if c.Status.Type.State == "in" {
					return 30 * time.Second
				}
			}
			start := parseESPNDate(event.Date)
			if !start.IsZero() && start.After(time.Now().Add(-15*time.Minute)) && start.Before(time.Now().Add(15*time.Minute)) {
				return 30 * time.Second
			}
		}
		return 15 * time.Minute
	default:
		return 30 * time.Second
	}
}

// sportsProviderKey shares one provider budget and rate-limit cooldown across
// ESPN's metadata hosts.
func sportsProviderKey(host string) string {
	switch strings.ToLower(host) {
	case "site.api.espn.com", "site.web.api.espn.com", "sports.api.espn.com", "sports.core.api.espn.com", "site.api.espncricinfo.com":
		return "espn"
	default:
		return strings.ToLower(host)
	}
}
