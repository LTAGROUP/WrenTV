package usenet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeServer is a news server holding a few articles.
type fakeServer struct {
	mu       sync.Mutex
	articles map[string][]byte
	open     atomic.Int32
	peak     atomic.Int32
	bodies   atomic.Int32
	addr     string
}

func newFakeServer(t *testing.T) *fakeServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &fakeServer{articles: map[string][]byte{}, addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeServer) serve(c net.Conn) {
	defer c.Close()
	n := s.open.Add(1)
	defer s.open.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	r, w := bufio.NewReader(c), bufio.NewWriter(c)
	fmt.Fprint(w, "200 hello\r\n")
	w.Flush()
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		cmd := strings.ToUpper(f[0])
		switch {
		case cmd == "AUTHINFO" && len(f) == 3 && strings.EqualFold(f[1], "USER"):
			fmt.Fprint(w, "381 more\r\n")
		case cmd == "AUTHINFO" && len(f) == 3 && strings.EqualFold(f[1], "PASS"):
			if f[2] == "secret" {
				authed = true
				fmt.Fprint(w, "281 ok\r\n")
			} else {
				fmt.Fprint(w, "481 no\r\n")
			}
		case !authed:
			fmt.Fprint(w, "480 login\r\n")
		case (cmd == "BODY" || cmd == "STAT") && len(f) == 2:
			id := strings.Trim(f[1], "<>")
			s.mu.Lock()
			body, ok := s.articles[id]
			s.mu.Unlock()
			switch {
			case !ok:
				fmt.Fprint(w, "430 no such article\r\n")
			case cmd == "STAT":
				fmt.Fprintf(w, "223 0 <%s>\r\n", id)
			default:
				s.bodies.Add(1)
				fmt.Fprintf(w, "222 0 <%s>\r\n", id)
				for _, l := range bytes.SplitAfter(body, []byte("\r\n")) {
					if len(l) == 0 {
						continue
					}
					if l[0] == '.' {
						w.WriteByte('.')
					}
					w.Write(l)
				}
				fmt.Fprint(w, ".\r\n")
			}
		case cmd == "QUIT":
			fmt.Fprint(w, "205 bye\r\n")
			w.Flush()
			return
		default:
			fmt.Fprint(w, "500 what\r\n")
		}
		w.Flush()
	}
}

// yenc encodes one part of a file the way posters do.
func yenc(name string, size, begin int64, part, total int, data []byte) []byte {
	var b bytes.Buffer
	if total > 1 {
		fmt.Fprintf(&b, "=ybegin part=%d total=%d line=128 size=%d name=%s\r\n", part, total, size, name)
		fmt.Fprintf(&b, "=ypart begin=%d end=%d\r\n", begin+1, begin+int64(len(data)))
	} else {
		fmt.Fprintf(&b, "=ybegin line=128 size=%d name=%s\r\n", size, name)
	}
	col := 0
	for i, c := range data {
		e := c + 42
		esc := e == 0 || e == '\n' || e == '\r' || e == '=' || (col == 0 && (e == '.' || e == ' ' || e == '\t')) ||
			(e == ' ' || e == '\t') && (col == 127 || i == len(data)-1)
		if esc {
			b.WriteByte('=')
			e += 64
			col++
		}
		b.WriteByte(e)
		col++
		if col >= 128 {
			b.WriteString("\r\n")
			col = 0
		}
	}
	if col > 0 {
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "=yend size=%d part=%d pcrc32=%08x\r\n", len(data), part, crc32.ChecksumIEEE(data))
	return b.Bytes()
}

// post splits a file into articles on the server and returns its NZB entry.
func (s *fakeServer) post(name string, data []byte, partSize int, subject string) NZBFile {
	total := (len(data) + partSize - 1) / partSize
	f := NZBFile{Subject: subject}
	for i := 0; i < total; i++ {
		begin := i * partSize
		chunk := data[begin:min(begin+partSize, len(data))]
		id := fmt.Sprintf("%s.%d@test", strings.ReplaceAll(name, " ", "_"), i+1)
		s.mu.Lock()
		s.articles[id] = yenc(name, int64(len(data)), int64(begin), i+1, total, chunk)
		s.mu.Unlock()
		f.Segments = append(f.Segments, Segment{Bytes: int64(len(chunk)), Number: i + 1, ID: id})
	}
	return f
}

func (s *fakeServer) pool(conns int) *Pool {
	host, port, _ := net.SplitHostPort(s.addr)
	var p int
	fmt.Sscan(port, &p)
	return NewPool([]Server{{Host: host, Port: p, User: "me", Pass: "secret", MaxConns: conns}})
}

func randomBytes(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func TestYEncRoundTrip(t *testing.T) {
	data := randomBytes(100000, 1)
	p, err := decodeYEnc(yenc("a.mkv", 300000, 100000, 2, 3, data))
	if err != nil {
		t.Fatal(err)
	}
	if p.Begin != 100000 || p.Size != 300000 || p.Name != "a.mkv" || !bytes.Equal(p.Data, data) {
		t.Fatalf("decoded %d bytes from %d, size %d, name %q", len(p.Data), p.Begin, p.Size, p.Name)
	}
	bad := yenc("a.mkv", 300000, 100000, 2, 3, data)
	bad[200] ^= 1
	if _, err := decodeYEnc(bad); err == nil {
		t.Fatal("damaged article decoded")
	}
}

func TestPlainVideo(t *testing.T) {
	s := newFakeServer(t)
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(1_000_000, 2)...)
	nzb := &NZB{Files: []NZBFile{
		s.post("Movie.2020.1080p.mkv", video, 64000, `[1/3] - "Movie.2020.1080p.mkv" yEnc`),
		s.post("Movie.2020.1080p.nfo", []byte("info"), 64000, `[2/3] - "Movie.2020.1080p.nfo" yEnc`),
		s.post("Movie.2020.1080p.par2", randomBytes(5000, 3), 64000, `[3/3] - "Movie.2020.1080p.par2" yEnc`),
	}}
	pool := s.pool(3)
	defer pool.Close()
	ctx := context.Background()
	vids, err := Inspect(ctx, pool, nzb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 1 || vids[0].Name != "Movie.2020.1080p.mkv" || vids[0].Size != int64(len(video)) {
		t.Fatalf("videos %+v", vids)
	}
	if ok, err := Available(ctx, pool, nzb, vids[0].Layout, 10); !ok || err != nil {
		t.Fatalf("available %v %v", ok, err)
	}
	f := NewFile(pool, nzb, vids[0].Layout)
	for _, r := range [][2]int64{{0, 10}, {63990, 20}, {500000, 200000}, {int64(len(video)) - 5, 5}} {
		got := make([]byte, r[1])
		if _, err := f.ReadAt(got, r[0]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, video[r[0]:r[0]+r[1]]) {
			t.Fatalf("bytes at %d differ", r[0])
		}
	}
	var buf bytes.Buffer
	if n, err := f.WriteRange(ctx, &buf, 1000, int64(len(video))-1000); err != nil || !bytes.Equal(buf.Bytes(), video[1000:]) {
		t.Fatalf("WriteRange %d %v", n, err)
	}
	if p := s.peak.Load(); p > 3 {
		t.Fatalf("opened %d connections, limit 3", p)
	}

	// A taken-down article fails the check.
	s.mu.Lock()
	delete(s.articles, nzb.Files[0].Segments[7].ID)
	s.mu.Unlock()
	if ok, _ := Available(ctx, pool, nzb, vids[0].Layout, 100); ok {
		t.Fatal("missing article not noticed")
	}
}

// rar4Volume builds a stored RAR4 volume holding part of a file.
func rar4Volume(name string, data []byte, before, after bool, first bool) []byte {
	var b bytes.Buffer
	b.Write(rar4Sig)
	// Archive header.
	var mflags uint16 = 0x0001 // volume
	if first {
		mflags |= 0x0100
	}
	hdr := []byte{0, 0, 0x73, 0, 0, 13, 0, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(hdr[3:], mflags)
	b.Write(hdr)
	// File header.
	fh := make([]byte, 32+len(name))
	fh[2] = 0x74
	var flags uint16 = 0x8000
	if before {
		flags |= 0x01
	}
	if after {
		flags |= 0x02
	}
	binary.LittleEndian.PutUint16(fh[3:], flags)
	binary.LittleEndian.PutUint16(fh[5:], uint16(len(fh)))
	binary.LittleEndian.PutUint32(fh[7:], uint32(len(data)))
	binary.LittleEndian.PutUint32(fh[11:], uint32(len(data)))
	fh[24] = 29
	fh[25] = 0x30 // stored
	binary.LittleEndian.PutUint16(fh[26:], uint16(len(name)))
	copy(fh[32:], name)
	b.Write(fh)
	b.Write(data)
	b.Write([]byte{0, 0, 0x7b, 0, 0x40, 7, 0})
	return b.Bytes()
}

func TestStoredRARVolumes(t *testing.T) {
	s := newFakeServer(t)
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(700_000, 4)...)
	const per = 250_000
	var files []NZBFile
	names := []string{"Show.S01E01.rar", "Show.S01E01.r00", "Show.S01E01.r01"}
	for i := 0; i < 3; i++ {
		chunk := video[i*per : min((i+1)*per, len(video))]
		vol := rar4Volume("Show.S01E01.1080p.mkv", chunk, i > 0, i < 2, i == 0)
		files = append(files, s.post(names[i], vol, 50_000, fmt.Sprintf(`"%s" yEnc`, names[i])))
	}
	// Posted out of order: names put them right.
	nzb := &NZB{Files: []NZBFile{files[2], files[0], files[1]}}
	pool := s.pool(4)
	defer pool.Close()
	vids, err := Inspect(context.Background(), pool, nzb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 1 || vids[0].Name != "Show.S01E01.1080p.mkv" || vids[0].Size != int64(len(video)) {
		t.Fatalf("videos %+v", vids)
	}
	f := NewFile(pool, nzb, vids[0].Layout)
	got := make([]byte, len(video))
	if _, err := f.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, video) {
		t.Fatal("joined volumes differ from the video")
	}

	// A compressed archive isn't streamable.
	packed := rar4Volume("x.mkv", randomBytes(1000, 5), false, false, true)
	packed[len(rar4Sig)+13+25] = 0x33
	nzb2 := &NZB{Files: []NZBFile{s.post("x.rar", packed, 50_000, `"x.rar" yEnc`)}}
	if _, err := Inspect(context.Background(), pool, nzb2, nil); err == nil {
		t.Fatal("compressed archive accepted")
	}
}

func TestParseNZB(t *testing.T) {
	doc := `<?xml version="1.0" encoding="iso-8859-1" ?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
 <file poster="a@b" date="1" subject="Movie [2/2] - &quot;movie.mkv&quot; yEnc (1/2)">
  <groups><group>alt.binaries.test</group></groups>
  <segments><segment bytes="10" number="2">b@x</segment><segment bytes="10" number="1">&lt;a@x&gt;</segment></segments>
 </file>
</nzb>`
	n, err := ParseNZB([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	f := n.Files[0]
	if f.Name() != "movie.mkv" || len(f.Segments) != 2 || f.Segments[0].ID != "a@x" || f.Segments[1].ID != "b@x" {
		t.Fatalf("parsed %+v", f)
	}
}

func TestEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.nzbgeek.info":      "https://api.nzbgeek.info/api",
		"https://api.nzbgeek.info/api/": "https://api.nzbgeek.info/api",
		"https://x.org/api?apikey=1":    "https://x.org/api",
	} {
		if got, err := Endpoint(in); err != nil || got != want {
			t.Errorf("Endpoint(%q) = %q, %v", in, got, err)
		}
	}
	if got := stripKey("https://api.nzbgeek.info/api?t=get&id=abc&apikey=SECRET"); strings.Contains(got, "SECRET") || !strings.Contains(got, "id=abc") {
		t.Errorf("stripKey kept %q", got)
	}
}
