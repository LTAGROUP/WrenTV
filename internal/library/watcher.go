package library

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/LTAGROUP/haven/internal/db"
)

// Watcher picks up new/removed files within seconds.
//
// Two mechanisms run together:
//   - native notifications (inotify / ReadDirectoryChangesW) on Linux and
//     Windows, one watch per directory;
//   - a directory-mtime poller everywhere. Adding or removing a file bumps its
//     parent directory's mtime, so stat-ing directories (not files) finds every
//     change cheaply. This also covers macOS (where kqueue needs a handle per
//     file) and network shares (where notifications don't arrive).
type Watcher struct {
	s        *Scanner
	db       *db.DB
	interval time.Duration

	mu      sync.Mutex
	dirs    map[string]dirState // dir -> state
	pending map[string]time.Time
	fsw     *fsnotify.Watcher
}

type dirState struct {
	mtime   time.Time
	libID   string
	libRoot string
}

func NewWatcher(s *Scanner, d *db.DB, pollInterval time.Duration) *Watcher {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
		if runtime.GOOS != "darwin" {
			pollInterval = 60 * time.Second
		}
	}
	return &Watcher{s: s, db: d, interval: pollInterval, dirs: map[string]dirState{}, pending: map[string]time.Time{}}
}

func (w *Watcher) Run(ctx context.Context) {
	if runtime.GOOS != "darwin" {
		if fsw, err := fsnotify.NewWatcher(); err == nil {
			w.fsw = fsw
			go w.notifyLoop(ctx)
		} else {
			slog.Warn("filesystem notifications unavailable, polling only", "err", err)
		}
	}
	w.Refresh(ctx)
	poll := time.NewTicker(w.interval)
	flush := time.NewTicker(time.Second)
	refresh := time.NewTicker(10 * time.Minute)
	defer poll.Stop()
	defer flush.Stop()
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			if w.fsw != nil {
				w.fsw.Close()
			}
			return
		case <-poll.C:
			w.poll()
		case <-flush.C:
			w.flush(ctx)
		case <-refresh.C:
			w.Refresh(ctx)
		}
	}
}

// Refresh rebuilds the directory set from the current libraries.
func (w *Watcher) Refresh(ctx context.Context) {
	libs, err := w.db.ListLibraries(ctx)
	if err != nil {
		return
	}
	next := map[string]dirState{}
	for _, l := range libs {
		for _, root := range l.Paths {
			_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
				if err != nil || !d.IsDir() {
					return nil
				}
				if p != root && (strings.HasPrefix(d.Name(), ".") || IsIgnored(p+string(filepath.Separator)+"x")) {
					return filepath.SkipDir
				}
				info, err := d.Info()
				if err != nil {
					return nil
				}
				next[p] = dirState{mtime: info.ModTime(), libID: l.ID, libRoot: root}
				return nil
			})
		}
	}
	w.mu.Lock()
	old := w.dirs
	w.dirs = next
	w.mu.Unlock()
	if w.fsw != nil {
		for d := range next {
			if _, ok := old[d]; !ok {
				_ = w.fsw.Add(d)
			}
		}
		for d := range old {
			if _, ok := next[d]; !ok {
				_ = w.fsw.Remove(d)
			}
		}
	}
}

func (w *Watcher) poll() {
	w.mu.Lock()
	dirs := make(map[string]dirState, len(w.dirs))
	for k, v := range w.dirs {
		dirs[k] = v
	}
	w.mu.Unlock()
	for d, st := range dirs {
		info, err := os.Stat(d)
		if err != nil {
			if os.IsNotExist(err) {
				// Directory removed: rescan its parent to drop files.
				w.mark(filepath.Dir(d))
				w.mu.Lock()
				delete(w.dirs, d)
				w.mu.Unlock()
			}
			continue
		}
		if !info.ModTime().Equal(st.mtime) {
			w.mu.Lock()
			st.mtime = info.ModTime()
			w.dirs[d] = st
			w.mu.Unlock()
			w.mark(d)
		}
	}
}

func (w *Watcher) notifyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			dir := filepath.Dir(ev.Name)
			if ev.Op&fsnotify.Create != 0 {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
					dir = ev.Name
				}
			}
			w.mark(dir)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			slog.Debug("watch error", "err", err)
		}
	}
}

// mark schedules a directory for processing once it has been quiet briefly
// (a file being copied keeps changing; we wait for it to settle).
func (w *Watcher) mark(dir string) {
	w.mu.Lock()
	w.pending[dir] = time.Now()
	w.mu.Unlock()
}

func (w *Watcher) flush(ctx context.Context) {
	const settle = 3 * time.Second
	w.mu.Lock()
	var ready []string
	for d, t := range w.pending {
		if time.Since(t) >= settle {
			ready = append(ready, d)
			delete(w.pending, d)
		}
	}
	w.mu.Unlock()
	if len(ready) == 0 {
		return
	}
	libs, err := w.db.ListLibraries(ctx)
	if err != nil {
		return
	}
	for _, d := range ready {
		lib, root := libraryFor(libs, d)
		if lib == nil {
			continue
		}
		if stillCopying(d) {
			w.mark(d) // check again shortly
			continue
		}
		w.s.ScanDir(ctx, lib, root, d)
		// Track any new subdirectories.
		_ = filepath.WalkDir(d, func(p string, de os.DirEntry, err error) error {
			if err != nil || !de.IsDir() {
				return nil
			}
			w.mu.Lock()
			_, known := w.dirs[p]
			if !known {
				if info, err := de.Info(); err == nil {
					w.dirs[p] = dirState{mtime: info.ModTime(), libID: lib.ID, libRoot: root}
				}
			}
			w.mu.Unlock()
			if !known && w.fsw != nil {
				_ = w.fsw.Add(p)
			}
			return nil
		})
	}
}

// stillCopying reports whether any video directly in dir was modified in the
// last few seconds (an in-progress copy or download).
func stillCopying(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !IsVideo(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) < 5*time.Second {
			return true
		}
	}
	return false
}

func libraryFor(libs []*db.Library, dir string) (*db.Library, string) {
	var best *db.Library
	bestRoot := ""
	for _, l := range libs {
		for _, root := range l.Paths {
			if (dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))) && len(root) > len(bestRoot) {
				best, bestRoot = l, root
			}
		}
	}
	return best, bestRoot
}
