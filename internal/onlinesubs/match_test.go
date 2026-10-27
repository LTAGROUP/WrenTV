package onlinesubs

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
)

func TestReleaseScore(t *testing.T) {
	file := "Lucifer.S02E08.2016.1080p.Netflix.WEB-DL.AVC.DDP.5.1-DBTV.mkv"
	cases := []struct {
		sub  string
		want int
	}{
		{"Lucifer.S02.2016.1080p.NF.WEB-DL.DDP5.1.x264-DBTV", 10}, // same source, service, group, resolution
		{"Lucifer.S02E08.720p.WEBRip.x264-GalaxyTV", 4},
		{"Lucifer.S02E08.HDTV.x264-LOL.C", -4},
		{"Lucifer.S02.1080p.BluRay.x265-YAWNiX", -3},
		{"Lucifer S02E08", 0},
	}
	for _, c := range cases {
		if got := releaseScore(file, c.sub); got != c.want {
			t.Errorf("%s: %d, want %d", c.sub, got, c.want)
		}
	}
	if releaseScore("", "Lucifer.S02E08.HDTV.x264-LOL") != 0 {
		t.Error("no file name should score nothing")
	}
}

func TestHash(t *testing.T) {
	const size = 200 << 10
	b := make([]byte, size)
	binary.LittleEndian.PutUint64(b[0:], 5)           // in the head
	binary.LittleEndian.PutUint64(b[size-8:], 7)      // in the tail
	binary.LittleEndian.PutUint64(b[100<<10:], 1<<40) // in neither
	h, err := Hash(bytes.NewReader(b), size)
	if err != nil {
		t.Fatal(err)
	}
	if h != "000000000003200c" { // 204800 + 5 + 7
		t.Fatalf("hash %s, want 000000000003200c", h)
	}
	if _, err := Hash(bytes.NewReader(b[:100]), 100); err == nil {
		t.Fatal("tiny file hashed")
	}
}

func TestSearchRanksMatchingRelease(t *testing.T) {
	rs := []Result{
		{Name: "Lucifer.S02E08.HDTV.x264-LOL", Language: "en", Downloads: 9000},
		{Name: "Lucifer.S02.1080p.NF.WEB-DL-DBTV", Language: "en", Downloads: 10},
		{Name: "Exact", Language: "en", Downloads: 1, hashMatch: true},
		{Name: "Lucifer.S02E08.1080p.NF.WEB-DL-DBTV", Language: "fr", Downloads: 50000},
	}
	s := &Service{}
	got := s.rank(rs, Query{Languages: []string{"en"}, Release: "Lucifer.S02E08.2016.1080p.Netflix.WEB-DL.AVC.DDP.5.1-DBTV.mkv"})
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	want := []string{"Exact", "Lucifer.S02.1080p.NF.WEB-DL-DBTV", "Lucifer.S02E08.HDTV.x264-LOL", "Lucifer.S02E08.1080p.NF.WEB-DL-DBTV"}
	if !slices.Equal(names, want) {
		t.Fatalf("order %q", names)
	}
	if !got[0].MatchesFile || !got[1].MatchesFile || got[2].MatchesFile {
		t.Fatalf("matches %+v", got)
	}
}
