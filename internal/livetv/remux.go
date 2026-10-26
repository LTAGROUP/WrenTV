package livetv

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// remuxer repackages a plain MPEG-TS channel as live HLS for players that
// can't read a raw TS stream (AVPlayer). Video is copied; audio becomes AAC,
// since broadcast MP2 audio doesn't play on Apple devices.
type remuxer struct {
	ffmpeg string
	dir    string

	once sync.Once
	err  error
	done chan struct{}
}

var segName = regexp.MustCompile(`^seg\d+\.ts$`)

// start runs ffmpeg for the session's lifetime; it stops when the session does.
func (m *remuxer) start(x *Session) {
	m.once.Do(func() {
		m.done = make(chan struct{})
		if err := os.MkdirAll(m.dir, 0o755); err != nil {
			m.err = err
			close(m.done)
			return
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
		// A provider that only serves with an allowlisted identity gets the
		// same headers here as on the direct proxy path. The agent goes through
		// -user_agent, so it never competes with a default the build supplies.
		agent := x.headers["User-Agent"]
		if agent == "" {
			agent = ua(x.userAgent)
		}
		args = append(args, "-user_agent", agent)
		if block := requestHeaderBlock(x.headers); block != "" {
			args = append(args, "-headers", block)
		}
		args = append(args,
			"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
			"-i", x.upstream,
			"-map", "0:v:0?", "-map", "0:a:0?",
			"-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ac", "2",
			"-f", "hls", "-hls_time", "2", "-hls_list_size", "8",
			"-hls_flags", "delete_segments+omit_endlist+independent_segments",
			"-hls_segment_filename", filepath.Join(m.dir, "seg%05d.ts"),
			filepath.Join(m.dir, "index.m3u8"),
		)
		cmd := exec.CommandContext(x.ctx, m.ffmpeg, args...)
		if err := cmd.Start(); err != nil {
			m.err = err
			close(m.done)
			os.RemoveAll(m.dir)
			return
		}
		go func() {
			if err := cmd.Wait(); err != nil && x.ctx.Err() == nil {
				slog.Warn("live TV remux stopped", "channel", x.ChannelID, "err", err)
			}
			close(m.done)
			os.RemoveAll(m.dir)
		}()
	})
}

// serve answers hls/index.m3u8 and hls/segNNNNN.ts.
func (m *remuxer) serve(ctx context.Context, x *Session, w http.ResponseWriter, r *http.Request, name string) {
	m.start(x)
	if m.err != nil {
		http.Error(w, "couldn't start the channel: "+m.err.Error(), http.StatusBadGateway)
		return
	}
	switch {
	case name == "index.m3u8":
		// The first playlist appears once ffmpeg has a couple of segments.
		path := filepath.Join(m.dir, name)
		deadline := time.Now().Add(25 * time.Second)
		for {
			if _, err := os.Stat(path); err == nil {
				break
			}
			select {
			case <-m.done:
				http.Error(w, "the channel didn't answer", http.StatusBadGateway)
				return
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				http.Error(w, "the channel is taking too long to start", http.StatusGatewayTimeout)
				return
			}
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, path)
	case segName.MatchString(name):
		w.Header().Set("Content-Type", "video/mp2t")
		http.ServeFile(w, r, filepath.Join(m.dir, name))
	default:
		http.NotFound(w, r)
	}
}
