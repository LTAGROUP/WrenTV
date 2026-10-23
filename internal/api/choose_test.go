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

func TestPreferredSubtitle(t *testing.T) {
	// The Minecraft Movie layout: a forced French default, full French and
	// English text tracks, then the same as pictures.
	info := &media.Info{Subtitles: []media.Stream{
		{TypeIdx: 0, Language: "fre", Forced: true, Default: true, TextBased: true},
		{TypeIdx: 1, Language: "fre", TextBased: true},
		{TypeIdx: 2, Language: "eng", TextBased: true},
		{TypeIdx: 3, Language: "eng", Title: "SDH"},
		{TypeIdx: 4, Language: "spa"},
	}}
	for lang, want := range map[string]int{"": -1, "en": 2, "fr": 1, "es": 4, "de": -1} {
		if got := preferredSubtitle(info, lang); got != want {
			t.Errorf("%q: got %d, want %d", lang, got, want)
		}
	}
}

func TestChooseStreamRank(t *testing.T) {
	web := withHEVC(media.ClientCaps{Engines: []string{"avplayer"}, NativeVideo: []string{"h264"}, NativeAudio: []string{"aac"}, NativeContainers: []string{"mp4"}})
	stream := func(id string, rank int, size int64) *db.MediaFile {
		return &db.MediaFile{ID: id, StreamID: id, Width: 3840, Height: 2160, StreamRank: rank, Size: size}
	}
	best, second := stream("best", 0, 10<<30), stream("second", 1, 50<<30)
	// Playing the second choice once probes it; the first stays the default.
	second.Probe, second.ProbedAt = probed("x", 3840, 2160, "hevc", 60000).Probe, 1
	if got := chooseFile([]*db.MediaFile{second, best}, playRequest{Caps: web}).ID; got != "best" {
		t.Fatalf("got %s, want the first-ranked stream", got)
	}
	if got := chooseFile([]*db.MediaFile{second, best}, playRequest{Caps: web, FileID: "second"}).ID; got != "second" {
		t.Fatalf("explicit choice: got %s", got)
	}
}

func TestStartingSubtitle(t *testing.T) {
	subs := []media.Stream{
		{TypeIdx: 0, Language: "eng", Forced: true},
		{TypeIdx: 1, Language: "eng"},
		{TypeIdx: 2, Language: "fra"},
		{TypeIdx: 3, Language: "fra", Title: "Forced"},
	}
	english := &media.Info{Audio: []media.Stream{{TypeIdx: 0, Language: "eng"}}, Subtitles: subs}
	japanese := &media.Info{Audio: []media.Stream{{TypeIdx: 0, Language: "jpn"}}, Subtitles: subs}
	untagged := &media.Info{Audio: []media.Stream{{TypeIdx: 0}}, Subtitles: subs}
	on := db.Prefs{AudioLanguage: "en", ForcedSubtitles: true, ForeignAudioSubtitles: true}
	off := db.Prefs{AudioLanguage: "en"}
	fr := on
	fr.SubtitleLanguage = "fr"
	cases := []struct {
		name string
		info *media.Info
		p    db.Prefs
		want int
	}{
		{"chosen language wins", english, fr, 2},
		{"forced for foreign lines", english, on, 0},
		{"nothing when both off", english, off, -1},
		{"full track for foreign audio", japanese, on, 1},
		{"untagged audio isn't foreign", untagged, on, 0},
		{"forced in the subtitle language first", english, db.Prefs{AudioLanguage: "en", SubtitleLanguage: "de", ForcedSubtitles: true}, 0},
	}
	for _, c := range cases {
		if got := startingSubtitle(c.info, c.p, 0); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	frForced := db.Prefs{AudioLanguage: "en", SubtitleLanguage: "fr", ForcedSubtitles: true}
	frOnly := &media.Info{Audio: english.Audio, Subtitles: []media.Stream{subs[0], subs[3]}}
	if got := startingSubtitle(frOnly, frForced, 0); got != 3 {
		t.Errorf("forced named in the title: got %d, want 3", got)
	}
}
