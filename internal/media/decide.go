package media

import (
	"fmt"
	"slices"
	"strings"
)

// ClientCaps is what a client reports it can play.
type ClientCaps struct {
	// Engines the client has, in preference order: "avplayer", "mpv".
	Engines []string `json:"engines"`
	// Native (AVPlayer-class) engine capabilities.
	NativeVideo      []string `json:"nativeVideo"`      // h264, hevc, av1
	NativeAudio      []string `json:"nativeAudio"`      // aac, ac3, eac3, alac, flac, mp3, opus
	NativeContainers []string `json:"nativeContainers"` // mp4, mov (direct); HLS remux covers the rest
	DolbyVision      bool     `json:"dolbyVision"`
	HDR              bool     `json:"hdr"`
	// MaxKbps limits bitrate (0 = unlimited, e.g. on LAN).
	MaxKbps int `json:"maxKbps"`
	// ForceEngine pins an engine ("" = let the server decide).
	ForceEngine string `json:"forceEngine"`
	// ForceTranscode is for testing / poor connections.
	ForceTranscode bool `json:"forceTranscode"`
}

type Method string

const (
	MethodDirect    Method = "direct"    // raw file bytes
	MethodRemux     Method = "remux"     // HLS fMP4, stream copy
	MethodTranscode Method = "transcode" // HLS fMP4, re-encoded video and/or audio
)

type Decision struct {
	Engine string `json:"engine"`
	Method Method `json:"method"`
	// AudioCopy is false when the selected audio track is re-encoded during a remux.
	AudioCopy  bool     `json:"audioCopy"`
	AudioCodec string   `json:"audioCodec"` // output audio codec
	VideoCopy  bool     `json:"videoCopy"`
	AudioIndex int      `json:"audioIndex"` // typeIndex of the chosen audio stream (-1 none)
	Reasons    []string `json:"reasons"`
}

// Codecs AVPlayer can carry inside fMP4 HLS.
var hlsAudio = []string{"aac", "ac3", "eac3", "alac", "flac", "mp3", "opus"}

// Decide picks the engine and delivery method. The goal is direct play at
// full quality: native engine for anything it can decode (true Dolby Vision
// and Atmos via AVPlayer), libmpv for everything else, and transcoding only
// when no engine can play the file.
func Decide(info *Info, caps ClientCaps, audioIdx int) Decision {
	d := Decision{AudioIndex: -1}
	var audio *Stream
	if len(info.Audio) > 0 {
		audio = chooseAudio(info.Audio, audioIdx)
		d.AudioIndex = audio.TypeIdx
	}
	video := info.PrimaryVideo()
	has := func(e string) bool { return slices.Contains(caps.Engines, e) }

	nativeVideoOK, videoWhy := nativeVideo(video, caps)
	nativeAudioOK := audio == nil || slices.Contains(caps.NativeAudio, audio.Codec)
	overBudget := caps.MaxKbps > 0 && info.Bitrate > int64(caps.MaxKbps)*1000

	if caps.ForceTranscode || overBudget {
		d.Engine = "avplayer"
		if !has("avplayer") && has("mpv") {
			d.Engine = "mpv"
		}
		d.Method = MethodTranscode
		d.AudioCodec = "aac"
		d.Reasons = append(d.Reasons, "bitrate limit or forced transcode")
		return d
	}

	tryNative := has("avplayer") && caps.ForceEngine != "mpv"
	tryMpv := has("mpv") && caps.ForceEngine != "avplayer"

	if tryNative && nativeVideoOK {
		d.Engine = "avplayer"
		d.VideoCopy = true
		if nativeAudioOK {
			d.AudioCopy = true
			if audio != nil {
				d.AudioCodec = audio.Codec
			}
			if slices.Contains(caps.NativeContainers, info.Container) && (audio == nil || audio.TypeIdx == defaultAudio(info.Audio).TypeIdx) {
				d.Method = MethodDirect
				d.Reasons = append(d.Reasons, "native container and codecs")
			} else {
				d.Method = MethodRemux
				d.Reasons = append(d.Reasons, fmt.Sprintf("remux %s to fMP4 (no re-encode)", info.Container))
			}
			return d
		}
		// Video is native but audio isn't (TrueHD, DTS). If the video carries
		// HDR/Dolby Vision, keep the native engine for true HDR output and
		// convert only the audio losslessly; otherwise mpv can play it all directly.
		if video != nil && video.HDR != "" || !tryMpv {
			d.Method = MethodRemux
			d.AudioCopy = false
			d.AudioCodec = "alac"
			d.Reasons = append(d.Reasons, fmt.Sprintf("keep %s video native; %s audio converted to lossless ALAC", hdrLabel(video), audio.Codec))
			return d
		}
	}

	if tryMpv {
		d.Engine = "mpv"
		d.Method = MethodDirect
		d.VideoCopy, d.AudioCopy = true, true
		if audio != nil {
			d.AudioCodec = audio.Codec
		}
		if !nativeVideoOK {
			d.Reasons = append(d.Reasons, "native engine can't play video: "+videoWhy)
		} else {
			d.Reasons = append(d.Reasons, "native engine can't play audio: "+audio.Codec)
		}
		return d
	}

	d.Engine = "avplayer"
	d.Method = MethodTranscode
	d.AudioCodec = "aac"
	if nativeAudioOK && audio != nil {
		d.AudioCopy = true
		d.AudioCodec = audio.Codec
	}
	d.Reasons = append(d.Reasons, "no engine can play the original video: "+videoWhy)
	return d
}

func nativeVideo(v *Stream, caps ClientCaps) (bool, string) {
	if v == nil {
		return true, ""
	}
	if !slices.Contains(caps.NativeVideo, v.Codec) {
		return false, "codec " + v.Codec
	}
	if v.DVProfile > 0 {
		switch {
		case v.DVProfile == 7:
			// Dual-layer (FEL/MEL) isn't supported by Apple decoders.
			return false, "Dolby Vision profile 7"
		case v.DVProfile == 5 && !caps.DolbyVision:
			// Profile 5 has no HDR10 fallback; without DV support colors are wrong.
			return false, "Dolby Vision profile 5 without DV display support"
		}
	}
	if v.Codec == "hevc" && v.BitDepth > 10 {
		return false, "HEVC >10-bit"
	}
	if strings.Contains(v.PixFmt, "444") || strings.Contains(v.PixFmt, "422") {
		return false, "chroma " + v.PixFmt
	}
	return true, ""
}

func hdrLabel(v *Stream) string {
	if v == nil || v.HDR == "" {
		return "SDR"
	}
	return v.HDR
}

func chooseAudio(streams []Stream, idx int) *Stream {
	for i := range streams {
		if streams[i].TypeIdx == idx {
			return &streams[i]
		}
	}
	return defaultAudio(streams)
}

func defaultAudio(streams []Stream) *Stream {
	for i := range streams {
		if streams[i].Default {
			return &streams[i]
		}
	}
	return &streams[0]
}

// ---------- HLS codec strings ----------

// VideoCodecString returns the RFC 6381 CODECS value and, for Dolby Vision
// profile 8, the SUPPLEMENTAL-CODECS value.
func VideoCodecString(v *Stream) (codecs, supplemental, videoRange string) {
	videoRange = "SDR"
	switch v.HDR {
	case "HDR10", "DV+HDR10", "DV":
		videoRange = "PQ"
	case "HLG":
		videoRange = "HLG"
	}
	switch v.Codec {
	case "h264":
		p := map[string]string{"Baseline": "42", "Constrained Baseline": "42", "Main": "4d", "High": "64", "High 10": "6e"}[v.Profile]
		if p == "" {
			p = "64"
		}
		lvl := v.Level
		if lvl <= 0 {
			lvl = 41
		}
		codecs = fmt.Sprintf("avc1.%s00%02x", p, lvl)
	case "hevc":
		prof := 1
		if strings.Contains(v.Profile, "10") || v.BitDepth == 10 {
			prof = 2
		}
		lvl := v.Level
		if lvl <= 0 {
			lvl = 153
		}
		compat := 6
		if prof == 2 {
			compat = 4
		}
		codecs = fmt.Sprintf("hvc1.%d.%d.L%d.B0", prof, compat, lvl)
	case "av1":
		depth := v.BitDepth
		if depth == 0 {
			depth = 8
		}
		codecs = fmt.Sprintf("av01.0.%02dM.%02d", 13, depth)
	}
	if v.DVProfile > 0 {
		lvl := v.DVLevel
		if lvl <= 0 {
			lvl = 6
		}
		switch v.DVProfile {
		case 5:
			codecs = fmt.Sprintf("dvh1.05.%02d", lvl)
			videoRange = "PQ"
		case 8:
			brand := map[int]string{1: "db1p", 2: "db2g", 4: "db4h"}[v.DVCompat]
			if brand == "" {
				brand = "db1p"
			}
			supplemental = fmt.Sprintf("dvh1.08.%02d/%s", lvl, brand)
		case 10:
			supplemental = fmt.Sprintf("dav1.10.%02d", lvl)
		}
	}
	return
}

func AudioCodecString(codec string) string {
	switch codec {
	case "aac":
		return "mp4a.40.2"
	case "ac3":
		return "ac-3"
	case "eac3":
		return "ec-3"
	case "flac":
		return "fLaC"
	case "alac":
		return "alac"
	case "mp3":
		return "mp4a.40.34"
	case "opus":
		return "Opus"
	}
	return ""
}

// HLSAudioOK reports whether a codec can be carried in fMP4 HLS.
func HLSAudioOK(codec string) bool { return slices.Contains(hlsAudio, codec) }
