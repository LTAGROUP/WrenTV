package usenet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"
)

// Layout says where a video's bytes are: it's the listed runs of the NZB's
// files, one after another. It's saved, so a stream opens without looking
// at the post again.
type Layout struct {
	Name   string       `json:"name"`
	Size   int64        `json:"size"`
	Files  []FileLayout `json:"files"`
	Pieces []Piece      `json:"pieces"`
}

// FileLayout is one NZB file: its decoded size and the size of every
// article but the last.
type FileLayout struct {
	Index    int   `json:"index"` // in NZB.Files
	Size     int64 `json:"size"`
	PartSize int64 `json:"partSize"`
}

// Piece is a run of bytes of Layout.Files[File].
type Piece struct {
	File int   `json:"file"`
	Off  int64 `json:"off"`
	Len  int64 `json:"len"`
}

// File reads a video from Usenet. Articles are fetched as they're needed,
// a few ahead of the reader, and the recent ones are kept.
type File struct {
	pool *Pool
	nzb  *NZB
	lay  *Layout
	// Ahead is how many articles a sequential read fetches in advance.
	Ahead int
	// ctx is for reads that have none of their own (fileReader).
	ctx context.Context

	mu    sync.Mutex
	cache map[segKey]*segEntry
	order []segKey // least recently used first
	max   int
}

type segKey struct{ file, seg int }

type segEntry struct {
	done chan struct{}
	data []byte
	err  error
}

func NewFile(pool *Pool, nzb *NZB, lay *Layout) *File {
	return &File{pool: pool, nzb: nzb, lay: lay, Ahead: 64, cache: map[segKey]*segEntry{}, max: 256}
}

func (f *File) Size() int64     { return f.lay.Size }
func (f *File) Layout() *Layout { return f.lay }

// span is part of one article that a read needs.
type span struct {
	key      segKey
	from, to int64 // within the article's data
}

// spans lists the article runs covering [off, off+n).
func (f *File) spans(off, n int64) ([]span, error) {
	var out []span
	end := min(off+n, f.lay.Size)
	pos := int64(0)
	for _, pc := range f.lay.Pieces {
		if pos >= end {
			break
		}
		if pos+pc.Len <= off {
			pos += pc.Len
			continue
		}
		fl := f.lay.Files[pc.File]
		if fl.PartSize <= 0 {
			return nil, errors.New("usenet: bad layout")
		}
		a := pc.Off + max(off-pos, 0)
		b := pc.Off + min(end-pos, pc.Len)
		for a < b {
			seg := a / fl.PartSize
			segEnd := min((seg+1)*fl.PartSize, b)
			out = append(out, span{key: segKey{pc.File, int(seg)}, from: a - seg*fl.PartSize, to: segEnd - seg*fl.PartSize})
			a = segEnd
		}
		pos += pc.Len
	}
	return out, nil
}

// segment returns an article's data, fetching it once however many ask.
func (f *File) segment(ctx context.Context, k segKey) ([]byte, error) {
	for {
		e := f.start(ctx, k)
		select {
		case <-e.done:
			// Whoever started the fetch went away first; fetch it again.
			if e.err != nil && ctx.Err() == nil && (errors.Is(e.err, context.Canceled) || errors.Is(e.err, context.DeadlineExceeded)) {
				continue
			}
			return e.data, e.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// start begins fetching an article in the background unless it's cached.
// The fetch is dropped if ctx ends before it reaches a connection, so a
// reader that leaves (a player seeking away) doesn't keep its read-ahead
// queued for connections others need.
func (f *File) start(ctx context.Context, k segKey) *segEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.cache[k]; e != nil {
		// Every segment the remuxer makes reopens the file and reads its
		// header and index again; keeping what's used, not just what's new,
		// keeps those here.
		if i := slices.Index(f.order, k); i >= 0 && i < len(f.order)-1 {
			f.order = append(slices.Delete(f.order, i, i+1), k)
		}
		return e
	}
	e := &segEntry{done: make(chan struct{})}
	f.cache[k] = e
	f.order = append(f.order, k)
	// Drop the oldest finished articles; ones still in flight stay.
	if len(f.order) > f.max {
		keep := f.order[:0]
		extra := len(f.order) - f.max
		for _, old := range f.order {
			oe := f.cache[old]
			if oe == nil {
				continue
			}
			if extra > 0 {
				select {
				case <-oe.done:
					delete(f.cache, old)
					extra--
					continue
				default:
				}
			}
			keep = append(keep, old)
		}
		f.order = keep
	}
	go func() {
		ctx, cancel := context.WithTimeout(finishing(ctx), 2*time.Minute)
		defer cancel()
		e.data, e.err = f.fetch(ctx, k)
		close(e.done)
		if e.err != nil {
			f.mu.Lock()
			if f.cache[k] == e {
				delete(f.cache, k) // let a later read try again
			}
			f.mu.Unlock()
		}
	}()
	return e
}

func (f *File) fetch(ctx context.Context, k segKey) ([]byte, error) {
	fl := f.lay.Files[k.file]
	nf := f.nzb.Files[fl.Index]
	if k.seg >= len(nf.Segments) {
		return nil, fmt.Errorf("usenet: article %d of %d", k.seg+1, len(nf.Segments))
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		body, err := f.pool.Body(ctx, nf.Segments[k.seg].ID)
		if err != nil {
			if errors.Is(err, ErrMissing) || ctx.Err() != nil {
				return nil, err
			}
			last = err
			continue
		}
		p, err := decodeYEnc(body)
		if err != nil {
			last = err
			continue
		}
		want := int64(k.seg) * fl.PartSize
		if p.Begin != want {
			return nil, fmt.Errorf("usenet: article %d starts at %d, not %d", k.seg+1, p.Begin, want)
		}
		return p.Data, nil
	}
	return nil, last
}

// ReadAtContext reads len(p) bytes at off, fetching the articles together.
func (f *File) ReadAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	if off >= f.lay.Size {
		return 0, io.EOF
	}
	sp, err := f.spans(off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	for _, s := range sp {
		f.start(ctx, s.key)
	}
	n := 0
	for _, s := range sp {
		data, err := f.segment(ctx, s.key)
		if err != nil {
			return n, err
		}
		if s.to > int64(len(data)) {
			return n, fmt.Errorf("usenet: article %d is short", s.key.seg+1)
		}
		n += copy(p[n:], data[s.from:s.to])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *File) ReadAt(p []byte, off int64) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return f.ReadAtContext(ctx, p, off)
}

// WriteRange writes n bytes from off to w, keeping Ahead articles in flight.
func (f *File) WriteRange(ctx context.Context, w io.Writer, off, n int64) (int64, error) {
	var written int64
	for n > 0 {
		// Plan a stretch at a time; long ranges would list many articles.
		chunk := min(n, 256<<20)
		sp, err := f.spans(off, chunk)
		if err != nil {
			return written, err
		}
		if len(sp) == 0 {
			return written, io.EOF
		}
		// Read-ahead starts small and doubles as the read goes on: players
		// probe with many short reads, and a seek shouldn't queue tens of
		// megabytes ahead of the bytes it's waiting for.
		ahead, started := min(2, max(f.Ahead, 1)), 0
		for i, s := range sp {
			if i > 0 && ahead < f.Ahead {
				ahead = min(ahead*2, f.Ahead)
			}
			for ; started < len(sp) && started <= i+ahead; started++ {
				f.start(ctx, sp[started].key)
			}
			data, err := f.segment(ctx, s.key)
			if err != nil {
				return written, err
			}
			if s.to > int64(len(data)) {
				return written, fmt.Errorf("usenet: article %d is short", s.key.seg+1)
			}
			m, err := w.Write(data[s.from:s.to])
			written += int64(m)
			if err != nil {
				return written, err
			}
		}
		off += chunk
		n -= chunk
	}
	return written, nil
}

// fileReader reads one NZB file through the cache, before a Layout exists.
type fileReader struct {
	f    *File
	file int
}

func (r fileReader) ReadAt(p []byte, off int64) (int, error) {
	fl := r.f.lay.Files[r.file]
	if off >= fl.Size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off < fl.Size {
		seg := off / fl.PartSize
		ctx := r.f.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		data, err := r.f.segment(ctx, segKey{r.file, int(seg)})
		if err != nil {
			return n, err
		}
		from := off - seg*fl.PartSize
		if from >= int64(len(data)) {
			return n, fmt.Errorf("usenet: article %d is short", seg+1)
		}
		m := copy(p[n:], data[from:])
		n += m
		off += int64(m)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
