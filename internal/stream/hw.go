package stream

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// HW describes the transcoding pipeline chosen for this machine.
type HW struct {
	Kind        string `json:"kind"` // videotoolbox, nvenc, qsv, vaapi, amf, software
	H264Encoder string `json:"h264Encoder"`
	HEVCEncoder string `json:"hevcEncoder,omitempty"`
	Decode      bool   `json:"hwDecode"`
	Tonemap     string `json:"tonemap"` // zscale, opencl, vaapi, none
	// Dovi is set when ffmpeg has tonemapx, which applies Dolby Vision
	// metadata (jellyfin-ffmpeg). Profile 5 has no HDR10 base layer, so
	// without it the picture comes out green and purple.
	Dovi        bool `json:"dovi"`
	vaapiDevice string
}

// DetectHW probes ffmpeg for a working hardware encoder. preference is the
// configured hwaccel ("auto" tries the platform's likely options in order).
func DetectHW(ffmpeg, preference, vaapiDevice string) HW {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var candidates []string
	switch preference {
	case "", "auto":
		switch runtime.GOOS {
		case "darwin":
			candidates = []string{"videotoolbox"}
		case "windows":
			candidates = []string{"nvenc", "qsv", "amf"}
		default:
			candidates = []string{"nvenc", "qsv", "vaapi"}
		}
	case "none", "software":
	default:
		candidates = []string{preference}
	}

	filters := runOut(ctx, ffmpeg, "-hide_banner", "-filters")
	tonemap := "none"
	if strings.Contains(filters, " zscale ") && strings.Contains(filters, " tonemap ") {
		tonemap = "zscale"
	}
	dovi := strings.Contains(filters, " tonemapx ")

	for _, c := range candidates {
		hw := HW{Kind: c, Decode: true, Tonemap: tonemap, Dovi: dovi, vaapiDevice: vaapiDevice}
		switch c {
		case "videotoolbox":
			hw.H264Encoder, hw.HEVCEncoder = "h264_videotoolbox", "hevc_videotoolbox"
		case "nvenc":
			hw.H264Encoder, hw.HEVCEncoder = "h264_nvenc", "hevc_nvenc"
		case "qsv":
			hw.H264Encoder, hw.HEVCEncoder = "h264_qsv", "hevc_qsv"
		case "vaapi":
			hw.H264Encoder, hw.HEVCEncoder = "h264_vaapi", "hevc_vaapi"
			if strings.Contains(filters, "tonemap_vaapi") {
				hw.Tonemap = "vaapi"
			}
		case "amf":
			hw.H264Encoder, hw.HEVCEncoder = "h264_amf", "hevc_amf"
		default:
			continue
		}
		if testEncoder(ctx, ffmpeg, hw) {
			slog.Info("hardware transcoding available", "kind", hw.Kind, "encoder", hw.H264Encoder)
			return hw
		}
		slog.Debug("hardware encoder unavailable", "kind", c)
	}
	slog.Info("using software transcoding (libx264)")
	return HW{Kind: "software", H264Encoder: "libx264", HEVCEncoder: "libx265", Tonemap: tonemap, Dovi: dovi}
}

// testEncoder runs a tiny encode through the same GPU filter path used for
// real transcodes, so a half-working setup falls back to software.
func testEncoder(ctx context.Context, ffmpeg string, hw HW) bool {
	args := []string{"-hide_banner", "-v", "error"}
	var vf string
	switch hw.Kind {
	case "qsv", "vaapi":
		// The test input is software frames.
		args = append(args, hw.DeviceArgs()...)
		vf = "format=nv12,hwupload=extra_hw_frames=16," + hw.VideoFilter(640, 360, 8, 320, false)
	}
	args = append(args, "-f", "lavfi", "-i", "color=black:s=640x360:r=24", "-frames:v", "3")
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	args = append(args, "-c:v", hw.H264Encoder, "-f", "null", "-")
	return exec.CommandContext(ctx, ffmpeg, args...).Run() == nil
}

func (hw HW) device() string {
	if hw.vaapiDevice != "" {
		return hw.vaapiDevice
	}
	return "/dev/dri/renderD128"
}

func runOut(ctx context.Context, name string, args ...string) string {
	out, _ := exec.CommandContext(ctx, name, args...).Output()
	return string(out)
}

// DeviceArgs set up the GPU for filters and the encoder without decoding on
// it, for software frames uploaded before encoding.
func (hw HW) DeviceArgs() []string {
	var out []string
	dec := hw.DecodeArgs()
	for i := 0; i+1 < len(dec); i += 2 {
		if dec[i] == "-init_hw_device" || dec[i] == "-filter_hw_device" {
			out = append(out, dec[i], dec[i+1])
		}
	}
	return out
}

// DecodeArgs are input options enabling hardware decoding.
func (hw HW) DecodeArgs() []string {
	if !hw.Decode {
		return nil
	}
	switch hw.Kind {
	case "videotoolbox":
		// Frames stay on the GPU for scale_vt (which also tone-maps HDR→SDR).
		return []string{"-hwaccel", "videotoolbox", "-hwaccel_output_format", "videotoolbox_vld"}
	case "nvenc":
		return []string{"-hwaccel", "cuda"}
	case "qsv":
		// Frames stay on the GPU; vpp_qsv scales and tone-maps.
		parent := []string{"-init_hw_device", "vaapi=va:" + hw.device(), "-init_hw_device", "qsv=qs@va"}
		if runtime.GOOS == "windows" {
			parent = []string{"-init_hw_device", "d3d11va=dx", "-init_hw_device", "qsv=qs@dx"}
		}
		return append(parent, "-filter_hw_device", "qs", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
	case "vaapi":
		return []string{"-init_hw_device", "vaapi=va:" + hw.device(), "-filter_hw_device", "va",
			"-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi", "-hwaccel_device", "va"}
	case "amf":
		return []string{"-hwaccel", "d3d11va"}
	}
	return nil
}

// VideoFilter builds scaling and HDR→SDR tone mapping for a source stream.
// Output is always 8-bit SDR BT.709 (what every client decodes).
func (hw HW) VideoFilter(srcW, srcH, bitDepth, maxWidth int, hdr bool) string {
	w, h := srcW, srcH
	if maxWidth > 0 && srcW > maxWidth {
		w = maxWidth
		h = int(float64(srcH)*float64(maxWidth)/float64(srcW)/2+0.5) * 2
	}
	if hw.Kind == "videotoolbox" && hw.Decode {
		f := "scale_vt=w=" + itoa(w) + ":h=" + itoa(h)
		if hdr {
			f += ":color_matrix=bt709:color_primaries=bt709:color_transfer=bt709"
		}
		dl := "nv12"
		if bitDepth > 8 {
			dl = "p010le"
		}
		return f + ",hwdownload,format=" + dl + ",format=nv12"
	}
	switch hw.Kind {
	case "qsv":
		f := "vpp_qsv=w=" + itoa(w) + ":h=" + itoa(h) + ":format=nv12"
		if hdr {
			f += ":tonemap=1"
		}
		return f
	case "vaapi":
		f := "scale_vaapi=w=" + itoa(w) + ":h=" + itoa(h) + ":format=nv12"
		if hdr {
			f = "tonemap_vaapi=format=nv12:t=bt709:m=bt709:p=bt709," + f
		}
		return f
	}
	var f []string
	if hdr && hw.Tonemap == "zscale" {
		f = append(f, "zscale=t=linear:npl=100", "format=gbrpf32le", "zscale=p=bt709", "tonemap=tonemap=hable:desat=0",
			"zscale=t=bt709:m=bt709:r=tv")
	}
	if w != srcW {
		f = append(f, "scale="+itoa(w)+":"+itoa(h))
	}
	f = append(f, "format=yuv420p")
	return strings.Join(f, ",")
}

// DoviFilter scales and tone-maps Dolby Vision to SDR with its metadata
// applied. Frames are decoded in software (DeviceArgs, not DecodeArgs), since
// GPU decoders drop the metadata.
func (hw HW) DoviFilter(srcW, srcH, maxWidth int) string {
	var f []string
	if maxWidth > 0 && srcW > maxWidth {
		h := int(float64(srcH)*float64(maxWidth)/float64(srcW)/2+0.5) * 2
		f = append(f, "scale="+itoa(maxWidth)+":"+itoa(h))
	}
	f = append(f, "tonemapx=tonemap=bt2390:t=bt709:m=bt709:p=bt709:r=tv:format=yuv420p")
	switch hw.Kind {
	case "qsv", "vaapi":
		f = append(f, "format=nv12", "hwupload=extra_hw_frames=16")
	}
	return strings.Join(f, ",")
}

// EncodeArgs returns encoder options for a target bitrate (kbps).
func (hw HW) EncodeArgs(kbps int) []string {
	b := itoa(kbps) + "k"
	buf := itoa(kbps*2) + "k"
	args := []string{"-c:v", hw.H264Encoder, "-b:v", b, "-maxrate", b, "-bufsize", buf,
		"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709"}
	switch hw.Kind {
	case "software":
		args = append(args, "-preset", "veryfast", "-profile:v", "high")
	case "videotoolbox":
		args = append(args, "-profile:v", "high", "-allow_sw", "1")
	case "nvenc":
		args = append(args, "-preset", "p4", "-profile:v", "high")
	case "qsv":
		// forced_idr makes -force_key_frames produce IDRs (clean HLS segment starts).
		args = append(args, "-preset", "faster", "-profile:v", "high", "-forced_idr", "1")
	case "vaapi":
		args = append(args, "-profile:v", "high")
	case "amf":
		args = append(args, "-quality", "speed")
	}
	return args
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
