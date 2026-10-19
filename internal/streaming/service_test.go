package streaming

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

type fakeProvider struct {
	mu      sync.Mutex
	cdn     string
	cached  map[string][]RemoteFile
	stale   bool // Link fails once, as when the debrid item expired
	slow    map[string]time.Duration
	adds    int
	checked int
}

func (f *fakeProvider) Name() string { return "torbox" }
func (f *fakeProvider) Check(_ context.Context, hashes []string) (map[string]bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checked++
	out := map[string]bool{}
	for _, h := range hashes {
		_, out[h] = f.cached[h]
	}
	return out, true, nil
}
func (f *fakeProvider) Add(_ context.Context, hash, _ string) (*Torrent, error) {
	f.mu.Lock()
	d := f.slow[hash]
	f.mu.Unlock()
	time.Sleep(d)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds++
	files, ok := f.cached[hash]
	if !ok {
		return nil, ErrNotCached
	}
	return &Torrent{Ref: "t-" + hash, Files: files}, nil
}
func (f *fakeProvider) Link(_ context.Context, ref, file string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stale {
		f.stale = false
		return "", ErrStale
	}
	return f.cdn + "/" + ref + "/" + file, nil
}

// waitLookups waits for background lookups: Ensure returns once every
// quality has a stream, while the other choices are still being found.
func waitLookups(t *testing.T, s *Service) {
	t.Helper()
	for range 500 {
		s.mu.Lock()
		n := len(s.lookups)
		s.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("lookup didn't finish")
}

func TestMovieChoicesPreferUsenet(t *testing.T) {
	ctx := context.Background()
	h := func(c string) string { return strings.Repeat(c, 40) }
	var mu sync.Mutex
	opened := map[string]int{}
	var addon *httptest.Server
	addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/play/"):
			// AIOStreams adds the post to TorBox, then redirects to its CDN.
			mu.Lock()
			opened[r.URL.Path]++
			mu.Unlock()
			http.Redirect(w, r, addon.URL+"/cdn/"+strings.TrimPrefix(r.URL.Path, "/play/")+"/Movie.2024.mkv", http.StatusFound)
			return
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			w.Header().Set("Content-Type", "video/x-matroska")
			w.Header().Set("Content-Range", "bytes 0-0/8000000000")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte{0})
			return
		}
		usenet := func(id, name string, cached bool) map[string]any {
			return map[string]any{"name": "[TB⚡] AIOStreams 2160p", "description": "🎥 WEB-DL 📦 15 GB", "url": addon.URL + "/play/" + id,
				"behaviorHints": map[string]any{"filename": name + ".mkv", "videoSize": 15 << 30},
				"streamData":    map[string]any{"type": "usenet", "folderName": name, "service": map[string]any{"id": "torbox", "cached": cached}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{
			{"title": "Movie.2024.2160p.REMUX-GRP\n👤 900 💾 50 GB", "infoHash": h("a")},
			{"title": "Movie.2024.2160p.WEB-DL-GRP\n👤 500 💾 15 GB", "infoHash": h("b")},
			{"title": "Movie.2024.2160p.BluRay.x265-OTHER\n👤 50 💾 20 GB", "infoHash": h("c")},
			{"title": "Movie.2024.1080p.BluRay-GRP\n👤 300 💾 10 GB", "infoHash": h("d")},
			{"title": "Movie.2024.1080p.WEB-DL-GRP\n👤 200 💾 5 GB", "infoHash": h("e")},
			usenet("u1", "Movie.2024.2160p.WEB-DL-NZB", true),
			// The same release as torrent b: offered once, from Usenet.
			usenet("u2", "Movie.2024.2160p.WEB-DL-GRP", true),
			// Not cached: opening it would start a download.
			usenet("u3", "Movie.2024.2160p.REMUX-SLOW", false),
			{"name": "[❌] AIOStreams", "description": "Something failed", "url": addon.URL + "/play/err", "streamData": map[string]any{"type": "error"}},
		}})
	}))
	defer addon.Close()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"2160p", "1080p"}, PerQuality: 3, Usenet: true,
		Addons: []config.AddonConfig{{Name: "fake", URL: addon.URL + "/manifest.json", Enabled: true}}}}
	file := func(n string) []RemoteFile { return []RemoteFile{{ID: "1", Name: n + ".mkv", Size: 1000}} }
	prov := &fakeProvider{cdn: "http://cdn", cached: map[string][]RemoteFile{
		h("a"): file("a"), h("b"): file("b"), h("c"): file("c"), h("d"): file("d"), h("e"): file("e")}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{prov}
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:2", Title: "Movie", TMDBID: 2, IMDBID: "tt2", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	if ready, err := s.Ensure(ctx, it, 10*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	waitLookups(t, s)
	sts, _ := d.StreamsForItem(ctx, it.ID)
	var got []string
	for _, st := range sts {
		got = append(got, fmt.Sprintf("%s/%d/%s/%s", st.Quality, st.Rank, st.Kind, cmp.Or(st.InfoHash, st.Release)[:2]))
	}
	want := []string{"2160p/0/usenet/Mo", "2160p/1/usenet/Mo", "2160p/2/torrent/" + h("a")[:2],
		"1080p/0/torrent/" + h("d")[:2], "1080p/1/torrent/" + h("e")[:2]}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("streams =\n%v\nwant\n%v", got, want)
	}
	// Someone's waiting, so whichever post is found first goes first.
	u1 := sts[0]
	if sts[1].Release == "Movie.2024.2160p.WEB-DL-NZB" {
		u1 = sts[1]
	}
	if u1.Release != "Movie.2024.2160p.WEB-DL-NZB" || u1.Size != 8000000000 || u1.FileName != "Movie.2024.2160p.WEB-DL-NZB.mkv" {
		t.Fatalf("usenet stream = %+v", u1)
	}
	files, _ := d.FilesForItem(ctx, it.ID)
	if len(files) != 5 || files[0].StreamRank != 0 || files[0].Tag != "Usenet · WEB-DL" || files[0].StreamID != sts[0].ID {
		t.Fatalf("files = %+v", files[0])
	}
	// The first play uses where the link led during the lookup; later ones
	// open the link again.
	if u, _, err := s.linkFor(ctx, u1.ID, false); err != nil || u != addon.URL+"/cdn/u1/Movie.2024.mkv" {
		t.Fatalf("usenet link = %q, %v", u, err)
	}
	if _, _, err := s.linkFor(ctx, u1.ID, true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if opened["/play/u1"] != 2 || opened["/play/u3"] != 0 || opened["/play/err"] != 0 {
		t.Fatalf("opened = %v", opened)
	}
	mu.Unlock()
	// A dropped stream is replaced by a later top-up; the others keep playing.
	d.DeleteStream(ctx, u1.ID)
	d.PutStreamLookup(ctx, db.StreamLookup{ItemID: it.ID, LookedAt: 0})
	if ready, _ := s.Ensure(ctx, it, time.Second); !ready {
		t.Fatal("remaining streams should still be ready")
	}
	waitLookups(t, s)
	if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) != 5 {
		t.Fatalf("after top-up = %d streams", len(sts))
	}
}

// Playback starts once a 4K stream is found, while 1080p is still looked
// up; each stream stored tells the apps.
func TestReadyOnFirstBestStream(t *testing.T) {
	ctx := context.Background()
	h := func(c string) string { return strings.Repeat(c, 40) }
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{
			{"title": "Movie.2024.2160p.WEB-DL-GRP\n👤 500 💾 15 GB", "infoHash": h("a")},
			{"title": "Movie.2024.1080p.WEB-DL-GRP\n👤 200 💾 5 GB", "infoHash": h("b")},
		}})
	}))
	defer addon.Close()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"2160p", "1080p"}, PerQuality: 1,
		Addons: []config.AddonConfig{{Name: "fake", URL: addon.URL + "/manifest.json", Enabled: true}}}}
	file := func(n string) []RemoteFile { return []RemoteFile{{ID: "1", Name: n + ".mkv", Size: 1000}} }
	prov := &fakeProvider{cdn: "http://cdn", cached: map[string][]RemoteFile{h("a"): file("a"), h("b"): file("b")},
		slow: map[string]time.Duration{h("b"): 2 * time.Second}}
	hub := library.NewHub()
	events, stop := hub.Subscribe()
	defer stop()
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: hub})
	s.testProviders = []Provider{prov}
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:3", Title: "Movie", TMDBID: 3, IMDBID: "tt3", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	t0 := time.Now()
	if ready, err := s.Ensure(ctx, it, 10*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	if took := time.Since(t0); took > time.Second {
		t.Fatalf("ready took %v; it waited for 1080p", took)
	}
	if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) != 1 || sts[0].Quality != "2160p" {
		t.Fatalf("streams at ready = %+v", sts)
	}
	waitLookups(t, s)
	if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) != 2 {
		t.Fatalf("streams = %d", len(sts))
	}
	updated := 0
	for len(events) > 0 {
		if e := <-events; e.Type == "item.updated" && e.ItemID == it.ID {
			updated++
		}
	}
	if updated != 2 {
		t.Fatalf("item.updated sent %d times", updated)
	}
}

// Torrents found while slow posts are still read stand in for them: one
// is kept, after whatever the posts find.
func TestTorrentStandsInForSlowPosts(t *testing.T) {
	ctx := context.Background()
	h := func(c string) string { return strings.Repeat(c, 40) }
	defer func(d time.Duration) { torrentAfter = d }(torrentAfter)
	torrentAfter = 100 * time.Millisecond
	var addon *httptest.Server
	addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/play/"):
			time.Sleep(500 * time.Millisecond)
			http.Redirect(w, r, addon.URL+"/cdn/Movie.2024.mkv", http.StatusFound)
			return
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			w.Header().Set("Content-Type", "video/x-matroska")
			w.Header().Set("Content-Range", "bytes 0-0/8000000000")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte{0})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{
			{"title": "Movie.2024.2160p.REMUX-GRP\n👤 900 💾 50 GB", "infoHash": h("a")},
			{"title": "Movie.2024.2160p.WEB-DL-GRP\n👤 500 💾 15 GB", "infoHash": h("b")},
			{"title": "Movie.2024.2160p.BluRay.x265-OTHER\n👤 50 💾 20 GB", "infoHash": h("c")},
			{"name": "[TB⚡] AIOStreams 2160p", "description": "🎥 WEB-DL 📦 15 GB", "url": addon.URL + "/play/u1",
				"behaviorHints": map[string]any{"filename": "Movie.2024.2160p.WEB-DL-NZB.mkv", "videoSize": 15 << 30},
				"streamData":    map[string]any{"type": "usenet", "folderName": "Movie.2024.2160p.WEB-DL-NZB", "service": map[string]any{"id": "torbox", "cached": true}}},
		}})
	}))
	defer addon.Close()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"2160p"}, PerQuality: 2, Usenet: true,
		Addons: []config.AddonConfig{{Name: "fake", URL: addon.URL + "/manifest.json", Enabled: true}}}}
	file := func(n string) []RemoteFile { return []RemoteFile{{ID: "1", Name: n + ".mkv", Size: 1000}} }
	prov := &fakeProvider{cdn: "http://cdn", cached: map[string][]RemoteFile{h("a"): file("a"), h("b"): file("b"), h("c"): file("c")}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{prov}
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:4", Title: "Movie", TMDBID: 4, IMDBID: "tt4", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	if ready, err := s.Ensure(ctx, it, 10*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	waitLookups(t, s)
	sts, _ := d.StreamsForItem(ctx, it.ID)
	var got []string
	for _, st := range sts {
		got = append(got, fmt.Sprintf("%d/%s", st.Rank, st.Kind))
	}
	if want := "0/usenet 100/torrent"; strings.Join(got, " ") != want {
		t.Fatalf("streams = %v, want %s", got, want)
	}
}

// Someone waiting to play gets the best quality's top posts first, read
// together rather than in turn; the other qualities are looked up once one
// is ready.
func TestBestPostFirst(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var log []string
	var addon *httptest.Server
	addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/play/"):
			id := strings.TrimPrefix(r.URL.Path, "/play/")
			mu.Lock()
			log = append(log, id+" start")
			mu.Unlock()
			time.Sleep(200 * time.Millisecond)
			mu.Lock()
			log = append(log, id+" end")
			mu.Unlock()
			http.Redirect(w, r, addon.URL+"/cdn/Movie.2024.mkv", http.StatusFound)
			return
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			w.Header().Set("Content-Type", "video/x-matroska")
			w.Header().Set("Content-Range", "bytes 0-0/8000000000")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte{0})
			return
		}
		post := func(id, name string) map[string]any {
			return map[string]any{"name": "[TB⚡] AIOStreams", "description": "🎥 WEB-DL 📦 15 GB", "url": addon.URL + "/play/" + id,
				"behaviorHints": map[string]any{"filename": name + ".mkv", "videoSize": 15 << 30},
				"streamData":    map[string]any{"type": "usenet", "folderName": name, "service": map[string]any{"id": "torbox", "cached": true}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{
			post("a", "Movie.2024.2160p.WEB-DL-AAA"), post("b", "Movie.2024.2160p.WEB-DL-BBB"), post("c", "Movie.2024.1080p.WEB-DL-CCC"),
		}})
	}))
	defer addon.Close()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"2160p", "1080p"}, PerQuality: 2, Usenet: true,
		Addons: []config.AddonConfig{{Name: "fake", URL: addon.URL + "/manifest.json", Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{&fakeProvider{cdn: "http://cdn"}}
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:5", Title: "Movie", TMDBID: 5, IMDBID: "tt5", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	if ready, err := s.Ensure(ctx, it, 10*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	waitLookups(t, s)
	mu.Lock()
	defer mu.Unlock()
	at := map[string]int{}
	for i, e := range log {
		at[e] = i
	}
	if len(log) != 6 || at["a start"] > 1 || at["b start"] > 1 {
		t.Fatalf("lookups = %v; the top 4K posts should start together", log)
	}
	if at["c start"] < at["a end"] && at["c start"] < at["b end"] {
		t.Fatalf("lookups = %v; 1080p should wait for a 4K post", log)
	}
	if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) != 3 {
		t.Fatalf("streams = %d", len(sts))
	}
}

// A watchlisted movie is looked up in the background before anyone plays it.
func TestWatchlistPrefetch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := func(c string) string { return strings.Repeat(c, 40) }
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{
			{"title": "Movie.2024.2160p.WEB-DL-GRP\n👤 500 💾 15 GB", "infoHash": h("a")},
		}})
	}))
	defer addon.Close()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"2160p"}, PerQuality: 1, Prefetch: true,
		Addons: []config.AddonConfig{{Name: "fake", URL: addon.URL + "/manifest.json", Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{&fakeProvider{cdn: "http://cdn", cached: map[string][]RemoteFile{h("a"): {{ID: "1", Name: "a.mkv", Size: 1000}}}}}
	s.Start(ctx)
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:6", Title: "Movie", TMDBID: 6, IMDBID: "tt6", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	s.PrefetchWatchlist("u1", it)
	for range 300 {
		if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) == 1 {
			waitLookups(t, s)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watchlisted movie wasn't looked up")
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
	waitLookups(t, s)
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

	// Without a debrid key the addon still plays; with neither, streaming is off.
	s.testProviders = []Provider{}
	if !s.Active() {
		t.Fatal("an addon should keep streaming on without debrid")
	}
	s.Cfg.Streaming.Addons = nil
	if s.Active() {
		t.Fatal("no providers, addons or Usenet should mean inactive")
	}
}

// An addon that streams Usenet itself, like StreamNZB, plays with no debrid
// service and no Usenet set up here.
func TestAddonPlaysWithoutDebrid(t *testing.T) {
	ctx := context.Background()
	var addon *httptest.Server
	var opened atomic.Int32
	addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/play/") {
			opened.Add(1)
			w.Header().Set("Content-Type", "video/x-matroska")
			w.Header().Set("Content-Range", "bytes 0-0/8000000000")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte{0})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{{
			"name": "StreamNZB · main", "description": "Movie.2024.2160p.WEB-DL-GRP\n📦 15 GB · NZBGeek",
			"url":           addon.URL + "/tok/play/abc",
			"behaviorHints": map[string]any{"filename": "Movie.2024.2160p.WEB-DL-GRP.mkv", "videoSize": 15 << 30, "notWebReady": true},
		}}})
	}))
	defer addon.Close()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"2160p"}, PerQuality: 1, Usenet: true,
		Addons: []config.AddonConfig{{Name: "streamnzb", URL: addon.URL + "/tok/manifest.json", Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{}
	if !s.Active() {
		t.Fatal("an addon alone should turn streaming on")
	}
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:2", Title: "Movie", TMDBID: 2, IMDBID: "tt2", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	if ready, err := s.Ensure(ctx, it, 10*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	waitLookups(t, s)
	sts, _ := d.StreamsForItem(ctx, it.ID)
	if len(sts) != 1 || sts[0].Release != "Movie.2024.2160p.WEB-DL-GRP.mkv" && sts[0].Release != "Movie.2024.2160p.WEB-DL-GRP" {
		t.Fatalf("streams = %+v", sts)
	}
	// Opening the link starts a stream on the addon: not until it's played.
	if n := opened.Load(); n != 0 {
		t.Fatalf("lookup opened the play link %d times", n)
	}
	if u, _, err := s.linkFor(ctx, sts[0].ID, true); err != nil || u != addon.URL+"/tok/play/abc" {
		t.Fatalf("link = %q, %v", u, err)
	}
}

// An addon that gives up on a release partway (StreamNZB) redirects to a
// short "stream failed" clip: that isn't played as part of the film, and
// the version is dropped so the next play uses another.
func TestAddonFailureClipNotPlayed(t *testing.T) {
	ctx := context.Background()
	var failed atomic.Bool
	var addon *httptest.Server
	addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/error/"):
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Content-Range", "bytes 0-0/2400000")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte{0})
		case strings.Contains(r.URL.Path, "/play/"):
			if failed.Load() {
				http.Redirect(w, r, addon.URL+"/error/failure.mp4", http.StatusTemporaryRedirect)
				return
			}
			w.Header().Set("Content-Type", "video/x-matroska")
			w.Header().Set("Content-Range", "bytes 0-0/8000000000")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte{0})
		default:
			json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{{
				"name": "StreamNZB", "description": "Movie.2024.2160p.WEB-DL-GRP\n📦 15 GB",
				"url":           addon.URL + "/play/abc",
				"behaviorHints": map[string]any{"filename": "Movie.2024.2160p.WEB-DL-GRP.mkv", "videoSize": 8000000000},
			}}})
		}
	}))
	defer addon.Close()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"2160p"}, PerQuality: 1,
		Addons: []config.AddonConfig{{Name: "streamnzb", URL: addon.URL + "/manifest.json", Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{}
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:7", Title: "Movie", TMDBID: 7, IMDBID: "tt7", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	if ready, err := s.Ensure(ctx, it, 10*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	waitLookups(t, s)
	files, _ := d.FilesForItem(ctx, it.ID)
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	src, err := s.Source(ctx, files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := src.ReaderAt().ReadAt(make([]byte, 1), 0); err != nil {
		t.Fatalf("first read: %v", err)
	}
	failed.Store(true)
	if _, err := src.ReaderAt().ReadAt(make([]byte, 1), 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("read after the addon gave up = %v, want ErrUnavailable", err)
	}
	if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) != 0 {
		t.Fatalf("the failed version should be dropped, have %d", len(sts))
	}
}
