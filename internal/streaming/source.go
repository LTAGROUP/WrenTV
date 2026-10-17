package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// throttle caps throughput with one or more token buckets (bytes/second).
// nil limiters are skipped.
type throttle []*rate.Limiter

func newLimiter(mbps float64) *rate.Limiter {
	if mbps <= 0 {
		return nil
	}
	bps := mbps * 1e6 / 8
	return rate.NewLimiter(rate.Limit(bps), int(max(bps/4, 1<<20)))
}

func (t throttle) wait(ctx context.Context, n int) error {
	for _, l := range t {
		if l == nil {
			continue
		}
		for n > 0 {
			c := min(n, l.Burst())
			if err := l.WaitN(ctx, c); err != nil {
				return err
			}
			n -= c
		}
	}
	return nil
}

type throttledReader struct {
	ctx context.Context
	r   io.Reader
	t   throttle
}

func (tr *throttledReader) Read(p []byte) (int, error) {
	if len(p) > 256<<10 {
		p = p[:256<<10]
	}
	n, err := tr.r.Read(p)
	if n > 0 {
		if werr := tr.t.wait(tr.ctx, n); werr != nil {
			return n, werr
		}
	}
	return n, err
}

var upstream = &http.Client{
	// No overall timeout: playback reads stream for hours. Stalls are cut by
	// the request contexts (players disconnect) and transport timeouts.
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
	},
}

// debridSource plays a stored stream through the server: every byte comes
// from the debrid CDN via this proxy, so bandwidth limits apply and the
// debrid link never reaches clients. ffmpeg reads it through a loopback URL.
type debridSource struct {
	svc      *Service
	streamID string
	token    string
	size     int64
	name     string
	t        throttle
	release  func()
	once     sync.Once

	mu  sync.Mutex
	url string
}

func (s *debridSource) Input() string {
	return fmt.Sprintf("http://127.0.0.1:%d/internal/stream/%s", s.svc.HTTPPort, s.token)
}

func (s *debridSource) Size() int64           { return s.size }
func (s *debridSource) ReaderAt() io.ReaderAt { return s }

func (s *debridSource) Close() error {
	s.once.Do(func() {
		s.svc.mu.Lock()
		delete(s.svc.sources, s.token)
		s.svc.mu.Unlock()
		if s.release != nil {
			s.release()
		}
	})
	return nil
}

func (s *debridSource) link() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.url
}

// refresh asks the provider for a new link after the old one stopped working.
func (s *debridSource) refresh(ctx context.Context, old string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.url != old {
		return s.url, nil // another request already refreshed it
	}
	u, _, err := s.svc.linkFor(ctx, s.streamID, true)
	if err != nil {
		return "", err
	}
	s.url = u
	return u, nil
}

// open issues a request upstream, refreshing the link once if it expired.
func (s *debridSource) open(ctx context.Context, method, rng string) (*http.Response, error) {
	u := s.link()
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, nil)
		if err != nil {
			return nil, err
		}
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := upstream.Do(req)
		expired := err == nil && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound ||
			resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusUnauthorized)
		if err == nil && !expired && resp.StatusCode < 500 {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		if attempt > 0 || ctx.Err() != nil {
			if err == nil {
				err = fmt.Errorf("debrid CDN: %s", resp.Status)
			}
			return nil, err
		}
		if u, err = s.refresh(ctx, u); err != nil {
			return nil, err
		}
	}
}

// redactURL drops a request error's URL down to its host: debrid links
// carry the account's download token.
func redactURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		host := ue.URL
		if u, perr := url.Parse(ue.URL); perr == nil {
			host = u.Host
		}
		return fmt.Errorf("%s %s: %w", ue.Op, host, ue.Err)
	}
	return err
}

// check reads the first byte, refreshing the link once if it fails.
func (s *debridSource) check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := s.open(ctx, http.MethodGet, "bytes=0-0")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("debrid CDN: %s", resp.Status)
	}
	_, err = io.ReadFull(resp.Body, make([]byte, 1))
	return err
}

func (s *debridSource) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	end := min(off+int64(len(p)), s.size) - 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	resp, err := s.open(ctx, http.MethodGet, "bytes="+strconv.FormatInt(off, 10)+"-"+strconv.FormatInt(end, 10))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("range request: %s", resp.Status)
	}
	n, err := io.ReadFull(&throttledReader{ctx: ctx, r: resp.Body, t: s.t}, p[:end-off+1])
	if err == nil && n < len(p) {
		err = io.EOF
	}
	return n, err
}

// ServeHTTP proxies a (Range) request to the CDN, throttled.
func (s *debridSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp, err := s.open(r.Context(), r.Method, r.Header.Get("Range"))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Warn("debrid stream failed", "stream", s.streamID, "range", r.Header.Get("Range"), "err", redactURL(err))
			http.Error(w, "stream unavailable", http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", contentType(s.name))
	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, &throttledReader{ctx: r.Context(), r: resp.Body, t: s.t})
}

func contentType(name string) string {
	switch ext := extOf(name); ext {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".ts", ".m2ts":
		return "video/mp2t"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	}
	return "application/octet-stream"
}

func extOf(name string) string {
	for i := len(name) - 1; i >= 0 && name[i] != '/'; i-- {
		if name[i] == '.' {
			b := []byte(name[i:])
			for j := range b {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return ""
}
