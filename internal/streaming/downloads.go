package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
)

// Server downloads save a stream as a real file in a library (download
// mode, or an admin's "Download to server"). The scanner then picks the file
// up and it replaces the stream.

type runningDownload struct {
	cancel context.CancelFunc
	bytes  atomic.Int64
}

// DownloadLibrary is the real library a streamed item downloads into.
func (s *Service) DownloadLibrary(ctx context.Context, kind string) (*db.Library, error) {
	id := s.cfg().MovieLibrary
	if kind == "episode" {
		id = s.cfg().ShowLibrary
	}
	if id == "" {
		return nil, errors.New("choose a library for downloads in Settings → Streaming")
	}
	lib, err := s.DB.GetLibrary(ctx, id)
	if err != nil || lib.Source != "files" || len(lib.Paths) == 0 {
		return nil, errors.New("the download library is missing or has no folder")
	}
	return lib, nil
}

// QueueDownload queues a streamed movie or episode for download.
func (s *Service) QueueDownload(ctx context.Context, it *db.Item, by string) (*db.Download, error) {
	if it.Kind != "movie" && it.Kind != "episode" {
		return nil, errors.New("only movies and episodes can be downloaded")
	}
	if _, err := s.DownloadLibrary(ctx, it.Kind); err != nil {
		return nil, err
	}
	if dl, err := s.DB.ActiveDownloadFor(ctx, it.ID); err == nil {
		return dl, nil
	}
	if _, err := s.DB.FileEquivalent(ctx, it); err == nil {
		return nil, errors.New("this title is already in a library")
	}
	st, err := s.pickDownloadStream(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	title := it.Title
	if it.Kind == "episode" {
		if show, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
			title = fmt.Sprintf("%s · S%02dE%02d", show.Title, it.SeasonNumber, it.EpisodeNumber)
		}
	}
	dl := &db.Download{ItemID: it.ID, StreamID: st.ID, Title: title, Quality: st.Quality, Status: "queued", Total: st.Size, RequestedBy: by}
	if err := s.DB.InsertDownload(ctx, dl); err != nil {
		return nil, err
	}
	s.kickDownloads()
	s.Hub.Publish(library.Event{Type: "download.updated", ItemID: it.ID})
	return dl, nil
}

// pickDownloadStream prefers the configured quality, else the best stored.
func (s *Service) pickDownloadStream(ctx context.Context, itemID string) (*db.Stream, error) {
	sts, err := s.DB.StreamsForItem(ctx, itemID)
	if err != nil || len(sts) == 0 {
		return nil, ErrNoStreams
	}
	want := s.cfg().DownloadQuality
	best := sts[0]
	for _, st := range sts {
		if st.Quality == want {
			return st, nil
		}
		if HeightOf(st.Quality) > HeightOf(best.Quality) {
			best = st
		}
	}
	return best, nil
}

// CancelDownload stops a download and removes its partial file.
func (s *Service) CancelDownload(ctx context.Context, id string) error {
	dl, err := s.DB.GetDownload(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	run := s.dlRunning[id]
	s.mu.Unlock()
	if run != nil {
		run.cancel()
	}
	switch dl.Status {
	case "queued", "downloading", "failed":
		dl.Status = "canceled"
		_ = s.DB.UpdateDownload(ctx, dl)
	default:
		return s.DB.DeleteDownload(ctx, id) // clears finished entries from the list
	}
	return nil
}

// RetryDownload requeues a failed download.
func (s *Service) RetryDownload(ctx context.Context, id string) error {
	dl, err := s.DB.GetDownload(ctx, id)
	if err != nil {
		return err
	}
	if dl.Status != "failed" && dl.Status != "canceled" {
		return nil
	}
	dl.Status, dl.Error, dl.Bytes = "queued", "", 0
	if err := s.DB.UpdateDownload(ctx, dl); err != nil {
		return err
	}
	s.kickDownloads()
	return nil
}

// Downloads lists recent downloads with live progress.
func (s *Service) Downloads(ctx context.Context) ([]*db.Download, error) {
	list, err := s.DB.ListDownloads(ctx, "", 200)
	s.mu.Lock()
	for _, dl := range list {
		if run := s.dlRunning[dl.ID]; run != nil {
			dl.Bytes = run.bytes.Load()
		}
	}
	s.mu.Unlock()
	return list, err
}

func (s *Service) kickDownloads() {
	select {
	case s.dlKick <- struct{}{}:
	default:
	}
}

func (s *Service) downloadLoop(ctx context.Context) {
	_ = s.DB.ResetInterruptedDownloads(ctx)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		s.startDownloads(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.dlKick:
		case <-t.C:
		}
	}
}

func (s *Service) startDownloads(ctx context.Context) {
	if !s.Active() {
		return
	}
	limit := max(s.cfg().Limits.MaxDownloads, 1)
	s.mu.Lock()
	running := len(s.dlRunning)
	s.mu.Unlock()
	if running >= limit {
		return
	}
	queued, err := s.DB.ListDownloads(ctx, "queued", 50)
	if err != nil {
		return
	}
	// Oldest first.
	for i := len(queued) - 1; i >= 0 && running < limit; i-- {
		dl := queued[i]
		if err := s.roomFor(ctx, dl.Total); err != nil {
			dl.Status, dl.Error = "failed", err.Error()
			_ = s.DB.UpdateDownload(ctx, dl)
			s.notify("download.failed", dl)
			continue
		}
		dctx, cancel := context.WithCancel(ctx)
		run := &runningDownload{cancel: cancel}
		s.mu.Lock()
		s.dlRunning[dl.ID] = run
		s.mu.Unlock()
		running++
		go func(dl *db.Download) {
			defer func() {
				cancel()
				s.mu.Lock()
				delete(s.dlRunning, dl.ID)
				s.mu.Unlock()
				s.kickDownloads()
			}()
			s.runDownload(dctx, dl, run)
		}(dl)
	}
}

// roomFor enforces the download quota and the free-space reserve.
func (s *Service) roomFor(ctx context.Context, size int64) error {
	l := s.cfg().Limits
	if l.DownloadQuotaGB > 0 {
		used, _ := s.DB.DownloadedBytes(ctx)
		if float64(used+size) > l.DownloadQuotaGB*(1<<30) {
			return fmt.Errorf("download quota of %.0f GB reached", l.DownloadQuotaGB)
		}
	}
	return nil
}

func (s *Service) checkFree(dir string, size int64) error {
	reserve := s.cfg().Limits.MinFreeGB
	if reserve <= 0 {
		return nil
	}
	free, ok := diskFree(dir)
	if ok && float64(free-size) < reserve*(1<<30) {
		return fmt.Errorf("not enough disk space (keeping %.0f GB free)", reserve)
	}
	return nil
}

func (s *Service) runDownload(ctx context.Context, dl *db.Download, run *runningDownload) {
	fail := func(err error) {
		if ctx.Err() != nil {
			return // canceled: CancelDownload already updated it
		}
		dl.Status, dl.Error = "failed", err.Error()
		_ = s.DB.UpdateDownload(context.Background(), dl)
		s.Hub.Publish(library.Event{Type: "download.updated", ItemID: dl.ItemID})
		s.notify("download.failed", dl)
		slog.Warn("download failed", "title", dl.Title, "err", err)
	}
	it, err := s.DB.GetItem(ctx, dl.ItemID)
	if err != nil {
		fail(errors.New("the title was removed"))
		return
	}
	st, err := s.DB.GetStream(ctx, dl.StreamID)
	if err != nil {
		if st, err = s.pickDownloadStream(ctx, it.ID); err != nil {
			fail(err)
			return
		}
	}
	lib, err := s.DownloadLibrary(ctx, it.Kind)
	if err != nil {
		fail(err)
		return
	}
	root := lib.Paths[0]
	rel, err := s.downloadPath(ctx, it, st)
	if err != nil {
		fail(err)
		return
	}
	dest := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fail(err)
		return
	}
	if err := s.checkFree(filepath.Dir(dest), st.Size); err != nil {
		fail(err)
		return
	}
	dl.Status, dl.Path, dl.Error = "downloading", dest, ""
	_ = s.DB.UpdateDownload(ctx, dl)
	s.Hub.Publish(library.Event{Type: "download.updated", ItemID: it.ID})
	slog.Info("download started", "title", dl.Title, "quality", st.Quality, "to", dest)

	part := dest + ".partial"
	if err := s.fetch(ctx, st, part, run); err != nil {
		fail(err)
		return
	}
	if err := os.Rename(part, dest); err != nil {
		fail(err)
		return
	}
	info, _ := os.Stat(dest)
	dl.Status, dl.Bytes = "done", info.Size()
	dl.Total = dl.Bytes
	_ = s.DB.UpdateDownload(context.Background(), dl)
	// Index it now rather than waiting for the watcher; the file replaces the stream.
	s.Scanner.ScanDir(context.Background(), lib, root, filepath.Dir(dest))
	s.Hub.Publish(library.Event{Type: "download.updated", ItemID: it.ID})
	s.notify("download.completed", dl)
	slog.Info("download finished", "title", dl.Title, "size", dl.Bytes)
}

// fetch copies a stream to path, resuming a partial file and refreshing
// expired links.
func (s *Service) fetch(ctx context.Context, st *db.Stream, path string, run *runningDownload) error {
	global, dlLimit := s.limiters()
	t := throttle{global, dlLimit}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for attempt := 0; ; attempt++ {
		off, _ := f.Seek(0, io.SeekEnd)
		run.bytes.Store(off)
		if st.Size > 0 && off >= st.Size {
			return nil
		}
		u, _, err := s.linkFor(ctx, st.ID, attempt > 0)
		if err != nil {
			return err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if off > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-")
		}
		resp, err := upstream.Do(req)
		if err == nil && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			err = fmt.Errorf("debrid CDN: %s", resp.Status)
		}
		if err == nil && off > 0 && resp.StatusCode == http.StatusOK {
			// No range support: start over.
			if err = f.Truncate(0); err == nil {
				_, err = f.Seek(0, io.SeekStart)
			}
			off = 0
		}
		if err == nil {
			_, err = io.Copy(f, &progressReader{r: &throttledReader{ctx: ctx, r: resp.Body, t: t}, n: &run.bytes, base: off})
			resp.Body.Close()
			if err == nil {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= 4 {
			return err
		}
		slog.Info("download interrupted, resuming", "stream", st.ID, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 5 * time.Second):
		}
	}
}

type progressReader struct {
	r    io.Reader
	n    *atomic.Int64
	base int64
	read int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	p.n.Store(p.base + p.read)
	return n, err
}

// downloadPath names a file the way the scanner expects, tagged with the
// TMDB id so it matches the same title:
//
//	Title (Year) {tmdb-1}/Title (Year) - 2160p.mkv
//	Show (Year) {tmdb-2}/Season 01/Show (Year) - S01E02 - 1080p.mkv
func (s *Service) downloadPath(ctx context.Context, it *db.Item, st *db.Stream) (string, error) {
	ext := extOf(st.FileName)
	if ext == "" {
		ext = ".mkv"
	}
	name := func(title string, year int) string {
		n := safeName(title)
		if year > 0 {
			n = fmt.Sprintf("%s (%d)", n, year)
		}
		return n
	}
	if it.Kind == "movie" {
		base := name(it.Title, it.Year)
		return filepath.Join(fmt.Sprintf("%s {tmdb-%d}", base, it.TMDBID), fmt.Sprintf("%s - %s%s", base, st.Quality, ext)), nil
	}
	show, err := s.DB.GetItem(ctx, it.ShowID)
	if err != nil {
		return "", err
	}
	base := name(show.Title, show.Year)
	return filepath.Join(fmt.Sprintf("%s {tmdb-%d}", base, show.TMDBID), fmt.Sprintf("Season %02d", it.SeasonNumber),
		fmt.Sprintf("%s - S%02dE%02d - %s%s", base, it.SeasonNumber, it.EpisodeNumber, st.Quality, ext)), nil
}

func safeName(s string) string {
	r := strings.NewReplacer("/", "-", `\`, "-", ":", " -", "*", "", "?", "", `"`, "", "<", "", ">", "", "|", "-")
	return strings.TrimSpace(r.Replace(s))
}
