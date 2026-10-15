// Package library scans folders into the database, keeps them current via
// filesystem watching, and enriches items with metadata in the background.
//
// Speed comes from doing as little as possible on the critical path:
//   - directories are walked in parallel and only new/changed files are touched
//   - items appear immediately using names parsed from the path
//   - metadata is fetched concurrently afterwards (one request per show season,
//     not per episode, all cached), and ffprobe runs lazily in a separate pool.
package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/haven/internal/db"
	"github.com/LTAGROUP/haven/internal/metadata/tmdb"
)

type Scanner struct {
	db      *db.DB
	tmdb    *tmdb.Client
	hub     *Hub
	ffprobe string

	matchWorkers int
	probeWorkers int

	// ingestMu serializes item creation so concurrent sources (watcher,
	// full scans, torrents) never create duplicate shows/seasons.
	ingestMu sync.Mutex

	scanMu   sync.Mutex
	scanning map[string]bool

	matchKick chan struct{}
	probeKick chan struct{}
}

type Options struct {
	FFprobe      string
	MatchWorkers int
	ProbeWorkers int
}

func NewScanner(d *db.DB, t *tmdb.Client, hub *Hub, o Options) *Scanner {
	return &Scanner{
		db: d, tmdb: t, hub: hub, ffprobe: o.FFprobe,
		matchWorkers: max(o.MatchWorkers, 1), probeWorkers: max(o.ProbeWorkers, 1),
		scanning:  map[string]bool{},
		matchKick: make(chan struct{}, 1),
		probeKick: make(chan struct{}, 1),
	}
}

func (s *Scanner) Hub() *Hub { return s.hub }

func kick(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Start launches the background matcher and prober.
func (s *Scanner) Start(ctx context.Context) {
	go s.matchLoop(ctx)
	go s.probeLoop(ctx)
}

type fileStat struct {
	path  string
	size  int64
	mtime int64
}

type ScanResult struct {
	Added, Updated, Removed, Unchanged int
	Duration                           time.Duration
}

// ScanLibrary does a full incremental scan of every path in the library.
func (s *Scanner) ScanLibrary(ctx context.Context, lib *db.Library) (ScanResult, error) {
	s.scanMu.Lock()
	if s.scanning[lib.ID] {
		s.scanMu.Unlock()
		return ScanResult{}, errors.New("scan already running")
	}
	s.scanning[lib.ID] = true
	s.scanMu.Unlock()
	defer func() {
		s.scanMu.Lock()
		delete(s.scanning, lib.ID)
		s.scanMu.Unlock()
	}()

	start := time.Now()
	s.hub.Publish(Event{Type: "scan.started", LibraryID: lib.ID})

	known, err := s.db.FilesForLibrary(ctx, lib.ID)
	if err != nil {
		return ScanResult{}, err
	}
	byPath := make(map[string]*db.MediaFile, len(known))
	for _, f := range known {
		byPath[f.Path] = f
	}

	var res ScanResult
	seen := make(map[string]bool, len(known))
	reachable := map[string]bool{}
	for _, root := range lib.Paths {
		if _, err := os.Stat(root); err != nil {
			// Never delete items because a share is temporarily offline.
			slog.Warn("library path unavailable, skipping", "path", root, "err", err)
			continue
		}
		reachable[root] = true
		files := walkParallel(ctx, root)
		for _, f := range files {
			seen[f.path] = true
			if ex, ok := byPath[f.path]; ok {
				if ex.Size == f.size && ex.MTime == f.mtime {
					res.Unchanged++
					continue
				}
				_ = s.db.UpdateFileStat(ctx, ex.ID, f.size, f.mtime)
				res.Updated++
				continue
			}
			if _, err := s.Ingest(ctx, lib, root, f.path, f.size, f.mtime, "", -1); err != nil {
				slog.Warn("ingest failed", "path", f.path, "err", err)
				continue
			}
			res.Added++
		}
	}
	for p, f := range byPath {
		if seen[p] {
			continue
		}
		if !underReachableRoot(p, reachable) {
			continue
		}
		_ = s.db.DeleteFile(ctx, f.ID)
		res.Removed++
	}
	if res.Removed > 0 {
		_ = s.db.PruneEmpty(ctx, lib.ID)
	}
	_ = s.db.TouchLibraryScan(ctx, lib.ID)
	res.Duration = time.Since(start)
	slog.Info("scan finished", "library", lib.Name, "added", res.Added, "updated", res.Updated, "removed", res.Removed,
		"unchanged", res.Unchanged, "took", res.Duration.Round(time.Millisecond))
	s.hub.Publish(Event{Type: "scan.finished", LibraryID: lib.ID, Message: fmt.Sprintf("%d added, %d removed", res.Added, res.Removed)})
	if res.Added+res.Updated+res.Removed > 0 {
		s.hub.Publish(Event{Type: "library.updated", LibraryID: lib.ID})
	}
	kick(s.matchKick)
	kick(s.probeKick)
	return res, nil
}

// ScanDir incrementally processes one directory (used by the watcher).
func (s *Scanner) ScanDir(ctx context.Context, lib *db.Library, root, dir string) {
	files := walkParallel(ctx, dir)
	present := map[string]bool{}
	changed := 0
	for _, f := range files {
		present[f.path] = true
		ex, err := s.db.FileByPath(ctx, f.path)
		if err == nil {
			if ex.Size != f.size || ex.MTime != f.mtime {
				_ = s.db.UpdateFileStat(ctx, ex.ID, f.size, f.mtime)
				changed++
			}
			continue
		}
		if _, err := s.Ingest(ctx, lib, root, f.path, f.size, f.mtime, "", -1); err == nil {
			changed++
		}
	}
	// Removals under this directory.
	known, _ := s.db.FilesForLibrary(ctx, lib.ID)
	prefix := dir + string(filepath.Separator)
	removed := 0
	for _, f := range known {
		if (strings.HasPrefix(f.Path, prefix) || f.Path == dir) && !present[f.Path] {
			if _, err := os.Stat(f.Path); errors.Is(err, fs.ErrNotExist) {
				_ = s.db.DeleteFile(ctx, f.ID)
				removed++
			}
		}
	}
	if removed > 0 {
		_ = s.db.PruneEmpty(ctx, lib.ID)
	}
	if changed+removed > 0 {
		slog.Info("picked up changes", "dir", dir, "changed", changed, "removed", removed)
		s.hub.Publish(Event{Type: "library.updated", LibraryID: lib.ID})
		kick(s.matchKick)
		kick(s.probeKick)
	}
}

func underReachableRoot(p string, roots map[string]bool) bool {
	for r := range roots {
		if strings.HasPrefix(p, r) {
			return true
		}
	}
	return false
}

// Ingest creates the item hierarchy for a file and attaches the file.
// root is the library folder the path is relative to (for torrents, "").
func (s *Scanner) Ingest(ctx context.Context, lib *db.Library, root, path string, size, mtime int64, torrentID string, fileIdx int) (*db.Item, error) {
	return s.IngestAs(ctx, lib, root, path, path, size, mtime, torrentID, fileIdx, nil)
}

// IngestAs is Ingest where the path used for parsing names differs from the
// stored path (torrent files are stored as torrent://id/index/path). If anchor
// is a movie the file attaches to it; if it is a show, episodes go under it
// (used when a replacement torrent must keep the same items and watch state).
func (s *Scanner) IngestAs(ctx context.Context, lib *db.Library, root, parsePath, storePath string, size, mtime int64, torrentID string, fileIdx int, anchor *db.Item) (*db.Item, error) {
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()

	var target *db.Item
	switch lib.Type {
	case "movies":
		if anchor != nil && anchor.Kind == "movie" {
			target = anchor
			break
		}
		mi := ParseMovie(root, parsePath)
		if mi.Title == "" {
			mi.Title = strings.TrimSuffix(filepath.Base(parsePath), filepath.Ext(parsePath))
		}
		key := MatchKey(mi.Title, mi.Year)
		if mi.TMDBID > 0 {
			key = fmt.Sprintf("tmdb:%d", mi.TMDBID)
		}
		it, created, err := s.db.UpsertTopLevel(ctx, &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: key, Title: mi.Title, Year: mi.Year, TMDBID: mi.TMDBID,
			SeasonNumber: -1, EpisodeNumber: -1})
		if err != nil {
			return nil, err
		}
		target = it
		if created {
			s.hub.Publish(Event{Type: "item.added", LibraryID: lib.ID, ItemID: it.ID})
		}
	case "shows":
		ep := ParseEpisode(root, parsePath)
		anchored := anchor != nil && anchor.Kind == "show"
		if !ep.OK || (ep.ShowTitle == "" && !anchored) {
			return nil, fmt.Errorf("could not determine season/episode from %q", filepath.Base(parsePath))
		}
		var show *db.Item
		if anchored {
			show = anchor
		} else {
			key := MatchKey(ep.ShowTitle, ep.ShowYear)
			if ep.ShowTMDB > 0 {
				key = fmt.Sprintf("tmdb:%d", ep.ShowTMDB)
			}
			var created bool
			var err error
			show, created, err = s.db.UpsertTopLevel(ctx, &db.Item{LibraryID: lib.ID, Kind: "show", MatchKey: key, Title: ep.ShowTitle, Year: ep.ShowYear,
				TMDBID: ep.ShowTMDB, SeasonNumber: -1, EpisodeNumber: -1})
			if err != nil {
				return nil, err
			}
			if created {
				s.hub.Publish(Event{Type: "item.added", LibraryID: lib.ID, ItemID: show.ID})
			}
		}
		season, err := s.db.FindChild(ctx, show.ID, "season", ep.Season)
		if errors.Is(err, db.ErrNotFound) {
			title := fmt.Sprintf("Season %d", ep.Season)
			if ep.Season == 0 {
				title = "Specials"
			}
			season = &db.Item{LibraryID: lib.ID, Kind: "season", ParentID: show.ID, ShowID: show.ID, Title: title,
				SortTitle: fmt.Sprintf("%04d", ep.Season), SeasonNumber: ep.Season, EpisodeNumber: -1, MatchState: show.MatchState}
			if show.MatchState == "matched" {
				season.MatchState = "pending"
			}
			if err := s.db.InsertItem(ctx, season); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		episode, err := s.db.FindChild(ctx, season.ID, "episode", ep.Episode)
		if errors.Is(err, db.ErrNotFound) {
			title := fmt.Sprintf("Episode %d", ep.Episode)
			if ep.EndEp > 0 {
				title = fmt.Sprintf("Episodes %d–%d", ep.Episode, ep.EndEp)
			}
			episode = &db.Item{LibraryID: lib.ID, Kind: "episode", ParentID: season.ID, ShowID: show.ID, Title: title,
				SortTitle: fmt.Sprintf("%04d%04d", ep.Season, ep.Episode), SeasonNumber: ep.Season, EpisodeNumber: ep.Episode}
			if err := s.db.InsertItem(ctx, episode); err != nil {
				return nil, err
			}
			// Touch the show so "recently added" surfaces it.
			_, _ = s.db.ExecContext(ctx, `UPDATE items SET added_at=? WHERE id=?`, db.Now(), show.ID)
		} else if err != nil {
			return nil, err
		}
		target = episode
	default:
		return nil, fmt.Errorf("unknown library type %q", lib.Type)
	}

	f := &db.MediaFile{ItemID: target.ID, LibraryID: lib.ID, Path: storePath, Size: size, MTime: mtime, TorrentID: torrentID, TorrentFileIndex: fileIdx}
	if err := s.db.InsertFile(ctx, f); err != nil {
		return nil, err
	}
	kick(s.matchKick)
	if torrentID == "" {
		kick(s.probeKick)
	}
	return target, nil
}

// walkParallel lists video files under root using concurrent directory reads.
func walkParallel(ctx context.Context, root string) []fileStat {
	var (
		mu    sync.Mutex
		out   []fileStat
		wg    sync.WaitGroup
		sem   = make(chan struct{}, 16)
		visit func(dir string)
	)
	visit = func(dir string) {
		defer wg.Done()
		if ctx.Err() != nil {
			return
		}
		sem <- struct{}{}
		entries, err := os.ReadDir(dir)
		<-sem
		if err != nil {
			return
		}
		var local []fileStat
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if e.IsDir() {
				if strings.HasPrefix(e.Name(), ".") || IsIgnored(p+string(filepath.Separator)+"x") {
					continue
				}
				wg.Add(1)
				go visit(p)
				continue
			}
			if !IsVideo(p) || IsIgnored(p) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			local = append(local, fileStat{path: p, size: info.Size(), mtime: info.ModTime().Unix()})
		}
		mu.Lock()
		out = append(out, local...)
		mu.Unlock()
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		if IsVideo(root) {
			return []fileStat{{path: root, size: st.Size(), mtime: st.ModTime().Unix()}}
		}
		return nil
	}
	wg.Add(1)
	visit(root)
	wg.Wait()
	return out
}
