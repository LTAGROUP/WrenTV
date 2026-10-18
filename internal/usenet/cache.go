package usenet

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Cache keeps decoded articles for every stream, keyed by message ID, so a
// rewind, a second viewer or the remuxer reopening a file doesn't fetch
// them again. Recent articles are kept in memory; with a folder set, a
// larger set is kept on disk behind it.
type Cache struct {
	mu     sync.Mutex
	mem    *lru
	disk   *lru // sizes of the files in dir; nil without one
	dir    string
	writes chan diskWrite
}

type diskWrite struct {
	id   string
	data []byte
}

// NewCache keeps up to memBytes of articles in memory and diskBytes in dir
// (no disk tier when dir is "" or diskBytes is 0).
func NewCache(memBytes int64, dir string, diskBytes int64) *Cache {
	c := &Cache{mem: newLRU(memBytes)}
	if dir != "" && diskBytes > 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Warn("usenet cache folder", "dir", dir, "err", err)
			return c
		}
		c.dir, c.disk = dir, newLRU(diskBytes)
		c.load()
		// Articles are written behind the read; a full queue drops them.
		c.writes = make(chan diskWrite, 64)
		go c.writer()
	}
	return c
}

// load lists what an earlier run left on disk, oldest first.
func (c *Cache) load() {
	type file struct {
		name string
		size int64
		mod  time.Time
	}
	var files []file
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || len(d.Name()) != 64 {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, file{d.Name(), info.Size(), info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		for _, gone := range c.disk.put(f.name, f.size) {
			os.Remove(c.path(gone))
		}
	}
}

func (c *Cache) path(name string) string { return filepath.Join(c.dir, name[:2], name) }

func diskName(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

// Get returns a cached article's data. It must not be changed.
func (c *Cache) Get(id string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	if e := c.mem.get(id); e != nil {
		c.mu.Unlock()
		return e.data, true
	}
	name := ""
	if c.disk != nil && c.disk.get(diskName(id)) != nil {
		name = diskName(id)
	}
	c.mu.Unlock()
	if name == "" {
		return nil, false
	}
	data, err := os.ReadFile(c.path(name))
	if err != nil {
		c.mu.Lock()
		c.disk.remove(name)
		c.mu.Unlock()
		return nil, false
	}
	c.mu.Lock()
	c.mem.putData(id, data)
	c.mu.Unlock()
	return data, true
}

// Put keeps an article. data must not be changed afterwards.
func (c *Cache) Put(id string, data []byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.mem.putData(id, data)
	onDisk := c.disk != nil && c.disk.get(diskName(id)) != nil
	c.mu.Unlock()
	if c.writes != nil && !onDisk {
		select {
		case c.writes <- diskWrite{id, data}:
		default:
		}
	}
}

func (c *Cache) writer() {
	for w := range c.writes {
		name := diskName(w.id)
		p := c.path(name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			continue
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, w.data, 0o644); err != nil || os.Rename(tmp, p) != nil {
			os.Remove(tmp)
			continue
		}
		c.mu.Lock()
		gone := c.disk.put(name, int64(len(w.data)))
		c.mu.Unlock()
		for _, g := range gone {
			os.Remove(c.path(g))
		}
	}
}

// lru tracks entries by size, least recently used first.
type lru struct {
	max, size int64
	order     *list.List
	items     map[string]*list.Element
}

type lruEntry struct {
	key  string
	size int64
	data []byte
}

func newLRU(max int64) *lru {
	return &lru{max: max, order: list.New(), items: map[string]*list.Element{}}
}

func (l *lru) get(key string) *lruEntry {
	el := l.items[key]
	if el == nil {
		return nil
	}
	l.order.MoveToBack(el)
	return el.Value.(*lruEntry)
}

func (l *lru) putData(key string, data []byte) {
	if int64(len(data)) > l.max {
		return
	}
	l.put(key, int64(len(data)))
	l.items[key].Value.(*lruEntry).data = data
}

// put adds or refreshes an entry and returns the keys evicted to fit it.
func (l *lru) put(key string, size int64) (evicted []string) {
	if el := l.items[key]; el != nil {
		e := el.Value.(*lruEntry)
		l.size += size - e.size
		e.size = size
		l.order.MoveToBack(el)
	} else {
		l.items[key] = l.order.PushBack(&lruEntry{key: key, size: size})
		l.size += size
	}
	for l.size > l.max && l.order.Len() > 1 {
		e := l.order.Front().Value.(*lruEntry)
		l.remove(e.key)
		evicted = append(evicted, e.key)
	}
	return evicted
}

func (l *lru) remove(key string) {
	if el := l.items[key]; el != nil {
		l.size -= el.Value.(*lruEntry).size
		l.order.Remove(el)
		delete(l.items, key)
	}
}
