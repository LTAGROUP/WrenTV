package streaming

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQualityAndEpisode(t *testing.T) {
	for in, want := range map[string]string{
		"Dune.Part.Two.2024.2160p.WEB-DL.DV.mkv": "2160p",
		"Show S01 4K HDR":                        "2160p",
		"Movie.2019.1080p.BluRay.x264":           "1080p",
		"Old.Movie.720p":                         "720p",
		"Movie.DVDRip":                           "",
	} {
		if got := QualityOf(in); got != want {
			t.Errorf("QualityOf(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string][2]int{
		"Show.S01E02.1080p.mkv":                    {1, 2},
		"Pack S01E01-E10/Show.s1e3.mkv":            {1, 3},
		"Show 2x05 HDTV.mp4":                       {2, 5},
		"Show.Season.1/Show - S01E12 - Title.mkv":  {1, 12},
		"Show (2008) Season 01. Episode 02. Remux": {1, 2},
	} {
		s, e, ok := EpisodeOf(in)
		if !ok || s != want[0] || e != want[1] {
			t.Errorf("EpisodeOf(%q) = %d,%d,%v want %v", in, s, e, ok, want)
		}
	}
}

func TestRankPrefersPacksAndDropsJunk(t *testing.T) {
	rels := []Release{
		{Name: "Show.S01E02.2160p.WEB-DL", Text: "Show.S01E02.2160p.WEB-DL\n👤 400", InfoHash: "a", Seeders: 400},
		{Name: "Show.S01.2160p.WEB-DL", Text: "Show.S01.2160p.WEB-DL\n👤 20", InfoHash: "b", Seeders: 20},
		{Name: "Show.S01E02.1080p", Text: "Show.S01E02.1080p", InfoHash: "c", Seeders: 900},
		{Name: "Show.S01E02.2160p.HDCAM", Text: "Show.S01E02.2160p.HDCAM", InfoHash: "d", Seeders: 999},
		{Name: "Show.S01E02.2160p.REMUX", Text: "Show.S01E02.2160p.REMUX", InfoHash: "e", Seeders: 50, Size: 90 << 30},
	}
	got := Rank(rels, "2160p", 60<<30, 1)
	if len(got) != 2 || got[0].InfoHash != "b" || got[1].InfoHash != "a" {
		t.Fatalf("rank = %+v", got)
	}
	// The .ts extension isn't a telesync.
	if r := Rank([]Release{{Text: "Movie.2020.1080p.WEB", FileName: "Movie.2020.1080p.WEB.ts", InfoHash: "f"}}, "1080p", 0, 0); len(r) != 1 {
		t.Fatal("a .ts file should not count as junk")
	}
}

func TestRankPrefersEnglish(t *testing.T) {
	rels := []Release{
		{Text: "오펜하이머 Oppenheimer,2023.1080p.KORSUB.WEBRip.H264.AAC.mkv", InfoHash: "kor", Seeders: 500},
		{Text: "Оппенгеймер / Oppenheimer [2023 BDRip 1080p] [IMAX] 2x Dub", InfoHash: "rus", Seeders: 500},
		{Text: "Oppenheimer.2023.MULTi.VFQ.1080p.BluRay", InfoHash: "fr", Seeders: 500},
		{Text: "Oppenheimer.2023.1080p.HC.WEBRip", InfoHash: "hc", Seeders: 500},
		{Text: "Oppenheimer.2023.1080p.WEBRip.x264", InfoHash: "en", Seeders: 3},
		{Text: "Amélie.2001.1080p.BluRay", InfoHash: "accent", Seeders: 2},
	}
	got := Rank(rels, "1080p", 0, 0)
	var order []string
	for _, r := range got {
		order = append(order, r.InfoHash)
	}
	// English first whatever the seeders, burned-in subtitles last.
	if o := strings.Join(order, ","); len(order) != 6 || !strings.HasPrefix(o, "accent,en,") || !strings.HasSuffix(o, ",kor,hc") {
		t.Fatalf("rank = %s", o)
	}
}

func TestPickFile(t *testing.T) {
	files := []RemoteFile{
		{ID: "1", Name: "Pack/Show.S01E01.mkv", Size: 2},
		{ID: "2", Name: "Pack/Show.S01E02.mkv", Size: 3},
		{ID: "3", Name: "Pack/Sample/Show.S01E02.sample.mkv", Size: 1},
		{ID: "4", Name: "Pack/Show.S01E02.nfo", Size: 9},
	}
	if f, ok := PickFile(files, "", 1, 2); !ok || f.ID != "2" {
		t.Fatalf("episode pick = %+v", f)
	}
	if f, ok := PickFile(files, "Show.S01E01.mkv", 1, 2); !ok || f.ID != "1" {
		t.Fatalf("file name hint should win, got %+v", f)
	}
	if f, ok := PickFile(files, "", 0, 0); !ok || f.ID != "2" {
		t.Fatalf("movie pick = largest video, got %+v", f)
	}
	if eps := EpisodeFiles(files, 1); len(eps) != 2 || eps[2].ID != "2" {
		t.Fatalf("episode files = %+v", eps)
	}
}

func TestReleaseFromTorrentio(t *testing.T) {
	var s addonStream
	_ = json.Unmarshal([]byte(`{"name":"Torrentio\n4k DV","title":"Dune.Part.Two.2024.2160p.WEB-DL.DV\n👤 152 💾 21.4 GB ⚙️ ThePirateBay",
		"infoHash":"0123456789ABCDEF0123456789abcdef01234567","fileIdx":3,"behaviorHints":{"filename":"Dune.Part.Two.mkv"},
		"sources":["tracker:udp://tracker.example:1337","dht:0123"]}`), &s)
	r, ok := releaseFrom(s, "torrentio")
	if !ok || r.Seeders != 152 || r.FileIdx != 3 || r.Size < 21<<30 || r.Name != "Dune.Part.Two.2024.2160p.WEB-DL.DV" ||
		r.InfoHash != "0123456789abcdef0123456789abcdef01234567" || QualityOf(r.Text) != "2160p" {
		t.Fatalf("release = %+v", r)
	}
	if m := r.Magnet(); m == "" || !strings.Contains(m, "tracker.example") {
		t.Fatalf("magnet = %s", m)
	}
	if a, err := ParseAddon("torrentio", "https://torrentio.strem.fun/manifest.json"); err != nil || a.BaseURL != "https://torrentio.strem.fun" {
		t.Fatalf("addon = %+v %v", a, err)
	}
	if a, err := ParseAddon("", "stremio://comet.feels.legal/abc/manifest.json"); err != nil || a.BaseURL != "https://comet.feels.legal/abc" {
		t.Fatalf("addon = %+v %v", a, err)
	}
}

func TestRankDropsUnplayable(t *testing.T) {
	rels := []Release{
		{Name: "Movie.2023.BDREMUX.2160p", Text: "2160p remux", FileName: "BDMV/STREAM/00000.m2ts"},
		{Name: "Movie.2023.2160p.BluRay.VVenC.H266-X.mp4", Text: "2160p bluray"},
		{Name: "Movie.2023.2160p.BluRay.x265-Y", Text: "2160p bluray", FileName: "Movie.2023.2160p.BluRay.x265-Y.mkv"},
	}
	if got := Rank(rels, "2160p", 0, 0); len(got) != 1 || got[0].Name != "Movie.2023.2160p.BluRay.x265-Y" {
		t.Fatalf("rank = %+v", got)
	}
}
