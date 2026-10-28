package usenet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeServer is a news server holding a few articles.
type fakeServer struct {
	mu       sync.Mutex
	articles map[string][]byte
	gone     map[string]bool // answered 451, as Giganews does for takedowns
	logins   atomic.Int32
	limit    int32         // more logins than this at once get 481, as Giganews answers
	slowAuth time.Duration // how long a login's answer takes
	refused  atomic.Int32  // logins refused for a wrong password
	authed   atomic.Int32
	open     atomic.Int32
	peak     atomic.Int32
	bodies   atomic.Int32
	stats    atomic.Int32
	addr     string
	// hang holds how many more BODY requests for an article go unanswered;
	// asked counts the requests for each.
	hang    map[string]int
	asked   map[string]int
	release chan struct{}
	// bodyDelay is how long every article takes to come.
	bodyDelay time.Duration
}

func newFakeServer(t *testing.T) *fakeServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &fakeServer{articles: map[string][]byte{}, addr: ln.Addr().String(), hang: map[string]int{}, asked: map[string]int{},
		release: make(chan struct{})}
	t.Cleanup(func() { close(s.release) })
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
			time.Sleep(s.slowAuth)
			if f[2] == "secret" && s.limit > 0 && s.authed.Add(1) > s.limit {
				s.authed.Add(-1)
				fmt.Fprint(w, "481 Too many connections\r\n")
			} else if f[2] == "secret" {
				if s.limit == 0 {
					s.authed.Add(1)
				}
				authed = true
				defer s.authed.Add(-1)
				s.logins.Add(1)
				fmt.Fprint(w, "281 ok\r\n")
			} else {
				s.refused.Add(1)
				fmt.Fprint(w, "481 no\r\n")
			}
		case !authed:
			fmt.Fprint(w, "480 login\r\n")
		case (cmd == "BODY" || cmd == "STAT") && len(f) == 2:
			id := strings.Trim(f[1], "<>")
			s.mu.Lock()
			body, ok := s.articles[id]
			gone := s.gone[id]
			hang := cmd == "BODY" && s.hang[id] > 0
			if cmd == "STAT" {
				s.stats.Add(1)
			}
			if cmd == "BODY" {
				s.asked[id]++
				if hang {
					s.hang[id]--
				}
			}
			delay := s.bodyDelay
			s.mu.Unlock()
			if cmd == "BODY" && delay > 0 {
				time.Sleep(delay)
			}
			if hang {
				<-s.release // never answers; the client gives up on the connection
				return
			}
			switch {
			case gone:
				fmt.Fprintf(w, "451 0 <%s>\r\n", id)
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

// Giganews answers taken-down articles with 451. That's a missing article,
// not a broken connection: the post fails fast and connections are kept.
func TestTakenDownArticles(t *testing.T) {
	s := newFakeServer(t)
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(400_000, 4)...)
	nzb := &NZB{Files: []NZBFile{s.post("Movie.2020.1080p.mkv", video, 64000, `"Movie.2020.1080p.mkv" yEnc`)}}
	s.mu.Lock()
	s.gone = map[string]bool{nzb.Files[0].Segments[0].ID: true, nzb.Files[0].Segments[3].ID: true}
	s.mu.Unlock()
	pool := s.pool(1)
	defer pool.Close()
	ctx := context.Background()
	if ok, err := Alive(ctx, pool, nzb, nil, 10); ok || err != nil {
		t.Fatalf("alive %v %v, want false, nil", ok, err)
	}
	if _, err := pool.Body(ctx, nzb.Files[0].Segments[0].ID); !errors.Is(err, ErrMissing) {
		t.Fatalf("body err %v, want ErrMissing", err)
	}
	if _, err := Inspect(ctx, pool, nzb, nil); !errors.Is(err, ErrMissing) {
		t.Fatalf("inspect err %v, want ErrMissing", err)
	}
	if n := s.logins.Load(); n != 1 {
		t.Fatalf("logged in %d times, want 1", n)
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
	// Volumes the file runs on from are read no further than their first
	// article; only the last is walked to its end.
	if n := s.bodies.Load(); n > 4 {
		t.Fatalf("inspect fetched %d articles for 3 volumes", n)
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

// A big archive is mapped from a few of its volumes. From volume 23 on
// they're laid out differently (as RAR5 headers grow at volume 128), and
// where that starts has to be found.
func TestManyRARVolumes(t *testing.T) {
	// "x.rar, x.r00, ..." and "x.part01.rar, ..." number their first
	// volume differently.
	old := func(i int) string {
		if i == 0 {
			return "Movie.rar"
		}
		return fmt.Sprintf("Movie.r%02d", i-1)
	}
	parts := func(i int) string { return fmt.Sprintf("Movie.part%03d.rar", i+1) }
	for _, name := range []func(int) string{old, parts} {
		testManyRARVolumes(t, name, name)
	}
	// The NZB's names can hide the real ones the volumes carry.
	hidden := func(i int) string { return fmt.Sprintf("a1b2c3d4.part%03d.rar", i+1) }
	testManyRARVolumes(t, parts, hidden)
}

func testManyRARVolumes(t *testing.T, volName, subject func(int) string) {
	s := newFakeServer(t)
	const vols, per = 40, 50_000
	var video []byte
	var files []NZBFile
	for i := 0; i < vols; i++ {
		n := per
		if i >= 23 {
			n -= 100
		}
		chunk := append([]byte(nil), randomBytes(n, int64(10+i))...)
		if i == 0 {
			copy(chunk, []byte{0x1a, 0x45, 0xdf, 0xa3})
		}
		if i == vols-1 {
			chunk = chunk[:per/3]
		}
		video = append(video, chunk...)
		name := volName(i)
		vol := rar4Volume("Movie.2160p.mkv", chunk, i > 0, i < vols-1, i == 0)
		files = append(files, s.post(name, vol, 20_000, fmt.Sprintf(`"%s" yEnc`, subject(i))))
	}
	nzb := &NZB{Files: files}
	pool := s.pool(4)
	defer pool.Close()
	vids, err := Inspect(context.Background(), pool, nzb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := s.bodies.Load(); n > 16 {
		t.Fatalf("inspect fetched %d articles for %d volumes", n, vols)
	}
	if len(vids) != 1 || vids[0].Size != int64(len(video)) {
		t.Fatalf("videos %+v, want size %d", vids, len(video))
	}
	if vids[0].Layout.Skips(nzb) {
		t.Fatal("layout skips volumes")
	}
	short := *vids[0].Layout
	short.Files = append(slices.Clone(short.Files[:3]), short.Files[len(short.Files)-2:]...)
	if !short.Skips(nzb) {
		t.Fatal("layout without its middle volumes doesn't skip any")
	}
	f := NewFile(pool, nzb, vids[0].Layout)
	got := make([]byte, len(video))
	if _, err := f.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, video) {
		t.Fatal("joined volumes differ from the video")
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

// An account allowing fewer connections than set up still plays: logins
// refused for that are waited out on our own connections, not taken as a
// bad password.
func TestLearnsConnectionLimit(t *testing.T) {
	s := newFakeServer(t)
	s.limit = 3
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(640_000, 5)...)
	nzb := &NZB{Files: []NZBFile{s.post("Movie.2020.1080p.mkv", video, 64000, `"Movie.2020.1080p.mkv" yEnc`)}}
	pool := s.pool(10)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make([]error, 30)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = pool.Body(ctx, nzb.Files[0].Segments[i%10].ID)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if _, err := pool.Body(ctx, nzb.Files[0].Segments[0].ID); err != nil {
		t.Fatalf("server left paused: %v", err)
	}
}

// Short reads after a seek fetch little beyond what they need, and the
// articles every reopen reads again (the header) stay cached.
func TestReadAheadAndCache(t *testing.T) {
	s := newFakeServer(t)
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(64000*200, 6)...)
	nzb := &NZB{Files: []NZBFile{s.post("Movie.2020.1080p.mkv", video, 64000, `"Movie.2020.1080p.mkv" yEnc`)}}
	pool := s.pool(8)
	defer pool.Close()
	ctx := context.Background()
	vids, err := Inspect(ctx, pool, nzb, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := NewFile(pool, nzb, vids[0].Layout)
	f.max = 12
	settle := func() int32 {
		for i := 0; i < 100; i++ {
			n := s.bodies.Load()
			time.Sleep(20 * time.Millisecond)
			if s.bodies.Load() == n {
				return n
			}
		}
		return s.bodies.Load()
	}
	base := settle()
	var buf bytes.Buffer
	if _, err := f.WriteRange(ctx, &buf, 64000*100, 100); err != nil {
		t.Fatal(err)
	}
	if n := settle() - base; n > 3 {
		t.Fatalf("a 100-byte read fetched %d articles", n)
	}
	// Read the header, then move through the file reading it again each time.
	for i := 0; i < 40; i++ {
		buf.Reset()
		if _, err := f.WriteRange(ctx, &buf, 0, 10); err != nil {
			t.Fatal(err)
		}
		buf.Reset()
		if _, err := f.WriteRange(ctx, &buf, int64(64000*(120+i*2)), 10); err != nil {
			t.Fatal(err)
		}
	}
	before := settle()
	buf.Reset()
	if _, err := f.WriteRange(ctx, &buf, 0, 10); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), video[:10]) {
		t.Fatal("header bytes differ")
	}
	if settle() != before {
		t.Fatal("header article was dropped from the cache")
	}
}

// cancelAfter cancels its context once anything is written to it.
type cancelAfter struct{ cancel context.CancelFunc }

func (c cancelAfter) Write(p []byte) (int, error) { c.cancel(); return len(p), nil }

// A reader that goes away (a player seeking elsewhere) drops its queued
// read-ahead, and the articles already on a connection finish there, so no
// connection is lost.
func TestAbandonedReadAhead(t *testing.T) {
	s := newFakeServer(t)
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(64000*200, 7)...)
	nzb := &NZB{Files: []NZBFile{s.post("Movie.2020.1080p.mkv", video, 64000, `"Movie.2020.1080p.mkv" yEnc`)}}
	pool := s.pool(2)
	defer pool.Close()
	vids, err := Inspect(context.Background(), pool, nzb, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := NewFile(pool, nzb, vids[0].Layout)
	settle := func() int32 {
		for i := 0; i < 100; i++ {
			n := s.bodies.Load()
			time.Sleep(20 * time.Millisecond)
			if s.bodies.Load() == n {
				return n
			}
		}
		return s.bodies.Load()
	}
	base := settle()
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		f.WriteRange(ctx, cancelAfter{cancel}, int64(64000*(20+i*30)), 64000*100)
	}
	if n := settle() - base; n > 5*8 {
		t.Fatalf("abandoned reads fetched %d articles", n)
	}
	if n := s.logins.Load(); n > 2 {
		t.Fatalf("%d logins for 2 connections: some were dropped", n)
	}
	// What's still wanted reads fine afterwards.
	var buf bytes.Buffer
	if _, err := f.WriteRange(context.Background(), &buf, 64000*25, 64000*3); err != nil || !bytes.Equal(buf.Bytes(), video[64000*25:64000*28]) {
		t.Fatalf("read after = %d bytes, %v", buf.Len(), err)
	}
}

// A server whose login is wrong is set aside at once, even when many
// connections to it log in together, rather than its refusals being
// waited out as a full account: checks of posts the other servers lack
// shouldn't each wait on its retries.
func TestWrongLoginSetAside(t *testing.T) {
	s := newFakeServer(t)
	s.slowAuth = time.Second
	host, port, _ := net.SplitHostPort(s.addr)
	var p int
	fmt.Sscan(port, &p)
	pool := NewPool([]Server{
		{Host: host, Port: p, User: "me", Pass: "secret", MaxConns: 10},
		{Host: host, Port: p, User: "me", Pass: "wrong", MaxConns: 10},
	})
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	var wg sync.WaitGroup
	errs := make([]error, 30)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = pool.Stat(ctx, []string{fmt.Sprintf("missing-%d@x", i)})
		}(i)
	}
	wg.Wait()
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("checks took %v waiting on the wrong login", took)
	}
	for i, err := range errs {
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("check %d: %v, want the login error", i, err)
		}
	}
	if n := s.refused.Load(); n > 10 {
		t.Errorf("wrong login tried %d times, want one round", n)
	}
	bad := pool.servers[1]
	bad.mu.Lock()
	aside := time.Until(bad.downUntil)
	bad.mu.Unlock()
	if aside < 5*time.Minute {
		t.Errorf("wrong login set aside for %v, want minutes", aside.Round(time.Second))
	}
}

// A post gone from the first server is asked of the backups one article
// first: gone there too, it's settled without asking about the rest.
func TestStatBackupsAskOneFirst(t *testing.T) {
	primary, backup := newFakeServer(t), newFakeServer(t)
	var ids []string
	for i := range 8 {
		ids = append(ids, fmt.Sprintf("gone%d@test", i))
	}
	servers := func() []Server {
		var out []Server
		for _, s := range []*fakeServer{primary, backup} {
			host, port, _ := net.SplitHostPort(s.addr)
			var p int
			fmt.Sscan(port, &p)
			out = append(out, Server{Host: host, Port: p, User: "me", Pass: "secret", MaxConns: 4})
		}
		return out
	}
	pool := NewPool(servers())
	defer pool.Close()
	ctx := context.Background()
	if ok, err := pool.Stat(ctx, ids); ok || err != nil {
		t.Fatalf("stat = %v, %v; want missing", ok, err)
	}
	if n := backup.stats.Load(); n != 1 {
		t.Fatalf("backup asked about %d articles, want 1", n)
	}
	// Gaps the backup fills still count as there.
	backup.mu.Lock()
	for _, id := range ids {
		backup.articles[id] = []byte("x")
	}
	backup.mu.Unlock()
	if ok, err := pool.Stat(ctx, ids); !ok || err != nil {
		t.Fatalf("stat with the backup's = %v, %v; want all there", ok, err)
	}
}

// A taken-down post is known from one article per lane, not the whole
// sample: servers can take half a second over each missing one.
func TestAliveAsksOneFirst(t *testing.T) {
	s := newFakeServer(t)
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(400_000, 5)...)
	nzb := &NZB{Files: []NZBFile{s.post("Movie.2020.1080p.mkv", video, 10000, `"Movie.2020.1080p.mkv" yEnc`)}}
	s.mu.Lock()
	s.gone = map[string]bool{}
	for _, seg := range nzb.Files[0].Segments {
		s.gone[seg.ID] = true
	}
	s.mu.Unlock()
	pool := s.pool(3)
	defer pool.Close()
	if ok, err := Alive(context.Background(), pool, nzb, nil, 24); ok || err != nil {
		t.Fatalf("alive %v %v, want false, nil", ok, err)
	}
	if n := s.stats.Load(); n > 3 {
		t.Fatalf("asked about %d articles, want at most 3", n)
	}
}

func TestSearchText(t *testing.T) {
	for in, want := range map[string]string{
		"Wonka's The Golden Ticket": "Wonkas The Golden Ticket",
		"Marvel’s Daredevil":        "Marvels Daredevil",
		"Mission: Impossible":       "Mission Impossible",
		"Law & Order":               "Law and Order",
	} {
		if got := searchText(in); got != want {
			t.Errorf("searchText(%q) = %q, want %q", in, got, want)
		}
	}
}
