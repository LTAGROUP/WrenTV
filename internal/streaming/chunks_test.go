package streaming

import (
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDebridSourceChunkCache(t *testing.T) {
	data := make([]byte, 3*chunkRun*chunkSize+12345)
	rand.New(rand.NewSource(1)).Read(data)
	var requests atomic.Int64
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.ServeContent(w, r, "film.mkv", time.Time{}, bytes.NewReader(data))
	}))
	defer cdn.Close()

	src := &debridSource{svc: &Service{}, streamID: "s", size: int64(len(data)), name: "film.mkv", url: cdn.URL}
	defer src.Close()
	read := func(start, end int64, bg bool) []byte {
		t.Helper()
		target := "/internal/stream/x"
		if bg {
			target += "?bg=1"
		}
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		rec := httptest.NewRecorder()
		src.ServeInternal(rec, req)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("range %d-%d: status %d", start, end, rec.Code)
		}
		got := rec.Body.Bytes()
		if !bytes.Equal(got, data[start:end+1]) {
			t.Fatalf("range %d-%d: wrong bytes (%d of %d)", start, end, len(got), end-start+1)
		}
		return got
	}

	// Before upstream has said how big the file is, reads go straight through.
	read(0, 4095, false)
	if src.cache() == nil {
		t.Fatal("no cache once upstream answered")
	}

	// A segment's video and audio runs read the same bytes: the second is
	// served from the cache.
	mid := int64(chunkRun*chunkSize + 777)
	read(mid, mid+3*chunkSize, false)
	before := requests.Load()
	read(mid+100, mid+2*chunkSize, false)
	if requests.Load() != before {
		t.Fatalf("a cached range went upstream again (%d requests)", requests.Load()-before)
	}

	// The end of the file, shorter than a chunk.
	n := int64(len(data))
	read(n-5000, n-1, false)

	// Background reads bypass the cache.
	before = requests.Load()
	read(mid, mid+10, true)
	if requests.Load() != before+1 {
		t.Fatal("a background read didn't go upstream")
	}

	// Reading through the whole file keeps at most chunksKept chunks.
	read(0, n-1, false)
	src.mu.Lock()
	kept := len(src.chunks.chunks)
	src.mu.Unlock()
	if kept > chunksKept+2*chunkRun {
		t.Fatalf("%d chunks kept", kept)
	}
}

func TestChunkCacheRetriesAfterFailure(t *testing.T) {
	data := make([]byte, 2*chunkSize)
	var fail atomic.Bool
	fail.Store(true)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-0" && fail.Load() {
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		http.ServeContent(w, r, "film.mkv", time.Time{}, bytes.NewReader(data))
	}))
	defer cdn.Close()
	src := &debridSource{svc: &Service{}, streamID: "s", size: int64(len(data)), name: "film.mkv", url: cdn.URL}
	defer src.Close()
	if err := src.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := src.cache()
	var buf bytes.Buffer
	if err := c.writeRange(t.Context(), &buf, 0, 100); err == nil {
		t.Fatal("expected the failing upstream's error")
	}
	fail.Store(false)
	buf.Reset()
	if err := c.writeRange(t.Context(), &buf, 0, 100); err != nil || buf.Len() != 101 {
		t.Fatalf("retry: %v (%d bytes)", err, buf.Len())
	}
}
