package tracking

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/simkl"
)

// pendingLink is a device code waiting for the user to approve it on simkl.com.
type pendingLink struct {
	UserCode  string `json:"userCode"`
	URL       string `json:"url"`
	URLFull   string `json:"urlComplete"`
	ExpiresAt int64  `json:"expiresAt"`
	Error     string `json:"error,omitempty"`
	cancel    context.CancelFunc
}

// SimklStatus is what the apps show on the Simkl settings page.
type SimklStatus struct {
	Available bool             `json:"available"` // the server has a Simkl app configured
	Linked    bool             `json:"linked"`
	Account   *db.SimklAccount `json:"account,omitempty"`
	Pending   *pendingLink     `json:"pending,omitempty"`
	Importing bool             `json:"importing"`
}

func (s *Service) SimklStatus(ctx context.Context, userID string) (SimklStatus, error) {
	st := SimklStatus{Available: s.Simkl.Enabled()}
	a, err := s.DB.GetSimkl(ctx, userID)
	if err != nil {
		return st, err
	}
	st.Linked, st.Account = a != nil, a
	s.mu.Lock()
	if p := s.links[userID]; p != nil {
		c := *p
		st.Pending = &c
	}
	st.Importing = s.imports[userID]
	s.mu.Unlock()
	return st, nil
}

// LinkSimkl starts the device sign-in. The server polls Simkl until the user
// approves the code, then stores the account and imports their history.
func (s *Service) LinkSimkl(ctx context.Context, userID string) (*pendingLink, error) {
	if !s.Simkl.Enabled() {
		return nil, errors.New("Simkl isn't set up on this server")
	}
	d, err := s.Simkl.StartDevice(ctx)
	if err != nil {
		return nil, err
	}
	life := time.Duration(d.ExpiresIn) * time.Second
	if life <= 0 || life > 15*time.Minute {
		life = 15 * time.Minute
	}
	pctx, cancel := context.WithTimeout(context.Background(), life)
	p := &pendingLink{UserCode: d.UserCode, URL: d.URL, URLFull: d.URLFull, ExpiresAt: s.now().Add(life).Unix(), cancel: cancel}
	if p.URL == "" {
		p.URL = "https://simkl.com/pin"
	}
	s.mu.Lock()
	if old := s.links[userID]; old != nil {
		old.cancel()
	}
	s.links[userID] = p
	s.mu.Unlock()
	go s.pollLink(pctx, userID, p, d)
	c := *p
	return &c, nil
}

func (s *Service) pollLink(ctx context.Context, userID string, p *pendingLink, d simkl.Device) {
	defer p.cancel()
	done := func(msg string) {
		s.mu.Lock()
		if s.links[userID] == p {
			if msg == "" {
				delete(s.links, userID)
			} else {
				p.Error = msg
			}
		}
		s.mu.Unlock()
	}
	interval := time.Duration(d.Interval) * time.Second
	if s.pollEvery > 0 {
		interval = s.pollEvery
	}
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			if s.links[userID] == p {
				delete(s.links, userID) // expired or cancelled
			}
			s.mu.Unlock()
			return
		case <-time.After(interval):
		}
		t, err := s.Simkl.PollDevice(ctx, d.DeviceCode)
		switch {
		case errors.Is(err, simkl.ErrPending):
			continue
		case errors.Is(err, simkl.ErrSlowDown):
			interval += 5 * time.Second
			continue
		case errors.Is(err, simkl.ErrExpired):
			done("The code expired. Start again.")
			return
		case err != nil:
			if ctx.Err() != nil {
				continue
			}
			slog.Warn("simkl link", "user", userID, "err", err)
			done(err.Error())
			return
		}
		a := &db.SimklAccount{UserID: userID, AccessToken: t.Access, RefreshToken: t.Refresh, ExpiresAt: t.Expires}
		a.Username, _ = s.Simkl.Username(ctx, t.Access)
		if err := s.DB.LinkSimkl(context.Background(), a); err != nil {
			done(err.Error())
			return
		}
		done("")
		slog.Info("simkl linked", "user", userID, "account", a.Username)
		s.Import(userID)
		return
	}
}

// CancelLink drops a pending sign-in.
func (s *Service) CancelLink(userID string) {
	s.mu.Lock()
	if p := s.links[userID]; p != nil {
		p.cancel()
		delete(s.links, userID)
	}
	s.mu.Unlock()
}

func (s *Service) UnlinkSimkl(ctx context.Context, userID string) error {
	s.CancelLink(userID)
	return s.DB.UnlinkSimkl(ctx, userID)
}

// ---------- history import ----------

// Import pulls the user's Simkl history in the background and marks every
// matching movie and episode in the library watched. Titles already watched
// here are left alone, and nothing is ever marked unwatched.
func (s *Service) Import(userID string) bool {
	s.mu.Lock()
	if s.imports[userID] {
		s.mu.Unlock()
		return false
	}
	s.imports[userID] = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.imports, userID)
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		_ = s.DB.SetSimklImport(ctx, userID, "running", 0, 0)
		movies, eps, err := s.runImport(ctx, userID)
		status := "done"
		if err != nil {
			status = err.Error()
			slog.Warn("simkl import", "user", userID, "err", err)
		} else {
			slog.Info("simkl import", "user", userID, "movies", movies, "episodes", eps)
		}
		_ = s.DB.SetSimklImport(context.Background(), userID, status, movies, eps)
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

func (m *matcher) find(kind string, md *simkl.Media) string {
	if md == nil {
		return ""
	}
	if v := md.ID("tmdb"); v != "" {
		if id := m.tmdb[kind+":"+v]; id != "" {
			return id
		}
	}
	if v := md.ID("imdb"); v != "" {
		if id := m.imdb[kind+":"+v]; id != "" {
			return id
		}
	}
	if md.Title != "" && md.Year > 0 {
		return m.title[kind+":"+normTitle(md.Title)+":"+strconv.Itoa(md.Year)]
	}
	return ""
}

func when(ts string) int64 {
	if t, err := time.Parse(time.RFC3339, ts); err == nil && t.Unix() > 0 {
		return t.Unix()
	}
	return 0
}

func (s *Service) runImport(ctx context.Context, userID string) (movies, episodes int, err error) {
	var a *db.SimklAccount
	if a, err = s.DB.GetSimkl(ctx, userID); err != nil || a == nil {
		return 0, 0, errors.New("Simkl isn't linked")
	}
	items, err := s.DB.ExternalItems(ctx)
	if err != nil {
		return 0, 0, err
	}
	m := newMatcher(items)
	// withToken calls Simkl, refreshing the token once if it's rejected.
	withToken := func(call func(access string) error) error {
		err := call(a.AccessToken)
		if errors.Is(err, simkl.ErrUnauthorized) && a.RefreshToken != "" && s.refresh(ctx, a) == nil {
			err = call(a.AccessToken)
		}
		return err
	}
	fetch := func(kind, status string) (out simkl.AllItems, err error) {
		err = withToken(func(access string) (err error) {
			out, err = s.Simkl.AllItems(ctx, access, kind, status)
			return err
		})
		return out, err
	}
	// Titles the server doesn't have are kept by TMDB id, so they show as
	// watched and are marked once added.
	var missing []db.ExternalMark
	tmdbOf := func(md *simkl.Media) int {
		id, _ := strconv.Atoi(md.ID("tmdb"))
		return id
	}
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
	got, err := fetch("movies", "completed")
	if err != nil {
		return 0, 0, err
	}
	for _, li := range got.Movies {
		if id := m.find("movie", li.Movie); id != "" {
			marks[id] = when(li.LastWatchedAt)
		} else if t := tmdbOf(li.Movie); t > 0 {
			missing = append(missing, db.ExternalMark{Kind: "movie", TMDBID: t, Season: -1, Episode: -1, WatchedAt: when(li.LastWatchedAt)})
		}
	}
	if movies, err = s.DB.ImportWatched(ctx, userID, marks); err != nil {
		return movies, 0, err
	}

	// Episodes watched, in every list a show can be in.
	marks = map[string]int64{}
	for _, kind := range []string{"shows", "anime"} {
		for _, status := range []string{"watching", "completed", "hold", "dropped"} {
			got, err := fetch(kind, status)
			if err != nil {
				return movies, 0, err
			}
			for _, li := range append(got.Shows, got.Anime...) {
				showID := m.find("show", li.Show)
				if showID == "" {
					t := tmdbOf(li.Show)
					if t <= 0 {
						continue
					}
					if status == "completed" {
						missing = append(missing, db.ExternalMark{Kind: "show", TMDBID: t, Season: -1, Episode: -1, WatchedAt: when(li.LastWatchedAt)})
					}
					for _, se := range li.Seasons {
						for _, ep := range se.Episodes {
							missing = append(missing, db.ExternalMark{Kind: "episode", TMDBID: t, Season: se.Number, Episode: ep.Number, WatchedAt: when(ep.WatchedAt)})
						}
					}
					continue
				}
				eps, err := episodeIDs(showID)
				if err != nil {
					return movies, 0, err
				}
				for _, se := range li.Seasons {
					for _, ep := range se.Episodes {
						if id := eps[[2]int{se.Number, ep.Number}]; id != "" {
							marks[id] = when(ep.WatchedAt)
						}
					}
				}
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

	// Where the user paused, for Continue Watching (titles the server has).
	// A failure here doesn't fail the import.
	var paused []simkl.Playback
	if err := withToken(func(access string) (err error) {
		paused, err = s.Simkl.Playbacks(ctx, access)
		return err
	}); err != nil {
		slog.Warn("simkl playback import", "user", userID, "err", err)
		return movies, episodes, nil
	}
	resumed := 0
	var elsewhere []db.ExternalProgress
	for _, pb := range paused {
		if pb.Progress <= 0 || pb.Progress >= 100 {
			continue
		}
		var id string
		if pb.Movie != nil {
			if id = m.find("movie", pb.Movie); id == "" {
				if t := tmdbOf(pb.Movie); t > 0 {
					elsewhere = append(elsewhere, db.ExternalProgress{Kind: "movie", TMDBID: t, Season: -1, Episode: -1, Percent: pb.Progress, PausedAt: when(pb.When())})
				}
			}
		} else if show := cmp.Or(pb.Show, pb.Anime); show != nil {
			sn, en := pb.SeasonEpisode()
			if showID := m.find("show", show); showID != "" {
				eps, err := episodeIDs(showID)
				if err != nil {
					return movies, episodes, err
				}
				id = eps[[2]int{sn, en}]
			} else if t := tmdbOf(show); t > 0 && en > 0 {
				elsewhere = append(elsewhere, db.ExternalProgress{Kind: "episode", TMDBID: t, Season: sn, Episode: en, Percent: pb.Progress, PausedAt: when(pb.When())})
			}
		}
		if id == "" {
			continue
		}
		it, err := s.DB.GetItem(ctx, id)
		if err != nil || it.RuntimeMin <= 0 {
			continue
		}
		dur := float64(it.RuntimeMin * 60)
		if ok, err := s.DB.ImportProgress(ctx, userID, id, dur*pb.Progress/100, dur, when(pb.When())); err != nil {
			return movies, episodes, err
		} else if ok {
			resumed++
		}
	}
	if resumed > 0 {
		slog.Info("simkl import", "user", userID, "resumable", resumed)
	}
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
			slog.Warn("simkl paused title", "kind", kind, "tmdb", p.TMDBID, "err", err)
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

// SimklWatchlist lists one status ("plantowatch", "watching", "completed"…)
// of a user's Simkl watchlist across movies, shows and anime, refreshing
// the token when it's rejected.
func (s *Service) SimklWatchlist(ctx context.Context, userID, status string) (simkl.AllItems, error) {
	var out simkl.AllItems
	if !s.Simkl.Enabled() {
		return out, errors.New("Simkl needs a client ID (Settings)")
	}
	a, err := s.DB.GetSimkl(ctx, userID)
	if err != nil || a == nil {
		return out, errors.New("link a Simkl account first (Account settings)")
	}
	for _, kind := range []string{"movies", "shows", "anime"} {
		l, err := s.Simkl.Watchlist(ctx, a.AccessToken, kind, status)
		if errors.Is(err, simkl.ErrUnauthorized) && a.RefreshToken != "" && s.refresh(ctx, a) == nil {
			l, err = s.Simkl.Watchlist(ctx, a.AccessToken, kind, status)
		}
		if err != nil {
			return out, err
		}
		out.Movies = append(out.Movies, l.Movies...)
		out.Shows = append(out.Shows, l.Shows...)
		out.Anime = append(out.Anime, l.Anime...)
	}
	return out, nil
}
