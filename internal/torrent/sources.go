package torrent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	atorrent "github.com/anacrolix/torrent"
)

// ---------- built-in engine source ----------

// fileSource streams one file of a torrent. Pieces are fetched in playback
// order on demand (the reader's position drives priorities); nothing is uploaded.
type fileSource struct {
	m         *Manager
	torrentID string
	f         *atorrent.File
	token     string
	once      sync.Once

	raMu sync.Mutex
	ra   atorrent.Reader // for ReaderAt (index parsing)
}

func (s *fileSource) Input() string {
	return fmt.Sprintf("http://127.0.0.1:%d/internal/torrent/%s", s.m.httpPort, s.token)
}

func (s *fileSource) Size() int64 { return s.f.Length() }

func (s *fileSource) ReaderAt() io.ReaderAt { return s }

func (s *fileSource) ReadAt(p []byte, off int64) (int, error) {
	s.raMu.Lock()
	defer s.raMu.Unlock()
	if s.ra == nil {
		s.ra = s.f.NewReader()
		s.ra.SetReadahead(256 << 10)
		s.ra.SetResponsive()
	}
	if _, err := s.ra.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	w := s.m.watch(s.torrentID)
	defer s.m.unwatch(w)
	return io.ReadFull(s.ra, p)
}

func (s *fileSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rd := s.f.NewReader()
	defer rd.Close()
	rd.SetContext(r.Context())
	rd.SetResponsive()
	// Read well ahead of the player so peers have time to deliver.
	rd.SetReadahead(32 << 20)
	w.Header().Set("Content-Type", contentType(s.f.DisplayPath()))
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, "", time.Time{}, &watchedReader{Reader: rd, m: s.m, torrentID: s.torrentID})
}

func (s *fileSource) Close() error {
	s.once.Do(func() {
		s.m.mu.Lock()
		delete(s.m.tokens, s.token)
		s.m.mu.Unlock()
		s.raMu.Lock()
		if s.ra != nil {
			s.ra.Close()
		}
		s.raMu.Unlock()
	})
	return nil
}

// watchedReader reports blocked reads to the stall detector.
type watchedReader struct {
	atorrent.Reader
	m         *Manager
	torrentID string
}

func (r *watchedReader) Read(p []byte) (int, error) {
	w := r.m.watch(r.torrentID)
	defer r.m.unwatch(w)
	return r.Reader.Read(p)
}

// ---------- debrid (HTTP) source ----------

type httpSource struct {
	url    string
	size   int64
	client *http.Client
}

func newHTTPSource(ctx context.Context, url string, size int64) (*httpSource, error) {
	s := &httpSource{url: url, size: size, client: &http.Client{Timeout: 60 * time.Second}}
	if size <= 0 {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
		s.size = resp.ContentLength
	}
	return s, nil
}

func (s *httpSource) Input() string         { return s.url }
func (s *httpSource) Size() int64           { return s.size }
func (s *httpSource) ReaderAt() io.ReaderAt { return s }
func (s *httpSource) Close() error          { return nil }

func (s *httpSource) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	end := off + int64(len(p)) - 1
	if end >= s.size {
		end = s.size - 1
	}
	req, _ := http.NewRequest(http.MethodGet, s.url, nil)
	req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-"+strconv.FormatInt(end, 10))
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("range request: %s", resp.Status)
	}
	n, err := io.ReadFull(resp.Body, p[:end-off+1])
	if err == nil && n < len(p) {
		err = io.EOF
	}
	return n, err
}

// ServeHTTP redirects players straight to the debrid CDN.
func (s *httpSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, s.url, http.StatusFound)
}

func contentType(name string) string {
	switch {
	case hasSuffixFold(name, ".mp4"), hasSuffixFold(name, ".m4v"):
		return "video/mp4"
	case hasSuffixFold(name, ".mkv"):
		return "video/x-matroska"
	case hasSuffixFold(name, ".webm"):
		return "video/webm"
	}
	return "application/octet-stream"
}

func hasSuffixFold(s, suf string) bool {
	if len(s) < len(suf) {
		return false
	}
	t := s[len(s)-len(suf):]
	for i := range t {
		a, b := t[i], suf[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
