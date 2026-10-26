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
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
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
	Started   time.Time
	upstream  string
	userAgent string
	// headers are the allowlisted provider headers this stream asked for
	// (referer/origin/player agent). They never contain credentials.
	headers map[string]string
	key     []byte   // signs the upstream URLs in rewritten playlists
	remux   *remuxer // a TS channel repackaged as HLS, for players that need it
	ctx     context.Context
	cancel  context.CancelFunc
	last    atomic.Int64 // Unix seconds of the last request
	open    atomic.Int32 // requests in flight
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
// wantHLS asks for a TS channel to be repackaged as HLS (Apple's players).
func (s *Service) Play(ctx context.Context, userID, channelID string, wantHLS bool) (*Session, error) {
	c := s.Channel(channelID)
	if c == nil {
		return nil, errors.New("channel not found")
	}
	streamURL, userAgent := c.URL, c.UserAgent
	var headers map[string]string
	if c.stremio {
		link, agent, addonHeaders, err := s.resolveStremio(ctx, c)
		if err != nil {
			return nil, err
		}
		streamURL, userAgent, headers = link, agent, addonHeaders
	}
	// An addon resolves its feeds again on every tune, and the same channel can
	// be HLS on one tune and MPEG-TS on the next, so its stream type is measured
	// afresh each time: a remembered type would hand the player the wrong shape
	// and every reconnect would fail the same way.
	return s.play(ctx, userID, channelID, streamURL, userAgent, headers, wantHLS, !c.stremio)
}

// PlayURL starts a session for a server-resolved stream. It exists for
// ephemeral sports sources: the provider URL never leaves the server, and the
// caller passes an opaque key so the session is reaped like any channel.
func (s *Service) PlayURL(ctx context.Context, userID, key, streamURL, userAgent string, wantHLS bool) (*Session, error) {
	return s.PlayURLWithHeaders(ctx, userID, key, streamURL, userAgent, nil, wantHLS)
}

// PlayURLWithHeaders is PlayURL for a provider that also wants an allowlisted
// browser identity (referer/origin). The set is sanitized here, so an addon can
// never inject a request header the proxy would otherwise send.
//
// A key that names an addon feed ("addon:…", "addons:…") is resolved fresh by
// its caller every time and never remembers its stream type, so one playback
// attempt cannot leave a stale type behind for the next one.
func (s *Service) PlayURLWithHeaders(ctx context.Context, userID, key, streamURL, userAgent string, headers map[string]string, wantHLS bool) (*Session, error) {
	return s.play(ctx, userID, key, streamURL, userAgent, headers, wantHLS, !isAddonSessionKey(key))
}

// isAddonSessionKey reports whether a session key names a resolved addon feed
// rather than a channel in the playlist. Those keys are opaque and per-viewer.
func isAddonSessionKey(key string) bool {
	key = strings.TrimSpace(key)
	return strings.HasPrefix(key, "addon:") || strings.HasPrefix(key, "addons:")
}

// play starts one session. [remember] says whether the stream's type may be
// cached for this key: a channel whose URL is fixed may, a key the caller
// resolves again for every attempt may not.
func (s *Service) play(ctx context.Context, userID, key, streamURL, userAgent string, headers map[string]string, wantHLS, remember bool) (*Session, error) {
	headers = SanitizeRequestHeaders(headers)
	if !strings.HasPrefix(streamURL, "http://") && !strings.HasPrefix(streamURL, "https://") {
		return nil, errors.New("stream is not playable")
	}
	started := time.Now()
	channelID := key
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
	kind := ""
	if remember {
		kind = ss.kinds[channelID]
	}
	ss.mu.Unlock()
	c := &Channel{ID: channelID, URL: streamURL, UserAgent: userAgent}
	if kind == "" {
		kind = s.probe(ctx, c, headers)
	}
	sctx, cancel := context.WithCancel(context.Background())
	sess := &Session{
		ID: randomHex(16), UserID: userID, ChannelID: channelID, Kind: kind, Started: time.Now(),
		upstream: streamURL, userAgent: userAgent, headers: headers,
		key: []byte(randomHex(16)), ctx: sctx, cancel: cancel,
	}
	if kind == "ts" && wantHLS && s.FFmpeg != "" {
		sess.remux = &remuxer{ffmpeg: s.FFmpeg, dir: filepath.Join(s.dir, "remux", sess.ID)}
	}
	sess.last.Store(time.Now().Unix())
	ss.mu.Lock()
	ss.byID[sess.ID] = sess
	if remember {
		ss.kinds[channelID] = kind
	}
	open := len(ss.byID)
	ss.mu.Unlock()
	// Diagnostics for playback and switching: the kind, whether this is an addon
	// feed, how long resolving it took and how many sessions are open. The key
	// itself is never logged: an addon key is a per-viewer token.
	slog.Debug("live session started", "kind", kind, "addon", isAddonSessionKey(key),
		"ms", time.Since(started).Milliseconds(), "sessions", open)
	return sess, nil
}

// Type is what the player gets: "hls" or "ts".
func (x *Session) Type() string {
	if x.remux != nil {
		return "hls"
	}
	return x.Kind
}

// URL is the session's stream, relative to the server.
func (x *Session) URL() string {
	if x.remux != nil {
		return "/live/" + x.ID + "/hls/index.m3u8"
	}
	if x.Kind == "hls" {
		return "/live/" + x.ID + "/index.m3u8"
	}
	return "/live/" + x.ID + "/stream.ts"
}

// probe looks at the start of a stream to tell HLS from MPEG-TS. A stream that
// only answers with its allowlisted headers is read with them.
func (s *Service) probe(ctx context.Context, c *Channel, headers map[string]string) string {
	p := strings.ToLower(strings.SplitN(c.URL, "?", 2)[0])
	if strings.HasSuffix(p, ".m3u8") || strings.HasSuffix(p, ".m3u") {
		return "hls"
	}
	if strings.HasSuffix(p, ".ts") {
		return "ts"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := get(ctx, c.URL, c.UserAgent, headers)
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

// Sessions lists who is watching what, for the dashboard.
func (s *Service) Sessions() []*Session {
	ss := &s.sessions
	ss.mu.Lock()
	defer ss.mu.Unlock()
	out := make([]*Session, 0, len(ss.byID))
	for _, x := range ss.byID {
		out = append(out, x)
	}
	return out
}

// Remuxed says whether a TS channel is being repackaged as HLS by ffmpeg.
func (x *Session) Remuxed() bool { return x.remux != nil }

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
		// Stopping the session cancels its upstream requests too (see
		// ServeHTTP), so a switched-away stream stops fetching from the provider.
		slog.Debug("live session stopped", "kind", x.Kind, "sessions", len(ss.byID))
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
	case x.remux != nil && strings.HasPrefix(file, "hls/"):
		x.remux.serve(ctx, x, w, r, strings.TrimPrefix(file, "hls/"))
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
	resp, err := get(ctx, x.upstream, x.userAgent, x.headers)
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
	resp, err := get(ctx, target, x.userAgent, x.headers)
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
	resp, err := get(ctx, target, x.userAgent, x.headers)
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

// get fetches upstream with the channel's agent plus any allowlisted headers
// the stream asked for. Callers pass headers that already went through
// SanitizeRequestHeaders.
func get(ctx context.Context, target, userAgent string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua(userAgent))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return streamClient.Do(req)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
