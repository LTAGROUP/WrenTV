package stream

import "testing"

func TestHDRFilter(t *testing.T) {
	tm := "tonemapx=tonemap=bt2390:t=bt709:m=bt709:p=bt709:r=tv:format="
	cases := []struct {
		name string
		hw   HW
		max  int
		want string
	}{
		{"qsv", HW{Kind: "qsv", Decode: true}, 1920,
			"vpp_qsv=w=1920:h=1080:format=p010,hwdownload,format=p010le," + tm + "nv12,hwupload=extra_hw_frames=16"},
		{"vaapi", HW{Kind: "vaapi", Decode: true}, 0,
			"scale_vaapi=w=3840:h=2160:format=p010,hwdownload,format=p010le," + tm + "nv12,hwupload=extra_hw_frames=16"},
		{"videotoolbox", HW{Kind: "videotoolbox", Decode: true}, 1280,
			"scale_vt=w=1280:h=720,hwdownload,format=p010le," + tm + "nv12"},
		{"qsv encode only", HW{Kind: "qsv"}, 1920,
			"scale=1920:1080," + tm + "yuv420p,format=nv12,hwupload=extra_hw_frames=16"},
		{"software", HW{Kind: "software"}, 0, tm + "yuv420p"},
		{"nvenc", HW{Kind: "nvenc", Decode: true}, 1920, "scale=1920:1080," + tm + "yuv420p"},
	}
	for _, c := range cases {
		if got := c.hw.HDRFilter(3840, 2160, c.max); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
}
