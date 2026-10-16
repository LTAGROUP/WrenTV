package api

import (
	"encoding/json"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/media"
)

func probed(id string, w, h int, codec string, kbps int64) *db.MediaFile {
	info := media.Info{Container: "mp4", Duration: 7200, Bitrate: kbps * 1000,
		Video: []media.Stream{{Codec: codec, Width: w, Height: h, PixFmt: "yuv420p", BitDepth: 8}},
		Audio: []media.Stream{{Codec: "aac", Default: true, Channels: 2}}}
	b, _ := json.Marshal(info)
	return &db.MediaFile{ID: id, Width: w, Height: h, VideoCodec: codec, Size: kbps * 900, Probe: string(b)}
}

func TestChooseFile(t *testing.T) {
	uhd := probed("4k", 3840, 2160, "hevc", 60000)
	fhd := probed("1080", 1920, 1080, "h264", 10000)
	files := []*db.MediaFile{uhd, fhd}
	web := media.ClientCaps{Engines: []string{"avplayer"}, NativeVideo: []string{"h264"}, NativeAudio: []string{"aac"}, NativeContainers: []string{"mp4"}}

	cases := []struct {
		name string
		req  playRequest
		want string
	}{
		{"explicit version wins", playRequest{FileID: "4k", Caps: web}, "4k"},
		{"client can't decode HEVC: serve 1080p instead of transcoding", playRequest{Caps: web}, "1080"},
		{"1080p limit: serve 1080p instead of transcoding 4K", playRequest{Caps: withHEVC(web), MaxWidth: 1920}, "1080"},
		{"no limit, everything plays: best quality", playRequest{Caps: withHEVC(web)}, "4k"},
	}
	for _, c := range cases {
		if got := chooseFile(files, c.req).ID; got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func withHEVC(c media.ClientCaps) media.ClientCaps {
	c.NativeVideo = append([]string{"hevc"}, c.NativeVideo...)
	return c
}

func TestPreferredAudio(t *testing.T) {
	info := &media.Info{Audio: []media.Stream{
		{TypeIdx: 0, Language: "jpn", Default: true},
		{TypeIdx: 1, Language: "eng", Title: "Commentary"},
		{TypeIdx: 2, Language: "eng"},
	}}
	if got := preferredAudio(info, "en"); got != 2 {
		t.Errorf("en: got %d, want 2", got)
	}
	if got := preferredAudio(info, "ja"); got != 0 {
		t.Errorf("ja: got %d, want 0", got)
	}
	if got := preferredAudio(info, "fr"); got != -1 {
		t.Errorf("fr: got %d, want -1", got)
	}
}
