package usenet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
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
	// Fill zero-fills a few missing articles instead of failing; see holes.go.
	Fill bool

	holeMu sync.Mutex // held while a hole's patch is worked out

	mu    sync.Mutex
	cache map[segKey]*segEntry
	order []segKey // least recently used first
	max   int
	holes map[segKey]*hole
	// took is how long recent articles took, for telling a stuck fetch
	// from a normal one; hedges counts duplicate requests out now.
	took   [32]time.Duration
	tookN  int
	hedges int
}

type segKey struct{ file, seg int }

type segEntry struct {
	done    chan struct{}
	data    []byte
	err     error
	started time.Time
	// running counts requests out for the article: the first, and a
	// hedge. The first to bring it wins; stop then cuts the other off.
	running int
	hedged  bool
	// urgent is set once a reader waits on the article: if it hasn't got a
	// connection yet, it goes before every stream's read-ahead.
	urgent atomic.Bool
	stop   context.Context
	abort  context.CancelFunc
}

// Hedging: a read waits on articles in order, so one article that's slow
// to come (a server's occasional multi-second tail, or a request queued
// behind read-ahead) stalls the stream however far ahead the rest is.
// Once the article a reader is waiting on has taken hedgeFactor times the
// recent median, and at least hedgeFloor, it's asked for a second time,
// ahead of read-ahead, and whichever copy arrives first is used. An
// article a server says is missing is never asked for twice.
const (
	hedgeFloor  = 1500 * time.Millisecond
	hedgeFactor = 4
	// maxHedges bounds the extra requests one file has out at once.
	maxHedges = 2
)

func NewFile(pool *Pool, nzb *NZB, lay *Layout) *File {
	return &File{pool: pool, nzb: nzb, lay: lay, Ahead: 64, cache: map[segKey]*segEntry{}, max: 256}
}

func (f *File) Size() int64     { return f.lay.Size }
func (f *File) Layout() *Layout { return f.lay }

// Available checks a spread of the file's articles; see Available.
func (f *File) Available(ctx context.Context, samples int) (bool, error) {
	return Available(ctx, f.pool, f.nzb, f.lay, samples)
}

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
		var hedge <-chan time.Time
		if ctx.Value(bgKey{}) == nil {
			e.urgent.Store(true)
			t := time.NewTimer(max(f.hedgeAfter()-time.Since(e.started), 250*time.Millisecond))
			hedge = t.C
			defer t.Stop()
		}
	wait:
		for {
			select {
			case <-e.done:
				break wait
			case <-hedge:
				hedge = nil
				f.hedge(ctx, k, e)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		// Whoever started the fetch went away first; fetch it again.
		if e.err != nil && ctx.Err() == nil && (errors.Is(e.err, context.Canceled) || errors.Is(e.err, context.DeadlineExceeded)) {
			continue
		}
		return e.data, e.err
	}
}

// hedgeAfter is how long an article may take before it's asked for again.
func (f *File) hedgeAfter() time.Duration {
	f.mu.Lock()
	n := min(f.tookN, len(f.took))
	ds := slices.Clone(f.took[:n])
	f.mu.Unlock()
	if n < 8 {
		return hedgeFloor
	}
	slices.Sort(ds)
	return max(hedgeFloor, hedgeFactor*ds[n/2])
}

// hedge asks for an article a second time, urgently, unless it has come,
// is already hedged, or the file has its share of hedges out.
func (f *File) hedge(ctx context.Context, k segKey, e *segEntry) {
	f.mu.Lock()
	select {
	case <-e.done:
		f.mu.Unlock()
		return
	default:
	}
	if e.hedged || f.hedges >= maxHedges {
		f.mu.Unlock()
		return
	}
	e.hedged, e.running = true, e.running+1
	f.hedges++
	f.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(abortable(finishing(Urgent(ctx)), e.stop), 2*time.Minute)
		defer cancel()
		data, err := f.fetch(ctx, k, false)
		f.mu.Lock()
		f.hedges--
		f.mu.Unlock()
		f.finish(k, e, data, err)
	}()
}

// finish settles an article with one request's result: the first success
// wins and stops the other request; a failure counts only once no other
// request is still out.
func (f *File) finish(k segKey, e *segEntry, data []byte, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.running--
	select {
	case <-e.done:
		return
	default:
	}
	if err != nil && e.running > 0 {
		return
	}
	e.data, e.err = data, err
	close(e.done)
	e.abort()
	if err != nil && f.cache[k] == e {
		delete(f.cache, k) // let a later read try again
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
	e := &segEntry{done: make(chan struct{}), started: time.Now(), running: 1}
	e.stop, e.abort = context.WithCancel(context.Background())
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
	if ctx.Value(bgKey{}) == nil && ctx.Value(urgentKey{}) == nil {
		ctx = urgentOnce(ctx, &e.urgent)
	}
	go func() {
		ctx, cancel := context.WithTimeout(abortable(finishing(ctx), e.stop), 2*time.Minute)
		defer cancel()
		data, err := f.fetch(ctx, k, true)
		f.finish(k, e, data, err)
	}()
	return e
}

// fetch brings an article from the shared cache or the news servers; fill
// zero-fills it if it's missing (see holes.go).
func (f *File) fetch(ctx context.Context, k segKey, fill bool) ([]byte, error) {
	fl := f.lay.Files[k.file]
	nf := f.nzb.Files[fl.Index]
	if k.seg >= len(nf.Segments) {
		return nil, fmt.Errorf("usenet: article %d of %d", k.seg+1, len(nf.Segments))
	}
	id := nf.Segments[k.seg].ID
	if data, ok := f.pool.cache.Get(id); ok {
		return data, nil
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		t := time.Now()
		body, err := f.pool.Body(ctx, id)
		if err != nil {
			if errors.Is(err, ErrMissing) {
				if !fill {
					return nil, err
				}
				if zeros, ok := f.fillHole(k); ok {
					return zeros, nil
				}
				return nil, err
			}
			if stopped(ctx) {
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
		f.mu.Lock()
		f.took[f.tookN%len(f.took)] = time.Since(t)
		f.tookN++
		f.mu.Unlock()
		f.pool.cache.Put(id, p.Data)
		return p.Data, nil
	}
	return nil, last
}

// ReadAtContext reads len(p) bytes at off, fetching the articles together.
func (f *File) ReadAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	return f.read(ctx, p, off, true)
}

// read is ReadAtContext; patch false leaves filled holes as plain zeros,
// for working out the patches themselves.
func (f *File) read(ctx context.Context, p []byte, off int64, patch bool) (int, error) {
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
		m := copy(p[n:], data[s.from:s.to])
		if patch && f.isHole(s.key) {
			f.patchHole(ctx, s.key, p[n:n+m], off+int64(n))
		}
		n += m
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
	// Read-ahead starts small and doubles as the read goes on: players
	// probe with many short reads, and a seek shouldn't queue tens of
	// megabytes ahead of the bytes it's waiting for.
	// Background reads (seek thumbnails) need a few megabytes, not the
	// tens a player streams through.
	most := max(f.Ahead, 1)
	if ctx.Value(bgKey{}) != nil {
		most = min(most, 4)
	}
	ahead := min(2, most)
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
		started := 0
		// The next stretch's first articles, fetched as this one ends, so
		// read-ahead doesn't run dry at the seam.
		var next []span
		nextStarted := 0
		pos := off
		for i, s := range sp {
			if i > 0 && ahead < most {
				ahead = min(ahead*2, most)
			}
			for ; started < len(sp) && started <= i+ahead; started++ {
				f.start(ctx, sp[started].key)
			}
			if over := i + ahead - (len(sp) - 1); over > 0 && n > chunk {
				if next == nil {
					next, _ = f.spans(off+chunk, min(n-chunk, 64<<20))
				}
				for ; nextStarted < len(next) && nextStarted < over; nextStarted++ {
					f.start(ctx, next[nextStarted].key)
				}
			}
			data, err := f.segment(ctx, s.key)
			if err != nil {
				return written, err
			}
			if s.to > int64(len(data)) {
				return written, fmt.Errorf("usenet: article %d is short", s.key.seg+1)
			}
			out := data[s.from:s.to]
			if f.isHole(s.key) {
				out = slices.Clone(out)
				f.patchHole(ctx, s.key, out, pos)
			}
			pos += int64(len(out))
			m, err := w.Write(out)
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
