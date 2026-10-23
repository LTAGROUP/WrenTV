package streaming

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
	"github.com/LTAGROUP/wrentv/internal/usenet"
	"github.com/LTAGROUP/wrentv/internal/usenet/usenettest"
)

// fakeIndexer is a Newznab indexer with one post per NZB.
func fakeIndexer(t *testing.T, posts map[string][]byte, sizes map[string]int64, fetched *atomic.Int32) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("apikey") != "ixkey" {
			fmt.Fprint(w, `<error code="100" description="Incorrect user credentials"/>`)
			return
		}
		switch {
		case r.URL.Path == "/getnzb":
			fetched.Add(1)
			w.Write(posts[q.Get("id")])
		case q.Get("t") == "caps":
			fmt.Fprint(w, `<caps><searching><search available="yes"/><movie-search available="yes" supportedParams="q,imdbid"/>
				<tv-search available="yes" supportedParams="q,season,ep"/></searching></caps>`)
		case q.Get("t") == "movie":
			// By ID, then by name since the ID found few.
			if q.Get("imdbid") != "0000002" && q.Get("q") == "" {
				t.Errorf("searched imdbid %q", q.Get("imdbid"))
			}
			var items strings.Builder
			for title := range posts {
				fmt.Fprintf(&items, `<item><title>%s</title><enclosure url="%s/getnzb?id=%s&amp;apikey=ixkey" length="%d"/>
					<newznab:attr name="size" value="%d"/><newznab:attr name="grabs" value="40"/></item>`,
					title, srv.URL, title, sizes[title], sizes[title])
			}
			fmt.Fprintf(w, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>%s</channel></rss>`, items.String())
		default:
			fmt.Fprint(w, `<rss><channel></channel></rss>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUsenetLookupAndPlayback(t *testing.T) {
	ctx := context.Background()
	video := make([]byte, 600_000)
	rand.New(rand.NewSource(1)).Read(video)
	copy(video, []byte{0x1a, 0x45, 0xdf, 0xa3})

	// One complete post and one that lost an article to a takedown.
	good, dead := usenettest.New(t), usenettest.New(t)
	good.Post("Movie.2024.1080p.WEB-DL-GRP.mkv", video, 50_000)
	good.Post("Movie.2024.1080p.WEB-DL-GRP.nfo", []byte("info"), 50_000)
	dead.Post("Movie.2024.1080p.BluRay-OTHER.mkv", video, 50_000)
	// Both posts live on one news server; the dead one is missing an article.
	ns := usenettest.New(t)
	ns.Post("Movie.2024.1080p.WEB-DL-GRP.mkv", video, 50_000)
	ns.Post("Movie.2024.1080p.BluRay-OTHER.mkv", video, 50_000)
	ns.Remove("Movie.2024.1080p.BluRay-OTHER.mkv", 7)

	var fetched atomic.Int32
	ix := fakeIndexer(t, map[string][]byte{"Movie.2024.1080p.WEB-DL-GRP": good.NZB(), "Movie.2024.1080p.BluRay-OTHER": dead.NZB()},
		map[string]int64{"Movie.2024.1080p.WEB-DL-GRP": int64(len(video)), "Movie.2024.1080p.BluRay-OTHER": int64(len(video))}, &fetched)

	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{DataDir: t.TempDir(), Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"1080p"}, PerQuality: 2, Usenet: true,
		Indexers:    []config.IndexerConfig{{Name: "fake", URL: ix.URL + "/api", APIKey: "ixkey", Enabled: true}},
		NewsServers: []config.NewsServerConfig{{Host: ns.Host, Port: ns.Port, Username: "me", Password: "secret", Connections: 4, Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	// No debrid account and no addons: Usenet alone is enough.
	s.testProviders = []Provider{}
	if !s.Active() {
		t.Fatal("Usenet alone should make streaming active")
	}

	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:2", Title: "Movie", Year: 2024, TMDBID: 2, IMDBID: "tt0000002", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	if ready, err := s.Ensure(ctx, it, 20*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	waitLookups(t, s)
	sts, _ := d.StreamsForItem(ctx, it.ID)
	if len(sts) != 1 || sts[0].Release != "Movie.2024.1080p.WEB-DL-GRP" || sts[0].Provider != usenetProvider || sts[0].Size != int64(len(video)) {
		t.Fatalf("streams = %+v", sts)
	}
	if strings.Contains(sts[0].NZB, "ixkey") {
		t.Fatal("the stored NZB link has the API key")
	}
	files, _ := d.FilesForItem(ctx, it.ID)
	if len(files) != 1 || files[0].StreamID != sts[0].ID {
		t.Fatalf("files = %+v", files)
	}

	// Playback reads the stored layout and the cached NZB: nothing is
	// fetched from the indexer again, even after a restart.
	before := fetched.Load()
	s = New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{}
	src, err := s.Source(ctx, files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Size() != int64(len(video)) {
		t.Fatalf("size = %d", src.Size())
	}
	for _, rg := range [][2]int{{0, 99}, {49_990, 50_020}, {599_000, 599_999}, {123_456, 400_000}} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rg[0], rg[1]))
		src.(http.Handler).ServeHTTP(rec, req)
		if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), video[rg[0]:rg[1]+1]) {
			t.Fatalf("range %v = %d, %d bytes", rg, rec.Code, rec.Body.Len())
		}
	}
	buf := make([]byte, 10)
	if n, err := src.ReaderAt().ReadAt(buf, 300_000); n != 10 || err != nil || !bytes.Equal(buf, video[300_000:300_010]) {
		t.Fatalf("ReadAt = %d %v", n, err)
	}
	if fetched.Load() != before {
		t.Fatal("playback fetched the NZB again")
	}
	src.Close()

	// One article lost since: the play goes on over the gap.
	src, err = s.Source(ctx, files[0])
	if err != nil {
		t.Fatal(err)
	}
	ns.Remove("Movie.2024.1080p.WEB-DL-GRP.mkv", 11)
	get := func(rg string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Range", "bytes="+rg)
		src.(http.Handler).ServeHTTP(rec, req)
		return rec
	}
	if rec := get("490000-560000"); rec.Body.Len() != 70_001 || !bytes.Equal(rec.Body.Bytes()[:10_000], video[490_000:500_000]) {
		t.Fatalf("read over the gap: %d bytes", rec.Body.Len())
	}
	if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) != 1 {
		t.Fatalf("streams after one gap = %+v", sts)
	}

	// Five in a row are past repair: the next play's spot check finds
	// them, the post is dropped, and the lookup for a replacement doesn't
	// store it again.
	src.Close()
	for part := 5; part <= 9; part++ {
		ns.Remove("Movie.2024.1080p.WEB-DL-GRP.mkv", part)
	}
	if _, err := s.Source(ctx, files[0]); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("source with five gaps: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the post with a hole wasn't dropped")
		}
	}
	waitLookups(t, s)
	if sts, _ := d.StreamsForItem(ctx, it.ID); len(sts) != 0 {
		t.Fatalf("streams after the drop = %+v", sts)
	}
	if _, err := s.Source(ctx, files[0]); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("source after the drop: %v", err)
	}
}

// Posts that all fail mustn't use up the tries torrents get.
func TestDeadPostsStillTryTorrents(t *testing.T) {
	ctx := context.Background()
	video := make([]byte, 120_000)
	ns := usenettest.New(t)
	posts, sizes := map[string][]byte{}, map[string]int64{}
	for i := range 8 {
		title := fmt.Sprintf("Movie.2024.1080p.WEB-DL-P%d", i)
		one := usenettest.New(t)
		one.Post(title+".mkv", video, 40_000)
		ns.Post(title+".mkv", video, 40_000)
		ns.Remove(title+".mkv", 2)
		posts[title], sizes[title] = one.NZB(), int64(len(video))
	}
	var fetched atomic.Int32
	ix := fakeIndexer(t, posts, sizes, &fetched)
	hash := strings.Repeat("d", 40)
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"streams":[{"title":"Movie.2024.1080p.BluRay-GRP\n👤 50 💾 9 GB","infoHash":"%s"}]}`, hash)
	}))
	defer addon.Close()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{DataDir: t.TempDir(), Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"1080p"}, PerQuality: 1, Usenet: true,
		Addons:      []config.AddonConfig{{Name: "fake", URL: addon.URL + "/manifest.json", Enabled: true}},
		Indexers:    []config.IndexerConfig{{Name: "fake", URL: ix.URL + "/api", APIKey: "ixkey", Enabled: true}},
		NewsServers: []config.NewsServerConfig{{Host: ns.Host, Port: ns.Port, Username: "me", Password: "secret", Connections: 4, Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{&fakeProvider{cdn: "http://cdn", cached: map[string][]RemoteFile{hash: {{ID: "1", Name: "m.mkv", Size: 1000}}}}}
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:2", Title: "Movie", Year: 2024, TMDBID: 2, IMDBID: "tt0000002", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)
	if ready, err := s.Ensure(ctx, it, 20*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	waitLookups(t, s)
	sts, _ := d.StreamsForItem(ctx, it.ID)
	if len(sts) != 1 || sts[0].InfoHash != hash {
		t.Fatalf("streams = %+v", sts)
	}
	if n := fetched.Load(); n > 1+postSpare {
		t.Fatalf("fetched %d NZBs for one stream", n)
	}
}

func TestNamedFor(t *testing.T) {
	for _, c := range []struct {
		name, title  string
		year, season int
		want         bool
	}{
		{"Supergirl.2026.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR.H.265-FLUX", "Supergirl", 2026, 0, true},
		{"Supergirl 2025 1080p WEBRip", "Supergirl", 2026, 0, true},
		{"Supergirl.1984.1080p.BluRay", "Supergirl", 2026, 0, false},
		{"Supergirl.S01E01.1080p", "Supergirl", 2026, 0, false},
		{"Supergirl.Woman.of.Tomorrow.2026.1080p", "Supergirl", 2026, 0, false},
		{"Spider.Man.No.Way.Home.2021.1080p", "Spider-Man: No Way Home", 2021, 0, true},
		{"Fast.and.Furious.2009.720p", "Fast & Furious", 2009, 0, true},
		{"Supergirl.S03E04.1080p.WEB", "Supergirl", 2015, 3, true},
		{"Supergirl.2015.S03E04.1080p.WEB", "Supergirl", 2015, 3, true},
		{"Supergirl.2026.1080p", "Supergirl", 2015, 3, false},
		{"Supergirl", "Supergirl", 2026, 0, false},
	} {
		if got := namedFor(c.name, c.title, c.year, c.season); got != c.want {
			t.Errorf("namedFor(%q, %q) = %v, want %v", c.name, c.title, got, c.want)
		}
	}
}

// A post found with articles gone isn't read again by later lookups, until
// the news servers change.
func TestMissingPostRemembered(t *testing.T) {
	ctx := context.Background()
	video := make([]byte, 300_000)
	rand.New(rand.NewSource(2)).Read(video)
	copy(video, []byte{0x1a, 0x45, 0xdf, 0xa3})
	const name = "Movie.2024.1080p.WEB-DL-GONE"
	post, ns := usenettest.New(t), usenettest.New(t)
	post.Post(name+".mkv", video, 50_000)
	ns.Post(name+".mkv", video, 50_000)
	ns.Remove(name+".mkv", 3)
	var fetched atomic.Int32
	ix := fakeIndexer(t, map[string][]byte{name: post.NZB()}, map[string]int64{name: int64(len(video))}, &fetched)

	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{DataDir: t.TempDir(), Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"1080p"}, PerQuality: 1, Usenet: true,
		Indexers:    []config.IndexerConfig{{Name: "fake", URL: ix.URL + "/api", APIKey: "ixkey", Enabled: true}},
		NewsServers: []config.NewsServerConfig{{Host: ns.Host, Port: ns.Port, Username: "me", Password: "secret", Connections: 4, Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{}
	lib, _ := d.StreamLibrary(ctx, "movies")
	it := &db.Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:2", Title: "Movie", Year: 2024, TMDBID: 2, IMDBID: "tt0000002", SeasonNumber: -1, EpisodeNumber: -1}
	d.InsertItem(ctx, it)

	lookup := func() {
		t.Helper()
		// Forget the miss and the NZB, so only the memory of the
		// missing articles keeps the post from being read.
		_ = d.PutStreamLookup(ctx, db.StreamLookup{ItemID: it.ID})
		os.RemoveAll(filepath.Join(cfg.DataDir, "cache", "nzb"))
		s.usenet.mu.Lock()
		s.usenet.nzbs = nil
		s.usenet.mu.Unlock()
		s.Ensure(ctx, it, 10*time.Second)
		waitLookups(t, s)
	}
	lookup()
	if fetched.Load() != 1 {
		t.Fatalf("NZB fetched %d times, want 1", fetched.Load())
	}
	lookup()
	if fetched.Load() != 1 {
		t.Fatal("a post with articles gone was read again")
	}
	// Another server might have them.
	cfg.Streaming.NewsServers[0].Connections = 5
	lookup()
	if fetched.Load() != 2 {
		t.Fatalf("after the servers changed, NZB fetched %d times, want 2", fetched.Load())
	}
}

// An NZB asked for while it's being fetched waits for that fetch rather
// than downloading it again.
func TestNZBFetchedOnce(t *testing.T) {
	post := usenettest.New(t)
	post.Post("Movie.2024.1080p.WEB-DL-ONE.mkv", make([]byte, 1000), 500)
	var fetched atomic.Int32
	ix := fakeIndexer(t, map[string][]byte{"one": post.NZB()}, nil, &fetched)
	cfg := &config.Config{DataDir: t.TempDir(), Streaming: config.StreamingConfig{Enabled: true, Usenet: true,
		Indexers: []config.IndexerConfig{{Name: "fake", URL: ix.URL + "/api", APIKey: "ixkey", Enabled: true}}}}
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	link := ix.URL + "/getnzb?id=one"
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n, err := s.nzb(context.Background(), link); err != nil || len(n.Files) != 1 {
				t.Errorf("nzb = %v, %v", n, err)
			}
		}()
	}
	wg.Wait()
	if fetched.Load() != 1 {
		t.Fatalf("NZB fetched %d times, want 1", fetched.Load())
	}
}

func TestUsenetSeasonPack(t *testing.T) {
	ctx := context.Background()
	video := make([]byte, 300_000)
	rand.New(rand.NewSource(2)).Read(video)
	copy(video, []byte{0x1a, 0x45, 0xdf, 0xa3})

	pack, single, ns := usenettest.New(t), usenettest.New(t), usenettest.New(t)
	for n := 1; n <= 3; n++ {
		name := fmt.Sprintf("Show.S01E%02d.1080p.WEB-DL-GRP.mkv", n)
		pack.Post(name, video, 50_000)
		ns.Post(name, video, 50_000)
	}
	single.Post("Show.S01E01.1080p.BluRay.REMUX-SOLO.mkv", video, 50_000)
	ns.Post("Show.S01E01.1080p.BluRay.REMUX-SOLO.mkv", video, 50_000)
	posts := map[string][]byte{"Show.S01.1080p.WEB-DL-GRP": pack.NZB(), "Show.S01E01.1080p.BluRay.REMUX-SOLO": single.NZB()}

	var episodeSearches, seasonSearches atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		item := func(title string) string {
			return fmt.Sprintf(`<item><title>%s</title><enclosure url="%s/getnzb?id=%s&amp;apikey=ixkey" length="%d"/>
				<newznab:attr name="size" value="%d"/><newznab:attr name="grabs" value="900"/></item>`, title, srv.URL, title, len(video), len(video))
		}
		var items string
		switch {
		case r.URL.Path == "/getnzb":
			w.Write(posts[q.Get("id")])
			return
		case q.Get("t") == "caps":
			fmt.Fprint(w, `<caps><searching><search available="yes"/><tv-search available="yes" supportedParams="q,season,ep,imdbid"/></searching></caps>`)
			return
		case q.Get("t") == "tvsearch" && q.Get("ep") != "":
			episodeSearches.Add(1)
			items = item("Show.S01E01.1080p.BluRay.REMUX-SOLO")
		case q.Get("t") == "tvsearch":
			// The season search finds single episodes too; only packs are kept.
			seasonSearches.Add(1)
			items = item("Show.S01.1080p.WEB-DL-GRP") + item("Show.S01E01.1080p.BluRay.REMUX-SOLO") + item("Show.S01E02.1080p.WEB-DL-X")
		}
		fmt.Fprintf(w, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>%s</channel></rss>`, items)
	}))
	t.Cleanup(srv.Close)

	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{DataDir: t.TempDir(), Streaming: config.StreamingConfig{Enabled: true, Qualities: []string{"1080p"}, PerQuality: 1, Usenet: true,
		Indexers:    []config.IndexerConfig{{Name: "fake", URL: srv.URL + "/api", APIKey: "ixkey", Enabled: true}},
		NewsServers: []config.NewsServerConfig{{Host: ns.Host, Port: ns.Port, Username: "me", Password: "secret", Connections: 4, Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	s.testProviders = []Provider{}

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

	if ready, err := s.Ensure(ctx, eps[0], 20*time.Second); !ready || err != nil {
		t.Fatalf("ensure = %v, %v", ready, err)
	}
	waitLookups(t, s)
	if episodeSearches.Load() == 0 || seasonSearches.Load() == 0 {
		t.Fatalf("searches: episode %d, season %d", episodeSearches.Load(), seasonSearches.Load())
	}
	// The pack wins over the better-tagged single episode and fills the season.
	for _, ep := range eps {
		sts, _ := d.StreamsForItem(ctx, ep.ID)
		if len(sts) != 1 || sts[0].Release != "Show.S01.1080p.WEB-DL-GRP" || sts[0].Provider != usenetProvider {
			t.Fatalf("episode %d streams = %+v", ep.EpisodeNumber, sts)
		}
	}
	files, _ := d.FilesForItem(ctx, eps[2].ID)
	if len(files) != 1 {
		t.Fatalf("episode 3 files = %+v", files)
	}
	src, err := s.Source(ctx, files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	buf := make([]byte, 100)
	if n, err := src.ReaderAt().ReadAt(buf, 120_000); n != 100 || err != nil || !bytes.Equal(buf, video[120_000:120_100]) {
		t.Fatalf("read episode 3 from the pack: %d, %v", n, err)
	}
}

func TestSeasonPackSearchShared(t *testing.T) {
	var seasonSearches atomic.Int32
	release := make(chan struct{})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var items string
		switch {
		case q.Get("t") == "caps":
			fmt.Fprint(w, `<caps><searching><search available="yes"/><tv-search available="yes" supportedParams="q,season,ep,imdbid"/></searching></caps>`)
			return
		case q.Get("ep") != "":
			items = fmt.Sprintf(`<item><title>Show.S01E%02s.1080p.WEB</title><enclosure url="%s/getnzb?id=ep" length="1"/></item>`, q.Get("ep"), srv.URL)
		default:
			if q.Get("imdbid") != "" {
				seasonSearches.Add(1) // not counting the search by name that follows
			}
			<-release // a slow indexer
			items = fmt.Sprintf(`<item><title>Show.S01.1080p.WEB-DL-GRP</title><enclosure url="%s/getnzb?id=pack" length="1"/></item>`, srv.URL)
		}
		fmt.Fprintf(w, `<rss><channel>%s</channel></rss>`, items)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{DataDir: t.TempDir(), Streaming: config.StreamingConfig{Enabled: true, Usenet: true,
		Indexers: []config.IndexerConfig{{Name: "fake", URL: srv.URL + "/api", APIKey: "ixkey", Enabled: true}}}}
	s := New(Deps{Cfg: cfg, DB: d, TMDB: tmdb.New("test", "en-US", d), Hub: library.NewHub()})
	names := func(rels []Release) string {
		var n []string
		for _, r := range rels {
			n = append(n, r.Name)
		}
		return strings.Join(n, ",")
	}

	// The episode lookup isn't held up by the slow season search.
	start := time.Now()
	got := s.searchIndexers(context.Background(), usenet.Query{IMDB: "tt1", Title: "Show", Season: 1, Episode: 1})
	if took := time.Since(start); took > packWait+time.Second || names(got) != "Show.S01E01.1080p.WEB" {
		t.Fatalf("episode 1: %s in %v", names(got), took)
	}
	close(release)
	// The next episodes reuse the season search and get its pack.
	for ep := 2; ep <= 4; ep++ {
		got = s.searchIndexers(context.Background(), usenet.Query{IMDB: "tt1", Title: "Show", Season: 1, Episode: ep})
		if want := fmt.Sprintf("Show.S01E%02d.1080p.WEB,Show.S01.1080p.WEB-DL-GRP", ep); names(got) != want {
			t.Fatalf("episode %d: %s", ep, names(got))
		}
	}
	if n := seasonSearches.Load(); n != 1 {
		t.Fatalf("season searched %d times", n)
	}
}
