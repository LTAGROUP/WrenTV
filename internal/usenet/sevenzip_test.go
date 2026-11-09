package usenet

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// The fixtures were made with 7-Zip 17+: sz.7z.* is `7z a -mx0 -mhe=on
// -p'pa55 wörd' -v70k` of a 1000-byte a.nfo and Show.S01E01.1080p.mkv
// (200004 bytes, the hash below); packed.7z is the video at -mx5.
const szVideoHash = "4b61a5ed6919b6c69717fedf440bd7c5cc6417b757635d371e0915ab2f4c2b18"

func TestEncryptedSevenZipVolumes(t *testing.T) {
	s := newFakeServer(t)
	var vols []NZBFile
	for _, n := range []string{"sz.7z.003", "sz.7z.001", "sz.7z.002"} {
		data, err := os.ReadFile("testdata/" + n)
		if err != nil {
			t.Fatal(err)
		}
		vols = append(vols, s.post(n, data, 20_000, fmt.Sprintf(`"%s" yEnc`, n)))
	}
	par := s.post("sz.par2", randomBytes(5000, 9), 20_000, `"sz.par2" yEnc`)
	nzbData := []byte(`<?xml version="1.0"?><nzb><head><meta type="password">pa55 wörd</meta></head>` +
		`<file subject="x"><segments><segment number="1">a@b</segment></segments></file></nzb>`)
	parsed, err := ParseNZB(nzbData)
	if err != nil || parsed.Password != "pa55 wörd" {
		t.Fatalf("NZB password %q, %v", parsed.Password, err)
	}
	nzb := &NZB{Files: append(vols, par), Password: parsed.Password}
	pool := s.pool(4)
	defer pool.Close()

	vids, err := Inspect(context.Background(), pool, nzb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 1 || vids[0].Name != "Show.S01E01.1080p.mkv" || vids[0].Size != 200004 || vids[0].Layout.AES == nil {
		t.Fatalf("videos %+v", vids)
	}
	f := NewFile(pool, nzb, vids[0].Layout)
	whole := make([]byte, vids[0].Size)
	if _, err := f.ReadAt(whole, 0); err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(whole); hex.EncodeToString(sum[:]) != szVideoHash {
		t.Fatal("decrypted video differs from the original")
	}
	checkReads(t, f, whole)

	// Without the password the post can't be read.
	if _, err := Inspect(context.Background(), pool, &NZB{Files: nzb.Files}, nil); !errors.Is(err, errEncrypted) {
		t.Fatalf("no password: %v", err)
	}
	// Nor with a wrong one: the header decrypts to noise.
	if _, err := Inspect(context.Background(), pool, &NZB{Files: nzb.Files, Password: "nope"}, nil); err == nil {
		t.Fatal("wrong password accepted")
	}
	// A missing volume is caught before anything is read.
	if _, err := Inspect(context.Background(), pool, &NZB{Files: []NZBFile{vols[0], vols[1]}, Password: "pa55 wörd"}, nil); err == nil {
		t.Fatal("archive with a volume missing accepted")
	}

	// Compressed archives don't stream.
	data, err := os.ReadFile("testdata/packed.7z")
	if err != nil {
		t.Fatal(err)
	}
	packed := &NZB{Files: []NZBFile{s.post("packed.7z", data, 20_000, `"packed.7z" yEnc`)}, Password: "pa55 wörd"}
	if _, err := Inspect(context.Background(), pool, packed, nil); !errors.Is(err, err7zPacked) {
		t.Fatalf("compressed archive: %v", err)
	}
}

// A file that starts partway into its folder's encrypted stream, as one
// after another in a solid archive would: its layout starts a block
// early to chain from.
func TestAESLayoutMidStream(t *testing.T) {
	s := newFakeServer(t)
	key, iv := randomBytes(32, 1), randomBytes(16, 2)
	plain := randomBytes(5000, 3)
	ct := append([]byte(nil), plain...)
	ct = append(ct, make([]byte, 16-len(ct)%16)...)
	b, _ := aes.NewCipher(key)
	cipher.NewCBCEncrypter(b, iv).CryptBlocks(ct, ct)
	archive := append(randomBytes(32, 4), ct...) // the folder starts at 32
	nzb := &NZB{Files: []NZBFile{s.post("x.7z", archive, 700, `"x.7z" yEnc`)}}
	pool := s.pool(2)
	defer pool.Close()
	whole := &Layout{Size: int64(len(archive)), Files: []FileLayout{{Index: 0, Size: int64(len(archive)), PartSize: 700}},
		Pieces: []Piece{{File: 0, Len: int64(len(archive))}}}
	for _, c := range []struct{ off, size int64 }{{0, 5000}, {0, 7}, {5, 30}, {16, 100}, {1003, 2500}, {4990, 10}} {
		lay := sevenZipFileLayout(whole, szFile{at: 32, off: c.off, size: c.size, packed: int64(len(ct)), aes: &AES{Key: key, IV: iv}})
		if lay == nil {
			t.Fatalf("%+v: no layout", c)
		}
		f := NewFile(pool, nzb, lay)
		got := make([]byte, c.size)
		if _, err := f.ReadAt(got, 0); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		want := plain[c.off : c.off+c.size]
		if !bytes.Equal(got, want) {
			t.Fatalf("%+v: wrong bytes", c)
		}
		checkReads(t, f, want)
	}
}

// checkReads reads f at random offsets and lengths, by ReadAt and
// WriteRange, and compares with want.
func checkReads(t *testing.T, f *File, want []byte) {
	t.Helper()
	r := rand.New(rand.NewSource(int64(len(want))))
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		off := r.Int63n(int64(len(want)))
		n := 1 + r.Int63n(min(int64(len(want))-off, 40_000))
		got := make([]byte, n)
		if _, err := f.ReadAtContext(ctx, got, off); err != nil {
			t.Fatalf("read %d at %d: %v", n, off, err)
		}
		if !bytes.Equal(got, want[off:off+n]) {
			t.Fatalf("read %d at %d: wrong bytes", n, off)
		}
		var buf bytes.Buffer
		w, err := f.WriteRange(ctx, &buf, off, n)
		if err != nil || w != n || !bytes.Equal(buf.Bytes(), want[off:off+n]) {
			t.Fatalf("write %d at %d: %d bytes, %v", n, off, w, err)
		}
	}
	// Past the end.
	if n, err := f.ReadAtContext(ctx, make([]byte, 10), int64(len(want))-3); n != 3 || err == nil {
		t.Fatalf("read over the end: %d, %v", n, err)
	}
	var buf bytes.Buffer
	if w, _ := f.WriteRange(ctx, &buf, int64(len(want))-3, 100); w != 3 {
		t.Fatalf("write over the end: %d", w)
	}
}
