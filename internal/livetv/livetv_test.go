package livetv

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

const playlist = `#EXTM3U url-tvg="http://guide.example/epg.xml.gz"
#EXTINF:-1 tvg-id="bbc1.uk" tvg-name="BBC One" tvg-logo="http://logo/bbc1.png" group-title="UK, Entertainment",BBC One HD
http://iptv.example/live/u/p/1.ts
#EXTINF:-1 tvg-id="" group-title="News" tvg-chno="5",Sky News
#EXTVLCOPT:http-user-agent=Custom/1.0
http://iptv.example/live/u/p/2.m3u8
#EXTINF:-1 group-title="Movies",Some Film (2020)
http://iptv.example/movie/u/p/3.mkv
#EXTINF:-1,No Group
http://iptv.example/live/u/p/4.ts
`

func TestParseM3U(t *testing.T) {
	pl, err := ParseM3U(strings.NewReader(playlist), "src")
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Channels) != 3 {
		t.Fatalf("got %d channels, want 3 (the film left out)", len(pl.Channels))
	}
	c := pl.Channels[0]
	if c.Name != "BBC One HD" || c.TvgID != "bbc1.uk" || c.Group != "UK, Entertainment" || c.Logo != "http://logo/bbc1.png" {
		t.Errorf("first channel: %+v", c)
	}
	if n := pl.Channels[1]; n.Number != "5" || n.UserAgent != "Custom/1.0" {
		t.Errorf("second channel: %+v", n)
	}
	if len(pl.GuideURLs) != 1 || pl.GuideURLs[0] != "http://guide.example/epg.xml.gz" {
		t.Errorf("guide urls: %v", pl.GuideURLs)
	}
	if pl.Channels[0].ID == pl.Channels[2].ID {
		t.Error("ids collide")
	}
}

func TestNumbering(t *testing.T) {
	pl, _ := ParseM3U(strings.NewReader(playlist), "src")
	s := &Service{cfg: &config.Config{}, all: pl.Channels}
	s.number()
	var got []string
	for _, c := range s.visible {
		got = append(got, c.Number+" "+c.Name)
	}
	// Explicit 5 stays; the others take 1 and 2.
	want := "1 BBC One HD|2 No Group|5 Sky News"
	if strings.Join(got, "|") != want {
		t.Errorf("got %q, want %q", strings.Join(got, "|"), want)
	}
	s.cfg.LiveTV.HiddenGroups = []string{"News"}
	s.number()
	if len(s.visible) != 2 {
		t.Errorf("hiding News left %d channels", len(s.visible))
	}
}

const xmltv = `<?xml version="1.0" encoding="UTF-8"?>
<tv>
<channel id="bbc1.uk"><display-name>BBC One</display-name></channel>
<channel id="skynews"><display-name>Sky News HD</display-name></channel>
<programme start="20261016190000 +0000" stop="20261016200000 +0000" channel="bbc1.uk"><title>Earlier</title></programme>
<programme start="20261016210000 +0100" stop="20261016220000 +0100" channel="bbc1.uk"><title>The News</title><desc>Headlines.</desc><episode-num system="onscreen">S1E2</episode-num></programme>
<programme start="20261016200000 +0000" stop="20261016203000 +0000" channel="skynews"><title>Sky at Eight</title></programme>
<programme start="20261016200000 +0000" stop="20261016203000 +0000" channel="unknown"><title>Nobody</title></programme>
</tv>`

func TestParseXMLTV(t *testing.T) {
	pl, _ := ParseM3U(strings.NewReader(playlist), "src")
	g := Guide{}
	from := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	if err := ParseXMLTV(strings.NewReader(xmltv), pl.Channels, from, from.Add(48*time.Hour), g); err != nil {
		t.Fatal(err)
	}
	g.Tidy()
	bbc := g[pl.Channels[0].ID]
	if len(bbc) != 2 {
		t.Fatalf("bbc programmes: %+v", bbc)
	}
	// 21:00 +0100 is 20:00 UTC, straight after "Earlier".
	at := time.Date(2026, 10, 16, 19, 30, 0, 0, time.UTC).Unix()
	now, _ := g.NowNext(pl.Channels[0].ID, at)
	if now == nil || now.Title != "Earlier" {
		t.Errorf("now on bbc: %+v", now)
	}
	// Sky News has no tvg-id: matched by name, "HD" ignored.
	sky := g[pl.Channels[1].ID]
	if len(sky) != 1 || sky[0].Title != "Sky at Eight" {
		t.Errorf("sky: %+v", sky)
	}
}

// A provider can answer a guide request with a 200 HTML error page. That must
// be an error, not an empty guide that hides the broken link.
func TestParseXMLTVRejectsNonGuideResponses(t *testing.T) {
	nilGuide := Guide{}
	from := time.Now()
	if err := ParseXMLTV(strings.NewReader("<html><body>Not found</body></html>"), nil, from, from.Add(time.Hour), nilGuide); err == nil {
		t.Fatal("an HTML page was accepted as an XMLTV guide")
	}
	if err := ParseXMLTV(strings.NewReader(""), nil, from, from.Add(time.Hour), nilGuide); err == nil {
		t.Fatal("an empty guide was accepted")
	}
}

func TestRewrite(t *testing.T) {
	x := &Session{key: []byte("k")}
	base, _ := url.Parse("http://cdn.example/live/chan/index.m3u8")
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhi/index.m3u8\n"
	out := string(x.rewrite([]byte(master), base))
	if !strings.Contains(out, "\nindex.m3u8?u=") {
		t.Errorf("master not rewritten to playlists: %s", out)
	}
	media := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:6,\nseg1.ts\n"
	out = string(x.rewrite([]byte(media), base))
	if !strings.Contains(out, `URI="seg?u=`) || !strings.Contains(out, "\nseg?u=") {
		t.Errorf("media not rewritten to segments: %s", out)
	}
	line := out[strings.LastIndex(strings.TrimSpace(out), "\n")+1:]
	q, _ := url.ParseQuery(strings.TrimSpace(strings.TrimPrefix(line, "seg?")))
	got, ok := x.verify(q.Get("u"), q.Get("s"))
	if !ok || got != "http://cdn.example/live/chan/seg1.ts" {
		t.Errorf("verify: %q %v", got, ok)
	}
	if _, ok := x.verify(q.Get("u"), "00"); ok {
		t.Error("bad signature accepted")
	}
}

func TestXtreamURLs(t *testing.T) {
	src := config.LiveTVSource{Kind: "xtream", URL: "http://provider.tv:8080/player_api.php?x=1", Username: "me", Password: "pw"}
	if got := PlaylistURL(src); got != "http://provider.tv:8080/get.php?output=ts&password=pw&type=m3u_plus&username=me" {
		t.Errorf("playlist url: %s", got)
	}
	if got := GuideURLs(src, nil); len(got) != 1 || !strings.HasPrefix(got[0], "http://provider.tv:8080/xmltv.php?") {
		t.Errorf("guide url: %v", got)
	}
	if r := redact("Get \"http://p/get.php?password=pw&username=me\": EOF"); strings.Contains(r, "pw") || strings.Contains(r, "me\"") {
		t.Errorf("redact: %s", r)
	}
}

func TestTidyDropsOverlaps(t *testing.T) {
	g := Guide{"a": {{Title: "B", Start: 10, End: 20}, {Title: "A", Start: 0, End: 10}, {Title: "dup", Start: 5, End: 15}}}
	g.Tidy()
	if len(g["a"]) != 2 || g["a"][0].Title != "A" || g["a"][1].Title != "B" {
		t.Errorf("tidy: %+v", g["a"])
	}
}

// Sports search relies on the retained guide: a fixture four days out is
// searchable, while anything past the seven-day horizon is not kept in memory.
func TestGuideRetainsSevenDaySearchHorizon(t *testing.T) {
	now := time.Now()
	channel := &Channel{ID: "c1", Name: "Sports One", TvgID: "sports1"}
	entry := func(title string, start time.Time) string {
		return `<programme start="` + start.Format("20060102150405") + ` +0000" stop="` +
			start.Add(time.Hour).Format("20060102150405") + ` +0000" channel="sports1"><title>` + title + `</title></programme>`
	}
	doc := `<tv><channel id="sports1"><display-name>Sports One</display-name></channel>` +
		entry("Weekend fixture", now.Add(4*24*time.Hour)) +
		entry("Next month", now.Add(8*24*time.Hour)) + `</tv>`
	guide := Guide{}
	if err := ParseXMLTV(strings.NewReader(doc), []*Channel{channel}, now.Add(-guideBehind), now.Add(guideAhead), guide); err != nil {
		t.Fatal(err)
	}
	guide.Tidy()
	programs := guide[channel.ID]
	if len(programs) != 1 || programs[0].Title != "Weekend fixture" {
		t.Fatalf("seven-day guide = %+v", programs)
	}
	// The search helper reads the same retained window.
	service := &Service{visible: []*Channel{channel}, guide: guide}
	live, upcoming := service.SearchPrograms(func(title, description string) bool {
		return strings.Contains(strings.ToLower(title), "fixture")
	}, 10)
	if len(live) != 0 || len(upcoming) != 1 || upcoming[0].ChannelID != channel.ID {
		t.Fatalf("guide search = %+v %+v", live, upcoming)
	}
}

// Ephemeral sources use the same session and probe guards as channels, without
// ever accepting a non-HTTP location from a provider.
func TestPlayURLAndProbeStreamShareChannelGuards(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n")
	}))
	defer upstream.Close()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, t.TempDir())
	if _, err := s.PlayURL(context.Background(), "u", "addon:x", "file:///etc/passwd", "", false); err == nil {
		t.Fatal("non-HTTP source was accepted")
	}
	sess, err := s.PlayURL(context.Background(), "u", "addon:x", upstream.URL+"/live.m3u8", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Type() != "hls" {
		t.Fatalf("stream type = %q", sess.Type())
	}
	if !s.Stop(sess.ID, "u", false) || s.Playing() != 0 {
		t.Fatal("addon session was not stopped")
	}
	// Without ffprobe the honest answer is unavailable, never a guessed quality.
	if _, err := s.ProbeStream(context.Background(), upstream.URL+"/live.m3u8", ""); !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("probe without ffprobe = %v", err)
	}
}
