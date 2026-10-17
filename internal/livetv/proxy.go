package livetv

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrBusy: every stream the provider allows is in use.
var ErrBusy = errors.New("all live TV streams are in use")

// Session is one viewer watching one channel. Its id is the credential in
// the stream URLs, like a film's.
type Session struct {
	ID        string
	UserID    string
	ChannelID string
	// Kind is "hls" or "ts".
	Kind      string
	upstream  string
	userAgent string
	key       []byte // signs the upstream URLs in rewritten playlists
	ctx       context.Context
	cancel    context.CancelFunc
	last      atomic.Int64 // Unix seconds of the last request
	open      atomic.Int32 // requests in flight
}

type sessions struct {
	mu   sync.Mutex
	byID map[string]*Session
	// kinds remembers whether a channel's stream is HLS or TS, so tuning
	// back to it needn't look again.
	kinds map[string]string
}

var streamClient = &http.Client{
	// No overall timeout: a TS stream stays open for as long as it's watched.
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConnsPerHost:   8,
	},
}

// Play starts a session on a channel for a user. A user's other sessions
// make room when the provider's limit is reached (the same TV flipping).
func (s *Service) Play(ctx context.Context, userID, channelID string) (*Session, error) {
	c := s.Channel(channelID)
	if c == nil {
		return nil, errors.New("channel not found")
	}
	ss := &s.sessions
	limit := s.cfg.LiveTV.MaxStreams
	ss.mu.Lock()
	if ss.byID == nil {
		ss.byID = map[string]*Session{}
		ss.kinds = map[string]string{}
	}
	if limit > 0 && len(ss.byID) >= limit {
		var oldest *Session
		for _, x := range ss.byID {
			if x.UserID == userID && (oldest == nil || x.last.Load() < oldest.last.Load()) {
				oldest = x
			}
		}
		if oldest == nil {
			ss.mu.Unlock()
			return nil, fmt.Errorf("%w (%d at once)", ErrBusy, limit)
		}
		ss.stopLocked(oldest.ID)
	}
	kind := ss.kinds[channelID]
	ss.mu.Unlock()

	if kind == "" {
		kind = s.probe(ctx, c)
	}
	sctx, cancel := context.WithCancel(context.Background())
	sess := &Session{
		ID: randomHex(16), UserID: userID, ChannelID: channelID, Kind: kind,
		upstream: c.URL, userAgent: c.UserAgent, key: []byte(randomHex(16)), ctx: sctx, cancel: cancel,
	}
	sess.last.Store(time.Now().Unix())
	ss.mu.Lock()
	ss.byID[sess.ID] = sess
	ss.kinds[channelID] = kind
	ss.mu.Unlock()
	return sess, nil
}

// URL is the session's stream, relative to the server.
func (x *Session) URL() string {
	if x.Kind == "hls" {
		return "/live/" + x.ID + "/index.m3u8"
	}
	return "/live/" + x.ID + "/stream.ts"
}

// probe looks at the start of a stream to tell HLS from MPEG-TS.
func (s *Service) probe(ctx context.Context, c *Channel) string {
	p := strings.ToLower(strings.SplitN(c.URL, "?", 2)[0])
	if strings.HasSuffix(p, ".m3u8") || strings.HasSuffix(p, ".m3u") {
		return "hls"
	}
	if strings.HasSuffix(p, ".ts") {
		return "ts"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := get(ctx, c.URL, c.UserAgent)
	if err != nil {
		return "ts"
	}
	defer resp.Body.Close()
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "mpegurl") {
		return "hls"
	}
	head := make([]byte, 16)
	n, _ := io.ReadFull(resp.Body, head)
	if bytes.HasPrefix(bytes.TrimSpace(head[:n]), []byte("#EXTM3U")) {
		return "hls"
	}
	return "ts"
}

// Get finds a live session.
func (s *Service) Get(id string) *Session {
	ss := &s.sessions
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.byID[id]
}

// Stop ends a session; only its viewer (or an admin) may.
func (s *Service) Stop(id, userID string, admin bool) bool {
	ss := &s.sessions
	ss.mu.Lock()
	defer ss.mu.Unlock()
	x := ss.byID[id]
	if x == nil || (x.UserID != userID && !admin) {
		return false
	}
	ss.stopLocked(id)
	return true
}

// Playing counts sessions, for the dashboard.
func (s *Service) Playing() int {
	ss := &s.sessions
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.byID)
}

func (ss *sessions) stopLocked(id string) {
	if x := ss.byID[id]; x != nil {
		x.cancel()
		delete(ss.byID, id)
	}
}

// reap ends sessions nobody has asked anything of for a minute: a TV that
// went to sleep, or a player that never said it stopped.
func (ss *sessions) reap(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cutoff := time.Now().Add(-time.Minute).Unix()
		ss.mu.Lock()
		for id, x := range ss.byID {
			if x.open.Load() == 0 && x.last.Load() < cutoff {
				ss.stopLocked(id)
			}
		}
		ss.mu.Unlock()
	}
}

// ServeHTTP answers a request under /live/{id}/.
func (x *Session) ServeHTTP(w http.ResponseWriter, r *http.Request, file string) {
	x.open.Add(1)
	defer func() { x.open.Add(-1); x.last.Store(time.Now().Unix()) }()
	x.last.Store(time.Now().Unix())
	// Stopping the session ends its streams too.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(x.ctx, cancel)
	defer stop()

	switch {
	case file == "stream.ts" && x.Kind == "ts":
		x.serveTS(ctx, w)
	case file == "index.m3u8" && x.Kind == "hls":
		target := x.upstream
		if u := r.URL.Query().Get("u"); u != "" {
			var ok bool
			if target, ok = x.verify(u, r.URL.Query().Get("s")); !ok {
				http.Error(w, "bad link", http.StatusForbidden)
				return
			}
		}
		x.servePlaylist(ctx, w, target)
	case file == "seg" && x.Kind == "hls":
		target, ok := x.verify(r.URL.Query().Get("u"), r.URL.Query().Get("s"))
		if !ok {
			http.Error(w, "bad link", http.StatusForbidden)
			return
		}
		x.serveBytes(ctx, w, r, target)
	default:
		http.NotFound(w, r)
	}
}

// serveTS passes a continuous MPEG-TS stream through.
func (x *Session) serveTS(ctx context.Context, w http.ResponseWriter) {
	resp, err := get(ctx, x.upstream, x.userAgent)
	if err != nil {
		http.Error(w, "the channel didn't answer", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		http.Error(w, "the provider answered "+resp.Status, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			x.last.Store(time.Now().Unix())
		}
		if err != nil {
			return
		}
	}
}

// servePlaylist fetches an HLS playlist and points everything in it back
// through this session, so the player only ever talks to the server.
func (x *Session) servePlaylist(ctx context.Context, w http.ResponseWriter, target string) {
	resp, err := get(ctx, target, x.userAgent)
	if err != nil {
		http.Error(w, "the channel didn't answer", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		http.Error(w, "the provider answered "+resp.Status, http.StatusBadGateway)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		http.Error(w, "the channel didn't answer", http.StatusBadGateway)
		return
	}
	// Relative links are relative to where redirects ended up.
	base := resp.Request.URL
	out := x.rewrite(body, base)
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}

func (x *Session) rewrite(body []byte, base *url.URL) []byte {
	master := bytes.Contains(body, []byte("#EXT-X-STREAM-INF"))
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case strings.TrimSpace(line) == "":
		case strings.HasPrefix(line, "#"):
			// Keys, init segments and alternate renditions carry their links in URI="…".
			if i := strings.Index(line, `URI="`); i >= 0 {
				j := strings.Index(line[i+5:], `"`)
				if j >= 0 {
					ref := line[i+5 : i+5+j]
					playlist := strings.HasPrefix(line, "#EXT-X-MEDIA") || strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF")
					line = line[:i+5] + x.link(base, ref, playlist) + line[i+5+j:]
				}
			}
		default:
			line = x.link(base, strings.TrimSpace(line), master)
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// link points an upstream reference at this session, signed so the session
// can't be used to fetch anything else.
func (x *Session) link(base *url.URL, ref string, playlist bool) string {
	u, err := base.Parse(ref)
	if err != nil {
		return ref
	}
	abs := u.String()
	enc := base64.RawURLEncoding.EncodeToString([]byte(abs))
	q := "u=" + enc + "&s=" + x.sign(abs)
	if playlist {
		return "index.m3u8?" + q
	}
	return "seg?" + q
}

func (x *Session) sign(s string) string {
	m := hmac.New(sha256.New, x.key)
	m.Write([]byte(s))
	return hex.EncodeToString(m.Sum(nil)[:12])
}

func (x *Session) verify(enc, sig string) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", false
	}
	s := string(b)
	return s, hmac.Equal([]byte(x.sign(s)), []byte(sig))
}

// serveBytes passes one segment (or key) through.
func (x *Session) serveBytes(ctx context.Context, w http.ResponseWriter, r *http.Request, target string) {
	resp, err := get(ctx, target, x.userAgent)
	if err != nil {
		http.Error(w, "the channel didn't answer", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		http.Error(w, "the provider answered "+resp.Status, http.StatusBadGateway)
		return
	}
	for _, h := range []string{"Content-Type", "Content-Length"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func get(ctx context.Context, target, userAgent string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua(userAgent))
	return streamClient.Do(req)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
