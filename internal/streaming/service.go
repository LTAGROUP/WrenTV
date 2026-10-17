// Package streaming lets a server act as a shared Stremio-style client.
//
// Any movie or show can be searched and played. When a title is played the
// server asks its Stremio addons (Torrentio, Comet, …) for releases, keeps
// those already cached on the owner's debrid accounts (TorBox, AllDebrid,
// Real-Debrid), and stores the best one per quality (2160p and 1080p) as a
// stream: just the info hash and the file, never the media. Bytes are
// proxied from the debrid CDN on demand. After an episode plays, the rest of
// its season is looked up in the background; a season pack fills every
// episode at once. Streamed titles live in "streaming" libraries and give
// way to real files of the same title. Removing the debrid keys hides them.
//
// In download mode, played titles are also saved as files into a chosen
// library; the scanner picks them up and the file replaces the stream.
package streaming

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
	"github.com/LTAGROUP/wrentv/internal/stream"
	"github.com/LTAGROUP/wrentv/internal/webhook"
)

var (
	ErrDisabled    = errors.New("streaming isn't set up on this server")
	ErrNoStreams   = errors.New("no cached stream is available for this title right now")
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
	sources   map[string]*debridSource
	streaming int // open debrid sources
	links     map[string]cachedLink
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
	url string
	at  time.Time
}

type lookup struct {
	done  chan struct{}
	found int
	err   error
}

func New(d Deps) *Service {
	return &Service{
		Deps: d, sources: map[string]*debridSource{}, links: map[string]cachedLink{}, cdnFails: map[string]int{}, lookups: map[string]*lookup{},
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

// Active reports whether streaming can be used: enabled, a debrid key, and TMDB.
func (s *Service) Active() bool {
	return s.cfg().Enabled && len(s.Providers()) > 0 && s.TMDB.Enabled()
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

func (s *Service) qualities() []string {
	q := s.cfg().Qualities
	if len(q) == 0 {
		return []string{"2160p", "1080p"}
	}
	return q
}

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
// creating it (with every aired season and episode) the first time.
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
	it.SortTitle = ""
	if err := s.DB.InsertItem(ctx, it); err != nil {
		// Someone else added it at the same moment.
		if existing, ferr := s.DB.FindStreamTitle(ctx, lib.ID, kind, tmdbID); ferr == nil {
			return existing, nil
		}
		return nil, err
	}
	if show != nil {
		s.syncEpisodes(ctx, it, show)
		s.mu.Lock()
		s.refreshed[it.ID] = time.Now()
		s.mu.Unlock()
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
			if s.DB.InsertItem(ctx, ep) == nil {
				added = true
			}
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
// running. ErrNoStreams means nothing cached was found.
func (s *Service) Ensure(ctx context.Context, it *db.Item, wait time.Duration) (ready bool, err error) {
	if !s.Active() {
		return false, ErrDisabled
	}
	if sts, _ := s.DB.StreamsForItem(ctx, it.ID); len(sts) > 0 {
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
		lk = &lookup{done: make(chan struct{})}
		s.lookups[it.ID] = lk
		go s.runLookup(it, lk)
	}
	s.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
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

func (s *Service) runLookup(it *db.Item, lk *lookup) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	lk.found, lk.err = s.resolve(ctx, it)
	close(lk.done)
	s.mu.Lock()
	delete(s.lookups, it.ID)
	s.mu.Unlock()
	if lk.found > 0 {
		s.Hub.Publish(library.Event{Type: "stream.updated", LibraryID: it.LibraryID, ItemID: it.ID})
	} else {
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
}

// resolve looks up and stores every missing quality of an item. It returns
// how many qualities the item now has.
func (s *Service) resolve(ctx context.Context, it *db.Item) (int, error) {
	have := map[string]bool{}
	if sts, _ := s.DB.StreamsForItem(ctx, it.ID); len(sts) > 0 {
		for _, st := range sts {
			have[st.Quality] = true
		}
	}
	missing := slices.DeleteFunc(slices.Clone(s.qualities()), func(q string) bool { return have[q] })
	if len(missing) == 0 {
		return len(have), nil
	}
	imdb, season, episode, err := s.target(ctx, it)
	if err != nil {
		s.recordLookup(ctx, it.ID, len(have), err)
		return len(have), err
	}
	rels, err := Search(ctx, s.addons(), imdb, season, episode)
	if err != nil && len(rels) == 0 {
		s.recordLookup(ctx, it.ID, len(have), err)
		return len(have), err
	}
	checked := map[string]map[string]bool{}
	found := len(have)
	for _, q := range missing {
		cands := Rank(rels, q, s.maxSize(q), season)
		p := s.findCached(ctx, cands, season, episode, s.maxSize(q), checked)
		if p == nil {
			continue
		}
		if err := s.store(ctx, it, q, p); err != nil {
			slog.Warn("store stream", "item", it.ID, "err", err)
			continue
		}
		found++
		if season > 0 {
			s.fillSeason(ctx, it, q, p) // a season pack covers the other episodes too
		}
	}
	slog.Info("stream lookup", "title", it.Title, "season", season, "episode", episode, "releases", len(rels), "qualities", found)
	s.recordLookup(ctx, it.ID, found, nil)
	return found, nil
}

func (s *Service) recordLookup(ctx context.Context, itemID string, found int, err error) {
	_ = s.DB.PutStreamLookup(ctx, db.StreamLookup{ItemID: itemID, LookedAt: db.Now(), Found: found, Error: errString(err)})
}

// findCached returns the best ranked release some provider has cached.
// checked memoizes bulk cache checks across the qualities of one lookup.
func (s *Service) findCached(ctx context.Context, cands []Release, season, episode int, maxSize int64, checked map[string]map[string]bool) *pick {
	if len(cands) == 0 {
		return nil
	}
	for _, p := range s.Providers() {
		if ctx.Err() != nil {
			return nil
		}
		top := cands[:min(len(cands), 40)]
		cached := checked[p.Name()]
		if cached == nil {
			cached = map[string]bool{}
			checked[p.Name()] = cached
		}
		var hashes []string
		for _, r := range top {
			if _, seen := cached[r.InfoHash]; !seen {
				hashes = append(hashes, r.InfoHash)
			}
		}
		ok := true
		if len(hashes) > 0 {
			res, known, err := p.Check(ctx, hashes)
			if err != nil {
				slog.Debug("cache check", "provider", p.Name(), "err", err)
				if errors.Is(err, ErrRateLimited) {
					continue
				}
			}
			if known {
				for _, h := range hashes {
					cached[h] = res[h]
				}
			} else {
				ok = false // the provider can't tell; try adding instead
			}
		}
		tries := 0
		for _, r := range top {
			if ok && !cached[r.InfoHash] {
				continue
			}
			if tries++; tries > 3 {
				break
			}
			tor, err := p.Add(ctx, r.InfoHash, r.Magnet())
			if err != nil {
				if errors.Is(err, ErrRateLimited) {
					break
				}
				continue
			}
			f, found := PickFile(tor.Files, r.FileName, season, episode)
			if !found || (maxSize > 0 && f.Size > maxSize) {
				continue
			}
			return &pick{rel: r, prov: p, tor: tor, file: f}
		}
	}
	return nil
}

func (s *Service) store(ctx context.Context, it *db.Item, q string, p *pick) error {
	st := &db.Stream{ItemID: it.ID, Quality: q, InfoHash: p.rel.InfoHash, FileName: p.file.Name, Size: p.file.Size,
		Release: p.rel.Name, Addon: p.rel.Addon, Provider: p.prov.Name(), ProviderRef: p.tor.Ref, ProviderFile: p.file.ID, FileIdx: p.rel.FileIdx}
	// Codec and HDR are guessed from the names until the first play probes it.
	f := &db.MediaFile{ItemID: it.ID, LibraryID: it.LibraryID, Path: p.file.Name, Size: p.file.Size}
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

// fillSeason stores a season pack's other episodes at the same quality.
func (s *Service) fillSeason(ctx context.Context, it *db.Item, q string, p *pick) {
	byEp := EpisodeFiles(p.tor.Files, it.SeasonNumber)
	if len(byEp) < 2 {
		return
	}
	eps, _, err := s.DB.QueryItems(ctx, db.ItemQuery{ParentID: it.ParentID, Kind: "episode"})
	if err != nil {
		return
	}
	n := 0
	for _, ep := range eps {
		f, ok := byEp[ep.EpisodeNumber]
		if !ok || ep.ID == it.ID {
			continue
		}
		sts, _ := s.DB.StreamsForItem(ctx, ep.ID)
		if slices.ContainsFunc(sts, func(st *db.Stream) bool { return st.Quality == q }) {
			continue
		}
		if m := s.maxSize(q); m > 0 && f.Size > m {
			continue
		}
		pp := *p
		pp.file = f
		if s.store(ctx, ep, q, &pp) == nil {
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
		want := len(s.qualities())
		for _, ep := range eps {
			if ep.ID == it.ID {
				continue
			}
			if sts, _ := s.DB.StreamsForItem(ctx, ep.ID); len(sts) >= want {
				continue
			}
			if l, err := s.DB.GetStreamLookup(ctx, ep.ID); err == nil && time.Since(time.Unix(l.LookedAt, 0)) < 24*time.Hour {
				continue
			}
			s.mu.Lock()
			if s.queued[ep.ID] {
				s.mu.Unlock()
				continue
			}
			s.queued[ep.ID] = true
			s.mu.Unlock()
			select {
			case s.prefetchQ <- ep.ID:
			default:
				s.mu.Lock()
				delete(s.queued, ep.ID)
				s.mu.Unlock()
			}
		}
	}()
}

func (s *Service) prefetchWorker(ctx context.Context, n int) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.prefetchQ:
			// Workers beyond the configured count idle, so the limit can change live.
			for n >= max(s.cfg().Limits.PrefetchWorkers, 1) {
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
			if it, err := s.DB.GetItem(ctx, id); err == nil && s.Active() {
				lctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				if found, _ := s.resolve(lctx, it); found > 0 {
					s.Hub.Publish(library.Event{Type: "stream.updated", LibraryID: it.LibraryID, ItemID: it.ID})
				}
				cancel()
			}
			s.mu.Lock()
			delete(s.queued, id)
			s.mu.Unlock()
		}
	}
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
	s.mu.Unlock()
	s.mu.Lock()
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
	src.ServeHTTP(w, r)
}

// linkFor returns a direct CDN URL for a stream and the file size. When the
// provider that had it no longer does, the torrent is found again on any
// provider; streams that keep failing are dropped so the next play looks
// up a new release.
func (s *Service) linkFor(ctx context.Context, streamID string, force bool) (string, int64, error) {
	s.mu.Lock()
	if c, ok := s.links[streamID]; ok && !force && time.Since(c.at) < time.Hour {
		s.mu.Unlock()
		return c.url, 0, nil
	}
	s.mu.Unlock()
	st, err := s.DB.GetStream(ctx, streamID)
	if err != nil {
		return "", 0, ErrUnavailable
	}
	remember := func(u string) (string, int64, error) {
		s.mu.Lock()
		s.links[streamID] = cachedLink{url: u, at: time.Now()}
		s.mu.Unlock()
		return u, st.Size, nil
	}
	if p := s.provider(st.Provider); p != nil && st.ProviderRef != "" {
		u, err := p.Link(ctx, st.ProviderRef, st.ProviderFile)
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
		return remember(u)
	}
	if n, _ := s.DB.StreamFailed(ctx, st.ID); n >= 2 {
		_ = s.DB.DeleteStream(ctx, st.ID)
		_ = s.DB.PutStreamLookup(ctx, db.StreamLookup{ItemID: st.ItemID, LookedAt: 0})
	}
	return "", 0, ErrUnavailable
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
