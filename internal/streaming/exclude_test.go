package streaming

import "testing"

func TestExcludeMatcher(t *testing.T) {
	ex, err := ExcludeMatcher([]string{"TS", "korean dub", "/\\bhevc\\b/i", " "})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"Movie.2023.1080p.BluRay.DTS-HD.MA.x264": false,
		"Movie.2023.TS.x264":                     true,
		"Movie_2023_TS_x264":                     true,
		"Movie.2023.1080p.Korean.Dub.WEB":        true,
		"Movie 2023 KOREAN-DUB 1080p":            true,
		"Movie.2023.1080p.Korean.Dubbed.WEB":     false,
		"Movie.2023.1080p.HEVC":                  true,
		"Movie.2023.1080p.WEB.H264-Tsunami":      false,
		"Movie.2023.1080p.WEB.H264-GROUP[TS]":    true,
	} {
		if got := ex(name); got != want {
			t.Errorf("%s: excluded %v, want %v", name, got, want)
		}
	}
	if _, err := ExcludeMatcher([]string{"/(/"}); err == nil {
		t.Error("bad expression accepted")
	}
	if ex, _ := ExcludeMatcher(nil); ex != nil {
		t.Error("empty list matches")
	}
	if !isJunk("Movie_2023_HDTS_x264") || isJunk("Movie.2023.DTS-HD.1080p") {
		t.Error("junk names with underscores")
	}
}
