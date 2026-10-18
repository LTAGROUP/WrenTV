package usenet

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path"
	"strings"
)

// A post that lost a few articles is still worth playing: each missing
// article is filled with zeros, which costs a glitch where it was. Past
// these limits the post is too damaged and reads fail with ErrMissing.
const (
	maxHoles   = 10 // per file
	maxHoleRun = 4  // articles in a row
)

// voidWindow is how far either side of a hole is read to find the
// Matroska elements around it.
const voidWindow = 4 << 20

// hole is a missing article that was filled. In a Matroska file the zeros
// are bridged with a Void element, so players skip them instead of
// stopping on bytes that aren't an element.
type hole struct {
	done  bool
	at    int64 // where patch goes, in the video
	patch []byte
}

// fillHole returns zeros for a missing article, or false when it can't be
// filled: filling is off, it holds the video's first bytes, or the file
// has too many holes.
func (f *File) fillHole(k segKey) ([]byte, bool) {
	if !f.Fill {
		return nil, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holes[k] == nil {
		if f.isStart(k) || len(f.holes) >= maxHoles || f.holeRun(k) > maxHoleRun {
			return nil, false
		}
		if f.holes == nil {
			f.holes = map[segKey]*hole{}
		}
		f.holes[k] = &hole{}
		slog.Warn("usenet article missing, filling the gap", "file", f.lay.Name, "article", k.seg+1, "holes", len(f.holes))
	}
	fl := f.lay.Files[k.file]
	n := min(fl.PartSize, fl.Size-int64(k.seg)*fl.PartSize)
	return make([]byte, max(n, 0)), true
}

// isStart says whether an article holds the video's first bytes: its
// container header, which no player gets past as zeros.
func (f *File) isStart(k segKey) bool {
	if len(f.lay.Pieces) == 0 {
		return false
	}
	pc := f.lay.Pieces[0]
	return k.file == pc.File && int64(k.seg) == pc.Off/f.lay.Files[pc.File].PartSize
}

// holeRun is how many holes in a row k makes, counting within one NZB
// file. Holes either side of a RAR volume boundary are counted apart.
func (f *File) holeRun(k segKey) int {
	n := 1
	for s := k.seg - 1; s >= 0 && f.holes[segKey{k.file, s}] != nil; s-- {
		n++
	}
	for s := k.seg + 1; f.holes[segKey{k.file, s}] != nil; s++ {
		n++
	}
	return n
}

func (f *File) isHole(k segKey) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holes[k] != nil
}

// patchHole puts a hole's Void header over the bytes of a read, buf being
// the video from off.
func (f *File) patchHole(ctx context.Context, k segKey, buf []byte, off int64) {
	f.mu.Lock()
	h := f.holes[k]
	f.mu.Unlock()
	if h == nil {
		return
	}
	f.holeMu.Lock()
	if !h.done {
		at, patch, err := f.voidPatch(ctx, k)
		// A read that went away is no verdict; the next one tries again.
		if err == nil || ctx.Err() == nil {
			h.done, h.at, h.patch = true, at, patch
			if patch != nil {
				slog.Info("usenet gap bridged with a Matroska void", "file", f.lay.Name, "article", k.seg+1, "at", at)
			} else {
				slog.Info("usenet gap left as zeros", "file", f.lay.Name, "article", k.seg+1)
			}
		}
	}
	at, patch := h.at, h.patch
	f.holeMu.Unlock()
	for i, b := range patch {
		if p := at + int64(i) - off; p >= 0 && p < int64(len(buf)) {
			buf[p] = b
		}
	}
}

// videoRange is where an article's bytes are in the video.
func (f *File) videoRange(k segKey) (from, to int64, ok bool) {
	fl := f.lay.Files[k.file]
	a := int64(k.seg) * fl.PartSize
	b := min(a+fl.PartSize, fl.Size)
	pos := int64(0)
	for _, pc := range f.lay.Pieces {
		if pc.File == k.file {
			lo, hi := max(a, pc.Off), min(b, pc.Off+pc.Len)
			if lo < hi {
				return pos + lo - pc.Off, pos + hi - pc.Off, true
			}
		}
		pos += pc.Len
	}
	return 0, 0, false
}

// voidPatch works out the Void element that bridges a hole: it follows the
// elements before the hole to the first one that starts in it, and finds
// where they pick up again after it. A nil patch leaves the zeros as they
// are, as for files that aren't Matroska.
func (f *File) voidPatch(ctx context.Context, k segKey) (int64, []byte, error) {
	switch strings.ToLower(path.Ext(f.lay.Name)) {
	case ".mkv", ".mka", ".webm":
	default:
		return 0, nil, nil
	}
	vs, ve, ok := f.videoRange(k)
	if !ok {
		return 0, nil, nil
	}
	base := max(vs-voidWindow, 0)
	before := make([]byte, vs-base)
	if _, err := f.read(ctx, before, base, false); err != nil {
		return 0, nil, err
	}
	after := make([]byte, min(voidWindow, f.lay.Size-ve))
	if n, err := f.read(ctx, after, ve, false); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, err
	} else {
		after = after[:n]
	}
	p, ok := boundaryInto(before, base, vs)
	if !ok || p >= ve {
		return 0, nil, nil // lost, or the hole is inside one element
	}
	for q := max(ve, p+2); q < ve+int64(len(after)); q++ {
		if ebmlChain(after, int(q-ve), 3) {
			if hdr := voidHeader(q-p, ve-p); hdr != nil {
				return p, hdr, nil
			}
			return 0, nil, nil
		}
	}
	return 0, nil, nil
}

// boundaryInto finds the elements in data (the video from base) and
// follows them to the first that starts at or after end.
func boundaryInto(data []byte, base, end int64) (int64, bool) {
	for x := 0; x < len(data); x++ {
		if !ebmlChain(data, x, 3) {
			continue
		}
		pos, ok := x, true
		for base+int64(pos) < end {
			next, valid := ebmlNext(data, pos)
			if !valid {
				ok = false
				break
			}
			pos = next
		}
		if ok {
			return base + int64(pos), true
		}
	}
	return 0, false
}

// ebmlChain says whether n elements in a row start at data[x].
func ebmlChain(data []byte, x, n int) bool {
	for i := 0; i < n; i++ {
		next, ok := ebmlNext(data, x)
		if !ok {
			return false
		}
		x = next
	}
	return true
}

// Matroska element IDs that turn up between clusters and blocks.
var (
	ebmlLeaf = map[byte]bool{
		0xA3: true, // SimpleBlock
		0xA0: true, // BlockGroup
		0xE7: true, // Timestamp
		0xAB: true, // PrevSize
		0xA7: true, // Position
		0xBF: true, // CRC-32
		0xEC: true, // Void
	}
	ebmlTop = map[uint32]bool{
		0x1C53BB6B: true, // Cues
		0x1254C367: true, // Tags
		0x1043A770: true, // Chapters
		0x1941A469: true, // Attachments
		0x114D9B74: true, // SeekHead
		0x1549A966: true, // Info
		0x1654AE6B: true, // Tracks
	}
)

const (
	ebmlCluster = 0x1F43B675
	ebmlSegment = 0x18538067
)

// ebmlNext reads the element at data[x] and returns where the next one
// starts: inside it for a cluster, after it for anything else. ok is false
// for bytes that don't look like an element, or one that runs past data.
func ebmlNext(data []byte, x int) (int, bool) {
	if x < 0 || x >= len(data) {
		return 0, false
	}
	var id uint32
	var idLen int
	switch b := data[x]; {
	case ebmlLeaf[b]:
		id, idLen = uint32(b), 1
	case x+4 <= len(data):
		id = uint32(data[x])<<24 | uint32(data[x+1])<<16 | uint32(data[x+2])<<8 | uint32(data[x+3])
		idLen = 4
		if id != ebmlCluster && id != ebmlSegment && !ebmlTop[id] {
			return 0, false
		}
	default:
		return 0, false
	}
	size, sizeLen, unknown, ok := ebmlSize(data, x+idLen)
	if !ok {
		return 0, false
	}
	payload := x + idLen + sizeLen
	switch id {
	case ebmlCluster, ebmlSegment:
		return payload, true
	}
	if unknown {
		return 0, false
	}
	switch id {
	case 0xA3, 0xA0:
		// A block starts with its track number, a one-byte number here.
		if size < 4 || size > 64<<20 || payload >= len(data) || (id == 0xA3 && data[payload]&0x80 == 0) {
			return 0, false
		}
	case 0xE7, 0xAB, 0xA7:
		if size < 1 || size > 8 {
			return 0, false
		}
	case 0xBF:
		if size != 4 {
			return 0, false
		}
	case 0xEC:
		if size > 64<<20 {
			return 0, false
		}
	default:
		if size > 1<<30 {
			return 0, false
		}
	}
	return payload + int(size), true
}

// ebmlSize reads an element size at data[x].
func ebmlSize(data []byte, x int) (size int64, n int, unknown, ok bool) {
	if x >= len(data) || data[x] == 0 {
		return 0, 0, false, false
	}
	b := data[x]
	n = 1
	for mask := byte(0x80); b&mask == 0; mask >>= 1 {
		n++
	}
	if x+n > len(data) {
		return 0, 0, false, false
	}
	v := int64(b & (0xFF >> n))
	all := v == int64(0xFF>>n)
	for i := 1; i < n; i++ {
		v = v<<8 | int64(data[x+i])
		all = all && data[x+i] == 0xFF
	}
	return v, n, all, true
}

// voidHeader is the header of a Void element total bytes long, if it fits
// in room bytes.
func voidHeader(total, room int64) []byte {
	var hdr []byte
	switch {
	case total-2 >= 0 && total-2 <= 126:
		hdr = []byte{0xEC, 0x80 | byte(total-2)}
	case total >= 9:
		n := total - 9
		hdr = []byte{0xEC, 0x01, byte(n >> 48), byte(n >> 40), byte(n >> 32), byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	default:
		return nil
	}
	if int64(len(hdr)) > room {
		return nil
	}
	return hdr
}
