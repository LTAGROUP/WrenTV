package stream

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/LTAGROUP/wrentv/internal/media"
)

// ---------- playlists ----------

func (s *Session) serveMaster(w http.ResponseWriter) {
	v := s.Info.PrimaryVideo()
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	bw := s.Info.Bitrate
	if s.tx != nil {
		bw = int64(s.KbpsTarget+400) * 1000
	}
	if bw <= 0 {
		bw = 20_000_000
	}
	attrs := []string{fmt.Sprintf("BANDWIDTH=%d", int64(float64(bw)*1.2)), fmt.Sprintf("AVERAGE-BANDWIDTH=%d", bw)}
	var codecs []string
	if v != nil {
		if s.tx != nil {
			codecs = append(codecs, "avc1.640028")
			attrs = append(attrs, "VIDEO-RANGE=SDR")
		} else {
			c, supp, rng := media.VideoCodecString(v)
			if c != "" {
				codecs = append(codecs, c)
			}
			if supp != "" {
				attrs = append(attrs, fmt.Sprintf("SUPPLEMENTAL-CODECS=%q", supp))
			}
			attrs = append(attrs, "VIDEO-RANGE="+rng)
		}
		w0, h0 := v.Width, v.Height
		if s.tx != nil && s.MaxWidth > 0 && w0 > s.MaxWidth {
			h0 = h0 * s.MaxWidth / w0
			w0 = s.MaxWidth
		}
		attrs = append(attrs, fmt.Sprintf("RESOLUTION=%dx%d", w0, h0))
		if v.FrameRate > 0 {
			attrs = append(attrs, fmt.Sprintf("FRAME-RATE=%.3f", v.FrameRate))
		}
	}
	audio := s.audioStream()
	if audio != nil {
		if c := media.AudioCodecString(s.Decision.AudioCodec); c != "" {
			codecs = append(codecs, c)
		}
	}
	if len(codecs) > 0 {
		attrs = append(attrs, fmt.Sprintf("CODECS=%q", strings.Join(codecs, ",")))
	}

	videoURI := "media.m3u8"
	if s.remux != nil {
		videoURI = "v/media.m3u8"
		if audio != nil {
			// Demuxed audio rendition: cut independently so boundaries are gapless.
			name := audio.Title
			if name == "" {
				name = strings.ToUpper(audio.Language)
			}
			if name == "" {
				name = "Audio"
			}
			ch := strconv.Itoa(max(audio.Channels, 2))
			if audio.Atmos && s.Decision.AudioCopy {
				ch = "16/JOC"
			}
			lang := ""
			if audio.Language != "" && audio.Language != "und" {
				lang = fmt.Sprintf(",LANGUAGE=%q", audio.Language)
			}
			fmt.Fprintf(&b, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=%q%s,DEFAULT=YES,AUTOSELECT=YES,CHANNELS=%q,URI=\"a/media.m3u8\"\n",
				name, lang, ch)
			attrs = append(attrs, `AUDIO="aud"`)
		}
		if v == nil {
			videoURI = "a/media.m3u8"
		}
	}
	if v != nil && s.subtitleRenditions(&b) {
		attrs = append(attrs, `SUBTITLES="subs"`)
	}
	b.WriteString("#EXT-X-STREAM-INF:" + strings.Join(attrs, ",") + "\n" + videoURI + "\n")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	io.WriteString(w, b.String())
}

func (s *Session) serveMediaPlaylist(w http.ResponseWriter) {
	target := 0.0
	for _, p := range s.plan {
		target = math.Max(target, p.end-p.start)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MAP:URI=\"init.mp4\"\n",
		int(math.Ceil(target)))
	for i, p := range s.plan {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n%d.m4s\n", p.end-p.start, i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	io.WriteString(w, b.String())
}

func (s *Session) audioStream() *media.Stream {
	if s.Decision.AudioIndex < 0 {
		return nil
	}
	for i := range s.Info.Audio {
		if s.Info.Audio[i].TypeIdx == s.Decision.AudioIndex {
			return &s.Info.Audio[i]
		}
	}
	return nil
}

// ---------- remux: one short ffmpeg run per segment and track ----------
//
// Each segment is cut independently with stream copy at keyframes taken from
// the file's index, so seeking anywhere is instant and nothing runs while the
// player is paused. Video and audio are separate renditions: video is cut at
// keyframes by frame count; audio is cut on its own packet boundaries
// (-copypriorss 0 + -to), which makes consecutive segments exactly contiguous.
// Timestamps are preserved (-copyts) so fragment decode times match the playlist.

// tsOffset keeps every output timestamp positive (encoder priming can make
// the first audio packets slightly negative, which fMP4 can't express).
const tsOffset = 10.0

// seekNudge compensates ffmpeg's demuxer seek, which backs off ~0.13s for
// streams with B-frames; the keyframe at the segment start is still chosen.
const seekNudge = 0.14

type remuxer struct {
	s   *Session
	sf  singleflight.Group
	sem chan struct{}
	tag string
}

func newRemuxer(s *Session) *remuxer {
	r := &remuxer{s: s, sem: make(chan struct{}, 4)}
	if v := s.Info.PrimaryVideo(); v != nil {
		r.tag = "hvc1"
		switch {
		case v.Codec == "h264":
			r.tag = "avc1"
		case v.Codec == "av1":
			r.tag = "av01"
		case v.DVProfile == 5:
			r.tag = "dvh1"
		}
	}
	return r
}

func (r *remuxer) trackDir(track string) string {
	d := filepath.Join(r.s.dir, track)
	_ = os.MkdirAll(d, 0o755)
	return d
}

func (r *remuxer) init(ctx context.Context, track string) (string, error) {
	p := filepath.Join(r.trackDir(track), "init.mp4")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	// The init segment comes from segment 0's output (codec config is identical for all).
	if _, err := r.segment(ctx, track, 0); err != nil {
		return "", err
	}
	return p, nil
}

func (r *remuxer) segment(ctx context.Context, track string, n int) (string, error) {
	p := filepath.Join(r.trackDir(track), strconv.Itoa(n)+".m4s")
	if _, err := os.Stat(p); err != nil {
		_, err, _ := r.sf.Do(track+strconv.Itoa(n), func() (any, error) {
			if _, err := os.Stat(p); err == nil {
				return nil, nil
			}
			return nil, r.produce(track, n)
		})
		if err != nil {
			return "", err
		}
	}
	r.prefetch(track, n)
	return p, nil
}

// prefetch prepares the next segments in the background so playback never waits.
func (r *remuxer) prefetch(track string, n int) {
	for i := n + 1; i <= n+3 && i < len(r.s.plan); i++ {
		i := i
		p := filepath.Join(r.trackDir(track), strconv.Itoa(i)+".m4s")
		if _, err := os.Stat(p); err == nil {
			continue
		}
		go r.sf.Do(track+strconv.Itoa(i), func() (any, error) {
			if _, err := os.Stat(p); err == nil {
				return nil, nil
			}
			return nil, r.produce(track, i)
		})
	}
}

func (r *remuxer) produce(track string, n int) error {
	select {
	case r.sem <- struct{}{}:
	case <-r.s.ctx.Done():
		return r.s.ctx.Err()
	}
	defer func() { <-r.sem }()

	s := r.s
	seg := s.plan[n]
	last := n == len(s.plan)-1
	args := []string{"-nostdin", "-hide_banner", "-v", "error", "-y", "-copyts"}

	switch track {
	case "v":
		v := s.Info.PrimaryVideo()
		if v == nil {
			return errors.New("no video stream")
		}
		if seg.start > 0 {
			args = append(args, "-ss", fmtTS(seg.start+seekNudge))
		}
		args = append(args, "-i", s.Src.Input(), "-map", fmt.Sprintf("0:v:%d", v.TypeIdx), "-c:v", "copy")
		if r.tag != "" {
			args = append(args, "-tag:v", r.tag)
		}
		if s.Decision.StripDolbyVision {
			args = append(args, "-bsf:v", "hevc_metadata=remove_dovi=1")
		}
		if !last {
			// Stop exactly before the next keyframe. -to compares DTS and overshoots
			// with B-frames, so use a frame count when the rate is constant.
			if v.FrameRate > 0 {
				args = append(args, "-frames:v", strconv.Itoa(int(math.Round((seg.end-seg.start)*v.FrameRate))))
			} else {
				args = append(args, "-to", fmtTS(seg.end))
			}
		}
	case "a":
		a := s.audioStream()
		if a == nil {
			return errors.New("no audio stream")
		}
		if seg.start > 0 {
			args = append(args, "-ss", fmtTS(seg.start))
		}
		args = append(args, "-i", s.Src.Input(), "-map", fmt.Sprintf("0:a:%d", a.TypeIdx))
		if s.Decision.AudioCopy {
			// Drop packets before the start; the previous segment owns them.
			args = append(args, "-c:a", "copy", "-copypriorss", "0")
		} else {
			switch s.Decision.AudioCodec {
			case "flac":
				// Lossless with no encoder priming (browsers); 24-bit max in FLAC.
				args = append(args, "-c:a", "flac", "-strict", "-2")
			case "aac":
				args = append(args, "-c:a", "aac", "-b:a", "384k")
			default:
				// Apple Lossless: bit-exact, and no encoder priming so segments join seamlessly.
				args = append(args, "-c:a", "alac")
			}
		}
		if !last {
			args = append(args, "-to", fmtTS(seg.end))
		}
	default:
		return errors.New("unknown track")
	}

	out := filepath.Join(r.trackDir(track), strconv.Itoa(n)+".tmp.mp4")
	args = append(args, "-sn", "-dn", "-map_chapters", "-1", "-map_metadata", "-1",
		"-output_ts_offset", fmtTS(tsOffset))
	if !s.Decision.StripDolbyVision {
		args = append(args, "-strict", "unofficial") // writes the Dolby Vision configuration box (dvcC/dvvC)
	}
	args = append(args, "-f", "mp4", "-movflags", "frag_keyframe+delay_moov+default_base_moof+frag_discont",
		out)
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.m.cfg.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(out)
		return fmt.Errorf("ffmpeg remux %s segment %d: %v: %s", track, n, err, strings.TrimSpace(stderr.String()))
	}
	return splitFragment(out, filepath.Join(r.trackDir(track), "init.mp4"), filepath.Join(r.trackDir(track), strconv.Itoa(n)+".m4s"))
}

// splitFragment separates ftyp+moov (init) from the moof/mdat media data.
// The init is kept only the first time (all segments share codec config).
func splitFragment(src, initPath, segPath string) error {
	b, err := os.ReadFile(src)
	os.Remove(src)
	if err != nil {
		return err
	}
	var initEnd int
	off := 0
	for off+8 <= len(b) {
		size := int(binary.BigEndian.Uint32(b[off:]))
		typ := string(b[off+4 : off+8])
		if size == 1 && off+16 <= len(b) {
			size = int(binary.BigEndian.Uint64(b[off+8:]))
		}
		if size < 8 || off+size > len(b) {
			return errors.New("malformed mp4 output")
		}
		if typ == "moof" {
			break
		}
		off += size
		initEnd = off
	}
	if initEnd == 0 || initEnd >= len(b) {
		return errors.New("segment has no media fragments")
	}
	if _, err := os.Stat(initPath); os.IsNotExist(err) {
		if err := writeAtomic(initPath, b[:initEnd]); err != nil {
			return err
		}
	}
	return writeAtomic(segPath, b[initEnd:])
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fmtTS(t float64) string { return strconv.FormatFloat(t, 'f', 3, 64) }
