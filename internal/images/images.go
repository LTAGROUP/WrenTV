// Package images caches TMDB artwork on disk so clients load it from the
// server (fast on LAN, works offline once cached).
package images

import (
	"io"
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
			return nil, c.fetch("https://image.tmdb.org/t/p/"+size+path, local)
		})
		if err != nil {
			http.Error(w, "image unavailable", http.StatusBadGateway)
			return
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
	http.ServeFile(w, r, local)
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
