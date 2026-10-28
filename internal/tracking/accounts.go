package tracking

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
)

// A tracker is Simkl or Trakt as the shared sign-in and import code sees
// them. Both sign in with a device code and hand over a watch history.
type tracker interface {
	id() string    // db.Simkl or db.Trakt
	label() string // "Simkl"
	on() bool
	start(ctx context.Context) (device, error)
	// poll returns errPending, errSlowDown or errExpired until it has a token.
	poll(ctx context.Context, d device) (token, error)
	renew(ctx context.Context, refresh string) (token, error)
	username(ctx context.Context, access string) (string, error)
	rejected(err error) bool
	history(ctx context.Context, call caller) (history, error)
	paused(ctx context.Context, call caller) ([]pausedTitle, error)
}

// caller runs a tracker call with the user's token, renewing it once if
// the tracker rejects it.
type caller func(fn func(access string) error) error

var (
	errPending  = errors.New("pending")
	errSlowDown = errors.New("slow down")
	errExpired  = errors.New("The code expired. Start again.")
)

type device struct {
	code, userCode, url, urlFull string
	expiresIn, interval          int
}

type token struct {
	access, refresh string
	expires         int64
}

// extRef is how a tracker names a title.
type extRef struct {
	tmdb  int
	imdb  string
	title string
	year  int
}

type watchedEp struct {
	season, number int
	at             int64
}

type watchedTitle struct {
	ref extRef
	at  int64
	// completed: a show finished (Simkl's completed list); only then is it
	// kept as watched when the server doesn't have it.
	completed bool
	eps       []watchedEp
}

type history struct {
	movies, shows []watchedTitle
}

type pausedTitle struct {
	movie          bool
	ref            extRef
	season, number int
	percent        float64
	at             int64
}

func (s *Service) tracker(id string) tracker {
	if id == db.Trakt {
		return traktTracker{s.Trakt}
	}
	return simklTracker{s.Simkl}
}

func linkKey(tracker, userID string) string { return tracker + "|" + userID }

// pendingLink is a device code waiting for the user to approve it on the
// tracker's site.
type pendingLink struct {
	UserCode  string `json:"userCode"`
	URL       string `json:"url"`
	URLFull   string `json:"urlComplete"`
	ExpiresAt int64  `json:"expiresAt"`
	Error     string `json:"error,omitempty"`
	cancel    context.CancelFunc
}

// TrackerStatus is what the apps show on a tracker's settings page.
type TrackerStatus struct {
	Available bool               `json:"available"` // the server has the tracker's app configured
	Linked    bool               `json:"linked"`
	Account   *db.TrackerAccount `json:"account,omitempty"`
	Pending   *pendingLink       `json:"pending,omitempty"`
	Importing bool               `json:"importing"`
}

// SimklStatus is TrackerStatus for Simkl.
type SimklStatus = TrackerStatus

func (s *Service) Status(ctx context.Context, trackerID, userID string) (TrackerStatus, error) {
	st := TrackerStatus{Available: s.tracker(trackerID).on()}
	a, err := s.DB.GetTracker(ctx, trackerID, userID)
	if err != nil {
		return st, err
	}
	st.Linked, st.Account = a != nil, a
	k := linkKey(trackerID, userID)
	s.mu.Lock()
	if p := s.links[k]; p != nil {
		c := *p
		st.Pending = &c
	}
	st.Importing = s.imports[k]
	s.mu.Unlock()
	return st, nil
}

// Link starts the device sign-in. The server polls the tracker until the
// user approves the code, then stores the account and imports their history.
func (s *Service) Link(ctx context.Context, trackerID, userID string) (*pendingLink, error) {
	t := s.tracker(trackerID)
	if !t.on() {
		return nil, errors.New(t.label() + " isn't set up on this server")
	}
	d, err := t.start(ctx)
	if err != nil {
		return nil, err
	}
	life := time.Duration(d.expiresIn) * time.Second
	if life <= 0 || life > 15*time.Minute {
		life = 15 * time.Minute
	}
	pctx, cancel := context.WithTimeout(context.Background(), life)
	p := &pendingLink{UserCode: d.userCode, URL: d.url, URLFull: d.urlFull, ExpiresAt: s.now().Add(life).Unix(), cancel: cancel}
	k := linkKey(trackerID, userID)
	s.mu.Lock()
	if old := s.links[k]; old != nil {
		old.cancel()
	}
	s.links[k] = p
	s.mu.Unlock()
	go s.pollLink(pctx, t, userID, p, d)
	c := *p
	return &c, nil
}

func (s *Service) pollLink(ctx context.Context, t tracker, userID string, p *pendingLink, d device) {
	defer p.cancel()
	k := linkKey(t.id(), userID)
	done := func(msg string) {
		s.mu.Lock()
		if s.links[k] == p {
			if msg == "" {
				delete(s.links, k)
			} else {
				p.Error = msg
			}
		}
		s.mu.Unlock()
	}
	interval := time.Duration(d.interval) * time.Second
	if s.pollEvery > 0 {
		interval = s.pollEvery
	}
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			if s.links[k] == p {
				delete(s.links, k) // expired or cancelled
			}
			s.mu.Unlock()
			return
		case <-time.After(interval):
		}
		tok, err := t.poll(ctx, d)
		switch {
		case errors.Is(err, errPending):
			continue
		case errors.Is(err, errSlowDown):
			interval += 5 * time.Second
			continue
		case errors.Is(err, errExpired):
			done(errExpired.Error())
			return
		case err != nil:
			if ctx.Err() != nil {
				continue
			}
			slog.Warn(t.id()+" link", "user", userID, "err", err)
			done(err.Error())
			return
		}
		a := &db.TrackerAccount{UserID: userID, AccessToken: tok.access, RefreshToken: tok.refresh, ExpiresAt: tok.expires}
		a.Username, _ = t.username(ctx, tok.access)
		if err := s.DB.LinkTracker(context.Background(), t.id(), a); err != nil {
			done(err.Error())
			return
		}
		done("")
		slog.Info(t.id()+" linked", "user", userID, "account", a.Username)
		s.ImportFrom(t.id(), userID)
		return
	}
}

// CancelLinkFor drops a pending sign-in.
func (s *Service) CancelLinkFor(trackerID, userID string) {
	k := linkKey(trackerID, userID)
	s.mu.Lock()
	if p := s.links[k]; p != nil {
		p.cancel()
		delete(s.links, k)
	}
	s.mu.Unlock()
}

func (s *Service) Unlink(ctx context.Context, trackerID, userID string) error {
	s.CancelLinkFor(trackerID, userID)
	return s.DB.UnlinkTracker(ctx, trackerID, userID)
}

// Simkl names for the shared calls.

func (s *Service) SimklStatus(ctx context.Context, userID string) (SimklStatus, error) {
	return s.Status(ctx, db.Simkl, userID)
}

func (s *Service) LinkSimkl(ctx context.Context, userID string) (*pendingLink, error) {
	return s.Link(ctx, db.Simkl, userID)
}

func (s *Service) CancelLink(userID string) { s.CancelLinkFor(db.Simkl, userID) }

func (s *Service) UnlinkSimkl(ctx context.Context, userID string) error {
	return s.Unlink(ctx, db.Simkl, userID)
}

func (s *Service) Import(userID string) bool { return s.ImportFrom(db.Simkl, userID) }

// ---------- tokens ----------

// renew swaps the account's refresh token for a new access token and saves it.
func (s *Service) renew(ctx context.Context, t tracker, a *db.TrackerAccount) error {
	tok, err := t.renew(ctx, a.RefreshToken)
	if err != nil {
		return err
	}
	a.AccessToken, a.ExpiresAt = tok.access, tok.expires
	if tok.refresh != "" {
		a.RefreshToken = tok.refresh
	}
	return s.DB.SetTrackerTokens(ctx, t.id(), a.UserID, a.AccessToken, a.RefreshToken, a.ExpiresAt)
}

// withAccount runs fn with the user's token, one call at a time per user
// and tracker (both lock or rate-limit each user), renewing the token when
// it's close to expiring or rejected. Accounts with scrobbling off are skipped.
func (s *Service) withAccount(trackerID, userID string, fn func(ctx context.Context, access string) error) {
	t := s.tracker(trackerID)
	s.enqueue(linkKey(trackerID, userID), func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		a, err := s.DB.GetTracker(ctx, trackerID, userID)
		if err != nil || a == nil || !a.Scrobble {
			return
		}
		// Trakt tokens last a day, so renew in the last quarter of that;
		// Simkl's last much longer and renew in their last day.
		margin := int64(24 * 3600)
		if trackerID == db.Trakt {
			margin = 6 * 3600
		}
		if a.RefreshToken != "" && a.ExpiresAt > 0 && a.ExpiresAt-s.now().Unix() < margin {
			if err := s.renew(ctx, t, a); err != nil {
				slog.Warn(trackerID+" refresh", "user", userID, "err", err)
			}
		}
		err = fn(ctx, a.AccessToken)
		if t.rejected(err) && a.RefreshToken != "" {
			if s.renew(ctx, t, a) == nil {
				err = fn(ctx, a.AccessToken)
			}
		}
		if err != nil {
			slog.Warn(trackerID, "user", userID, "err", err)
		}
	})
}

// ---------- history import ----------

// ImportFrom pulls the user's history from a tracker in the background and
// marks every matching movie and episode in the library watched. Titles
// already watched here are left alone, and nothing is ever marked unwatched.
func (s *Service) ImportFrom(trackerID, userID string) bool {
	k := linkKey(trackerID, userID)
	s.mu.Lock()
	if s.imports[k] {
		s.mu.Unlock()
		return false
	}
	s.imports[k] = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.imports, k)
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		_ = s.DB.SetTrackerImport(ctx, trackerID, userID, "running", 0, 0)
		movies, eps, err := s.runImport(ctx, s.tracker(trackerID), userID)
		status := "done"
		if err != nil {
			status = err.Error()
			slog.Warn(trackerID+" import", "user", userID, "err", err)
		} else {
			slog.Info(trackerID+" import", "user", userID, "movies", movies, "episodes", eps)
		}
		_ = s.DB.SetTrackerImport(context.Background(), trackerID, userID, status, movies, eps)
	}()
	return true
}

type matcher struct {
	tmdb  map[string]string // kind:tmdb → id
	imdb  map[string]string
	title map[string]string // kind:title:year → id
}

func normTitle(t string) string { return strings.Join(strings.Fields(strings.ToLower(t)), " ") }

func newMatcher(items []db.ExternalItem) *matcher {
	m := &matcher{tmdb: map[string]string{}, imdb: map[string]string{}, title: map[string]string{}}
	for _, it := range items {
		if it.TMDBID > 0 {
			m.tmdb[it.Kind+":"+strconv.Itoa(it.TMDBID)] = it.ID
		}
		if it.IMDBID != "" {
			m.imdb[it.Kind+":"+it.IMDBID] = it.ID
		}
		m.title[it.Kind+":"+normTitle(it.Title)+":"+strconv.Itoa(it.Year)] = it.ID
	}
	return m
}

func (m *matcher) find(kind string, r extRef) string {
	if r.tmdb > 0 {
		if id := m.tmdb[kind+":"+strconv.Itoa(r.tmdb)]; id != "" {
			return id
		}
	}
	if r.imdb != "" {
		if id := m.imdb[kind+":"+r.imdb]; id != "" {
			return id
		}
	}
	if r.title != "" && r.year > 0 {
		return m.title[kind+":"+normTitle(r.title)+":"+strconv.Itoa(r.year)]
	}
	return ""
}

func when(ts string) int64 {
	if t, err := time.Parse(time.RFC3339, ts); err == nil && t.Unix() > 0 {
		return t.Unix()
	}
	return 0
}

func (s *Service) runImport(ctx context.Context, t tracker, userID string) (movies, episodes int, err error) {
	var a *db.TrackerAccount
	if a, err = s.DB.GetTracker(ctx, t.id(), userID); err != nil || a == nil {
		return 0, 0, errors.New(t.label() + " isn't linked")
	}
	items, err := s.DB.ExternalItems(ctx)
	if err != nil {
		return 0, 0, err
	}
	m := newMatcher(items)
	call := func(fn func(access string) error) error {
		err := fn(a.AccessToken)
		if t.rejected(err) && a.RefreshToken != "" && s.renew(ctx, t, a) == nil {
			err = fn(a.AccessToken)
		}
		return err
	}
	h, err := t.history(ctx, call)
	if err != nil {
		return 0, 0, err
	}
	// Titles the server doesn't have are kept by TMDB id, so they show as
	// watched and are marked once added.
	var missing []db.ExternalMark
	episodesOf := map[string]map[[2]int]string{}
	episodeIDs := func(showID string) (map[[2]int]string, error) {
		eps, ok := episodesOf[showID]
		if !ok {
			if eps, err = s.DB.EpisodeIDs(ctx, showID); err != nil {
				return nil, err
			}
			episodesOf[showID] = eps
		}
		return eps, nil
	}

	// Movies the user finished.
	marks := map[string]int64{}
	for _, w := range h.movies {
		if id := m.find("movie", w.ref); id != "" {
			marks[id] = w.at
		} else if w.ref.tmdb > 0 {
			missing = append(missing, db.ExternalMark{Kind: "movie", TMDBID: w.ref.tmdb, Season: -1, Episode: -1, WatchedAt: w.at})
		}
	}
	if movies, err = s.DB.ImportWatched(ctx, userID, marks); err != nil {
		return movies, 0, err
	}

	// Episodes watched.
	marks = map[string]int64{}
	for _, w := range h.shows {
		showID := m.find("show", w.ref)
		if showID == "" {
			if w.ref.tmdb <= 0 {
				continue
			}
			if w.completed {
				missing = append(missing, db.ExternalMark{Kind: "show", TMDBID: w.ref.tmdb, Season: -1, Episode: -1, WatchedAt: w.at})
			}
			for _, ep := range w.eps {
				missing = append(missing, db.ExternalMark{Kind: "episode", TMDBID: w.ref.tmdb, Season: ep.season, Episode: ep.number, WatchedAt: ep.at})
			}
			continue
		}
		eps, err := episodeIDs(showID)
		if err != nil {
			return movies, 0, err
		}
		for _, ep := range w.eps {
			if id := eps[[2]int{ep.season, ep.number}]; id != "" {
				marks[id] = ep.at
			}
		}
	}
	if episodes, err = s.DB.ImportWatched(ctx, userID, marks); err != nil {
		return movies, episodes, err
	}
	if err := s.DB.ImportExternalWatched(ctx, userID, missing); err != nil {
		return movies, episodes, err
	}
	// Anything added while this ran picks its marks up now.
	if err := s.DB.ApplyExternalWatched(ctx); err != nil {
		return movies, episodes, err
	}

	// Where the user paused, for Continue Watching. A failure here doesn't
	// fail the import.
	paused, err := t.paused(ctx, call)
	if err != nil {
		slog.Warn(t.id()+" playback import", "user", userID, "err", err)
		return movies, episodes, nil
	}
	resumed, skipped := 0, 0
	var elsewhere []db.ExternalProgress
	for _, pb := range paused {
		if pb.percent <= 0 || pb.percent >= 100 {
			continue
		}
		var id string
		if pb.movie {
			if id = m.find("movie", pb.ref); id == "" && pb.ref.tmdb > 0 {
				elsewhere = append(elsewhere, db.ExternalProgress{Kind: "movie", TMDBID: pb.ref.tmdb, Season: -1, Episode: -1, Percent: pb.percent, PausedAt: pb.at})
			}
		} else if showID := m.find("show", pb.ref); showID != "" {
			eps, err := episodeIDs(showID)
			if err != nil {
				return movies, episodes, err
			}
			id = eps[[2]int{pb.season, pb.number}]
		} else if pb.ref.tmdb > 0 && pb.number > 0 {
			elsewhere = append(elsewhere, db.ExternalProgress{Kind: "episode", TMDBID: pb.ref.tmdb, Season: pb.season, Episode: pb.number, Percent: pb.percent, PausedAt: pb.at})
		}
		if id == "" {
			continue
		}
		dur, err := s.DB.PlayLength(ctx, id)
		if err != nil || dur <= 0 {
			skipped++
			continue
		}
		if ok, err := s.DB.ImportProgress(ctx, userID, id, dur*pb.percent/100, dur, pb.at); err != nil {
			return movies, episodes, err
		} else if ok {
			resumed++
		}
	}
	slog.Info(t.id()+" playback import", "user", userID, "paused", len(paused), "resumable", resumed, "not here", len(elsewhere), "no length", skipped)
	if err := s.DB.ImportExternalProgress(ctx, userID, elsewhere); err != nil {
		return movies, episodes, err
	}
	s.addPaused(ctx, userID, elsewhere)
	return movies, episodes, nil
}

// maxPausedAdds caps how many titles one import adds for Continue Watching.
const maxPausedAdds = 20

// addPaused adds the most recently paused titles the server doesn't have to
// the streaming library, where their pause points become resume positions.
func (s *Service) addPaused(ctx context.Context, userID string, ps []db.ExternalProgress) {
	if s.AddTitle == nil || len(ps) == 0 {
		return
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].PausedAt > ps[j].PausedAt })
	seen := map[[2]any]bool{}
	for _, p := range ps {
		kind := map[string]string{"movie": "movie", "episode": "show"}[p.Kind]
		k := [2]any{kind, p.TMDBID}
		if seen[k] {
			continue
		}
		if seen[k] = true; len(seen) > maxPausedAdds {
			return
		}
		if err := s.AddTitle(ctx, userID, kind, p.TMDBID); err != nil {
			slog.Warn("paused title", "kind", kind, "tmdb", p.TMDBID, "err", err)
			if ctx.Err() != nil {
				return
			}
			continue
		}
		// A title already there applies now; a new show applies once its
		// episodes are in.
		if err := s.DB.ApplyExternalProgressFor(ctx, kind, p.TMDBID); err != nil {
			slog.Warn("apply paused", "kind", kind, "tmdb", p.TMDBID, "err", err)
		}
	}
}
