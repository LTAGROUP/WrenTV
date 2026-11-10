// Package images caches TMDB artwork on disk so clients load it from the
// server (fast on LAN, works offline once cached).
package images

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"golang.org/x/sync/singleflight"
)

type Cache struct {
	dir    string
	client *http.Client
	sf     singleflight.Group
}

func New(dir string) *Cache {
	_ = os.MkdirAll(dir, 0o755)
	return &Cache{dir: dir, client: &http.Client{Timeout: 30 * time.Second}}
}

// imageBase is TMDB's image CDN; WRENTV_TMDB_IMAGE_URL points it at a proxy
// or a test server.
var imageBase = envOr("WRENTV_TMDB_IMAGE_URL", "https://image.tmdb.org/t/p/")

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	sizes     = []string{"w92", "w154", "w185", "w300", "w342", "w500", "w780", "w1280", "original"}
	validPath = regexp.MustCompile(`^/[A-Za-z0-9_-]+\.(jpg|png|svg|webp)$`)
)

// ServeTMDB serves /images/{size}{path}, fetching from TMDB on first use.
func (c *Cache) ServeTMDB(w http.ResponseWriter, r *http.Request, size, path string) {
	if !slices.Contains(sizes, size) || !validPath.MatchString(path) {
		http.NotFound(w, r)
		return
	}
	local := filepath.Join(c.dir, size, filepath.Base(path))
	if _, err := os.Stat(local); err != nil {
		_, err, _ := c.sf.Do(local, func() (any, error) {
			if _, err := os.Stat(local); err == nil {
				return nil, nil
			}
			return nil, c.fetch(imageBase+size+path, local)
		})
		if err != nil {
			http.Error(w, "image unavailable", http.StatusBadGateway)
			return
		}
	}
	// The file's time is when it was last asked for (to the day), which is
	// what Trim goes by.
	if st, err := os.Stat(local); err == nil && time.Since(st.ModTime()) > 24*time.Hour {
		now := time.Now()
		_ = os.Chtimes(local, now, now)
	}
	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
	http.ServeFile(w, r, local)
}

// Trim keeps the cache under maxBytes by removing the artwork asked for
// longest ago (it's fetched again if wanted). It reports the bytes freed.
func (c *Cache) Trim(maxBytes int64) int64 {
	if maxBytes <= 0 {
		return 0
	}
	type file struct {
		path string
		size int64
		at   time.Time
	}
	var files []file
	var total int64
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, file{p, info.Size(), info.ModTime()})
			total += info.Size()
		}
		return nil
	})
	if total <= maxBytes {
		return 0
	}
	slices.SortFunc(files, func(a, b file) int { return a.at.Compare(b.at) })
	// Go a little under, so it isn't trimmed again straight away.
	target, freed := maxBytes*9/10, int64(0)
	for _, f := range files {
		if total-freed <= target {
			break
		}
		if os.Remove(f.path) == nil {
			freed += f.size
		}
	}
	return freed
}

func (c *Cache) fetch(url, dst string) error {
	resp, err := c.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &os.PathError{Op: "fetch", Path: url, Err: os.ErrNotExist}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, dst)
}
