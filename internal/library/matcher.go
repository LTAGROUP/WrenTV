package library

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/LTAGROUP/haven/internal/db"
	"github.com/LTAGROUP/haven/internal/media"
	"github.com/LTAGROUP/haven/internal/metadata/tmdb"
)

// Kick wakes the matcher and prober (e.g. after a torrent is added).
func (s *Scanner) Kick() {
	kick(s.matchKick)
	kick(s.probeKick)
}

func (s *Scanner) matchLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		if s.tmdb.Enabled() {
			s.matchPending(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.matchKick:
			// Coalesce bursts (a season pack lands as dozens of files).
			time.Sleep(300 * time.Millisecond)
		case <-t.C:
		}
	}
}

func (s *Scanner) matchPending(ctx context.Context) {
	for ctx.Err() == nil {
		items, err := s.db.PendingMatches(ctx, 200)
		if err != nil || len(items) == 0 {
			break
		}
		s.parallel(ctx, items, func(it *db.Item) {
			var err error
			if it.Kind == "movie" {
				err = s.matchMovie(ctx, it)
			} else {
				err = s.matchShow(ctx, it)
			}
			if err != nil {
				slog.Debug("match failed", "title", it.Title, "err", err)
				it.MatchState = "unmatched"
				_ = s.db.UpdateItemMetadata(ctx, it)
			}
			s.hub.Publish(Event{Type: "item.updated", LibraryID: it.LibraryID, ItemID: it.ID})
		})
	}
	// New episodes of shows that are already matched.
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT e.show_id FROM items e JOIN items sh ON sh.id=e.show_id
		WHERE e.kind IN ('episode','season') AND e.match_state='pending' AND sh.match_state='matched'`)
	if err != nil {
		return
	}
	var showIDs []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			showIDs = append(showIDs, id)
		}
	}
	rows.Close()
	var shows []*db.Item
	for _, id := range showIDs {
		if sh, err := s.db.GetItem(ctx, id); err == nil {
			shows = append(shows, sh)
		}
	}
	s.parallel(ctx, shows, func(sh *db.Item) {
		s.fillSeasons(ctx, sh)
		s.hub.Publish(Event{Type: "item.updated", LibraryID: sh.LibraryID, ItemID: sh.ID})
	})
}

func (s *Scanner) parallel(ctx context.Context, items []*db.Item, fn func(*db.Item)) {
	sem := make(chan struct{}, s.matchWorkers)
	var wg sync.WaitGroup
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it *db.Item) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(it)
		}(it)
	}
	wg.Wait()
}

func (s *Scanner) matchMovie(ctx context.Context, it *db.Item) error {
	id := it.TMDBID
	if id == 0 && it.IMDBID != "" {
		id, _, _ = s.tmdb.FindIMDB(ctx, it.IMDBID)
	}
	if id == 0 {
		res, err := s.tmdb.SearchMovie(ctx, it.Title, it.Year)
		if err != nil {
			return err
		}
		best := tmdb.BestMatch(res, it.Title, it.Year)
		if best == nil && it.Year > 0 {
			// Year in the filename is sometimes off by one or wrong entirely.
			if res, err = s.tmdb.SearchMovie(ctx, it.Title, 0); err == nil {
				best = tmdb.BestMatch(res, it.Title, it.Year)
			}
		}
		if best == nil {
			return tmdb.ErrNotFound
		}
		id = best.ID
	}
	m, err := s.tmdb.Movie(ctx, id)
	if err != nil {
		return err
	}
	it.TMDBID = m.ID
	it.Title = m.Title
	it.OriginalTitle = m.OriginalTitle
	it.Overview = m.Overview
	it.Tagline = m.Tagline
	it.Year = yearOf(m.ReleaseDate, it.Year)
	it.AirDate = m.ReleaseDate
	it.RuntimeMin = m.Runtime
	it.PosterPath = m.PosterPath
	it.BackdropPath = m.BackdropPath
	it.LogoPath = m.Images.BestLogo()
	it.Rating = m.VoteAverage
	it.IMDBID = m.IMDBID
	it.ContentRating = tmdb.ContentRatingUS(m)
	it.Genres = genres(m.Genres)
	it.MatchState = "matched"
	return s.db.UpdateItemMetadata(ctx, it)
}

func (s *Scanner) matchShow(ctx context.Context, it *db.Item) error {
	id := it.TMDBID
	if id == 0 && it.IMDBID != "" {
		_, id, _ = s.tmdb.FindIMDB(ctx, it.IMDBID)
	}
	if id == 0 {
		res, err := s.tmdb.SearchTV(ctx, it.Title, it.Year)
		if err != nil {
			return err
		}
		best := tmdb.BestMatch(res, it.Title, it.Year)
		if best == nil && it.Year > 0 {
			if res, err = s.tmdb.SearchTV(ctx, it.Title, 0); err == nil {
				best = tmdb.BestMatch(res, it.Title, it.Year)
			}
		}
		if best == nil {
			return tmdb.ErrNotFound
		}
		id = best.ID
	}
	sh, err := s.tmdb.Show(ctx, id)
	if err != nil {
		return err
	}
	it.TMDBID = sh.ID
	it.Title = sh.Name
	it.OriginalTitle = sh.OriginalName
	it.Overview = sh.Overview
	it.Tagline = sh.Tagline
	it.Year = yearOf(sh.FirstAirDate, it.Year)
	it.AirDate = sh.FirstAirDate
	if len(sh.EpisodeRunTime) > 0 {
		it.RuntimeMin = sh.EpisodeRunTime[0]
	}
	it.PosterPath = sh.PosterPath
	it.BackdropPath = sh.BackdropPath
	it.LogoPath = sh.Images.BestLogo()
	it.Rating = sh.VoteAverage
	it.IMDBID = sh.ExternalIDs.IMDBID
	it.ContentRating = tmdb.ShowRatingUS(sh)
	it.Genres = genres(sh.Genres)
	it.MatchState = "matched"
	if err := s.db.UpdateItemMetadata(ctx, it); err != nil {
		return err
	}
	s.fillSeasons(ctx, it)
	return nil
}

// fillSeasons fetches each season present on disk once and fills all its episodes.
func (s *Scanner) fillSeasons(ctx context.Context, show *db.Item) {
	seasons, _, err := s.db.QueryItems(ctx, db.ItemQuery{ParentID: show.ID, Kind: "season", Sort: "episode"})
	if err != nil {
		return
	}
	for _, se := range seasons {
		data, err := s.tmdb.Season(ctx, show.TMDBID, se.SeasonNumber)
		if err != nil {
			se.MatchState = "unmatched"
			_ = s.db.UpdateItemMetadata(ctx, se)
			continue
		}
		if data.Name != "" {
			se.Title = data.Name
		}
		se.Overview, se.PosterPath, se.AirDate = data.Overview, data.PosterPath, data.AirDate
		se.Year = yearOf(data.AirDate, 0)
		se.MatchState = "matched"
		se.SortTitle = ""
		_ = s.db.UpdateItemMetadata(ctx, se)
		_, _ = s.db.ExecContext(ctx, `UPDATE items SET sort_title=? WHERE id=?`, padNum(se.SeasonNumber), se.ID)

		eps, _, err := s.db.QueryItems(ctx, db.ItemQuery{ParentID: se.ID, Kind: "episode", Sort: "episode"})
		if err != nil {
			continue
		}
		byNum := map[int]tmdb.Episode{}
		for _, e := range data.Episodes {
			byNum[e.EpisodeNumber] = e
		}
		for _, ep := range eps {
			e, ok := byNum[ep.EpisodeNumber]
			if !ok {
				ep.MatchState = "unmatched"
				_ = s.db.UpdateItemMetadata(ctx, ep)
				continue
			}
			if e.Name != "" {
				ep.Title = e.Name
			}
			ep.Overview, ep.StillPath, ep.AirDate = e.Overview, e.StillPath, e.AirDate
			ep.RuntimeMin, ep.Rating = e.Runtime, e.VoteAverage
			ep.Year = yearOf(e.AirDate, 0)
			ep.MatchState = "matched"
			_ = s.db.UpdateItemMetadata(ctx, ep)
			_, _ = s.db.ExecContext(ctx, `UPDATE items SET sort_title=? WHERE id=?`, padNum(ep.SeasonNumber)+padNum(ep.EpisodeNumber), ep.ID)
		}
	}
}

// Rematch forces a specific TMDB id onto a movie or show.
func (s *Scanner) Rematch(ctx context.Context, it *db.Item, tmdbID int) error {
	it.TMDBID = tmdbID
	if it.Kind == "movie" {
		return s.matchMovie(ctx, it)
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE items SET match_state='pending' WHERE show_id=?`, it.ID)
	return s.matchShow(ctx, it)
}

// ---------- probing ----------

func (s *Scanner) probeLoop(ctx context.Context) {
	if s.probeWorkers < 0 {
		return // background probing disabled; files are probed on first play
	}
	if s.probeWorkers == 0 {
		s.probeWorkers = 4
	}
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		s.probePending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.probeKick:
			time.Sleep(time.Second)
		case <-t.C:
		}
	}
}

func (s *Scanner) probePending(ctx context.Context) {
	for ctx.Err() == nil {
		files, err := s.db.UnprobedFiles(ctx, 64)
		if err != nil || len(files) == 0 {
			return
		}
		sem := make(chan struct{}, s.probeWorkers)
		var wg sync.WaitGroup
		failed := 0
		var mu sync.Mutex
		for _, f := range files {
			wg.Add(1)
			sem <- struct{}{}
			go func(f *db.MediaFile) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := s.ProbeFile(ctx, f, f.Path); err != nil {
					mu.Lock()
					failed++
					mu.Unlock()
					slog.Debug("probe failed", "path", f.Path, "err", err)
					// Mark probed so we don't retry forever; playback re-probes on demand.
					f.ProbedAt = db.Now()
					_ = s.db.UpdateFileProbe(ctx, f)
				}
			}(f)
		}
		wg.Wait()
		if failed == len(files) {
			return
		}
	}
}

// ProbeFile runs ffprobe on input and stores the result for file f.
func (s *Scanner) ProbeFile(ctx context.Context, f *db.MediaFile, input string) error {
	info, err := media.Probe(ctx, s.ffprobe, input)
	if err != nil {
		return err
	}
	ApplyProbe(f, info)
	return s.db.UpdateFileProbe(ctx, f)
}

// ApplyProbe copies summary fields from a probe onto a file row.
func ApplyProbe(f *db.MediaFile, info *media.Info) {
	b, _ := json.Marshal(info)
	f.Probe = string(b)
	f.Container = info.Container
	f.Duration = info.Duration
	if v := info.PrimaryVideo(); v != nil {
		f.VideoCodec, f.Width, f.Height, f.HDR, f.DVProfile = v.Codec, v.Width, v.Height, v.HDR, v.DVProfile
	}
}

func genres(g []tmdb.Genre) []string {
	out := make([]string, 0, len(g))
	for _, x := range g {
		out = append(out, x.Name)
	}
	return out
}

func yearOf(date string, fallback int) int {
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return y
		}
	}
	return fallback
}

func padNum(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}
