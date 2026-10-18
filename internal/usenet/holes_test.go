package usenet

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"testing"
)

// mkvClusters builds Matroska clusters of interleaved blocks and returns
// them with where each block starts.
func mkvClusters(n int) ([]byte, []int) {
	r := rand.New(rand.NewSource(7))
	var b bytes.Buffer
	b.Write([]byte{0x1a, 0x45, 0xdf, 0xa3, 0x80}) // empty EBML header
	b.Write([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	var starts []int
	for c := 0; c < n; c++ {
		b.Write([]byte{0x1F, 0x43, 0xB6, 0x75, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
		b.Write([]byte{0xE7, 0x82, byte(c >> 8), byte(c)})
		for k := 0; k < 30; k++ {
			size := 300 + r.Intn(8000)
			if k%3 == 0 {
				size = 200 + r.Intn(400) // audio
			}
			starts = append(starts, b.Len())
			b.Write([]byte{0xA3, 0x40 | byte(size>>8), byte(size)})
			payload := make([]byte, size)
			r.Read(payload)
			payload[0] = 0x81 + byte(k%3/2)
			b.Write(payload)
		}
	}
	return b.Bytes(), starts
}

// walkMKV follows the elements from the first cluster to the end, as a
// strict demuxer would, and counts the blocks it passes.
func walkMKV(t *testing.T, data []byte) (blocks, voids int) {
	t.Helper()
	pos := 17
	for pos < len(data) {
		next, ok := ebmlNext(data, pos)
		if !ok {
			t.Fatalf("no element at %d", pos)
		}
		switch data[pos] {
		case 0xA3:
			blocks++
		case 0xEC:
			voids++
		}
		pos = next
	}
	if pos != len(data) {
		t.Fatalf("elements end at %d, file at %d", pos, len(data))
	}
	return blocks, voids
}

func TestFilledHoles(t *testing.T) {
	video, starts := mkvClusters(40)
	all, _ := walkMKV(t, video)
	s := newFakeServer(t)
	const part = 20_000
	nzb := &NZB{Files: []NZBFile{s.post("Movie.mkv", video, part, `"Movie.mkv" yEnc`)}}
	pool := s.pool(2)
	defer pool.Close()
	lay := &Layout{Name: "Movie.mkv", Size: int64(len(video)),
		Files:  []FileLayout{{Index: 0, Size: int64(len(video)), PartSize: part}},
		Pieces: []Piece{{File: 0, Off: 0, Len: int64(len(video))}}}
	gone := func(segs ...int) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.gone = map[string]bool{}
		for _, g := range segs {
			s.gone[nzb.Files[0].Segments[g].ID] = true
		}
	}
	ctx := context.Background()

	// Without Fill a hole fails the read, as before.
	gone(9)
	f := NewFile(pool, nzb, lay)
	if _, err := f.WriteRange(ctx, &bytes.Buffer{}, 0, int64(len(video))); !errors.Is(err, ErrMissing) {
		t.Fatalf("err %v, want ErrMissing", err)
	}

	// Two holes: the file plays through, the blocks the holes hit are
	// skipped as voids and every other byte is as posted.
	gone(9, 20)
	f = NewFile(pool, nzb, lay)
	f.Fill = true
	var buf bytes.Buffer
	if _, err := f.WriteRange(ctx, &buf, 0, int64(len(video))); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	blocks, voids := walkMKV(t, got)
	if voids != 2 || blocks >= all || blocks < all-12 {
		t.Fatalf("%d blocks of %d, %d voids", blocks, all, voids)
	}
	for _, st := range starts {
		in := func(g int) bool { return st >= g*part-8400 && st < (g+1)*part+8400 }
		if !in(9) && !in(20) && !bytes.Equal(got[st:st+3], video[st:st+3]) {
			t.Fatalf("block at %d changed", st)
		}
	}
	// A read starting in the middle sees the same bytes.
	mid := make([]byte, 50_000)
	if _, err := f.ReadAt(mid, 9*part-10_000); err != nil || !bytes.Equal(mid, got[9*part-10_000:9*part+40_000]) {
		t.Fatalf("read in the middle: %v", err)
	}

	// The first article, or too many in a row, can't be filled.
	for _, segs := range [][]int{{0}, {10, 11, 12, 13, 14}} {
		gone(segs...)
		f = NewFile(pool, nzb, lay)
		f.Fill = true
		if _, err := f.WriteRange(ctx, &bytes.Buffer{}, 0, int64(len(video))); !errors.Is(err, ErrMissing) {
			t.Fatalf("holes %v: err %v, want ErrMissing", segs, err)
		}
	}
}
