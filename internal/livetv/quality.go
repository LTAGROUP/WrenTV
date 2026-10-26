package livetv

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/media"
)

// Probe failures are distinct so the API can answer honestly: a busy checker,
// a provider limit and an unreadable stream are different problems.
var (
	ErrNoChannel         = errors.New("channel not found")
	ErrProbeBusy         = errors.New("quality checker busy")
	ErrProbeLimit        = errors.New("provider connection limit reached")
	ErrProbeUnavailable  = errors.New("quality check unavailable")
	qualityProbeSlots    = 2
	qualityProbeDeadline = 30 * time.Second
)

// QualityAudio is one audio stream as measured by ffprobe.
type QualityAudio struct {
	Codec      string `json:"codec,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	Layout     string `json:"layout,omitempty"`
	Language   string `json:"language,omitempty"`
	BitrateBps int64  `json:"bitrateBps,omitempty"`
	SampleRate int64  `json:"sampleRate,omitempty"`
}

// Quality is a measured stream description. It is never inferred from a
// channel label, and an empty result does not claim playback will fail.
type Quality struct {
	Width     int            `json:"width,omitempty"`
	Height    int            `json:"height,omitempty"`
	Codec     string         `json:"codec,omitempty"`
	FrameRate float64        `json:"frameRate,omitempty"`
	Bitrate   int64          `json:"bitrateBps,omitempty"`
	Audio     []QualityAudio `json:"audio"`
	Adaptive  bool           `json:"adaptive"`
	CheckedAt time.Time      `json:"checkedAt"`
}

// ProbeChannel measures one channel without creating a playback session.
// One connection is opened at a time per checker slot, the provider's stream
// limit is respected, and a cancelled request kills ffprobe immediately.
func (s *Service) ProbeChannel(ctx context.Context, channelID string) (*Quality, error) {
	c := s.Channel(channelID)
	if c == nil {
		return nil, ErrNoChannel
	}
	streamURL, userAgent := c.URL, c.UserAgent
	var headers map[string]string
	if c.stremio {
		link, agent, addonHeaders, err := s.resolveStremio(ctx, c)
		if err != nil {
			return nil, err
		}
		streamURL, userAgent, headers = link, agent, addonHeaders
	}
	return s.ProbeStreamWithHeaders(ctx, streamURL, userAgent, headers)
}

// ProbeStream measures a server-resolved stream under the same slot, provider
// limit and timeout guards as a playlist channel. Ephemeral sports addon
// sources use it so a provider URL and its credentials stay server-side.
func (s *Service) ProbeStream(ctx context.Context, streamURL, userAgent string) (*Quality, error) {
	return s.ProbeStreamWithHeaders(ctx, streamURL, userAgent, nil)
}

// ProbeStreamWithHeaders also sends the stream's allowlisted browser identity,
// so a provider that refuses an anonymous probe is measured like playback is.
func (s *Service) ProbeStreamWithHeaders(ctx context.Context, streamURL, userAgent string, headers map[string]string) (*Quality, error) {
	headers = SanitizeRequestHeaders(headers)
	if s.FFprobe == "" {
		return nil, ErrProbeUnavailable
	}
	if !strings.HasPrefix(streamURL, "http://") && !strings.HasPrefix(streamURL, "https://") {
		return nil, ErrProbeUnavailable
	}
	select {
	case s.probeSem <- struct{}{}:
		defer func() { <-s.probeSem }()
	default:
		return nil, ErrProbeBusy
	}
	limit := s.cfg.LiveTV.MaxStreams
	if limit > 0 && s.Playing() >= limit {
		return nil, ErrProbeLimit
	}
	ctx, cancel := context.WithTimeout(ctx, qualityProbeDeadline)
	defer cancel()
	info, err := media.ProbeURLWithHeaders(ctx, s.FFprobe, streamURL, userAgent, headers)
	if err != nil {
		return nil, err
	}
	return qualityFromInfo(info), nil
}

// qualityFromInfo keeps the highest-resolution video stream and every audio
// stream. A master playlist's aggregate bandwidth is never reported as the
// video bitrate.
func qualityFromInfo(info *media.Info) *Quality {
	q := &Quality{Audio: []QualityAudio{}, CheckedAt: time.Now().UTC()}
	q.Adaptive = strings.Contains(info.Container, "hls") || strings.Contains(info.Container, "applehttp") ||
		strings.Contains(info.Container, "dash")
	for _, stream := range info.Video {
		if stream.Height > q.Height || (stream.Height == q.Height && stream.Width > q.Width) {
			q.Width, q.Height, q.Codec = stream.Width, stream.Height, stream.Codec
			q.FrameRate, q.Bitrate = stream.FrameRate, stream.Bitrate
		}
	}
	for _, stream := range info.Audio {
		q.Audio = append(q.Audio, QualityAudio{
			Codec: stream.Codec, Channels: stream.Channels, Layout: stream.ChannelLayout,
			Language: stream.Language, BitrateBps: stream.Bitrate, SampleRate: int64(stream.SampleRate),
		})
	}
	return q
}
