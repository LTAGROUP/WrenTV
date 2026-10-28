// Package streaming lets a server act as a shared Stremio-style client.
//
// Any movie or show can be searched and played. When a title is played the
// server asks its Stremio addons (Torrentio, Comet, …) for releases and its
// Newznab indexers for Usenet posts, keeps the torrents already cached on the
// owner's debrid accounts (TorBox, AllDebrid, Real-Debrid) and the posts
// still complete on the owner's news servers, and stores the best few per
// quality (2160p and 1080p, Usenet first) as streams users can choose
// between: just the info hash or NZB and the file, never the media. Bytes are
// proxied from the debrid CDN, or read from the news servers, on demand. After an episode plays, the rest of
// its season is looked up in the background; a season pack fills every
// episode at once. Streamed titles live in "streaming" libraries and give
// way to real files of the same title. Removing the debrid keys hides them.
//
// In download mode, played titles are also saved as files into a chosen
// library; the scanner picks them up and the file replaces the stream.
package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
	"github.com/LTAGROUP/wrentv/internal/stream"
	"github.com/LTAGROUP/wrentv/internal/usenet"
	"github.com/LTAGROUP/wrentv/internal/webhook"
)

var (
	ErrDisabled    = errors.New("streaming isn't set up on this server")
	ErrNoStreams   = errors.New("no stream was found for this title right now")
	ErrUnavailable = errors.New("this stream is no longer available")
	ErrBusy        = errors.New("the server is already playing as many streams as it allows")
)

type Deps struct {
	Cfg      *config.Config
	DB       *db.DB
	TMDB     *tmdb.Client
	Scanner  *library.Scanner
	Hub      *library.Hub
	Hooks    *webhook.Sender
	Server   func() webhook.Server
	HTTPPort int
}

type Service struct {
	Deps

	mu        sync.Mutex
	provKey   string
	provs     []Provider
	sources   map[string]http.Handler // open sources, served to ffmpeg
	streaming int                     // open debrid and Usenet sources
	usenet    usenetState
	links     map[string]cachedLink
	// pending holds where addon links led while a title was looked up, so
	// the first play doesn't open the link a second time.
	pending map[string]cachedLink
	// cdnFails counts plays in a row whose link the CDN wouldn't serve. It's
	// kept here: finding the torrent again resets the stream's own count.
	cdnFails  map[string]int
	lookups   map[string]*lookup
	refreshed map[string]time.Time
	limKey    string
	global    *rate.Limiter
	dlLimit   *rate.Limiter

	prefetchQ chan string
	queued    map[string]bool

	dlKick    chan struct{}
	dlRunning map[string]*runningDownload

	testProviders []Provider // tests replace the debrid services
}

type cachedLink struct {
	url      string
	at       time.Time
	provider string
	// checked is when the CDN last answered for url; opening the item
	// warms the link, so play right after needn't ask again.
	checked time.Time
}

type lookup struct {
	done  chan struct{}
	ready chan struct{} // every quality has a stream; more may follow
	found int
	err   error
	// urgent is a lookup someone is waiting to play; see resolve.
	urgent bool
}

func New(d Deps) *Service {
	return &Service{
		Deps: d, sources: map[string]http.Handler{}, links: map[string]cachedLink{}, pending: map[string]cachedLink{}, cdnFails: map[string]int{}, lookups: map[string]*lookup{},
		refreshed: map[string]time.Time{}, prefetchQ: make(chan string, 1000), queued: map[string]bool{},
		dlKick: make(chan struct{}, 1), dlRunning: map[string]*runningDownload{},
	}
}

func (s *Service) cfg() *config.StreamingConfig { return &s.Cfg.Streaming }

// Start runs the prefetch workers and the download queue.
func (s *Service) Start(ctx context.Context) {
	for i := 0; i < 8; i++ {
		go s.prefetchWorker(ctx, i)
	}
	go s.downloadLoop(ctx)
	go s.watchlistLoop(ctx)
	if s.Active() {
		_, _ = s.Libraries(ctx)
	}
}

// ---------- configuration ----------

func (s *Service) keys() map[string]string {
	c := s.cfg()
	return map[string]string{"torbox": c.TorBoxKey, "alldebrid": c.AllDebridKey, "realdebrid": c.RealDebridKey}
}

// Providers returns the configured debrid providers, rebuilt when keys change.
func (s *Service) Providers() []Provider {
	c := s.cfg()
	key := strings.Join(c.ProviderOrder, ",") + "|" + c.TorBoxKey + "|" + c.AllDebridKey + "|" + c.RealDebridKey
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.testProviders != nil {
		return s.testProviders
	}
	if key != s.provKey {
		s.provKey = key
		s.provs = NewProviders(c.ProviderOrder, s.keys())
		s.links = map[string]cachedLink{}
	}
	return s.provs
}

func (s *Service) provider(name string) Provider {
	for _, p := range s.Providers() {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// Active reports whether streaming can be used: enabled, TMDB, and
// something to play from: a debrid key, Usenet, or an addon. Addons like
// StreamNZB serve their own play links, with no debrid account.
func (s *Service) Active() bool {
	return s.cfg().Enabled && (len(s.Providers()) > 0 || s.usenetOn() || len(s.addons()) > 0) && s.TMDB.Enabled()
}

func (s *Service) addons() []Addon {
	var out []Addon
	for _, a := range s.cfg().Addons {
		if !a.Enabled {
			continue
		}
		if ad, err := ParseAddon(a.Name, a.URL); err == nil {
			out = append(out, ad)
		}
	}
	return out
}

// readyGrace is how long playback waits for the best quality after a lower
// one is found.
const readyGrace = 8 * time.Second

// orderWait is how long a found Usenet post waits for better-ranked posts
// still being checked before it's stored ahead of them.
const orderWait = 3 * time.Second

// torrentAfter is how long torrents wait for Usenet posts that have found
// nothing yet before they're tried alongside.
var torrentAfter = 10 * time.Second

// postSpare is how many more posts than wanted are tried per quality.
// New releases lose most of their posts to takedowns within days; those
// fail in a second or two, so a few dead ones at the top shouldn't use
// up the tries before a live one is reached (postTime still bounds it).
const postSpare = 10

// newestToo alternates ranked posts with the newest ones left. Popular
// titles are taken down within days, so the best-ranked posts are often
// gone while a lower-ranked one posted since still plays.
func newestToo(ranked []Release) []Release {
	byAge := make([]int, len(ranked))
	for i := range byAge {
		byAge[i] = i
	}
	slices.SortStableFunc(byAge, func(a, b int) int { return ranked[b].Posted.Compare(ranked[a].Posted) })
	out := make([]Release, 0, len(ranked))
	used := make([]bool, len(ranked))
	take := func(i int) {
		used[i] = true
		out = append(out, ranked[i])
	}
	ri, ai := 0, 0
	for len(out) < len(ranked) {
		if len(out)%2 == 1 {
			for ai < len(byAge) && (used[byAge[ai]] || ranked[byAge[ai]].Posted.IsZero()) {
				ai++
			}
			if ai < len(byAge) {
				take(byAge[ai])
				continue
			}
		}
		for used[ri] {
			ri++
		}
		take(ri)
	}
	return out
}

// packsFirst is newestToo with the season packs Rank put on top kept
// ahead of the rest, so they're tried before newer single episodes
// (packs ranked lower, such as dubs, stay mixed in).
func packsFirst(ranked []Release, season int) []Release {
	n := 0
	for n < len(ranked) && isPack(ranked[n].Name, season) {
		n++
	}
	return append(newestToo(ranked[:n:n]), newestToo(ranked[n:])...)
}

// postsAtOnce is how many posts a quality reads at once; while someone
// waits to play, the best quality's go ahead of read-ahead. Popular
// titles' top posts are mostly taken down, and each dead one takes
// seconds to rule out, so they race rather than queue. Each check takes
// only a few connections.
const postsAtOnce = 6

// nzbsAhead is how many of a quality's posts have their NZBs fetched
// ahead of being read, a few at a time, as indexers take a second or two
// to send each.
const nzbsAhead, nzbFetches = 12, 4

// urgentKey marks a post someone is waiting on: it isn't held to the
// background share of connections.
type urgentKey struct{}

// standInRank is added to the rank of a torrent stored while Usenet posts
// are still being read, so the posts it stood in for play first.
const standInRank = 100

func (s *Service) qualities() []string {
	q := s.cfg().Qualities
	if len(q) == 0 {
		return []string{"2160p", "1080p"}
	}
	return q
}

// perQuality is how many streams each quality keeps.
func (s *Service) perQuality() int { return max(s.cfg().PerQuality, 1) }

func (s *Service) maxSize(q string) int64 {
	return int64(s.cfg().MaxSizeGB[q] * (1 << 30))
}

// limiters returns the shared bandwidth limiters, rebuilt when limits change.
func (s *Service) limiters() (global, downloads *rate.Limiter) {
	l := s.cfg().Limits
	key := fmt.Sprint(l.BandwidthMbps, l.DownloadMbps)
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != s.limKey {
		s.limKey = key
		s.global, s.dlLimit = newLimiter(l.BandwidthMbps), newLimiter(l.DownloadMbps)
	}
	return s.global, s.dlLimit
}

// Libraries returns the streaming libraries (created when streaming is active).
func (s *Service) Libraries(ctx context.Context) ([]*db.Library, error) {
	var out []*db.Library
	for _, typ := range []string{"movies", "shows"} {
		l, err := s.DB.StreamLibrary(ctx, typ)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// ---------- titles ----------

// AddTitle returns the streaming library item for a TMDB movie or show,
// creating it the first time. A new show's aired seasons and episodes are
// added in the background.
func (s *Service) AddTitle(ctx context.Context, kind string, tmdbID int) (*db.Item, error) {
	if !s.Active() {
		return nil, ErrDisabled
	}
	typ := map[string]string{"movie": "movies", "show": "shows"}[kind]
	if typ == "" || tmdbID <= 0 {
		return nil, fmt.Errorf("unknown title")
	}
	lib, err := s.DB.StreamLibrary(ctx, typ)
	if err != nil {
		return nil, err
	}
	if it, err := s.DB.FindStreamTitle(ctx, lib.ID, kind, tmdbID); err == nil {
		if kind == "show" {
			s.MaybeRefresh(it)
		}
		return it, nil
	}
	it := &db.Item{LibraryID: lib.ID, Kind: kind, MatchKey: fmt.Sprintf("tmdb:%d", tmdbID), SeasonNumber: -1, EpisodeNumber: -1}
	// Someone is waiting on this, so a slow step is worth a line in the log.
	start := time.Now()
	var fetched time.Duration
	defer func() {
		if took := time.Since(start); took > 3*time.Second {
			slog.Warn("slow title add", "kind", kind, "tmdb", tmdbID, "ms", took.Milliseconds(),
				"tmdb_ms", fetched.Milliseconds(), "db_ms", (took - fetched).Milliseconds())
		}
	}()
	var show *tmdb.Show
	switch kind {
	case "movie":
		m, err := s.TMDB.Movie(ctx, tmdbID)
		if err != nil {
			return nil, err
		}
		library.ApplyMovie(it, m)
	case "show":
		if show, err = s.TMDB.Show(ctx, tmdbID); err != nil {
			return nil, err
		}
		library.ApplyShow(it, show)
	}
	fetched = time.Since(start)
	it.SortTitle = ""
	if err := s.DB.InsertItem(ctx, it); err != nil {
		// Someone else added it at the same moment.
		if existing, ferr := s.DB.FindStreamTitle(ctx, lib.ID, kind, tmdbID); ferr == nil {
			return existing, nil
		}
		return nil, err
	}
	if show != nil {
		s.mu.Lock()
		s.refreshed[it.ID] = time.Now()
		s.mu.Unlock()
		// A long-running show has a season request each, so the page opens
		// now and the seasons follow (item.updated) once they're in.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			s.syncEpisodes(ctx, it, show)
			// Simkl marks for its episodes can only land once they exist.
			if err := s.DB.ApplyExternalWatchedFor(ctx, "show", tmdbID); err != nil {
				slog.Warn("apply watched marks", "show", tmdbID, "err", err)
			}
			if err := s.DB.ApplyExternalProgressFor(ctx, "show", tmdbID); err != nil {
				slog.Warn("apply paused", "show", tmdbID, "err", err)
			}
		}()
	} else {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := s.DB.ApplyExternalWatchedFor(ctx, "movie", tmdbID); err != nil {
				slog.Warn("apply watched marks", "movie", tmdbID, "err", err)
			}
			if err := s.DB.ApplyExternalProgressFor(ctx, "movie", tmdbID); err != nil {
				slog.Warn("apply paused", "movie", tmdbID, "err", err)
			}
		}()
	}
	s.Hub.Publish(library.Event{Type: "item.added", LibraryID: lib.ID, ItemID: it.ID})
	return it, nil
}

// MaybeRefresh adds newly aired episodes to a streamed show (at most twice a day).
func (s *Service) MaybeRefresh(show *db.Item) {
	if show.Kind != "show" || !s.TMDB.Enabled() {
		return
	}
	s.mu.Lock()
	last, ok := s.refreshed[show.ID]
	if ok && time.Since(last) < 12*time.Hour {
		s.mu.Unlock()
		return
	}
	s.refreshed[show.ID] = time.Now()
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		sh, err := s.TMDB.Show(ctx, show.TMDBID)
		if err != nil {
			return
		}
		s.syncEpisodes(ctx, show, sh)
	}()
}

// syncEpisodes creates the aired seasons and episodes of a show.
func (s *Service) syncEpisodes(ctx context.Context, show *db.Item, sh *tmdb.Show) {
	today := time.Now().Format("2006-01-02")
	aired := func(d string) bool { return d != "" && d <= today }
	type fetched struct {
		n    int
		data *tmdb.Season
	}
	var nums []int
	for _, se := range sh.Seasons {
		if se.SeasonNumber > 0 && aired(se.AirDate) {
			nums = append(nums, se.SeasonNumber)
		}
	}
	results := make([]fetched, len(nums))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, n := range nums {
		wg.Add(1)
		go func(i, n int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data, err := s.TMDB.Season(ctx, sh.ID, n)
			if err == nil {
				results[i] = fetched{n, data}
			}
		}(i, n)
	}
	wg.Wait()
	added := false
	for _, r := range results {
		if r.data == nil {
			continue
		}
		season, err := s.DB.FindChild(ctx, show.ID, "season", r.n)
		if errors.Is(err, db.ErrNotFound) {
			season = &db.Item{LibraryID: show.LibraryID, Kind: "season", ParentID: show.ID, ShowID: show.ID, Title: r.data.Name,
				SortTitle: fmt.Sprintf("%04d", r.n), SeasonNumber: r.n, EpisodeNumber: -1, Overview: r.data.Overview,
				PosterPath: r.data.PosterPath, AirDate: r.data.AirDate, MatchState: "matched"}
			if season.Title == "" {
				season.Title = fmt.Sprintf("Season %d", r.n)
			}
			if len(r.data.AirDate) >= 4 {
				fmt.Sscan(r.data.AirDate[:4], &season.Year)
			}
			if err := s.DB.InsertItem(ctx, season); err != nil {
				continue
			}
		} else if err != nil {
			continue
		}
		var eps []*db.Item
		for _, e := range r.data.Episodes {
			if !aired(e.AirDate) {
				continue
			}
			if _, err := s.DB.FindChild(ctx, season.ID, "episode", e.EpisodeNumber); err == nil {
				continue
			}
			ep := &db.Item{LibraryID: show.LibraryID, Kind: "episode", ParentID: season.ID, ShowID: show.ID,
				Title: fmt.Sprintf("Episode %d", e.EpisodeNumber), SortTitle: fmt.Sprintf("%04d%04d", r.n, e.EpisodeNumber),
				SeasonNumber: r.n, EpisodeNumber: e.EpisodeNumber}
			library.ApplyEpisode(ep, e)
			eps = append(eps, ep)
		}
		if err := s.DB.InsertItems(ctx, eps); err != nil {
			slog.Warn("add episodes", "show", show.ID, "season", r.n, "err", err)
		} else if len(eps) > 0 {
			added = true
		}
	}
	if added {
		_, _ = s.DB.ExecContext(ctx, `UPDATE items SET updated_at=? WHERE id=?`, db.Now(), show.ID)
		s.Hub.Publish(library.Event{Type: "item.updated", LibraryID: show.LibraryID, ItemID: show.ID})
	}
}

// ---------- lookups ----------

// Ensure makes sure a streaming movie or episode has streams, looking them
// up if needed. It waits up to wait; ready=false means the lookup is still
// running. ErrNoStreams means nothing cached was found. A lookup is ready
// once the best quality has its first stream; the other choices keep
// coming in the background.
func (s *Service) Ensure(ctx context.Context, it *db.Item, wait time.Duration) (ready bool, err error) {
	if !s.Active() {
		return false, ErrDisabled
	}
	if sts, _ := s.DB.StreamsForItem(ctx, it.ID); len(sts) > 0 {
		if len(sts) < len(s.qualities())*s.perQuality() {
			s.topUp(it)
		}
		return true, nil
	}
	s.mu.Lock()
	lk := s.lookups[it.ID]
	if lk == nil {
		// A miss is remembered for a while so repeated plays don't hammer addons.
		if l, err := s.DB.GetStreamLookup(ctx, it.ID); err == nil && l.Found == 0 && time.Since(time.Unix(l.LookedAt, 0)) < 10*time.Minute {
			s.mu.Unlock()
			return false, ErrNoStreams
		}
		lk = s.startLookup(it, true)
	}
	s.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-lk.ready:
		return true, nil
	case <-lk.done:
		if lk.err != nil {
			return false, lk.err
		}
		if lk.found == 0 {
			return false, ErrNoStreams
		}
		return true, nil
	case <-t.C:
		return false, nil
	case <-ctx.Done():
		return false, nil
	}
}

// topUp looks for more choices for an item that has fewer than it keeps,
// as when a stream was dropped. Recent lookups aren't repeated.
func (s *Service) topUp(it *db.Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookups[it.ID] != nil {
		return
	}
	if l, err := s.DB.GetStreamLookup(context.Background(), it.ID); err == nil && time.Since(time.Unix(l.LookedAt, 0)) < 6*time.Hour {
		return
	}
	s.startLookup(it, false)
}

// startLookup runs a lookup in the background. Callers hold s.mu.
func (s *Service) startLookup(it *db.Item, urgent bool) *lookup {
	lk := &lookup{done: make(chan struct{}), ready: make(chan struct{}), urgent: urgent}
	s.lookups[it.ID] = lk
	go s.runLookup(it, lk)
	return lk
}

func (s *Service) runLookup(it *db.Item, lk *lookup) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var once sync.Once
	lk.found, lk.err = s.resolve(ctx, it, lk.urgent, func() {
		once.Do(func() {
			close(lk.ready)
			s.Hub.Publish(library.Event{Type: "stream.updated", LibraryID: it.LibraryID, ItemID: it.ID})
		})
	})
	close(lk.done)
	s.mu.Lock()
	delete(s.lookups, it.ID)
	s.mu.Unlock()
	if lk.found > 0 {
		s.Hub.Publish(library.Event{Type: "stream.updated", LibraryID: it.LibraryID, ItemID: it.ID})
	} else if lk.urgent {
		s.notify("stream.unavailable", map[string]any{"itemId": it.ID, "title": it.Title, "error": errString(lk.err)})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// target is what addons are asked for: the IMDb id, plus season/episode.
func (s *Service) target(ctx context.Context, it *db.Item) (imdb string, season, episode int, err error) {
	switch it.Kind {
	case "movie":
		imdb = it.IMDBID
	case "episode":
		show, err := s.DB.GetItem(ctx, it.ShowID)
		if err != nil {
			return "", 0, 0, err
		}
		imdb, season, episode = show.IMDBID, it.SeasonNumber, it.EpisodeNumber
	default:
		return "", 0, 0, fmt.Errorf("only movies and episodes have streams")
	}
	if imdb == "" {
		return "", 0, 0, errors.New("TMDB has no IMDb id for this title, which addons need")
	}
	return imdb, season, episode, nil
}

type pick struct {
	rel  Release
	prov Provider
	tor  *Torrent
	file RemoteFile
	// layouts says where each video of a Usenet post is, by file name.
	layouts map[string]*usenet.Layout
}

// usenetProvider is the debrid service that serves Usenet, when Usenet is on.
func (s *Service) usenetProvider() UsenetProvider {
	if !s.cfg().Usenet {
		return nil
	}
	for _, p := range s.Providers() {
		if u, ok := p.(UsenetProvider); ok {
			return u
		}
	}
	return nil
}

// search asks the addons for streams and the indexers for Usenet posts.
// When Usenet is preferred its posts come back apart, to be tried first.
func (s *Service) search(ctx context.Context, q usenet.Query) (torrents, posts []Release, err error) {
	var all, found []Release
	var wg sync.WaitGroup
	if why := s.usenetOff(); why != "" && why != "not set up" {
		slog.Info("usenet skipped", "title", q.Title, "why", why)
	}
	if s.usenetOn() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found = s.searchIndexers(ctx, q)
		}()
	}
	if addons := s.addons(); len(addons) > 0 {
		all, err = Search(ctx, addons, q.IMDB, q.Season, q.Episode)
	}
	wg.Wait()
	all = append(all, found...)
	if len(found) > 0 && len(all) == len(found) {
		err = nil
	}
	if !s.cfg().Usenet {
		return all, nil, err
	}
	for _, r := range all {
		if r.Usenet {
			posts = append(posts, r)
		} else {
			torrents = append(torrents, r)
		}
	}
	return torrents, posts, err
}

// releaseKey tells releases apart: by torrent when known, else by link or NZB.
func releaseKey(r Release) string {
	switch {
	case r.InfoHash != "":
		return r.InfoHash
	case r.Link != "":
		return "link:" + r.Link
	case r.NZB != "":
		return "nzb:" + r.NZB
	}
	return ""
}

// cacheState is what one provider said about the torrents of a lookup.
type cacheState struct {
	known  bool // false: the provider can't tell, so adding is the test
	cached map[string]bool
	busy   atomic.Bool // rate limited: skip it for this lookup
}

// checkCached asks each provider in one go which of the top torrents of
// every quality it has cached.
func (s *Service) checkCached(ctx context.Context, rels []Release, qualities []string, season int) map[string]*cacheState {
	var hashes []string
	seen := map[string]bool{}
	for _, q := range qualities {
		cands := s.rank(rels, q, s.maxSize(q), season)
		for _, r := range cands[:min(len(cands), 40)] {
			if r.InfoHash != "" && r.Link == "" && !seen[r.InfoHash] {
				seen[r.InfoHash] = true
				hashes = append(hashes, r.InfoHash)
			}
		}
	}
	out := map[string]*cacheState{}
	for _, p := range s.Providers() {
		cs := &cacheState{known: true, cached: map[string]bool{}}
		out[p.Name()] = cs
		if len(hashes) == 0 || ctx.Err() != nil {
			continue
		}
		res, known, err := p.Check(ctx, hashes)
		if err != nil {
			slog.Debug("cache check", "provider", p.Name(), "err", err)
			cs.busy.Store(errors.Is(err, ErrRateLimited))
		}
		cs.known = known
		for h, ok := range res {
			cs.cached[h] = ok
		}
	}
	return out
}

// resolve looks up and stores the missing streams of an item: up to
// perQuality per quality, Usenet first, then torrents, each in rank order.
// ready is called once the best quality has a stream, or once it can't be
// found and another quality has one (see readyGrace). It returns how many streams the item has.
func (s *Service) resolve(ctx context.Context, it *db.Item, urgent bool, ready func()) (int, error) {
	per := s.perQuality()
	sts, _ := s.DB.StreamsForItem(ctx, it.ID)
	have := map[string][]*db.Stream{}
	for _, st := range sts {
		have[st.Quality] = append(have[st.Quality], st)
	}
	var missing []string
	for _, q := range s.qualities() {
		if len(have[q]) < per {
			missing = append(missing, q)
		}
	}
	if len(missing) == 0 {
		return len(sts), nil
	}
	imdb, season, episode, err := s.target(ctx, it)
	if err != nil {
		s.recordLookup(ctx, it.ID, len(sts), err)
		return len(sts), err
	}
	rels, nzbs, err := s.search(ctx, s.usenetQuery(ctx, it, imdb, season, episode))
	if err != nil && len(rels) == 0 {
		s.recordLookup(ctx, it.ID, len(sts), err)
		return len(sts), err
	}
	if season == 0 {
		rels, nzbs = bigEnough(rels, it.RuntimeMin), bigEnough(nzbs, it.RuntimeMin)
	}
	caches := s.checkCached(ctx, rels, missing, season)

	started := time.Now()
	// Playback starts on the first stream of the best quality wanted, or
	// on any stream once that one gives up or a little after something
	// else is found; the rest keep coming in the background.
	best := missing[0]
	for _, q := range missing {
		if HeightOf(q) > HeightOf(best) {
			best = q
		}
	}
	// Someone waiting to play gets the best quality's top posts first,
	// one at a time and with every connection; the other qualities start
	// once playback can (or the best one has run out of posts).
	urgent = urgent && ready != nil && len(have[best]) == 0
	lead := make(chan struct{})
	var leadOnce sync.Once
	unlead := func() { leadOnce.Do(func() { close(lead) }) }
	if !urgent {
		unlead()
	}
	var mu sync.Mutex
	var readyOnce sync.Once
	found := len(sts)
	fire := func() {
		unlead()
		if ready != nil {
			readyOnce.Do(ready)
		}
	}
	settle := func(q string, got bool) {
		mu.Lock()
		any := found > 0
		mu.Unlock()
		switch {
		case q == best && any:
			fire()
		case got:
			time.AfterFunc(readyGrace, fire)
		}
	}
	var wg sync.WaitGroup
	for _, q := range missing {
		wg.Add(1)
		go func(q string) {
			defer wg.Done()
			if q != best {
				select {
				case <-lead:
				case <-ctx.Done():
				}
			}
			settled := false
			defer func() {
				if !settled {
					settle(q, false)
				}
			}()
			maxSize := s.maxSize(q)
			rank, skip := 0, map[string]bool{"": true}
			for _, st := range have[q] {
				rank = max(rank, st.Rank+1)
				skip[st.InfoHash], skip[sameRelease(st.Release)] = true, true
				if st.Link != "" {
					skip["link:"+st.Link] = true
				}
				if st.NZB != "" {
					skip["nzb:"+st.NZB] = true
				}
			}
			need := per - len(have[q])
			// keep stores a stream that was found; qmu is held. A stand-in
			// is a torrent found while posts are still being read: it's
			// ranked after anything they find and doesn't take their place.
			var qmu sync.Mutex
			keep := func(r Release, p *pick, standIn bool) {
				at := rank
				if standIn {
					at = rank + standInRank
				}
				if err := s.store(ctx, it, q, at, p); err != nil {
					slog.Warn("store stream", "item", it.ID, "err", err)
					return
				}
				skip[releaseKey(r)], skip[sameRelease(r.Name)] = true, true
				if !standIn {
					rank++
					need--
				}
				mu.Lock()
				found++
				mu.Unlock()
				s.Hub.Publish(library.Event{Type: "item.updated", LibraryID: it.LibraryID, ItemID: it.ID})
				if !settled {
					settled = true
					settle(q, true)
				}
				if season > 0 {
					s.fillSeason(ctx, it, q, p) // a season pack covers the other episodes too
				}
			}
			seen := func(r Release) bool {
				return skip[releaseKey(r)] || (sameRelease(r.Name) != "" && skip[sameRelease(r.Name)])
			}

			// Usenet posts go first, as many at once as are still wanted:
			// a post is only known to play once its files are read, which
			// takes seconds. Found ones are stored in rank order. Posts get
			// their own small budget so torrents are still tried when they
			// fail.
			var pwg sync.WaitGroup
			solo := urgent && q == best
			slots := make(chan struct{}, max(need, postsAtOnce))
			var tried []Release
			var picks []*pick
			var done, stored []bool
			next := 0
			postsDone := make(chan struct{})
			go func() {
				defer close(postsDone)
				if q == best {
					defer unlead()
				}
				order := packsFirst(s.rank(nzbs, q, maxSize, season), season)
				go s.prefetchNZBs(ctx, order[:min(len(order), nzbsAhead)], season, episode)
				for _, r := range order {
					slots <- struct{}{}
					qmu.Lock()
					stop := need <= 0 || ctx.Err() != nil || len(tried) >= need+postSpare || time.Since(started) > postTime
					// Posts already found with articles gone are passed over
					// without using up a try.
					dup := !stop && (seen(r) || s.usenet.wasMissing(r, season, episode))
					k := len(tried)
					rush := solo && k < postsAtOnce && !settled
					if !stop && !dup {
						tried, picks, done, stored = append(tried, r), append(picks, nil), append(done, false), append(stored, false)
					}
					qmu.Unlock()
					if stop || dup {
						<-slots
						if stop {
							break
						}
						continue
					}
					pwg.Add(1)
					go func(k int, r Release) {
						defer pwg.Done()
						defer func() { <-slots }()
						actx := ctx
						if rush {
							actx = context.WithValue(ctx, urgentKey{}, true)
						}
						p, _ := s.add(actx, r, caches, season, episode, maxSize)
						qmu.Lock()
						defer qmu.Unlock()
						picks[k], done[k] = p, true
						for ; next < len(done) && done[next]; next++ {
							if p := picks[next]; p != nil && !stored[next] && need > 0 && !seen(tried[next]) {
								stored[next] = true
								keep(tried[next], p, false)
							}
						}
						// A find waiting on a slow post ranked above it is stored
						// after a moment anyway, so playback isn't held up; at
						// once while someone waits on a quality with none yet,
						// unless what's above it is a season pack, which is
						// worth that moment.
						packAbove := false
						for i := next; i < k && !packAbove; i++ {
							packAbove = !done[i] && isPack(tried[i].Name, season)
						}
						if p != nil && k >= next && solo && !settled && need > 0 && !seen(tried[k]) && !packAbove {
							stored[k] = true
							keep(tried[k], p, false)
						} else if p != nil && k >= next {
							time.AfterFunc(orderWait, func() {
								qmu.Lock()
								defer qmu.Unlock()
								if !stored[k] && need > 0 && ctx.Err() == nil && !seen(tried[k]) {
									stored[k] = true
									keep(tried[k], p, false)
								}
							})
						}
					}(k, r)
				}
				pwg.Wait()
			}()
			// Torrents wait for the posts, unless the posts have found
			// nothing for a while: then both are tried at once, so the
			// item doesn't sit with no versions.
			select {
			case <-postsDone:
			case <-time.After(torrentAfter):
				qmu.Lock()
				some := settled
				qmu.Unlock()
				if some {
					<-postsDone
				}
			}
			defer func() { <-postsDone }()

			tries, standIn, counted := 0, false, false
			for _, r := range s.rank(rels, q, maxSize, season) {
				if standIn && !counted {
					// One stand-in is enough; the posts may fill the rest.
					// What they leave, it takes a place of.
					<-postsDone
					qmu.Lock()
					need--
					qmu.Unlock()
					counted = true
				}
				qmu.Lock()
				enough := need <= 0 || tries >= need*3+3
				qmu.Unlock()
				if enough || ctx.Err() != nil {
					break
				}
				qmu.Lock()
				dup := seen(r)
				qmu.Unlock()
				if dup {
					continue
				}
				p, tried := s.add(ctx, r, caches, season, episode, maxSize)
				if tried {
					tries++
				}
				early := false
				select {
				case <-postsDone:
				default:
					early = true
				}
				qmu.Lock()
				if p != nil && need > 0 && !seen(r) {
					keep(r, p, early)
					standIn = standIn || early
				}
				qmu.Unlock()
			}
		}(q)
	}
	wg.Wait()
	slog.Info("stream lookup", "title", it.Title, "season", season, "episode", episode, "releases", len(rels), "usenet", len(nzbs), "streams", found)
	s.recordLookup(ctx, it.ID, found, nil)
	return found, nil
}

func (s *Service) recordLookup(ctx context.Context, itemID string, found int, err error) {
	_ = s.DB.PutStreamLookup(ctx, db.StreamLookup{ItemID: itemID, LookedAt: db.Now(), Found: found, Error: errString(err)})
}

// add puts a cached release on a debrid account and finds its video.
// tried reports whether a provider was asked to add it.
func (s *Service) add(ctx context.Context, r Release, caches map[string]*cacheState, season, episode int, maxSize int64) (*pick, bool) {
	fits := func(tor *Torrent) (RemoteFile, bool) {
		f, ok := PickFile(tor.Files, r.FileName, season, episode)
		return f, ok && (maxSize <= 0 || f.Size <= maxSize)
	}
	if r.Indexer == "" && s.usenet.isDead(r) {
		return nil, true
	}
	if r.Link != "" {
		// An addon that streams the file itself (StreamNZB) starts a stream
		// for every link opened, and a lookup keeps several versions, so
		// when it says how big the file is, the link waits until it's
		// played; a dead one is found then and another version plays.
		// Debrid links are opened now: that adds the release to the
		// account, and finds the ones it can't get.
		// With CheckAddonLinks on, every link is opened now.
		if !r.Debrid && r.Size > 0 && !s.cfg().CheckAddonLinks {
			if r.Size < 50<<20 || maxSize > 0 && r.Size > maxSize {
				return nil, true
			}
			f := RemoteFile{Name: linkFileName(r.FileName, ""), Size: r.Size}
			return &pick{rel: r, tor: &Torrent{}, file: f}, true
		}
		target, size, err := openLink(ctx, r.Link)
		if err != nil {
			slog.Debug("addon link", "release", r.Name, "addon", r.Addon, "err", err)
			return nil, true
		}
		if maxSize > 0 && size > maxSize {
			return nil, true
		}
		s.mu.Lock()
		for k, c := range s.pending {
			if time.Since(c.at) > 10*time.Minute {
				delete(s.pending, k)
			}
		}
		s.pending[r.Link] = cachedLink{url: target, at: time.Now()}
		s.mu.Unlock()
		f := RemoteFile{Name: linkFileName(r.FileName, target), Size: size}
		return &pick{rel: r, tor: &Torrent{}, file: f}, true
	}
	if r.Indexer != "" {
		return s.addPost(ctx, r, season, episode, maxSize)
	}
	if r.NZB != "" {
		u := s.usenetProvider()
		if u == nil {
			return nil, false
		}
		tor, err := u.AddUsenet(ctx, r.InfoHash, r.NZB, r.Name)
		if err != nil {
			slog.Debug("usenet add", "release", r.Name, "err", err)
			return nil, true
		}
		if f, ok := fits(tor); ok {
			return &pick{rel: r, prov: u, tor: tor, file: f}, true
		}
		return nil, true
	}
	tried := false
	for _, p := range s.Providers() {
		cs := caches[p.Name()]
		if cs == nil || cs.busy.Load() || (cs.known && !cs.cached[r.InfoHash]) {
			continue
		}
		tried = true
		tor, err := p.Add(ctx, r.InfoHash, r.Magnet())
		if err != nil {
			if errors.Is(err, ErrRateLimited) {
				cs.busy.Store(true)
			}
			continue
		}
		if f, ok := fits(tor); ok {
			return &pick{rel: r, prov: p, tor: tor, file: f}, true
		}
	}
	return nil, tried
}

func (s *Service) store(ctx context.Context, it *db.Item, q string, rank int, p *pick) error {
	st := &db.Stream{ItemID: it.ID, Quality: q, Rank: rank, Kind: "torrent", InfoHash: p.rel.InfoHash, NZB: p.rel.NZB, Link: p.rel.Link,
		FileName: p.file.Name, Size: p.file.Size, Release: p.rel.Name, Addon: p.rel.Addon, ProviderRef: p.tor.Ref,
		ProviderFile: p.file.ID, FileIdx: p.rel.FileIdx}
	if p.prov != nil {
		st.Provider = p.prov.Name()
	}
	if p.rel.NZB != "" || p.rel.Usenet {
		st.Kind = "usenet"
	}
	if lay := p.layouts[p.file.Name]; lay != nil {
		b, err := json.Marshal(lay)
		if err != nil {
			return err
		}
		st.Provider, st.ProviderRef, st.ProviderFile = usenetProvider, string(b), ""
	}
	// Codec and HDR are guessed from the names until the first play probes it.
	f := &db.MediaFile{ItemID: it.ID, LibraryID: it.LibraryID, Path: p.file.Name, Size: p.file.Size, Tag: ReleaseTag(st.Kind, p.rel.Name)}
	library.GuessFromName(f)
	if f.HDR == "" {
		g := &db.MediaFile{Path: p.rel.Name + ".mkv"}
		library.GuessFromName(g)
		f.HDR = g.HDR
		if f.VideoCodec == "" {
			f.VideoCodec = g.VideoCodec
		}
	}
	h := HeightOf(q)
	f.Height, f.Width = h, map[int]int{2160: 3840, 1080: 1920, 720: 1280}[h]
	return s.DB.PutStream(ctx, st, f)
}

// fillSeason stores a season pack's other episodes at the same quality,
// after the choices those episodes already have.
func (s *Service) fillSeason(ctx context.Context, it *db.Item, q string, p *pick) {
	byEp := EpisodeFiles(p.tor.Files, it.SeasonNumber)
	if len(byEp) < 2 {
		return
	}
	eps, _, err := s.DB.QueryItems(ctx, db.ItemQuery{ParentID: it.ParentID, Kind: "episode"})
	if err != nil {
		return
	}
	per := s.perQuality()
	n := 0
	for _, ep := range eps {
		f, ok := byEp[ep.EpisodeNumber]
		if !ok || ep.ID == it.ID {
			continue
		}
		sts, _ := s.DB.StreamsForItem(ctx, ep.ID)
		rank, count, dup := 0, 0, false
		for _, st := range sts {
			if st.Quality == q {
				count++
				rank = max(rank, st.Rank+1)
				dup = dup || st.InfoHash == p.rel.InfoHash
			}
		}
		if dup || count >= per {
			continue
		}
		if m := s.maxSize(q); m > 0 && f.Size > m {
			continue
		}
		pp := *p
		pp.file = f
		if s.store(ctx, ep, q, rank, &pp) == nil {
			n++
		}
	}
	if n > 0 {
		slog.Info("season pack", "release", p.rel.Name, "quality", q, "episodes", n)
	}
}

// ---------- prefetch ----------

// Prefetch queues the rest of an episode's season for lookup.
func (s *Service) Prefetch(it *db.Item) {
	if it.Kind != "episode" || !s.cfg().Prefetch || !s.Active() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		eps, _, err := s.DB.QueryItems(ctx, db.ItemQuery{ParentID: it.ParentID, Kind: "episode", Sort: "episode"})
		if err != nil {
			return
		}
		// Next episodes first, then the ones before.
		slices.SortStableFunc(eps, func(a, b *db.Item) int {
			ka, kb := a.EpisodeNumber < it.EpisodeNumber, b.EpisodeNumber < it.EpisodeNumber
			switch {
			case ka != kb && !ka:
				return -1
			case ka != kb:
				return 1
			}
			return a.EpisodeNumber - b.EpisodeNumber
		})
		for _, ep := range eps {
			if ep.ID != it.ID {
				s.queue(ctx, ep)
			}
		}
	}()
}

// queue adds an item to the background lookups unless it has all its
// streams, was looked up in the last day, or is already queued.
func (s *Service) queue(ctx context.Context, it *db.Item) {
	if sts, _ := s.DB.StreamsForItem(ctx, it.ID); len(sts) >= len(s.qualities())*s.perQuality() {
		return
	}
	if l, err := s.DB.GetStreamLookup(ctx, it.ID); err == nil && time.Since(time.Unix(l.LookedAt, 0)) < 24*time.Hour {
		return
	}
	s.mu.Lock()
	if s.queued[it.ID] || s.lookups[it.ID] != nil {
		s.mu.Unlock()
		return
	}
	s.queued[it.ID] = true
	s.mu.Unlock()
	select {
	case s.prefetchQ <- it.ID:
	default:
		s.mu.Lock()
		delete(s.queued, it.ID)
		s.mu.Unlock()
	}
}

// PrefetchWatchlist queues a watchlisted title for lookup, so it plays at
// once when picked: a movie, or the user's next episode of a show. Usenet
// posts take a while to look over the first time; after that they're kept.
func (s *Service) PrefetchWatchlist(userID string, it *db.Item) {
	if !s.cfg().Prefetch || !s.Active() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		libs, err := s.Libraries(ctx)
		if err != nil || !slices.ContainsFunc(libs, func(l *db.Library) bool { return l.ID == it.LibraryID }) {
			return // not a streaming title
		}
		switch it.Kind {
		case "movie":
			s.queue(ctx, it)
		case "show":
			if ep, err := s.DB.FirstUnwatched(ctx, userID, it.ID); err == nil {
				s.queue(ctx, ep)
			}
		}
	}()
}

// watchlistLoop looks up watchlisted titles a little after start and then
// every few hours, so new posts and dropped streams are caught up on.
func (s *Service) watchlistLoop(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		users, items, err := s.DB.Watchlisted(ctx)
		if err == nil {
			for k, id := range items {
				if it, err := s.DB.GetItem(ctx, id); err == nil {
					s.PrefetchWatchlist(users[k], it)
				}
			}
		}
		t.Reset(6 * time.Hour)
	}
}

func (s *Service) prefetchWorker(ctx context.Context, n int) {
	for {
		// Workers beyond the configured count idle, so the limit can change
		// live. They wait before taking work, not holding it.
		if n >= max(s.cfg().Limits.PrefetchWorkers, 1) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case id := <-s.prefetchQ:
			if it, err := s.DB.GetItem(ctx, id); err == nil && s.Active() {
				// Run as an ordinary lookup, so a play meanwhile waits on it
				// rather than starting another. One that played meanwhile
				// isn't looked up again.
				s.mu.Lock()
				var lk *lookup
				if s.lookups[id] == nil {
					if l, err := s.DB.GetStreamLookup(ctx, id); err != nil || time.Since(time.Unix(l.LookedAt, 0)) > time.Hour {
						lk = s.startLookup(it, false)
					}
				}
				s.mu.Unlock()
				if lk != nil {
					select {
					case <-lk.done:
					case <-ctx.Done():
					}
				}
			}
			s.mu.Lock()
			delete(s.queued, id)
			s.mu.Unlock()
		}
	}
}

// dropStream forgets a stream that can't play any more, so the next play
// uses another version and the title is looked up again.
func (s *Service) dropStream(ctx context.Context, streamID, why string) {
	st, err := s.DB.GetStream(context.WithoutCancel(ctx), streamID)
	if err != nil {
		return
	}
	slog.Warn("dropping stream", "stream", streamID, "file", st.FileName, "why", why)
	s.mu.Lock()
	delete(s.links, streamID)
	s.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	_ = s.DB.DeleteStream(ctx, streamID)
	_ = s.DB.PutStreamLookup(ctx, db.StreamLookup{ItemID: st.ItemID, LookedAt: 0})
}

// ---------- playback ----------

// Source opens a stored stream for playback through the server.
func (s *Service) Source(ctx context.Context, f *db.MediaFile) (stream.Source, error) {
	if !s.Active() {
		return nil, ErrDisabled
	}
	st, err := s.DB.GetStream(ctx, f.StreamID)
	if err != nil {
		return nil, ErrUnavailable
	}
	release, err := s.acquireStream()
	if err != nil {
		return nil, err
	}
	if st.Provider == usenetProvider {
		src, err := s.usenetPlay(ctx, st, release)
		if err != nil {
			release()
			return nil, err
		}
		return src, nil
	}
	u, size, err := s.linkFor(ctx, st.ID, false)
	if err != nil {
		release()
		return nil, err
	}
	if size <= 0 {
		size = st.Size
	}
	global, _ := s.limiters()
	src := &debridSource{svc: s, streamID: st.ID, token: randToken(), size: size, name: st.FileName, url: u, release: release,
		t: throttle{global, newLimiter(s.cfg().Limits.PerStreamMbps)}}
	// The provider can hand out a link whose CDN only returns errors. Find out
	// now, so the stream is replaced instead of the player waiting on
	// segments that never come.
	s.mu.Lock()
	c := s.links[st.ID]
	s.mu.Unlock()
	if c.url == u && time.Since(c.checked) < 2*time.Minute {
		s.mu.Lock()
		s.sources[src.token] = src
		s.mu.Unlock()
		return src, nil
	}
	if err := src.check(ctx); err != nil {
		release()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Warn("debrid stream failed", "stream", st.ID, "file", st.FileName, "err", redactURL(err))
		s.mu.Lock()
		delete(s.links, st.ID)
		s.cdnFails[st.ID]++
		n := s.cdnFails[st.ID]
		if n >= 2 {
			delete(s.cdnFails, st.ID)
		}
		s.mu.Unlock()
		// Twice in a row: drop it, so another version or release plays instead.
		if n >= 2 {
			slog.Info("dropping stream its CDN won't serve", "stream", st.ID, "file", st.FileName)
			_ = s.DB.DeleteStream(ctx, st.ID)
			_ = s.DB.PutStreamLookup(ctx, db.StreamLookup{ItemID: st.ItemID, LookedAt: 0})
		} else {
			_, _ = s.DB.StreamFailed(ctx, st.ID) // find the torrent again next time
		}
		return nil, ErrUnavailable
	}
	s.mu.Lock()
	delete(s.cdnFails, st.ID)
	if c, ok := s.links[st.ID]; ok && c.url == u {
		c.checked = time.Now()
		s.links[st.ID] = c
	}
	s.sources[src.token] = src
	s.mu.Unlock()
	return src, nil
}

func (s *Service) acquireStream() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.cfg().Limits.MaxStreams; m > 0 && s.streaming >= m {
		return nil, ErrBusy
	}
	s.streaming++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.streaming--
			s.mu.Unlock()
		})
	}, nil
}

// ActiveStreams is how many debrid streams are open.
func (s *Service) ActiveStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streaming
}

// ServeInternal serves a source to ffmpeg on the loopback interface.
func (s *Service) ServeInternal(w http.ResponseWriter, r *http.Request, token string) {
	s.mu.Lock()
	src := s.sources[token]
	s.mu.Unlock()
	if src == nil {
		http.Error(w, "unknown stream", http.StatusNotFound)
		return
	}
	if d, ok := src.(*debridSource); ok {
		d.ServeInternal(w, r)
		return
	}
	src.ServeHTTP(w, r)
}

// linkFor returns a direct CDN URL for a stream and the file size. When the
// provider that had it no longer does, the torrent is found again on any
// provider; streams that keep failing are dropped so the next play looks
// up a new release.
func (s *Service) linkFor(ctx context.Context, streamID string, force bool) (string, int64, error) {
	s.mu.Lock()
	c, ok := s.links[streamID]
	s.mu.Unlock()
	if ok && !force && time.Since(c.at) < s.linkTTL(c.provider) {
		return c.url, 0, nil
	}
	st, err := s.DB.GetStream(ctx, streamID)
	if err != nil {
		return "", 0, ErrUnavailable
	}
	remember := func(u string) (string, int64, error) {
		provider := st.Provider
		if st.Link != "" {
			provider = "addon"
		}
		s.mu.Lock()
		s.links[streamID] = cachedLink{url: u, at: time.Now(), provider: provider}
		s.mu.Unlock()
		return u, st.Size, nil
	}
	if st.Link != "" {
		// The addon adds the release when its link is opened; the link it
		// just opened while looking the title up is still fresh.
		s.mu.Lock()
		c, ok := s.pending[st.Link]
		delete(s.pending, st.Link)
		s.mu.Unlock()
		if ok && !force && time.Since(c.at) < 10*time.Minute {
			return remember(c.url)
		}
		u, size, err := openLink(ctx, st.Link)
		if err == nil {
			if size > 0 {
				st.Size = size
			}
			return remember(u)
		}
		if ctx.Err() != nil {
			return "", 0, err
		}
		slog.Info("addon link failed", "stream", st.ID, "addon", st.Addon, "err", err)
		// A link that answers with something unplayable won't get better;
		// one that didn't answer gets another try.
		if n, _ := s.DB.StreamFailed(ctx, st.ID); n >= 2 || errors.Is(err, errBadLink) {
			_ = s.DB.DeleteStream(ctx, st.ID)
			_ = s.DB.PutStreamLookup(ctx, db.StreamLookup{ItemID: st.ItemID, LookedAt: 0})
		}
		return "", 0, ErrUnavailable
	}
	if p := s.provider(st.Provider); p != nil && st.ProviderRef != "" {
		var u string
		var err error
		if st.Kind == "usenet" {
			if up, ok := p.(UsenetProvider); ok {
				u, err = up.LinkUsenet(ctx, st.ProviderRef, st.ProviderFile)
			} else {
				err = ErrStale
			}
		} else {
			u, err = p.Link(ctx, st.ProviderRef, st.ProviderFile)
		}
		if err == nil {
			return remember(u)
		}
		if errors.Is(err, ErrRateLimited) || ctx.Err() != nil {
			return "", 0, err
		}
		slog.Info("stream link failed, looking again", "stream", st.ID, "provider", st.Provider, "err", err)
	}
	it, err := s.DB.GetItem(ctx, st.ItemID)
	if err != nil {
		return "", 0, ErrUnavailable
	}
	season, episode := 0, 0
	if it.Kind == "episode" {
		season, episode = it.SeasonNumber, it.EpisodeNumber
	}
	if st.Kind == "usenet" {
		// The post expired from the account: add it again from its NZB.
		for _, p := range s.Providers() {
			up, ok := p.(UsenetProvider)
			if !ok || st.NZB == "" {
				continue
			}
			tor, err := up.AddUsenet(ctx, st.InfoHash, st.NZB, st.Release)
			if err != nil {
				break
			}
			if f, ok := PickFile(tor.Files, st.FileName, season, episode); ok {
				if u, err := up.LinkUsenet(ctx, tor.Ref, f.ID); err == nil {
					_ = s.DB.SetStreamProvider(ctx, st.ID, p.Name(), tor.Ref, f.ID)
					st.Provider = p.Name()
					return remember(u)
				}
			}
			break
		}
	} else {
		magnet := Release{InfoHash: st.InfoHash}.Magnet()
		for _, p := range s.Providers() {
			if cached, ok, err := p.Check(ctx, []string{st.InfoHash}); ok && err == nil && !cached[st.InfoHash] {
				continue
			}
			tor, err := p.Add(ctx, st.InfoHash, magnet)
			if err != nil {
				continue
			}
			f, ok := PickFile(tor.Files, st.FileName, season, episode)
			if !ok {
				continue
			}
			u, err := p.Link(ctx, tor.Ref, f.ID)
			if err != nil {
				continue
			}
			_ = s.DB.SetStreamProvider(ctx, st.ID, p.Name(), tor.Ref, f.ID)
			st.Provider = p.Name()
			return remember(u)
		}
	}
	if n, _ := s.DB.StreamFailed(ctx, st.ID); n >= 2 {
		_ = s.DB.DeleteStream(ctx, st.ID)
		_ = s.DB.PutStreamLookup(ctx, db.StreamLookup{ItemID: st.ItemID, LookedAt: 0})
	}
	return "", 0, ErrUnavailable
}

// linkTTL is how long a provider's resolved link is reused.
func (s *Service) linkTTL(provider string) time.Duration {
	if m := s.cfg().LinkTTLMinutes[provider]; m > 0 {
		return time.Duration(m) * time.Minute
	}
	return time.Hour
}

// Invalidate throws away a stream's resolved link, so the next play asks
// the provider for a fresh one.
func (s *Service) Invalidate(streamID string) {
	s.mu.Lock()
	delete(s.links, streamID)
	delete(s.cdnFails, streamID)
	s.mu.Unlock()
}

// PlayedThrough clears a stream's failures: someone watched it to the end,
// so earlier breaks were the network's, not the stream's.
func (s *Service) PlayedThrough(ctx context.Context, streamID string) {
	if err := s.DB.ResetStreamFailures(ctx, streamID); err != nil {
		slog.Warn("reset stream failures", "stream", streamID, "err", err)
	}
}

// PlaybackFailed records a stream that broke while it played: a torrent
// no longer cached, a post that lost articles. Its link is thrown away and
// it's found again from scratch next time; one that breaks twice is
// dropped, so another source plays and the title is looked up again.
func (s *Service) PlaybackFailed(ctx context.Context, streamID string) (dropped bool) {
	s.Invalidate(streamID)
	st, err := s.DB.GetStream(ctx, streamID)
	if err != nil {
		return true
	}
	var n int
	if st.Provider == usenetProvider {
		// The post's layout is kept: it's read from the news servers again.
		n, err = s.DB.CountStreamFailure(ctx, st.ID)
	} else {
		n, err = s.DB.StreamFailed(ctx, st.ID)
	}
	if err != nil || n >= 2 {
		s.dropStream(ctx, streamID, "failed during playback")
		return true
	}
	return false
}

// Forget drops an item's streams so the next play looks them up again.
func (s *Service) Forget(ctx context.Context, itemID string) error {
	return s.DB.ClearStreams(ctx, itemID)
}

func (s *Service) notify(event string, data any) {
	if s.Hooks == nil {
		return
	}
	env := webhook.Envelope{Event: event, ID: db.NewID(), Data: data}
	if s.Server != nil {
		env.Server = s.Server()
	}
	s.Hooks.Send(env)
}

func randToken() string { return db.NewID() + db.NewID() }

// Probeable reports whether a stream can be read ahead of play without
// costing anything: everything but an addon link not opened yet, which
// would start a stream on the addon (StreamNZB).
func (s *Service) Probeable(st *db.Stream) bool {
	if st.Link == "" || s.cfg().CheckAddonLinks {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, opened := s.links[st.ID]
	_, pending := s.pending[st.Link]
	return opened || pending
}

// Looking reports whether streams are being looked up for an item now.
func (s *Service) Looking(itemID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookups[itemID] != nil || s.queued[itemID]
}
