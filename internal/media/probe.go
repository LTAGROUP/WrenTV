// Package media inspects files (ffprobe), extracts keyframe indexes and
// decides how a client should play them.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Info struct {
	Container string    `json:"container"`
	Duration  float64   `json:"duration"`
	Bitrate   int64     `json:"bitrate"`
	Size      int64     `json:"size"`
	Video     []Stream  `json:"video"`
	Audio     []Stream  `json:"audio"`
	Subtitles []Stream  `json:"subtitles"`
	Chapters  []Chapter `json:"chapters,omitempty"`
}

type Stream struct {
	Index    int    `json:"index"`     // absolute ffprobe stream index
	TypeIdx  int    `json:"typeIndex"` // index among streams of the same type (0:a:N)
	Codec    string `json:"codec"`
	Profile  string `json:"profile,omitempty"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced"`
	Bitrate  int64  `json:"bitrate,omitempty"`

	// Video
	Width          int     `json:"width,omitempty"`
	Height         int     `json:"height,omitempty"`
	FrameRate      float64 `json:"frameRate,omitempty"`
	PixFmt         string  `json:"pixFmt,omitempty"`
	BitDepth       int     `json:"bitDepth,omitempty"`
	ColorTransfer  string  `json:"colorTransfer,omitempty"`
	ColorPrimaries string  `json:"colorPrimaries,omitempty"`
	HDR            string  `json:"hdr,omitempty"` // "", "HDR10", "HLG", "DV", "DV+HDR10"
	DVProfile      int     `json:"dvProfile,omitempty"`
	DVLevel        int     `json:"dvLevel,omitempty"`
	DVCompat       int     `json:"dvCompatId,omitempty"` // bl_signal_compatibility_id
	DVHasEL        bool    `json:"dvEnhancementLayer,omitempty"`
	CodecTag       string  `json:"codecTag,omitempty"`
	Level          int     `json:"level,omitempty"`

	// Audio
	Channels      int    `json:"channels,omitempty"`
	ChannelLayout string `json:"channelLayout,omitempty"`
	SampleRate    int    `json:"sampleRate,omitempty"`
	Atmos         bool   `json:"atmos,omitempty"`

	// Subtitles
	TextBased bool `json:"textBased,omitempty"`
}

type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

func (i *Info) PrimaryVideo() *Stream {
	if len(i.Video) == 0 {
		return nil
	}
	return &i.Video[0]
}

type ffprobeOut struct {
	Format struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		Size       string            `json:"size"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		Index          int               `json:"index"`
		CodecType      string            `json:"codec_type"`
		CodecName      string            `json:"codec_name"`
		CodecTagString string            `json:"codec_tag_string"`
		Profile        string            `json:"profile"`
		Level          int               `json:"level"`
		Width          int               `json:"width"`
		Height         int               `json:"height"`
		PixFmt         string            `json:"pix_fmt"`
		BitsPerRaw     string            `json:"bits_per_raw_sample"`
		ColorTransfer  string            `json:"color_transfer"`
		ColorPrimaries string            `json:"color_primaries"`
		AvgFrameRate   string            `json:"avg_frame_rate"`
		RFrameRate     string            `json:"r_frame_rate"`
		Channels       int               `json:"channels"`
		ChannelLayout  string            `json:"channel_layout"`
		SampleRate     string            `json:"sample_rate"`
		BitRate        string            `json:"bit_rate"`
		Tags           map[string]string `json:"tags"`
		Disposition    map[string]int    `json:"disposition"`
		SideDataList   []map[string]any  `json:"side_data_list"`
	} `json:"streams"`
	Chapters []struct {
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

// Probe runs ffprobe on a path or URL.
func Probe(ctx context.Context, ffprobe, input string) (*Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-hide_banner",
		"-probesize", "20M", "-analyzeduration", "20M",
		"-show_format", "-show_streams", "-show_chapters", "-of", "json", input)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbe(out)
}

// ProbeURL runs ffprobe against a live URL with the provider's User-Agent and
// a bounded read timeout, so a stalled stream cannot hold the process. The
// caller's context still controls the overall deadline.
func ProbeURL(ctx context.Context, ffprobe, input, userAgent string) (*Info, error) {
	return ProbeURLWithHeaders(ctx, ffprobe, input, userAgent, nil)
}

// ProbeURLWithHeaders also sends a provider's allowlisted request headers, so
// a stream that refuses an anonymous probe is measured the way it is played.
// The caller is responsible for the allowlist and control-character checks
// (livetv.SanitizeRequestHeaders); a header name or value containing a newline
// would otherwise inject another ffprobe argument.
func ProbeURLWithHeaders(ctx context.Context, ffprobe, input, userAgent string, headers map[string]string) (*Info, error) {
	args := probeURLArgs(input, userAgent, headers)
	cmd := exec.CommandContext(ctx, ffprobe, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbe(out)
}

// probeURLArgs builds the ffprobe command. The agent goes through -user_agent
// (the option ffprobe applies itself); the rest of the identity goes through
// -headers, CRLF separated and sorted so the command is deterministic.
func probeURLArgs(input, userAgent string, headers map[string]string) []string {
	args := []string{"-v", "error", "-hide_banner",
		"-probesize", "5000000", "-analyzeduration", "5000000", "-rw_timeout", "10000000",
		"-show_format", "-show_streams", "-of", "json"}
	if agent := headers["User-Agent"]; agent != "" {
		userAgent = agent
	}
	if userAgent != "" {
		args = append(args, "-user_agent", userAgent)
	}
	if len(headers) > 0 {
		names := make([]string, 0, len(headers))
		for name := range headers {
			if name == "User-Agent" {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		var b strings.Builder
		for _, name := range names {
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(headers[name])
			b.WriteString("\r\n")
		}
		if b.Len() > 0 {
			args = append(args, "-headers", b.String())
		}
	}
	return append(args, "-i", input)
}

func parseProbe(out []byte) (*Info, error) {
	var p ffprobeOut
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, err
	}
	info := &Info{
		Container: normalizeContainer(p.Format.FormatName),
		Duration:  atof(p.Format.Duration),
		Bitrate:   atoi64(p.Format.BitRate),
		Size:      atoi64(p.Format.Size),
	}
	var nv, na, ns int
	for _, s := range p.Streams {
		st := Stream{
			Index:    s.Index,
			Codec:    s.CodecName,
			Profile:  s.Profile,
			Language: s.Tags["language"],
			Title:    s.Tags["title"],
			Default:  s.Disposition["default"] == 1,
			Forced:   s.Disposition["forced"] == 1,
			Bitrate:  atoi64(s.BitRate),
			CodecTag: s.CodecTagString,
		}
		if st.Bitrate == 0 {
			st.Bitrate = atoi64(s.Tags["BPS"])
		}
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				continue
			}
			st.TypeIdx = nv
			nv++
			st.Width, st.Height = s.Width, s.Height
			st.Level = s.Level
			st.PixFmt = s.PixFmt
			st.BitDepth = int(atoi64(s.BitsPerRaw))
			if st.BitDepth == 0 && strings.Contains(s.PixFmt, "10") {
				st.BitDepth = 10
			}
			st.ColorTransfer, st.ColorPrimaries = s.ColorTransfer, s.ColorPrimaries
			st.FrameRate = frac(s.AvgFrameRate)
			if st.FrameRate == 0 {
				st.FrameRate = frac(s.RFrameRate)
			}
			switch s.ColorTransfer {
			case "smpte2084":
				st.HDR = "HDR10"
			case "arib-std-b67":
				st.HDR = "HLG"
			}
			for _, sd := range s.SideDataList {
				if sd["side_data_type"] == "DOVI configuration record" {
					st.DVProfile = int(num(sd["dv_profile"]))
					st.DVLevel = int(num(sd["dv_level"]))
					st.DVCompat = int(num(sd["dv_bl_signal_compatibility_id"]))
					st.DVHasEL = num(sd["el_present_flag"]) == 1
					if st.HDR == "HDR10" || st.DVCompat == 1 || st.DVCompat == 6 {
						st.HDR = "DV+HDR10"
					} else {
						st.HDR = "DV"
					}
				}
			}
			info.Video = append(info.Video, st)
		case "audio":
			st.TypeIdx = na
			na++
			st.Channels, st.ChannelLayout = s.Channels, s.ChannelLayout
			st.SampleRate = int(atoi64(s.SampleRate))
			p := strings.ToLower(s.Profile)
			st.Atmos = strings.Contains(p, "atmos") || strings.Contains(strings.ToLower(st.Title), "atmos")
			info.Audio = append(info.Audio, st)
		case "subtitle":
			st.TypeIdx = ns
			ns++
			switch s.CodecName {
			case "subrip", "ass", "ssa", "webvtt", "mov_text", "text":
				st.TextBased = true
			}
			info.Subtitles = append(info.Subtitles, st)
		}
	}
	for _, c := range p.Chapters {
		info.Chapters = append(info.Chapters, Chapter{Start: atof(c.StartTime), End: atof(c.EndTime), Title: c.Tags["title"]})
	}
	return info, nil
}

func normalizeContainer(f string) string {
	switch {
	case strings.Contains(f, "matroska"):
		return "mkv"
	case strings.Contains(f, "mp4") || strings.Contains(f, "mov"):
		return "mp4"
	case f == "mpegts":
		return "ts"
	case f == "avi":
		return "avi"
	}
	if i := strings.IndexByte(f, ','); i > 0 {
		return f[:i]
	}
	return f
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func frac(s string) float64 {
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return atof(s)
	}
	d := atof(b)
	if d == 0 {
		return 0
	}
	return atof(a) / d
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		return atof(x)
	}
	return 0
}
