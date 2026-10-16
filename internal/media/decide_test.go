package media

import "testing"

func TestDecideDolbyVisionProfile7(t *testing.T) {
	info := &Info{
		Container: "mkv",
		Video:     []Stream{{Codec: "hevc", BitDepth: 10, PixFmt: "yuv420p10le", HDR: "DV+HDR10", DVProfile: 7, DVCompat: 6}},
		Audio:     []Stream{{Codec: "eac3", Default: true}},
	}
	iphone := ClientCaps{Engines: []string{"avplayer"}, NativeVideo: []string{"h264", "hevc"},
		NativeAudio: []string{"aac", "ac3", "eac3"}, NativeContainers: []string{"mp4", "mov"}, DolbyVision: true}

	d := Decide(info, iphone, -1)
	if d.Method != MethodRemux || !d.VideoCopy || !d.StripDolbyVision {
		t.Fatalf("AVPlayer only: want a remux of the HDR10 base layer, got %+v", d)
	}

	// Profile 7 in an mp4 still needs the remux to strip the enhancement layer.
	mp4 := *info
	mp4.Container = "mp4"
	if d := Decide(&mp4, iphone, -1); d.Method != MethodRemux || !d.StripDolbyVision {
		t.Fatalf("mp4: want remux, got %+v", d)
	}

	// mpv plays it directly, enhancement layer and all.
	mac := iphone
	mac.Engines = []string{"avplayer", "mpv"}
	if d := Decide(info, mac, -1); d.Engine != "mpv" || d.Method != MethodDirect || d.StripDolbyVision {
		t.Fatalf("with mpv: want direct play in mpv, got %+v", d)
	}

	// Profile 8 is untouched.
	p8 := *info
	p8.Video = []Stream{{Codec: "hevc", BitDepth: 10, PixFmt: "yuv420p10le", HDR: "DV+HDR10", DVProfile: 8, DVCompat: 1}}
	if d := Decide(&p8, iphone, -1); d.StripDolbyVision || d.Method != MethodRemux {
		t.Fatalf("profile 8: want a plain remux, got %+v", d)
	}
}
