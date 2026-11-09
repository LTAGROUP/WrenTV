package usenet

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/ulikunitz/xz/lzma"
)

// 7z archives stream only when their files are stored: the Copy method,
// optionally AES-encrypted. CBC decrypts any block from the one before
// it, so an encrypted stored file can still be read from any offset.
// Posts often hide the video in a password-protected 7z split into
// .7z.001, .7z.002, … and put the password in the NZB.

var sevenZipSig = []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}

var (
	err7zPacked = errors.New("7z archive is compressed")
	err7zFormat = errors.New("7z archive can't be read")
)

const (
	k7zEnd             = 0x00
	k7zHeader          = 0x01
	k7zArchiveProps    = 0x02
	k7zAdditional      = 0x03
	k7zMainStreams     = 0x04
	k7zFilesInfo       = 0x05
	k7zPackInfo        = 0x06
	k7zUnpackInfo      = 0x07
	k7zSubStreams      = 0x08
	k7zSize            = 0x09
	k7zCRC             = 0x0A
	k7zFolder          = 0x0B
	k7zCodersUnpack    = 0x0C
	k7zNumUnpackStream = 0x0D
	k7zEmptyStream     = 0x0E
	k7zEmptyFile       = 0x0F
	k7zName            = 0x11
	k7zEncodedHeader   = 0x17
	k7zDummy           = 0x19
)

var (
	coderCopy  = []byte{0x00}
	coderAES   = []byte{0x06, 0xF1, 0x07, 0x01}
	coderLZMA  = []byte{0x03, 0x01, 0x01}
	coderLZMA2 = []byte{0x21}
)

// AES is how a layout's bytes are decrypted: AES-256-CBC with Key. The
// pieces start on a block of the encrypted stream; IV is the block
// before it, or empty when the pieces' first block is only there to
// chain from. The file starts Skip bytes into the pieces.
type AES struct {
	Key  []byte `json:"key"`
	IV   []byte `json:"iv,omitempty"`
	Skip int64  `json:"skip"`
}

// szFile is a file in a 7z archive, as a run of the archive's bytes.
type szFile struct {
	name string
	size int64
	// at is where the file's folder starts in the archive, and off where
	// the file starts within the folder once decoded.
	at, off int64
	// packed is the folder's size in the archive.
	packed int64
	aes    *AES // nil when stored as is
}

type szCoder struct {
	id         []byte
	numIn      int
	numOut     int
	props      []byte
	complexity bool
}

type szFolder struct {
	coders      []szCoder
	bindPairs   [][2]uint64
	packed      []uint64
	unpackSizes []uint64
}

func (f szFolder) unpackSize() uint64 {
	// The folder's output is the one out stream no bind pair consumes.
	bound := map[uint64]bool{}
	for _, bp := range f.bindPairs {
		bound[bp[1]] = true
	}
	for i := len(f.unpackSizes) - 1; i >= 0; i-- {
		if !bound[uint64(i)] {
			return f.unpackSizes[i]
		}
	}
	return 0
}

type szStreams struct {
	packPos    uint64
	packSizes  []uint64
	folders    []szFolder
	numStreams []uint64 // per folder
	subSizes   []uint64 // per stream, folder after folder
}

type szReader struct {
	b   []byte
	pos int
	err error
}

func (r *szReader) byte() byte {
	if r.err != nil {
		return 0
	}
	if r.pos >= len(r.b) {
		r.err = err7zFormat
		return 0
	}
	c := r.b[r.pos]
	r.pos++
	return c
}

func (r *szReader) bytes(n uint64) []byte {
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.b)-r.pos) {
		r.err = err7zFormat
		return nil
	}
	out := r.b[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out
}

// number is 7z's variable-length integer: the leading one bits of the
// first byte count the bytes that follow.
func (r *szReader) number() uint64 {
	first := r.byte()
	mask := byte(0x80)
	var v uint64
	for i := 0; i < 8; i++ {
		if first&mask == 0 {
			hi := uint64(first & (mask - 1))
			return v | hi<<(8*i)
		}
		v |= uint64(r.byte()) << (8 * i)
		mask >>= 1
	}
	return v
}

// count is a number used as a length, bounded so a bad header can't ask
// for a huge allocation.
func (r *szReader) count() int {
	n := r.number()
	if n > uint64(len(r.b)) && n > 1<<16 {
		r.err = err7zFormat
		return 0
	}
	return int(n)
}

func (r *szReader) bits(n int) []bool {
	out := make([]bool, n)
	var cur, mask byte
	for i := range out {
		if mask == 0 {
			cur, mask = r.byte(), 0x80
		}
		out[i] = cur&mask != 0
		mask >>= 1
	}
	return out
}

func (r *szReader) digests(n int) {
	all := r.byte()
	defined := make([]bool, n)
	if all != 0 {
		for i := range defined {
			defined[i] = true
		}
	} else {
		defined = r.bits(n)
	}
	for _, d := range defined {
		if d {
			r.bytes(4)
		}
	}
}

func (r *szReader) packInfo(s *szStreams) {
	s.packPos = r.number()
	n := r.count()
	for {
		switch id := r.number(); id {
		case k7zEnd:
			return
		case k7zSize:
			s.packSizes = make([]uint64, n)
			for i := range s.packSizes {
				s.packSizes[i] = r.number()
			}
		case k7zCRC:
			r.digests(n)
		default:
			r.err = err7zFormat
		}
		if r.err != nil {
			return
		}
	}
}

func (r *szReader) folder() szFolder {
	var f szFolder
	numIn, numOut := 0, 0
	for i, n := 0, r.count(); i < n && r.err == nil; i++ {
		flag := r.byte()
		c := szCoder{id: bytes.Clone(r.bytes(uint64(flag & 0x0F))), numIn: 1, numOut: 1}
		if flag&0x10 != 0 {
			c.complexity = true
			c.numIn, c.numOut = r.count(), r.count()
		}
		if flag&0x20 != 0 {
			c.props = bytes.Clone(r.bytes(r.number()))
		}
		if flag&0x80 != 0 {
			r.err = err7zFormat // alternative methods: not written since 7-Zip 4
		}
		numIn += c.numIn
		numOut += c.numOut
		f.coders = append(f.coders, c)
	}
	for i := 0; i < numOut-1 && r.err == nil; i++ {
		f.bindPairs = append(f.bindPairs, [2]uint64{r.number(), r.number()})
	}
	np := numIn - len(f.bindPairs)
	if np == 1 {
		// The in stream no bind pair feeds.
		bound := map[uint64]bool{}
		for _, bp := range f.bindPairs {
			bound[bp[0]] = true
		}
		for i := 0; i < numIn; i++ {
			if !bound[uint64(i)] {
				f.packed = []uint64{uint64(i)}
				break
			}
		}
	} else {
		for i := 0; i < np && r.err == nil; i++ {
			f.packed = append(f.packed, r.number())
		}
	}
	f.unpackSizes = make([]uint64, numOut)
	return f
}

func (r *szReader) unpackInfo(s *szStreams) {
	if r.number() != k7zFolder {
		r.err = err7zFormat
		return
	}
	n := r.count()
	if r.byte() != 0 {
		r.err = err7zFormat // folders kept elsewhere
		return
	}
	for i := 0; i < n && r.err == nil; i++ {
		s.folders = append(s.folders, r.folder())
	}
	if r.number() != k7zCodersUnpack {
		r.err = err7zFormat
		return
	}
	for i := range s.folders {
		for j := range s.folders[i].unpackSizes {
			s.folders[i].unpackSizes[j] = r.number()
		}
	}
	for r.err == nil {
		switch id := r.number(); id {
		case k7zEnd:
			return
		case k7zCRC:
			r.digests(len(s.folders))
		default:
			r.err = err7zFormat
		}
	}
}

func (r *szReader) subStreams(s *szStreams) {
	s.numStreams = make([]uint64, len(s.folders))
	for i := range s.numStreams {
		s.numStreams[i] = 1
	}
	id := r.number()
	if id == k7zNumUnpackStream {
		for i := range s.numStreams {
			s.numStreams[i] = r.number()
			if s.numStreams[i] > uint64(len(r.b)) {
				r.err = err7zFormat
				return
			}
		}
		id = r.number()
	}
	sizes := id == k7zSize
	for i, f := range s.folders {
		n := s.numStreams[i]
		if n == 0 {
			continue
		}
		var sum uint64
		for j := uint64(1); j < n; j++ {
			v := uint64(0)
			if sizes {
				v = r.number()
			}
			s.subSizes = append(s.subSizes, v)
			sum += v
		}
		total := f.unpackSize()
		if sum > total {
			r.err = err7zFormat
			return
		}
		s.subSizes = append(s.subSizes, total-sum)
	}
	if sizes {
		id = r.number()
	}
	for r.err == nil && id != k7zEnd {
		if id != k7zCRC {
			r.err = err7zFormat
			return
		}
		// Digests for the streams whose folder has no CRC of its own; the
		// count doesn't matter to us beyond skipping them.
		n := 0
		for i := range s.folders {
			n += int(s.numStreams[i])
		}
		r.digests(n)
		id = r.number()
	}
}

func (r *szReader) streamsInfo() *szStreams {
	s := &szStreams{}
	for r.err == nil {
		switch id := r.number(); id {
		case k7zEnd:
			if s.numStreams == nil {
				s.numStreams = make([]uint64, len(s.folders))
				for i, f := range s.folders {
					s.numStreams[i] = 1
					s.subSizes = append(s.subSizes, f.unpackSize())
				}
			}
			return s
		case k7zPackInfo:
			r.packInfo(s)
		case k7zUnpackInfo:
			r.unpackInfo(s)
		case k7zSubStreams:
			r.subStreams(s)
		default:
			r.err = err7zFormat
		}
	}
	return s
}

type szEntry struct {
	name   string
	stream bool
}

func (r *szReader) filesInfo() []szEntry {
	n := r.count()
	files := make([]szEntry, n)
	for i := range files {
		files[i].stream = true
	}
	for r.err == nil {
		typ := r.number()
		if typ == k7zEnd {
			break
		}
		size := r.number()
		data := r.bytes(size)
		if r.err != nil {
			break
		}
		sub := &szReader{b: data}
		switch typ {
		case k7zEmptyStream:
			for i, empty := range sub.bits(n) {
				files[i].stream = !empty
			}
		case k7zName:
			if sub.byte() != 0 {
				r.err = err7zFormat
				return nil
			}
			rest := data[1:]
			for i := range files {
				var u []uint16
				for len(rest) >= 2 {
					c := binary.LittleEndian.Uint16(rest)
					rest = rest[2:]
					if c == 0 {
						break
					}
					u = append(u, c)
				}
				files[i].name = string(utf16.Decode(u))
			}
		}
		if sub.err != nil {
			r.err = sub.err
		}
	}
	return files
}

// sevenZipKey derives 7z's AES key: SHA-256 over 2^cycles rounds of the
// salt, the UTF-16LE password and a round counter.
func sevenZipKey(password string, salt []byte, cycles int) []byte {
	pw := utf16.Encode([]rune(password))
	pb := make([]byte, 2*len(pw))
	for i, c := range pw {
		binary.LittleEndian.PutUint16(pb[2*i:], c)
	}
	if cycles == 0x3F {
		key := make([]byte, 32)
		n := copy(key, salt)
		copy(key[n:], pb)
		return key
	}
	h := sha256.New()
	buf := make([]byte, 0, len(salt)+len(pb)+8)
	var ctr [8]byte
	for i := uint64(0); i < 1<<cycles; i++ {
		binary.LittleEndian.PutUint64(ctr[:], i)
		buf = append(append(append(buf[:0], salt...), pb...), ctr[:]...)
		h.Write(buf)
	}
	return h.Sum(nil)
}

// sevenZipAES reads an AES coder's properties and derives its key.
func sevenZipAES(props []byte, password string) (*AES, error) {
	if len(props) < 1 {
		return nil, err7zFormat
	}
	b0 := props[0]
	cycles := int(b0 & 0x3F)
	var salt, iv []byte
	if b0&0xC0 != 0 {
		if len(props) < 2 {
			return nil, err7zFormat
		}
		b1 := props[1]
		saltSize := int(b0>>7&1) + int(b1>>4)
		ivSize := int(b0>>6&1) + int(b1&0x0F)
		if len(props) < 2+saltSize+ivSize {
			return nil, err7zFormat
		}
		salt = props[2 : 2+saltSize]
		iv = props[2+saltSize : 2+saltSize+ivSize]
	}
	if cycles > 24 && cycles != 0x3F {
		return nil, err7zFormat // a key that takes minutes to work out
	}
	if password == "" {
		return nil, errEncrypted
	}
	full := make([]byte, aes.BlockSize)
	copy(full, iv)
	return &AES{Key: sevenZipKey(password, salt, cycles), IV: full}, nil
}

// folderAES says how a folder decodes when it can be read in place: a
// chain of Copy coders with at most one AES among them and a single
// packed stream. Anything else (LZMA and the rest) is compressed.
func folderAES(f szFolder, password string) (*AES, error) {
	if len(f.packed) != 1 {
		return nil, err7zPacked
	}
	var out *AES
	for _, c := range f.coders {
		if c.numIn != 1 || c.numOut != 1 {
			return nil, err7zPacked
		}
		switch {
		case bytes.Equal(c.id, coderCopy):
		case bytes.Equal(c.id, coderAES) && out == nil:
			a, err := sevenZipAES(c.props, password)
			if err != nil {
				return nil, err
			}
			out = a
		default:
			return nil, err7zPacked
		}
	}
	return out, nil
}

// decryptAt decrypts whole blocks of an AES stream in place; prev is the
// block before them (the IV for the stream's first).
func decryptAt(a *AES, prev, data []byte) error {
	if len(data)%aes.BlockSize != 0 || len(prev) != aes.BlockSize {
		return errors.New("usenet: AES read isn't block aligned")
	}
	b, err := aes.NewCipher(a.Key)
	if err != nil {
		return err
	}
	cipher.NewCBCDecrypter(b, prev).CryptBlocks(data, data)
	return nil
}

// sevenZipFiles reads a 7z archive's headers through ra, an archive of
// size bytes, and lists its files.
func sevenZipFiles(ra io.ReaderAt, size int64, password string) ([]szFile, error) {
	sig := make([]byte, 32)
	if _, err := ra.ReadAt(sig, 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(sig[:6], sevenZipSig) {
		return nil, err7zFormat
	}
	off := binary.LittleEndian.Uint64(sig[12:])
	n := binary.LittleEndian.Uint64(sig[20:])
	if n == 0 || n > 64<<20 || off > uint64(size) || 32+off+n > uint64(size) {
		return nil, err7zFormat
	}
	hdr := make([]byte, n)
	if _, err := ra.ReadAt(hdr, int64(32+off)); err != nil {
		return nil, err
	}
	// An encoded header is itself packed in the archive, usually with
	// LZMA and then AES when the names are hidden.
	for depth := 0; len(hdr) > 0 && hdr[0] == k7zEncodedHeader; depth++ {
		if depth > 4 {
			return nil, err7zFormat
		}
		r := &szReader{b: hdr, pos: 1}
		s := r.streamsInfo()
		if r.err != nil {
			return nil, r.err
		}
		if len(s.folders) != 1 || len(s.packSizes) != 1 {
			return nil, err7zFormat
		}
		packed := s.packSizes[0]
		if packed > 64<<20 || 32+s.packPos+packed > uint64(size) {
			return nil, err7zFormat
		}
		data := make([]byte, packed)
		if _, err := ra.ReadAt(data, int64(32+s.packPos)); err != nil {
			return nil, err
		}
		var err error
		if hdr, err = decodeFolder(s.folders[0], data, password); err != nil {
			return nil, err
		}
	}
	r := &szReader{b: hdr}
	if r.byte() != k7zHeader {
		if errors.Is(r.err, err7zFormat) || r.err == nil {
			// What a wrong password decrypts the header to.
			if password != "" {
				return nil, fmt.Errorf("%w (or the password is wrong)", err7zFormat)
			}
			return nil, err7zFormat
		}
		return nil, r.err
	}
	var s *szStreams
	var entries []szEntry
	for r.err == nil {
		switch id := r.number(); id {
		case k7zEnd:
			return sevenZipLayout(s, entries, size, password)
		case k7zArchiveProps:
			for r.err == nil {
				if r.number() == k7zEnd {
					break
				}
				r.bytes(r.number())
			}
		case k7zAdditional:
			r.streamsInfo()
		case k7zMainStreams:
			s = r.streamsInfo()
		case k7zFilesInfo:
			entries = r.filesInfo()
		default:
			r.err = err7zFormat
		}
	}
	return nil, r.err
}

func sevenZipLayout(s *szStreams, entries []szEntry, size int64, password string) ([]szFile, error) {
	if s == nil {
		return nil, nil
	}
	var out []szFile
	stream := 0
	packAt := 32 + s.packPos
	folder, inFolder := 0, uint64(0)
	var folderOff uint64
	packIdx := 0
	folderAt := make([]uint64, len(s.folders))
	for i, f := range s.folders {
		folderAt[i] = packAt
		for range f.packed {
			if packIdx >= len(s.packSizes) {
				return nil, err7zFormat
			}
			packAt += s.packSizes[packIdx]
			packIdx++
		}
	}
	if packAt > uint64(size) {
		return nil, err7zFormat
	}
	firstPack := make([]int, len(s.folders))
	for i, p := 0, 0; i < len(s.folders); i++ {
		firstPack[i] = p
		p += len(s.folders[i].packed)
	}
	for _, e := range entries {
		if !e.stream {
			continue
		}
		for folder < len(s.folders) && inFolder >= s.numStreams[folder] {
			folder, inFolder, folderOff = folder+1, 0, 0
		}
		if folder >= len(s.folders) || stream >= len(s.subSizes) {
			return nil, err7zFormat
		}
		fsize := s.subSizes[stream]
		f := szFile{name: e.name, size: int64(fsize), at: int64(folderAt[folder]), off: int64(folderOff),
			packed: int64(s.packSizes[firstPack[folder]])}
		a, err := folderAES(s.folders[folder], password)
		switch {
		case errors.Is(err, err7zPacked):
			f.packed = -1 // listed, but can't be read in place
		case err != nil:
			return nil, err
		default:
			f.aes = a
		}
		out = append(out, f)
		stream++
		inFolder++
		folderOff += fsize
	}
	return out, nil
}

// sevenZipVolume is a 7z volume's set and number: "a.7z.003" is ("a.7z",
// 3), and a lone "a.7z" is ("a.7z", 0).
func sevenZipVolume(name string) (string, int, bool) {
	m := sevenZipVolRE.FindStringSubmatch(name)
	if m == nil {
		return "", 0, false
	}
	if m[2] == "" {
		return m[1], 0, true
	}
	n := 0
	for _, c := range m[2] {
		n = n*10 + int(c-'0')
		if n > 100000 {
			return "", 0, false
		}
	}
	return m[1], n, true
}

var sevenZipVolRE = regexp.MustCompile(`(?i)^(.*\.7z)(?:\.(\d{1,6}))?$`)

// ctxReader reads a File with a context.
type ctxReader struct {
	ctx context.Context
	f   *File
}

func (r ctxReader) ReadAt(p []byte, off int64) (int, error) { return r.f.ReadAtContext(r.ctx, p, off) }

// sevenZipVideos lists the stored videos in a post's 7z archives. files
// are the NZB files named as 7z volumes.
func sevenZipVideos(ctx context.Context, pool *Pool, nzb *NZB, files []int) ([]Video, error) {
	sets := map[string]map[int]int{}
	var order []string
	for _, i := range files {
		set, n, ok := sevenZipVolume(nzb.Files[i].Name())
		if !ok {
			continue
		}
		if sets[set] == nil {
			sets[set] = map[int]int{}
			order = append(order, set)
		}
		if _, dup := sets[set][n]; !dup {
			sets[set][n] = i
		}
	}
	var videos []Video
	var firstErr error
	for _, set := range order {
		vs, err := sevenZipSet(ctx, pool, nzb, sets[set])
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		videos = append(videos, vs...)
	}
	if len(videos) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return videos, nil
}

func sevenZipSet(ctx context.Context, pool *Pool, nzb *NZB, vols map[int]int) ([]Video, error) {
	// Volumes run 1, 2, 3, … (or a lone 0) with none missing.
	start := 1
	if _, ok := vols[0]; ok {
		if len(vols) != 1 {
			return nil, err7zFormat
		}
		start = 0
	}
	idx := make([]int, 0, len(vols))
	for n := start; n < start+len(vols); n++ {
		i, ok := vols[n]
		if !ok {
			return nil, errors.New("7z archive is missing a volume")
		}
		idx = append(idx, i)
	}
	// Volumes are cut to one size, the last shorter: the first and last
	// are read, and any whose article count says it differs.
	heads := map[int]head{}
	need := []int{idx[0], idx[len(idx)-1]}
	firstCount := len(nzb.Files[idx[0]].Segments)
	for k, i := range idx {
		if k > 0 && k < len(idx)-1 && len(nzb.Files[i].Segments) != firstCount {
			need = append(need, i)
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, i := range need {
		if _, ok := heads[i]; ok {
			continue
		}
		heads[i] = head{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := readHead(ctx, pool, nzb.Files[i])
			mu.Lock()
			heads[i] = h
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for _, i := range need {
		if heads[i].err != nil {
			return nil, heads[i].err
		}
	}
	if !bytes.HasPrefix(heads[idx[0]].data, sevenZipSig) {
		return nil, err7zFormat
	}
	whole := &Layout{Name: "archive"}
	for k, i := range idx {
		h, ok := heads[i]
		if !ok {
			h = heads[idx[0]]
		}
		whole.Files = append(whole.Files, FileLayout{Index: i, Size: h.size, PartSize: h.partSize})
		whole.Pieces = append(whole.Pieces, Piece{File: k, Off: 0, Len: h.size})
		whole.Size += h.size
	}
	files, err := sevenZipFiles(ctxReader{ctx, NewFile(pool, nzb, whole)}, whole.Size, nzb.Password)
	if err != nil {
		return nil, err
	}
	var videos []Video
	packed := false
	for _, f := range files {
		name := path.Base(strings.ReplaceAll(f.name, `\`, "/"))
		if !isVideo(name) || sampleRE.MatchString(name) || f.size <= 0 {
			continue
		}
		if f.packed < 0 {
			packed = true
			continue
		}
		lay := sevenZipFileLayout(whole, f)
		if lay == nil {
			return nil, err7zFormat
		}
		lay.Name = name
		videos = append(videos, Video{Name: name, Size: f.size, Layout: lay})
	}
	if len(videos) == 0 {
		if packed {
			return nil, err7zPacked
		}
		return nil, ErrUnplayable
	}
	return videos, nil
}

// sevenZipFileLayout is the run of volumes holding a file of the archive
// laid out in whole.
func sevenZipFileLayout(whole *Layout, f szFile) *Layout {
	from, to := f.at+f.off, f.at+f.off+f.size
	var a *AES
	if f.aes != nil {
		const bs = aes.BlockSize
		blk := f.off / bs * bs
		begin := max(blk-bs, 0)
		end := min((f.off+f.size+bs-1)/bs*bs, f.packed)
		a = &AES{Key: f.aes.Key, Skip: f.off - begin}
		if blk == 0 {
			a.IV = f.aes.IV
		}
		from, to = f.at+begin, f.at+end
	}
	if from < 0 || to > whole.Size || from >= to {
		return nil
	}
	lay := &Layout{Size: f.size, AES: a}
	pos := int64(0)
	for _, pc := range whole.Pieces {
		lo, hi := max(from, pos), min(to, pos+pc.Len)
		if lo < hi {
			lay.Files = append(lay.Files, whole.Files[pc.File])
			lay.Pieces = append(lay.Pieces, Piece{File: len(lay.Files) - 1, Off: pc.Off + lo - pos, Len: hi - lo})
		}
		pos += pc.Len
	}
	return lay
}

// decodeFolder decodes a small folder, a header, in memory: a chain of
// Copy, AES, LZMA and LZMA2 coders.
func decodeFolder(f szFolder, data []byte, password string) ([]byte, error) {
	if len(f.packed) != 1 || len(f.coders) > 4 {
		return nil, err7zFormat
	}
	for _, c := range f.coders {
		if c.numIn != 1 || c.numOut != 1 {
			return nil, err7zPacked
		}
	}
	// With single-stream coders, coder i reads in stream i and writes out
	// stream i; bind pairs say whose output feeds whose input.
	c := int(f.packed[0])
	for steps := 0; ; steps++ {
		if c < 0 || c >= len(f.coders) || steps > len(f.coders) {
			return nil, err7zFormat
		}
		coder := f.coders[c]
		size := f.unpackSizes[c]
		if size > 64<<20 {
			return nil, err7zFormat
		}
		switch {
		case bytes.Equal(coder.id, coderCopy):
		case bytes.Equal(coder.id, coderAES):
			a, err := sevenZipAES(coder.props, password)
			if err != nil {
				return nil, err
			}
			if err := decryptAt(a, a.IV, data[:len(data)/aes.BlockSize*aes.BlockSize]); err != nil {
				return nil, err
			}
		case bytes.Equal(coder.id, coderLZMA):
			if len(coder.props) != 5 {
				return nil, err7zFormat
			}
			// The .lzma header: the coder's properties and the size.
			head := make([]byte, 13)
			copy(head, coder.props)
			binary.LittleEndian.PutUint64(head[5:], size)
			zr, err := lzma.NewReader(io.MultiReader(bytes.NewReader(head), bytes.NewReader(data)))
			if err != nil {
				return nil, fmt.Errorf("%w (or the password is wrong)", err7zFormat)
			}
			out := make([]byte, size)
			if _, err := io.ReadFull(zr, out); err != nil {
				return nil, fmt.Errorf("%w (or the password is wrong)", err7zFormat)
			}
			data = out
		case bytes.Equal(coder.id, coderLZMA2):
			if len(coder.props) != 1 || coder.props[0] > 40 {
				return nil, err7zFormat
			}
			p := coder.props[0]
			dict := uint32(2|p&1) << (p/2 + 11)
			if p == 40 {
				dict = 0xFFFFFFFF
			}
			zr, err := lzma.Reader2Config{DictCap: int(min(max(dict, 4096), 1<<30))}.NewReader2(bytes.NewReader(data))
			if err != nil {
				return nil, err7zFormat
			}
			out := make([]byte, size)
			if _, err := io.ReadFull(zr, out); err != nil {
				return nil, fmt.Errorf("%w (or the password is wrong)", err7zFormat)
			}
			data = out
		default:
			return nil, err7zPacked
		}
		if uint64(len(data)) < size {
			return nil, err7zFormat
		}
		data = data[:size]
		next := -1
		for _, bp := range f.bindPairs {
			if bp[1] == uint64(c) {
				next = int(bp[0])
			}
		}
		if next < 0 {
			return data, nil
		}
		c = next
	}
}
