package streaming

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// The remuxer cuts each six-second segment with its own ffmpeg runs, one
// per track, and every run opens the file again: the header, the index and
// the same interleaved bytes are read over and over. From a debrid CDN that
// costs little; from an addon that streams Usenet posts each request takes
// a second or more to answer, and playback falls behind. So what ffmpeg
// reads of an addon or debrid stream is kept in chunks, fetched a run at a
// time and a little ahead of the reader.

// chunkSize is the unit a stream is kept in.
const chunkSize = 4 << 20

// chunkRun is how many chunks one upstream request fetches.
const chunkRun = 8

// chunksKept caps the chunks one stream keeps (chunkSize each).
const chunksKept = 32

// chunkRunTime bounds one run's request, so a stalled one gives way to a retry.
const chunkRunTime = 2 * time.Minute

type chunk struct {
	ready chan struct{} // closed once data or err is set
	data  []byte
	err   error
	used  int64 // when it was last read, in ticks
}

func (c *chunk) done() bool {
	select {
	case <-c.ready:
		return true
	default:
		return false
	}
}

type chunkCache struct {
	ctx   context.Context // ends when the stream closes
	size  int64
	fetch func(ctx context.Context, rng string) (*http.Response, error)
	t     throttle

	mu     sync.Mutex
	chunks map[int64]*chunk
	tick   int64
}

func newChunkCache(ctx context.Context, size int64, t throttle, fetch func(ctx context.Context, rng string) (*http.Response, error)) *chunkCache {
	return &chunkCache{ctx: ctx, size: size, fetch: fetch, t: t, chunks: map[int64]*chunk{}}
}

func (c *chunkCache) count() int64 { return (c.size + chunkSize - 1) / chunkSize }

// get returns chunk i, fetching it (and the run after it) if it isn't kept.
// ahead also starts fetching the chunks after it, for a reader moving through.
func (c *chunkCache) get(i int64, ahead bool) *chunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tick++
	ch := c.chunks[i]
	if ch == nil {
		c.startRun(i)
		ch = c.chunks[i]
	}
	ch.used = c.tick
	if ahead {
		for j := i + 1; j <= i+chunkRun && j < c.count(); j++ {
			if c.chunks[j] == nil {
				c.startRun(j)
				break
			}
		}
	}
	c.evict()
	return ch
}

// startRun fetches up to chunkRun chunks from i that aren't kept, in one
// request. c.mu is held.
func (c *chunkCache) startRun(i int64) {
	var run []*chunk
	for j := i; j < i+chunkRun && j < c.count() && c.chunks[j] == nil; j++ {
		ch := &chunk{ready: make(chan struct{}), used: c.tick}
		c.chunks[j] = ch
		run = append(run, ch)
	}
	go c.fill(i, run)
}

func (c *chunkCache) fill(first int64, run []*chunk) {
	ctx, cancel := context.WithTimeout(c.ctx, chunkRunTime)
	defer cancel()
	start := first * chunkSize
	end := min(start+int64(len(run))*chunkSize, c.size) - 1
	k := 0
	fail := func(err error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		for j, ch := range run[k:] {
			ch.err = err
			close(ch.ready)
			// Not kept: the next read tries again.
			if c.chunks[first+int64(k+j)] == ch {
				delete(c.chunks, first+int64(k+j))
			}
		}
	}
	resp, err := c.fetch(ctx, "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
	if err != nil {
		fail(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		fail(fmt.Errorf("range request: %s", resp.Status))
		return
	}
	body := &throttledReader{ctx: ctx, r: resp.Body, t: c.t}
	for ; k < len(run); k++ {
		off := start + int64(k)*chunkSize
		buf := make([]byte, min(chunkSize, c.size-off))
		if _, err := io.ReadFull(body, buf); err != nil {
			fail(err)
			return
		}
		run[k].data = buf
		close(run[k].ready)
	}
}

// evict drops the least recently read chunks past chunksKept. Chunks still
// being fetched stay; a reader holding a dropped one keeps its bytes. c.mu
// is held.
func (c *chunkCache) evict() {
	for len(c.chunks) > chunksKept {
		var oldest int64 = -1
		for i, ch := range c.chunks {
			if ch.done() && (oldest < 0 || ch.used < c.chunks[oldest].used) {
				oldest = i
			}
		}
		if oldest < 0 {
			return
		}
		delete(c.chunks, oldest)
	}
}

// writeRange writes bytes start..end (inclusive) to w.
func (c *chunkCache) writeRange(ctx context.Context, w io.Writer, start, end int64) error {
	first := start / chunkSize
	for off := start; off <= end; {
		i := off / chunkSize
		// A read that only takes the header or the index fetches its run;
		// one that carries on into the next chunk is moving through the
		// file, so what comes after is fetched ahead of it.
		ch := c.get(i, i > first)
		select {
		case <-ch.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
		if ch.err != nil {
			return ch.err
		}
		lo := off - i*chunkSize
		hi := min(end-i*chunkSize+1, int64(len(ch.data)))
		if _, err := w.Write(ch.data[lo:hi]); err != nil {
			return err
		}
		off += hi - lo
	}
	return nil
}
