package livetv

import (
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
