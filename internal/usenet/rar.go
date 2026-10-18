package usenet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	rar4Sig = []byte("Rar!\x1a\x07\x00")
	rar5Sig = []byte("Rar!\x1a\x07\x01\x00")

	errPacked    = errors.New("archive is compressed")
	errEncrypted = errors.New("archive is password protected")
)

// rarEntry is the part of a packed file that one volume holds.
type rarEntry struct {
	Name        string
	DataOff     int64 // in the volume
	DataLen     int64
	SplitBefore bool // continues a file from the previous volume
	SplitAfter  bool
}

// rarVolumeInfo is what one volume's headers say.
type rarVolumeInfo struct {
	Entries []rarEntry
	Number  int // RAR5 volume number, -1 when unknown
}

func isRAR(head []byte) bool {
	return bytes.HasPrefix(head, rar4Sig) || bytes.HasPrefix(head, rar5Sig)
}

// readRARVolume reads the file headers of one volume. Only stored (not
// compressed), unencrypted files can be streamed.
func readRARVolume(r io.ReaderAt, size int64) (*rarVolumeInfo, error) {
	sig := make([]byte, 8)
	if _, err := r.ReadAt(sig, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	switch {
	case bytes.HasPrefix(sig, rar5Sig):
		return readRAR5(r, size)
	case bytes.HasPrefix(sig, rar4Sig):
		return readRAR4(r, size)
	}
	return nil, errors.New("not a RAR archive")
}

func readRAR4(r io.ReaderAt, size int64) (*rarVolumeInfo, error) {
	v := &rarVolumeInfo{Number: -1}
	off := int64(len(rar4Sig))
	for off+7 <= size {
		h := make([]byte, 7)
		if _, err := r.ReadAt(h, off); err != nil {
			return nil, err
		}
		typ := h[2]
		flags := binary.LittleEndian.Uint16(h[3:])
		hsize := int64(binary.LittleEndian.Uint16(h[5:]))
		if hsize < 7 {
			return nil, errors.New("RAR: bad header")
		}
		var add int64
		if flags&0x8000 != 0 || typ == 0x74 || typ == 0x7a {
			b := make([]byte, 4)
			if _, err := r.ReadAt(b, off+7); err != nil {
				return nil, err
			}
			add = int64(binary.LittleEndian.Uint32(b))
		}
		switch typ {
		case 0x73: // archive
			if flags&0x0080 != 0 {
				return nil, errEncrypted // headers are encrypted
			}
		case 0x74: // file
			hdr := make([]byte, hsize)
			if _, err := r.ReadAt(hdr, off); err != nil {
				return nil, err
			}
			if len(hdr) < 32 {
				return nil, errors.New("RAR: short file header")
			}
			pack := int64(binary.LittleEndian.Uint32(hdr[7:]))
			method := hdr[25]
			nameLen := int(binary.LittleEndian.Uint16(hdr[26:]))
			p := 32
			if flags&0x0100 != 0 && len(hdr) >= 40 {
				pack |= int64(binary.LittleEndian.Uint32(hdr[32:])) << 32
				p = 40
			}
			name := ""
			if p+nameLen <= len(hdr) {
				name = string(hdr[p : p+nameLen])
				if i := bytes.IndexByte([]byte(name), 0); i >= 0 {
					name = name[:i] // a Unicode name follows; the ASCII one will do
				}
			}
			if flags&0x0004 != 0 {
				return nil, errEncrypted
			}
			if method != 0x30 {
				return nil, errPacked
			}
			if flags&0x00e0 == 0x00e0 { // directory
				break
			}
			v.Entries = append(v.Entries, rarEntry{Name: name, DataOff: off + hsize, DataLen: pack,
				SplitBefore: flags&0x01 != 0, SplitAfter: flags&0x02 != 0})
			add = pack
		case 0x7b: // end of archive
			return v, nil
		}
		off += hsize + add
	}
	return v, nil
}

// vint reads RAR5's variable-length integer.
func vint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

func readRAR5(r io.ReaderAt, size int64) (*rarVolumeInfo, error) {
	v := &rarVolumeInfo{Number: -1}
	off := int64(len(rar5Sig))
	for off+7 <= size {
		pre := make([]byte, 4+10)
		n, err := r.ReadAt(pre, off)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		hsize, k := vint(pre[4:n])
		if k == 0 || hsize == 0 || hsize > 2<<20 {
			return nil, errors.New("RAR: bad header")
		}
		start := off + 4 + int64(k) // header fields begin here
		hdr := make([]byte, hsize)
		if _, err := r.ReadAt(hdr, start); err != nil {
			return nil, err
		}
		p := 0
		next := func() uint64 {
			x, k := vint(hdr[p:])
			p += k
			if k == 0 {
				p = len(hdr)
			}
			return x
		}
		typ := next()
		hflags := next()
		var extraSize, dataSize uint64
		if hflags&0x01 != 0 {
			extraSize = next()
		}
		if hflags&0x02 != 0 {
			dataSize = next()
		}
		dataOff := start + int64(hsize)
		switch typ {
		case 1: // main
			aflags := next()
			if aflags&0x02 != 0 {
				v.Number = int(next())
			} else if aflags&0x01 != 0 {
				v.Number = 0
			}
		case 4: // encryption
			return nil, errEncrypted
		case 2: // file
			fflags := next()
			next() // unpacked size
			next() // attributes
			if fflags&0x02 != 0 {
				p += 4
			}
			if fflags&0x04 != 0 {
				p += 4
			}
			comp := next()
			next() // host os
			nameLen := int(next())
			name := ""
			if p+nameLen <= len(hdr) {
				name = string(hdr[p : p+nameLen])
			}
			if extraSize > 0 && rar5Encrypted(hdr[len(hdr)-int(min(extraSize, uint64(len(hdr)))):]) {
				return nil, errEncrypted
			}
			if (comp>>7)&7 != 0 {
				return nil, errPacked
			}
			if fflags&0x01 == 0 { // not a directory
				v.Entries = append(v.Entries, rarEntry{Name: name, DataOff: dataOff, DataLen: int64(dataSize),
					SplitBefore: hflags&0x08 != 0, SplitAfter: hflags&0x10 != 0})
			}
		case 5: // end of archive
			return v, nil
		}
		off = dataOff + int64(dataSize)
	}
	return v, nil
}

// rar5Encrypted looks for an encryption record in a file header's extra area.
func rar5Encrypted(extra []byte) bool {
	for len(extra) > 0 {
		size, k := vint(extra)
		if k == 0 || size == 0 || int(size)+k > len(extra) {
			return false
		}
		rec := extra[k : k+int(size)]
		if typ, _ := vint(rec); typ == 1 {
			return true
		}
		extra = extra[k+int(size):]
	}
	return false
}

// joinRAR puts a file split across volumes back together.
type rarFile struct {
	Name   string
	Pieces []Piece // File is the volume's position in the set
	Size   int64
}

func joinRAR(vols []*rarVolumeInfo) ([]rarFile, error) {
	var out []rarFile
	var cur *rarFile
	for vi, v := range vols {
		for _, e := range v.Entries {
			if e.SplitBefore {
				if cur == nil || cur.Name != e.Name {
					return nil, fmt.Errorf("RAR: volume %d continues %q, which didn't start", vi+1, e.Name)
				}
			} else {
				if cur != nil {
					out = append(out, *cur)
				}
				cur = &rarFile{Name: e.Name}
			}
			cur.Pieces = append(cur.Pieces, Piece{File: vi, Off: e.DataOff, Len: e.DataLen})
			cur.Size += e.DataLen
			if !e.SplitAfter {
				out = append(out, *cur)
				cur = nil
			}
		}
	}
	if cur != nil {
		return nil, fmt.Errorf("RAR: %q is missing its last volume", cur.Name)
	}
	return out, nil
}
