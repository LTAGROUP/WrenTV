package usenet

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"io"
)

// rawSize is how many bytes the layout's pieces hold: Size, unless
// they're encrypted and carry padding and a block to chain from.
func (f *File) rawSize() int64 {
	if f.lay.AES == nil {
		return f.lay.Size
	}
	var n int64
	for _, pc := range f.lay.Pieces {
		n += pc.Len
	}
	return n
}

// aesSpan is the run of stored bytes that decrypts to n bytes at off:
// whole blocks, from the one before the first (chain) unless that is
// the IV, to past the last.
func (f *File) aesSpan(off, n int64) (from, to int64, chain bool) {
	a := f.lay.AES
	p := a.Skip + off
	blk := p / aes.BlockSize * aes.BlockSize
	to = min((p+n+aes.BlockSize-1)/aes.BlockSize*aes.BlockSize, f.rawSize())
	if blk > 0 || len(a.IV) == 0 {
		return max(blk-aes.BlockSize, 0), to, true
	}
	return 0, to, false
}

func (f *File) readAES(ctx context.Context, p []byte, off int64) (int, error) {
	if off >= f.lay.Size {
		return 0, io.EOF
	}
	want := min(int64(len(p)), f.lay.Size-off)
	from, to, chain := f.aesSpan(off, want)
	raw := make([]byte, to-from)
	if n, err := f.read(ctx, raw, from, false); n < len(raw) {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, err
	}
	prev, data := f.lay.AES.IV, raw
	if chain {
		prev, data = raw[:aes.BlockSize], raw[aes.BlockSize:]
	}
	if err := decryptAt(f.lay.AES, append([]byte(nil), prev...), data); err != nil {
		return 0, err
	}
	skip := f.lay.AES.Skip + off - from
	if chain {
		skip -= aes.BlockSize
	}
	n := copy(p[:want], data[skip:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *File) writeAES(ctx context.Context, w io.Writer, off, n int64) (int64, error) {
	if off >= f.lay.Size {
		return 0, io.EOF
	}
	n = min(n, f.lay.Size-off)
	from, to, chain := f.aesSpan(off, n)
	b, err := aes.NewCipher(f.lay.AES.Key)
	if err != nil {
		return 0, err
	}
	cw := &cbcWriter{w: w, block: b, skip: f.lay.AES.Skip + off - from, left: n}
	if chain {
		cw.skip -= aes.BlockSize
	} else {
		cw.mode = cipher.NewCBCDecrypter(b, f.lay.AES.IV)
	}
	_, err = f.writeRaw(ctx, cw, from, to-from)
	if cw.err != nil {
		err = cw.err
	}
	return cw.out, err
}

// cbcWriter decrypts what's written to it on the way to w: the first
// block sets the chain when mode is nil, skip decrypted bytes are
// dropped, and at most left are passed on.
type cbcWriter struct {
	w       io.Writer
	block   cipher.Block
	mode    cipher.BlockMode
	pending []byte
	skip    int64
	left    int64
	out     int64
	err     error
}

func (c *cbcWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	c.pending = append(c.pending, p...)
	if c.mode == nil {
		if len(c.pending) < aes.BlockSize {
			return len(p), nil
		}
		c.mode = cipher.NewCBCDecrypter(c.block, append([]byte(nil), c.pending[:aes.BlockSize]...))
		c.pending = c.pending[aes.BlockSize:]
	}
	whole := len(c.pending) / aes.BlockSize * aes.BlockSize
	if whole == 0 {
		return len(p), nil
	}
	data := c.pending[:whole]
	c.mode.CryptBlocks(data, data)
	if c.skip > 0 {
		d := min(c.skip, int64(len(data)))
		data, c.skip = data[d:], c.skip-d
	}
	data = data[:min(int64(len(data)), c.left)]
	if len(data) > 0 {
		m, err := c.w.Write(data)
		c.out += int64(m)
		c.left -= int64(m)
		if err != nil {
			c.err = err
			return 0, err
		}
	}
	c.pending = append(c.pending[:0], c.pending[whole:]...)
	return len(p), nil
}
