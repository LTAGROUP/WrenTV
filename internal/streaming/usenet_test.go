package streaming

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
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
			if q.Get("imdbid") != "0000002" {
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
	if n := fetched.Load(); n > 3 {
		t.Fatalf("fetched %d NZBs for one stream", n)
	}
}
