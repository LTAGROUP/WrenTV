package streaming

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/usenet"
)

// usenetProvider names streams that play from the news servers.
const usenetProvider = "usenet"

// postTime is how long a lookup spends on Usenet posts before only
// torrents are tried.
const postTime = 40 * time.Second

// usenetState is the shared news server pool, rebuilt when servers change.
type usenetState struct {
	mu   sync.Mutex
	key  string
	pool *usenet.Pool
	// nzbs keeps parsed NZBs of recent plays.
	nzbs map[string]*usenet.NZB
}

func (s *Service) newsPool() *usenet.Pool {
	var servers []usenet.Server
	var key strings.Builder
	for _, ns := range s.cfg().NewsServers {
		if !ns.Enabled || ns.Host == "" {
			continue
		}
		servers = append(servers, usenet.Server{Host: ns.Host, Port: ns.Port, TLS: ns.TLS, User: ns.Username, Pass: ns.Password,
			MaxConns: max(ns.Connections, 1)})
		fmt.Fprintf(&key, "%s|%d|%v|%s|%s|%d;", ns.Host, ns.Port, ns.TLS, ns.Username, ns.Password, ns.Connections)
	}
	u := &s.usenet
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.pool == nil || u.key != key.String() {
		if u.pool != nil {
			u.pool.Close()
		}
		u.key, u.pool = key.String(), usenet.NewPool(servers)
	}
	return u.pool
}

func (s *Service) indexers() []usenet.Indexer {
	var out []usenet.Indexer
	for _, ix := range s.cfg().Indexers {
		if ix.Enabled && ix.URL != "" && ix.APIKey != "" {
			out = append(out, usenet.Indexer{Name: ix.Name, URL: ix.URL, APIKey: ix.APIKey})
		}
	}
	return out
}

// usenetOn reports whether posts can be searched and played.
func (s *Service) usenetOn() bool {
	return s.usenetOff() == ""
}

// usenetOff says why Usenet isn't used, or "" when it is.
func (s *Service) usenetOff() string {
	c := s.cfg()
	if len(c.Indexers) == 0 && len(c.NewsServers) == 0 {
		return "not set up"
	}
	if len(s.indexers()) == 0 {
		for _, ix := range c.Indexers {
			if ix.Enabled && ix.APIKey == "" {
				return fmt.Sprintf("indexer %s has no API key", ix.Name)
			}
		}
		return "no indexer is turned on"
	}
	if s.newsPool().Empty() {
		return "no news server is turned on"
	}
	return ""
}

// searchIndexers asks every indexer for posts of a title.
func (s *Service) searchIndexers(ctx context.Context, q usenet.Query) []Release {
	ixs := s.indexers()
	ch := make(chan []Release, len(ixs))
	for _, ix := range ixs {
		go func(ix usenet.Indexer) {
			t := time.Now()
			res, err := ix.Search(ctx, q)
			if err != nil {
				slog.Warn("indexer search failed", "indexer", ix.Name, "err", err)
			} else {
				slog.Info("indexer search", "indexer", ix.Name, "title", q.Title, "posts", len(res), "took", time.Since(t).Round(time.Millisecond))
			}
			var rels []Release
			for _, r := range res {
				rels = append(rels, Release{Name: r.Title, Text: r.Title, NZB: r.NZB, Indexer: ix.Name, Usenet: true,
					FileIdx: -1, Size: r.Size, Seeders: r.Grabs, Addon: ix.Name})
			}
			ch <- rels
		}(ix)
	}
	var out []Release
	for range ixs {
		out = append(out, <-ch...)
	}
	return out
}

// usenetQuery is what indexers are asked for an item.
func (s *Service) usenetQuery(ctx context.Context, it *db.Item, imdb string, season, episode int) usenet.Query {
	q := usenet.Query{IMDB: imdb, Title: it.Title, Year: it.Year, Season: season, Episode: episode}
	if it.Kind == "episode" {
		if show, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
			q.Title, q.Year = show.Title, show.Year
		}
	}
	return q
}

// nzbPath is where a post's NZB is kept, so plays don't fetch it again.
func (s *Service) nzbPath(link string) string {
	h := sha1.Sum([]byte(link))
	return filepath.Join(s.Cfg.DataDir, "cache", "nzb", hex.EncodeToString(h[:])+".nzb")
}

// nzb returns a post's NZB: from memory, the disk, or the indexer.
func (s *Service) nzb(ctx context.Context, link string) (*usenet.NZB, error) {
	u := &s.usenet
	u.mu.Lock()
	if n := u.nzbs[link]; n != nil {
		u.mu.Unlock()
		return n, nil
	}
	u.mu.Unlock()
	path := s.nzbPath(link)
	data, err := os.ReadFile(path)
	if err != nil {
		var ix *usenet.Indexer
		for _, x := range s.indexers() {
			if x.Owns(link) {
				ix = &x
				break
			}
		}
		if ix == nil {
			return nil, errors.New("the indexer this post came from isn't set up any more")
		}
		if data, err = ix.FetchNZB(ctx, link); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			_ = os.WriteFile(path, data, 0o644)
		}
	}
	n, err := usenet.ParseNZB(data)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	u.mu.Lock()
	if u.nzbs == nil || len(u.nzbs) > 64 {
		u.nzbs = map[string]*usenet.NZB{}
	}
	u.nzbs[link] = n
	u.mu.Unlock()
	return n, nil
}

// addPost reads a post's files, picks the video and checks its articles
// are still on the news servers. tried is false when Usenet isn't set up.
func (s *Service) addPost(ctx context.Context, r Release, season, episode int, maxSize int64) (*pick, bool) {
	pool := s.newsPool()
	if pool.Empty() {
		return nil, false
	}
	if ctx.Value(urgentKey{}) == nil {
		ctx = usenet.Background(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t := time.Now()
	fail := func(step string, err error) (*pick, bool) {
		slog.Info("usenet post skipped", "release", r.Name, "step", step, "err", err, "took", time.Since(t).Round(time.Millisecond))
		return nil, true
	}
	nzb, err := s.nzb(ctx, r.NZB)
	if err != nil {
		return fail("nzb", err)
	}
	var keep func(string) bool
	if season > 0 {
		keep = func(name string) bool {
			s, e, ok := EpisodeOf(name)
			return !ok || (s == season && e == episode)
		}
	}
	if ok, err := usenet.Alive(ctx, pool, nzb, keep, 24); !ok {
		if err == nil {
			err = errors.New("articles missing from the news server")
		}
		return fail("check", err)
	}
	vids, err := usenet.Inspect(ctx, pool, nzb, keep)
	if err != nil {
		return fail("inspect", err)
	}
	files := make([]RemoteFile, len(vids))
	layouts := map[string]*usenet.Layout{}
	for i, v := range vids {
		files[i] = RemoteFile{ID: v.Name, Name: v.Name, Size: v.Size}
		layouts[v.Name] = v.Layout
	}
	f, ok := PickFile(files, "", season, episode)
	if !ok {
		return fail("pick", errors.New("no video for this title"))
	}
	if maxSize > 0 && f.Size > maxSize {
		return fail("pick", fmt.Errorf("%s is over the size limit", f.Name))
	}
	if ok, err := usenet.Available(ctx, pool, nzb, layouts[f.Name], 48); !ok {
		if err == nil {
			err = errors.New("articles missing from the news server")
		}
		return fail("check", err)
	}
	slog.Info("usenet post found", "release", r.Name, "file", f.Name, "size", f.Size, "took", time.Since(t).Round(time.Millisecond))
	return &pick{rel: r, tor: &Torrent{Files: files}, file: f, layouts: layouts}, true
}

// openPost opens a stored Usenet stream for reading.
func (s *Service) openPost(ctx context.Context, st *db.Stream) (*usenet.File, error) {
	var lay usenet.Layout
	if err := json.Unmarshal([]byte(st.ProviderRef), &lay); err != nil || len(lay.Pieces) == 0 {
		return nil, ErrUnavailable
	}
	pool := s.newsPool()
	if pool.Empty() {
		return nil, ErrDisabled
	}
	nzb, err := s.nzb(ctx, st.NZB)
	if err != nil {
		return nil, err
	}
	for _, fl := range lay.Files {
		if fl.Index >= len(nzb.Files) {
			return nil, ErrUnavailable
		}
	}
	return usenet.NewFile(pool, nzb, &lay), nil
}

// usenetSource plays a stored Usenet stream through the server.
type usenetSource struct {
	svc     *Service
	f       *usenet.File
	token   string
	name    string
	t       throttle
	release func()
	once    sync.Once
}

func (s *usenetSource) Input() string {
	return fmt.Sprintf("http://127.0.0.1:%d/internal/stream/%s", s.svc.HTTPPort, s.token)
}

func (s *usenetSource) Size() int64           { return s.f.Size() }
func (s *usenetSource) ReaderAt() io.ReaderAt { return s.f }

func (s *usenetSource) Close() error {
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

func (s *usenetSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	size := s.f.Size()
	start, end, ok := parseRange(r.Header.Get("Range"), size)
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	w.Header().Set("Content-Type", contentType(s.name))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	status := http.StatusOK
	if r.Header.Get("Range") != "" {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	t0 := time.Now()
	fw := &firstWrite{w: &throttledWriter{ctx: r.Context(), w: w, t: s.t}}
	n, err := s.f.WriteRange(r.Context(), fw, start, end-start+1)
	if err != nil && r.Context().Err() == nil {
		slog.Warn("usenet stream failed", "file", s.name, "offset", start, "err", err)
	}
	slog.Debug("usenet read", "offset", start, "bytes", n, "first", fw.at.Sub(t0).Round(time.Millisecond), "took", time.Since(t0).Round(time.Millisecond))
}

// firstWrite notes when the first bytes went out, for the debug log.
type firstWrite struct {
	w  io.Writer
	at time.Time
}

func (f *firstWrite) Write(p []byte) (int, error) {
	if f.at.IsZero() {
		f.at = time.Now()
	}
	return f.w.Write(p)
}

// parseRange reads a single "bytes=a-b" range; no header means everything.
func parseRange(h string, size int64) (start, end int64, ok bool) {
	if h == "" {
		return 0, size - 1, size > 0
	}
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	a, b, _ := strings.Cut(spec, "-")
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "":
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		start, end = max(size-n, 0), size-1
	default:
		var err error
		if start, err = strconv.ParseInt(a, 10, 64); err != nil {
			return 0, 0, false
		}
		end = size - 1
		if b != "" {
			if end, err = strconv.ParseInt(b, 10, 64); err != nil {
				return 0, 0, false
			}
			end = min(end, size-1)
		}
	}
	return start, end, start >= 0 && start <= end && start < size
}

type throttledWriter struct {
	ctx context.Context
	w   io.Writer
	t   throttle
}

func (tw *throttledWriter) Write(p []byte) (int, error) {
	if err := tw.t.wait(tw.ctx, len(p)); err != nil {
		return 0, err
	}
	return tw.w.Write(p)
}

// usenetPlay opens a Usenet stream for playback. Posts that lost articles
// since they were found are dropped after failing twice.
func (s *Service) usenetPlay(ctx context.Context, st *db.Stream, release func()) (*usenetSource, error) {
	f, err := s.openPost(ctx, st)
	if err == nil {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = f.ReadAtContext(cctx, make([]byte, 1), 0)
		cancel()
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Warn("usenet stream failed", "stream", st.ID, "file", st.FileName, "err", err)
		if errors.Is(err, usenet.ErrMissing) || errors.Is(err, ErrUnavailable) {
			if n, _ := s.DB.StreamFailed(ctx, st.ID); n >= 2 {
				_ = s.DB.DeleteStream(ctx, st.ID)
				_ = s.DB.PutStreamLookup(ctx, db.StreamLookup{ItemID: st.ItemID, LookedAt: 0})
			}
			return nil, ErrUnavailable
		}
		return nil, err
	}
	global, _ := s.limiters()
	src := &usenetSource{svc: s, f: f, token: randToken(), name: st.FileName, release: release,
		t: throttle{global, newLimiter(s.cfg().Limits.PerStreamMbps)}}
	s.mu.Lock()
	s.sources[src.token] = src
	s.mu.Unlock()
	return src, nil
}

// CheckUsenet logs in to news servers and tries each indexer, for the
// settings page (before they're saved).
func CheckUsenet(ctx context.Context, servers []config.NewsServerConfig, indexers []config.IndexerConfig) error {
	var ss []usenet.Server
	for _, ns := range servers {
		if ns.Enabled && ns.Host != "" {
			ss = append(ss, usenet.Server{Host: ns.Host, Port: ns.Port, TLS: ns.TLS, User: ns.Username, Pass: ns.Password, MaxConns: 1})
		}
	}
	if len(ss) == 0 {
		return errors.New("add a news server, such as TorBox's")
	}
	pool := usenet.NewPool(ss)
	defer pool.Close()
	if err := pool.Check(ctx); err != nil {
		return err
	}
	n := 0
	for _, ix := range indexers {
		if !ix.Enabled || ix.URL == "" {
			continue
		}
		n++
		if ix.APIKey == "" {
			return fmt.Errorf("%s: enter its API key", ix.Name)
		}
		if err := (usenet.Indexer{Name: ix.Name, URL: ix.URL, APIKey: ix.APIKey}).Check(ctx); err != nil {
			return err
		}
	}
	if n == 0 {
		return errors.New("add an indexer, such as NZBGeek")
	}
	return nil
}
