package usenet

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

func (s *fakeServer) server(conns int, backup bool) Server {
	host, port, _ := net.SplitHostPort(s.addr)
	p, _ := strconv.Atoi(port)
	return Server{Host: host, Port: p, User: "me", Pass: "secret", MaxConns: conns, Backup: backup}
}

func (s *fakeServer) askedFor(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked[id]
}

// An article that doesn't come is asked for again, and the read goes on
// with the copy that does instead of waiting out the first request.
func TestHedgedArticle(t *testing.T) {
	s := newFakeServer(t)
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(64000*20, 8)...)
	nzb := &NZB{Files: []NZBFile{s.post("Movie.2020.1080p.mkv", video, 64000, `"Movie.2020.1080p.mkv" yEnc`)}}
	pool := s.pool(4)
	defer pool.Close()
	vids, err := Inspect(context.Background(), pool, nzb, nil)
	if err != nil {
		t.Fatal(err)
	}
	stuck := nzb.Files[0].Segments[10].ID
	s.mu.Lock()
	s.hang[stuck] = 1
	s.mu.Unlock()
	f := NewFile(pool, nzb, vids[0].Layout)
	start := time.Now()
	var buf bytes.Buffer
	if _, err := f.WriteRange(context.Background(), &buf, 0, int64(len(video))); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), video) {
		t.Fatal("bytes differ")
	}
	if d := time.Since(start); d > hedgeFloor+3*time.Second {
		t.Fatalf("read took %v with one article stuck", d)
	}
	if n := s.askedFor(stuck); n != 2 {
		t.Fatalf("stuck article asked for %d times, want 2", n)
	}
	// Articles that came promptly weren't asked for twice.
	if n := s.askedFor(nzb.Files[0].Segments[15].ID); n != 1 {
		t.Fatalf("article asked for %d times", n)
	}
}

// Articles read once are kept for every file on the pool.
func TestSharedArticleCache(t *testing.T) {
	s := newFakeServer(t)
	video := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(64000*10, 9)...)
	nzb := &NZB{Files: []NZBFile{s.post("Movie.2020.1080p.mkv", video, 64000, `"Movie.2020.1080p.mkv" yEnc`)}}
	for _, disk := range []bool{false, true} {
		dir, diskBytes := "", int64(0)
		if disk {
			dir, diskBytes = t.TempDir(), 1<<30
		}
		cache := NewCache(1<<30, dir, diskBytes)
		pool := NewPool([]Server{s.server(4, false)}).WithCache(cache)
		vids, err := Inspect(context.Background(), pool, nzb, nil)
		if err != nil {
			t.Fatal(err)
		}
		read := func() {
			var buf bytes.Buffer
			if _, err := NewFile(pool, nzb, vids[0].Layout).WriteRange(context.Background(), &buf, 0, int64(len(video))); err != nil || !bytes.Equal(buf.Bytes(), video) {
				t.Fatalf("read: %v", err)
			}
		}
		read()
		before := s.bodies.Load()
		read()
		if n := s.bodies.Load() - before; n != 0 {
			t.Fatalf("second file fetched %d articles", n)
		}
		pool.Close()
		if disk {
			// A new run finds the articles on disk.
			time.Sleep(200 * time.Millisecond) // written behind the read
			c2 := NewCache(1, dir, diskBytes)
			if _, ok := c2.Get(nzb.Files[0].Segments[3].ID); !ok {
				t.Fatal("article not kept on disk")
			}
		}
	}
}

// Backup servers are only asked for what the others don't have.
func TestBackupServer(t *testing.T) {
	main, backup := newFakeServer(t), newFakeServer(t)
	data := randomBytes(64000*4, 10)
	f := main.post("a.mkv", data, 64000, `"a.mkv" yEnc`)
	backup.post("a.mkv", data, 64000, `"a.mkv" yEnc`)
	// Listed first, but it's the backup.
	pool := NewPool([]Server{backup.server(2, true), main.server(2, false)})
	defer pool.Close()
	ctx := context.Background()
	if _, err := pool.Body(ctx, f.Segments[0].ID); err != nil {
		t.Fatal(err)
	}
	if backup.bodies.Load() != 0 {
		t.Fatal("backup server asked before the main one")
	}
	main.mu.Lock()
	delete(main.articles, f.Segments[1].ID)
	main.mu.Unlock()
	if _, err := pool.Body(ctx, f.Segments[1].ID); err != nil {
		t.Fatalf("backup didn't fill in: %v", err)
	}
	if backup.bodies.Load() != 1 {
		t.Fatal("backup server not asked for the missing article")
	}
}

// A server that stops answering mid-read gives up the connection after
// stallFor rather than holding the read for minutes.
func TestStalledRead(t *testing.T) {
	old := stallFor
	stallFor = 300 * time.Millisecond
	defer func() { stallFor = old }()
	s := newFakeServer(t)
	f := s.post("a.mkv", randomBytes(1000, 11), 64000, `"a.mkv" yEnc`)
	s.hang[f.Segments[0].ID] = 100
	pool := NewPool([]Server{s.server(1, false)})
	defer pool.Close()
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := pool.Body(ctx, f.Segments[0].ID); err == nil {
		t.Fatal("hung article read")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("gave up after %v", d)
	}
}

// An article a player is waiting on goes before other streams' read-ahead
// that's still queued for a connection.
func TestWaitedArticleSkipsQueuedReadAhead(t *testing.T) {
	s := newFakeServer(t)
	s.bodyDelay = 40 * time.Millisecond
	other := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(64000*40, 10)...)
	mine := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, randomBytes(64000*4, 11)...)
	nzb := &NZB{Files: []NZBFile{
		s.post("Other.2020.1080p.mkv", other, 64000, `"Other.2020.1080p.mkv" yEnc`),
		s.post("Mine.2020.1080p.mkv", mine, 64000, `"Mine.2020.1080p.mkv" yEnc`),
	}}
	pool := s.pool(2)
	defer pool.Close()
	vids, err := Inspect(context.Background(), pool, nzb, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lay [2]*Layout
	for _, v := range vids {
		if v.Layout.Name == "Mine.2020.1080p.mkv" {
			lay[1] = v.Layout
		} else {
			lay[0] = v.Layout
		}
	}
	// The other stream queues forty articles ahead: a second's worth on
	// two connections.
	o := NewFile(pool, nzb, lay[0])
	sp, _ := o.spans(0, o.Size())
	for _, x := range sp {
		o.start(context.Background(), x.key)
	}
	start := time.Now()
	var buf bytes.Buffer
	if _, err := NewFile(pool, nzb, lay[1]).WriteRange(context.Background(), &buf, 0, 64000); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("waited %v behind another stream's read-ahead", d)
	}
}
