package library

import (
	"testing"

	"github.com/LTAGROUP/wrentv/internal/db"
)

func TestGuessFromName(t *testing.T) {
	cases := []struct {
		path, container, codec, hdr string
		height                      int
	}{
		{"/tv/Friends (1994)/Season 1/Friends (1994) - S01E21 [1080p].mkv", "mkv", "", "", 1080},
		{"/m/Top Gun - Maverick (2022) {imdb-tt1745960}/Top Gun - Maverick (2022) {imdb-tt1745960} [2160p].mkv", "mkv", "", "", 2160},
		{"/m/Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC.TrueHD.Atmos.7.1-GRP.mkv", "mkv", "hevc", "DV+HDR10", 2160},
		{"/m/Oppenheimer.2023.1080p.WEB-DL.DDP5.1.Atmos.H.264.m4v", "mp4", "h264", "", 1080},
		{"/m/Show.S01.720p.x265-GRP/Show.S01E01.mkv", "mkv", "hevc", "", 720},
		{"/m/Movie.2020.HDR10+.4K.mp4", "mp4", "", "HDR10", 2160},
		{"/m/Heat (1995)/Heat.avi", "avi", "", "", 0},
	}
	for _, c := range cases {
		f := &db.MediaFile{Path: c.path}
		GuessFromName(f)
		if f.Container != c.container || f.VideoCodec != c.codec || f.HDR != c.hdr || f.Height != c.height {
			t.Errorf("%s: got %s/%s/%s/%d", c.path, f.Container, f.VideoCodec, f.HDR, f.Height)
		}
	}
	probed := &db.MediaFile{Path: "/m/X [2160p].mkv", Width: 1920, Height: 800, Container: "mkv"}
	if GuessFromName(probed) || probed.Height != 800 {
		t.Errorf("guess overwrote probed info: %+v", probed)
	}
}
