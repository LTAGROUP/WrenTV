package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Keyframes returns sorted video keyframe timestamps (seconds). For Matroska
// it reads the Cues index (a few small reads); otherwise it falls back to
// scanning packets with ffprobe.
func Keyframes(ctx context.Context, ffprobe, input string, r io.ReaderAt, size int64) ([]float64, error) {
	if r != nil {
		if kf, err := MatroskaCues(r, size); err == nil && len(kf) > 1 {
			return kf, nil
		}
	}
	return ffprobeKeyframes(ctx, ffprobe, input)
}

func ffprobeKeyframes(ctx context.Context, ffprobe, input string) ([]float64, error) {
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", input)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var kf []float64
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		ts, flags, ok := strings.Cut(sc.Text(), ",")
		if !ok || !strings.HasPrefix(flags, "K") {
			continue
		}
		if t, err := strconv.ParseFloat(ts, 64); err == nil {
			kf = append(kf, t)
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffprobe keyframes: %w", err)
	}
	sort.Float64s(kf)
	return kf, nil
}

// ---------- minimal EBML / Matroska reader ----------

const (
	idEBML        = 0x1A45DFA3
	idSegment     = 0x18538067
	idSeekHead    = 0x114D9B74
	idSeek        = 0x4DBB
	idSeekID      = 0x53AB
	idSeekPos     = 0x53AC
	idInfo        = 0x1549A966
	idTimescale   = 0x2AD7B1
	idTracks      = 0x1654AE6B
	idTrackEntry  = 0xAE
	idTrackNumber = 0xD7
	idTrackType   = 0x83
	idCues        = 0x1C53BB6B
	idCuePoint    = 0xBB
	idCueTime     = 0xB3
	idCueTrackPos = 0xB7
	idCueTrack    = 0xF7
	idCluster     = 0x1F43B675
)

var errEBML = errors.New("ebml: malformed")

// blockSize covers the EBML header, SeekHead, Info and Tracks of a typical
// file in one read.
const blockSize = 256 << 10

// blockReader serves small reads from cached blocks; reads larger than a
// block (the Cues element) go straight through.
type blockReader struct {
	r      io.ReaderAt
	size   int64
	blocks map[int64][]byte
}

func (b *blockReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) > blockSize {
		return b.r.ReadAt(p, off)
	}
	n := 0
	for n < len(p) {
		pos := off + int64(n)
		if pos >= b.size {
			return n, io.EOF
		}
		start := pos - pos%blockSize
		blk, ok := b.blocks[start]
		if !ok {
			blk = make([]byte, min(blockSize, b.size-start))
			m, err := b.r.ReadAt(blk, start)
			if m < len(blk) {
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				return n, err
			}
			b.blocks[start] = blk
		}
		n += copy(p[n:], blk[pos-start:])
	}
	return n, nil
}

type ebmlReader struct {
	r    io.ReaderAt
	size int64
}

// readVint reads an EBML variable-length integer at off. If keepMarker, the
// length marker bit is kept (element IDs); otherwise it is masked (sizes).
func (e *ebmlReader) readVint(off int64, keepMarker bool) (val uint64, n int, unknown bool, err error) {
	var b [8]byte
	if _, err = e.r.ReadAt(b[:1], off); err != nil {
		return
	}
	first := b[0]
	n = 1
	for mask := byte(0x80); n <= 8 && first&mask == 0; mask >>= 1 {
		n++
	}
	if n > 8 {
		return 0, 0, false, errEBML
	}
	if n > 1 {
		if _, err = e.r.ReadAt(b[1:n], off+1); err != nil {
			return
		}
	}
	if keepMarker {
		val = uint64(first)
	} else {
		val = uint64(first & (0xFF >> n))
	}
	allOnes := val == uint64(0xFF>>n)
	for i := 1; i < n; i++ {
		val = val<<8 | uint64(b[i])
		if b[i] != 0xFF {
			allOnes = false
		}
	}
	return val, n, !keepMarker && allOnes, nil
}

type element struct {
	id      uint64
	dataOff int64
	size    int64 // -1 if unknown
	end     int64
}

func (e *ebmlReader) element(off int64) (element, error) {
	id, n1, _, err := e.readVint(off, true)
	if err != nil {
		return element{}, err
	}
	sz, n2, unknown, err := e.readVint(off+int64(n1), false)
	if err != nil {
		return element{}, err
	}
	el := element{id: id, dataOff: off + int64(n1+n2), size: int64(sz)}
	if unknown {
		el.size = -1
		el.end = e.size
	} else {
		el.end = el.dataOff + el.size
	}
	return el, nil
}

func (e *ebmlReader) readUint(el element) (uint64, error) {
	if el.size > 8 || el.size < 0 {
		return 0, errEBML
	}
	var b [8]byte
	if _, err := e.r.ReadAt(b[8-el.size:], el.dataOff); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}

// children iterates the direct children of [start,end).
func (e *ebmlReader) children(start, end int64, fn func(el element) (stop bool, err error)) error {
	off := start
	for off < end {
		el, err := e.element(off)
		if err != nil {
			return err
		}
		stop, err := fn(el)
		if err != nil || stop {
			return err
		}
		if el.size < 0 {
			return nil // unknown-size element: cannot skip
		}
		off = el.end
	}
	return nil
}

// MatroskaCues returns the keyframe times of the first video track from the Cues element.
func MatroskaCues(r io.ReaderAt, size int64) ([]float64, error) {
	// Element headers are read a few bytes at a time. Over a remote source
	// every read is an HTTP request, and a debrid CDN refuses hundreds of
	// them in a row, so read through whole blocks instead.
	r = &blockReader{r: r, size: size, blocks: map[int64][]byte{}}
	e := &ebmlReader{r: r, size: size}
	hdr, err := e.element(0)
	if err != nil || hdr.id != idEBML {
		return nil, errEBML
	}
	seg, err := e.element(hdr.end)
	if err != nil || seg.id != idSegment {
		return nil, errEBML
	}
	segData := seg.dataOff
	segEnd := seg.end
	if segEnd > size {
		segEnd = size
	}

	positions := map[uint64]int64{}
	var parseSeekHead func(el element) error
	parseSeekHead = func(el element) error {
		return e.children(el.dataOff, el.end, func(s element) (bool, error) {
			if s.id != idSeek {
				return false, nil
			}
			var sid uint64
			var spos int64 = -1
			_ = e.children(s.dataOff, s.end, func(c element) (bool, error) {
				switch c.id {
				case idSeekID:
					var b [4]byte
					if c.size <= 4 {
						if _, err := r.ReadAt(b[4-c.size:], c.dataOff); err == nil {
							sid = uint64(binary.BigEndian.Uint32(b[:]))
						}
					}
				case idSeekPos:
					v, err := e.readUint(c)
					if err == nil {
						spos = int64(v)
					}
				}
				return false, nil
			})
			if spos >= 0 {
				if _, seen := positions[sid]; !seen || sid != idSeekHead {
					positions[sid] = segData + spos
				}
			}
			return false, nil
		})
	}

	// Walk top-level elements until the first Cluster, collecting SeekHead/Info/Tracks.
	var infoEl, tracksEl, cuesEl *element
	err = e.children(segData, segEnd, func(el element) (bool, error) {
		switch el.id {
		case idSeekHead:
			_ = parseSeekHead(el)
		case idInfo:
			c := el
			infoEl = &c
		case idTracks:
			c := el
			tracksEl = &c
		case idCues:
			c := el
			cuesEl = &c
		case idCluster:
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	// A secondary SeekHead (often at the end) may hold the Cues position.
	if pos, ok := positions[idSeekHead]; ok && positions[idCues] == 0 {
		if el, err := e.element(pos); err == nil && el.id == idSeekHead {
			_ = parseSeekHead(el)
		}
	}
	load := func(id uint64, cur *element) *element {
		if cur != nil {
			return cur
		}
		if pos, ok := positions[id]; ok {
			if el, err := e.element(pos); err == nil && el.id == id {
				return &el
			}
		}
		return nil
	}
	infoEl = load(idInfo, infoEl)
	tracksEl = load(idTracks, tracksEl)
	cuesEl = load(idCues, cuesEl)
	if cuesEl == nil {
		return nil, errors.New("matroska: no cues")
	}

	timescale := uint64(1_000_000)
	if infoEl != nil {
		_ = e.children(infoEl.dataOff, infoEl.end, func(c element) (bool, error) {
			if c.id == idTimescale {
				if v, err := e.readUint(c); err == nil && v > 0 {
					timescale = v
				}
				return true, nil
			}
			return false, nil
		})
	}
	videoTrack := uint64(0)
	if tracksEl != nil {
		_ = e.children(tracksEl.dataOff, tracksEl.end, func(te element) (bool, error) {
			if te.id != idTrackEntry {
				return false, nil
			}
			var num, typ uint64
			_ = e.children(te.dataOff, te.end, func(c element) (bool, error) {
				switch c.id {
				case idTrackNumber:
					num, _ = e.readUint(c)
				case idTrackType:
					typ, _ = e.readUint(c)
				}
				return false, nil
			})
			if typ == 1 {
				videoTrack = num
				return true, nil
			}
			return false, nil
		})
	}

	// Read the whole Cues element in one go; it is small (KBs to a few MB).
	if cuesEl.size <= 0 || cuesEl.size > 64<<20 {
		return nil, errors.New("matroska: bad cues size")
	}
	buf := make([]byte, cuesEl.size)
	if _, err := r.ReadAt(buf, cuesEl.dataOff); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	be := &ebmlReader{r: bytes.NewReader(buf), size: int64(len(buf))}
	var times []float64
	err = be.children(0, int64(len(buf)), func(cp element) (bool, error) {
		if cp.id != idCuePoint {
			return false, nil
		}
		var t uint64
		matches := videoTrack == 0
		_ = be.children(cp.dataOff, cp.end, func(c element) (bool, error) {
			switch c.id {
			case idCueTime:
				t, _ = be.readUint(c)
			case idCueTrackPos:
				_ = be.children(c.dataOff, c.end, func(tp element) (bool, error) {
					if tp.id == idCueTrack {
						if n, _ := be.readUint(tp); n == videoTrack {
							matches = true
						}
					}
					return false, nil
				})
			}
			return false, nil
		})
		if matches {
			times = append(times, float64(t)*float64(timescale)/1e9)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	sort.Float64s(times)
	// Dedupe.
	out := times[:0]
	for i, t := range times {
		if i == 0 || t-out[len(out)-1] > 0.0005 {
			out = append(out, t)
		}
	}
	return out, nil
}
