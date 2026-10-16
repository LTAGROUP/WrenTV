package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

type fakeProvider struct {
	cdn     string
	cached  map[string][]RemoteFile
	stale   bool // Link fails once, as when the debrid item expired
	adds    int
	checked int
}

func (f *fakeProvider) Name() string { return "torbox" }
func (f *fakeProvider) Check(_ context.Context, hashes []string) (map[string]bool, bool, error) {
	f.checked++
	out := map[string]bool{}
	for _, h := range hashes {
		_, out[h] = f.cached[h]
	}
	return out, true, nil
}
func (f *fakeProvider) Add(_ context.Context, hash, _ string) (*Torrent, error) {
	f.adds++
	files, ok := f.cached[hash]
	if !ok {
		return nil, ErrNotCached
	}
	return &Torrent{Ref: "t-" + hash, Files: files}, nil
}
func (f *fakeProvider) Link(_ context.Context, ref, file string) (string, error) {
	if f.stale {
		f.stale = false
		return "", ErrStale
	}
	return f.cdn + "/" + ref + "/" + file, nil
}

func TestStreamLookupPackAndPlayback(t *testing.T) {
	ctx := context.Background()
	hashA := strings.Repeat("a", 40) // 1080p season pack
	hashB := strings.Repeat("b", 40) // 2160p single episode
	hashC := strings.Repeat("c", 40) // 2160p, not cached
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/stream/series/tt1:1:") {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{
			{"name": "Torrentio\n1080p", "title": "Show.S01.1080p.WEB-DL\n👤 50 💾 3 GB", "infoHash": hashA},
			{"name": "Torrentio\n4k", "title": "Show.S01E01.2160p.WEB-DL\n👤 90 💾 8 GB", "infoHash": hashB},
			{"name": "Torrentio\n4k", "title": "Show.S01E01.2160p.REMUX\n👤 900 💾 9 GB", "infoHash": hashC},
		}})
	}))
	defer addon.Close()
	payload := bytes.Repeat([]byte("0123456789"), 1000)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x.mkv", time.Time{}, bytes.NewReader(payload))
	}))
	defer cdn.Close()

	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"2160p", "1080p"},
		Addons: []config.AddonConfig{{Name: "fake", URL: addon.URL + "/manifest.json", Enabled: true}}}}
	prov := &fakeProvider{cdn: cdn.URL, cached: map[string][]RemoteFile{
		hashA: {{ID: "1", Name: "Show.S01/Show.S01E01.1080p.mkv", Size: 10000}, {ID: "2", Name: "Show.S01/Show.S01E02.1080p.mkv", Size: 10000},
			{ID: "3", Name: "Show.S01/Show.S01E03.1080p.mkv", Size: 10000}},
		hashB: {{ID: "9", Name: "Show.S01E01.2160p.mkv", Size: 10000}},
	}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{prov}
	if !s.Active() {
		t.Fatal("service should be active")
	}

	lib, _ := d.StreamLibrary(ctx, "shows")
	show := &db.Item{LibraryID: lib.ID, Kind: "show", MatchKey: "tmdb:1", Title: "Show", TMDBID: 1, IMDBID: "tt1", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, show)
	season := &db.Item{LibraryID: lib.ID, Kind: "season", ParentID: show.ID, ShowID: show.ID, Title: "Season 1", SeasonNumber: 1, EpisodeNumber: -1}
	d.InsertItem(ctx, season)
	var eps []*db.Item
	for n := 1; n <= 3; n++ {
		ep := &db.Item{LibraryID: lib.ID, Kind: "episode", ParentID: season.ID, ShowID: show.ID, Title: fmt.Sprint("E", n), SeasonNumber: 1, EpisodeNumber: n}
		d.InsertItem(ctx, ep)
		eps = append(eps, ep)
	}

	ready, err := s.Ensure(ctx, eps[0], 10*time.Second)
	if !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	sts, _ := d.StreamsForItem(ctx, eps[0].ID)
	if len(sts) != 2 {
		t.Fatalf("episode 1 streams = %d, want 2160p and 1080p", len(sts))
	}
	for _, st := range sts {
		if st.Quality == "2160p" && st.InfoHash != hashB {
			t.Fatalf("2160p should skip the uncached remux, got %s", st.InfoHash)
		}
	}
	// The pack filled the rest of the season at 1080p without more lookups.
	for _, ep := range eps[1:] {
		sts, _ := d.StreamsForItem(ctx, ep.ID)
		if len(sts) != 1 || sts[0].Quality != "1080p" || sts[0].InfoHash != hashA {
			t.Fatalf("episode %d streams = %+v", ep.EpisodeNumber, sts)
		}
	}
	files, _ := d.FilesForItem(ctx, eps[1].ID)
	if len(files) != 1 || files[0].StreamID == "" || files[0].Height != 1080 {
		t.Fatalf("episode 2 files = %+v", files)
	}

	// Playback proxies byte ranges from the CDN; an expired link is refreshed.
	prov.stale = true
	src, err := s.Source(ctx, files[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveStreams() != 1 {
		t.Fatalf("active streams = %d", s.ActiveStreams())
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=10-19")
	src.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "0123456789" {
		t.Fatalf("proxy = %d %q", rec.Code, rec.Body.String())
	}
	buf := make([]byte, 5)
	if n, err := src.ReaderAt().ReadAt(buf, 3); n != 5 || err != nil || string(buf) != "34567" {
		t.Fatalf("ReadAt = %d %v %q", n, err, buf)
	}
	src.Close()
	if s.ActiveStreams() != 0 {
		t.Fatal("closing the source should free its slot")
	}

	// The server-wide stream limit.
	cfg.Streaming.Limits.MaxStreams = 1
	a, err := s.Source(ctx, files[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Source(ctx, files[0]); err != ErrBusy {
		t.Fatalf("second stream = %v, want ErrBusy", err)
	}
	a.Close()

	// Without a debrid key streaming is off.
	s.testProviders = []Provider{}
	if s.Active() {
		t.Fatal("no providers should mean inactive")
	}
}
