package api

import (
	"cmp"
	"context"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/media"
	"github.com/LTAGROUP/wrentv/internal/stream"
	"github.com/LTAGROUP/wrentv/internal/sysstat"
)

// ---------- live playback state ----------

// activity remembers what players last reported, where each session came
// from, and the streams an admin stopped.
type activity struct {
	mu       sync.Mutex
	reports  map[string]*report  // user|item
	sessions map[string]sessFrom // session id
	kills    map[string]killNote // session id
	stopped  map[string]killNote // user|item
}

type report struct {
	pos, dur float64
	at       time.Time
	state    string // as the player said, or ""
	moving   bool   // the position advanced since the report before
}

type sessFrom struct {
	ip, client string
}

type killNote struct {
	msg string
	at  time.Time
}

// How long a stopped title refuses to start again, and how long progress
// reports from the stopped player are answered with the reason.
const (
	refuseFor  = time.Minute
	stoppedFor = 10 * time.Minute
)

func (s *Server) act() *activity {
	s.activityOnce.Do(func() {
		s.activity = &activity{reports: map[string]*report{}, sessions: map[string]sessFrom{},
			kills: map[string]killNote{}, stopped: map[string]killNote{}}
	})
	return s.activity
}

func (a *activity) progress(userID, itemID string, pos, dur float64, state string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := userID + "|" + itemID
	prev := a.reports[k]
	r := &report{pos: pos, dur: dur, at: time.Now(), state: state}
	if prev != nil {
		r.moving = pos > prev.pos+0.5 && pos-prev.pos < time.Since(prev.at).Seconds()+30
	} else {
		r.moving = true
	}
	a.reports[k] = r
	// Forget what's long over.
	for k, x := range a.reports {
		if time.Since(x.at) > 6*time.Hour {
			delete(a.reports, k)
		}
	}
}

func (a *activity) started(sid, ip, client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[sid] = sessFrom{ip, client}
	if len(a.sessions) > 500 {
		for k := range a.sessions {
			delete(a.sessions, k)
			break
		}
	}
}

func (a *activity) from(sid string) sessFrom {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[sid]
}

// live is where a playback is now: its state, the position last reported
// and when. Players report every ten seconds while playing; one that has
// gone quiet with its session still open is paused.
func (a *activity) live(userID, itemID string) (state string, pos, dur float64, at time.Time, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.reports[userID+"|"+itemID]
	if r == nil {
		return "", 0, 0, time.Time{}, false
	}
	state = r.state
	if state == "" || state == "playing" {
		switch {
		case time.Since(r.at) > 25*time.Second:
			state = "paused"
		case !r.moving && r.state == "":
			state = "paused"
		default:
			state = "playing"
		}
	}
	return state, r.pos, r.dur, r.at, true
}

func (a *activity) kill(sid, userID, itemID, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "The server owner stopped this stream."
	} else {
		msg = "The server owner stopped this stream: " + msg
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	n := killNote{msg, time.Now()}
	a.kills[sid] = n
	a.stopped[userID+"|"+itemID] = n
	for k, x := range a.kills {
		if time.Since(x.at) > time.Hour {
			delete(a.kills, k)
		}
	}
	for k, x := range a.stopped {
		if time.Since(x.at) > stoppedFor {
			delete(a.stopped, k)
		}
	}
}

// killed is the message for a stopped session's stream requests.
func (a *activity) killed(sid string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n, ok := a.kills[sid]
	return n.msg, ok
}

// refused says whether starting this title again is still refused. Once the
// minute is up it may start, and stops answering progress with the reason.
func (a *activity) refused(userID, itemID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := userID + "|" + itemID
	n, ok := a.stopped[k]
	if !ok {
		return "", false
	}
	if time.Since(n.at) > refuseFor {
		delete(a.stopped, k)
		return "", false
	}
	return n.msg, true
}

// stoppedFor says whether a player's progress report is for a playback an
// admin stopped.
func (a *activity) stoppedFor(userID, itemID string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n, ok := a.stopped[userID+"|"+itemID]
	if !ok || time.Since(n.at) > stoppedFor {
		return "", false
	}
	return n.msg, true
}

// clientIP is the viewer's address. Behind a reverse proxy on the same
// network the proxy's X-Forwarded-For names the viewer; it is only used for
// display, never to grant access.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if a, err := netip.ParseAddr(host); err == nil && (a.IsLoopback() || a.IsPrivate()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if fa, err := netip.ParseAddr(strings.TrimSpace(first)); err == nil {
				return fa.Unmap().String()
			}
		}
		return a.Unmap().String()
	}
	return host
}

// isLocal says whether an address is on the server's own network.
func isLocal(ip string) bool {
	a, err := netip.ParseAddr(ip)
	return err == nil && (a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast())
}

// ---------- now playing ----------

// trackInfo describes a file's picture and sound, or what is sent of them.
type trackInfo struct {
	Container     string  `json:"container,omitempty"`
	Kbps          int     `json:"kbps,omitempty"`
	VideoCodec    string  `json:"videoCodec,omitempty"`
	VideoProfile  string  `json:"videoProfile,omitempty"`
	Width         int     `json:"width,omitempty"`
	Height        int     `json:"height,omitempty"`
	FrameRate     float64 `json:"frameRate,omitempty"`
	HDR           string  `json:"hdr,omitempty"`
	BitDepth      int     `json:"bitDepth,omitempty"`
	AudioCodec    string  `json:"audioCodec,omitempty"`
	AudioChannels int     `json:"audioChannels,omitempty"`
	AudioLayout   string  `json:"audioLayout,omitempty"`
	AudioLanguage string  `json:"audioLanguage,omitempty"`
	AudioTitle    string  `json:"audioTitle,omitempty"`
	// Video and Audio say what happens to each on the way:
	// "direct", "copy" or "transcode".
	Video string `json:"video,omitempty"`
	Audio string `json:"audio,omitempty"`
}

type nowPlaying struct {
	SessionID  string `json:"sessionId"`
	UserID     string `json:"userId"`
	UserName   string `json:"userName"`
	ItemID     string `json:"itemId"`
	Kind       string `json:"kind"` // movie, episode or live
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle,omitempty"`
	Year       int    `json:"year,omitempty"`
	PosterPath string `json:"posterPath,omitempty"`
	Backdrop   string `json:"backdropPath,omitempty"`
	Library    string `json:"library,omitempty"`

	// State is playing or paused. Position was reported at PositionAt (Unix
	// seconds); while playing it advances in real time from there.
	State      string  `json:"state"`
	Position   float64 `json:"position"`
	PositionAt float64 `json:"positionAt"`
	Duration   float64 `json:"duration"`
	Started    int64   `json:"started"`

	Client string `json:"client"`
	IP     string `json:"ip,omitempty"`
	Local  bool   `json:"local"`

	Method     string   `json:"method"`
	Engine     string   `json:"engine"`
	VideoCopy  bool     `json:"videoCopy"`
	AudioCopy  bool     `json:"audioCopy"`
	AudioCodec string   `json:"audioCodec,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
	Quality    string   `json:"quality"`

	Source   trackInfo       `json:"source"`
	Stream   trackInfo       `json:"stream"`
	Delivery stream.Delivery `json:"delivery"`
	// Transcode details: the hardware doing it and Dolby Vision handling.
	HW          string `json:"hw,omitempty"`
	HWDecode    bool   `json:"hwDecode,omitempty"`
	Tonemap     bool   `json:"tonemap,omitempty"`
	StripDV     bool   `json:"stripDolbyVision,omitempty"`
	ChannelLogo string `json:"channelLogo,omitempty"`
}

func kbps(bps int64) int { return int(bps / 1000) }

func sourceInfo(info *media.Info, audioIdx int) trackInfo {
	t := trackInfo{}
	if info == nil {
		return t
	}
	t.Container, t.Kbps = info.Container, kbps(info.Bitrate)
	if v := info.PrimaryVideo(); v != nil {
		t.VideoCodec, t.VideoProfile, t.Width, t.Height, t.FrameRate, t.HDR, t.BitDepth =
			v.Codec, v.Profile, v.Width, v.Height, v.FrameRate, v.HDR, v.BitDepth
		if v.DVProfile > 0 {
			t.HDR = "Dolby Vision " + strconv.Itoa(v.DVProfile)
		}
	}
	for _, a := range info.Audio {
		if a.TypeIdx == audioIdx || (audioIdx < 0 && t.AudioCodec == "") {
			t.AudioCodec, t.AudioChannels, t.AudioLayout, t.AudioLanguage, t.AudioTitle =
				a.Codec, a.Channels, a.ChannelLayout, a.Language, a.Title
		}
	}
	return t
}

// streamInfo is what the player is sent.
func streamInfo(src trackInfo, dec media.Decision, d stream.Delivery) trackInfo {
	out := src
	switch dec.Method {
	case media.MethodDirect:
		out.Video, out.Audio = "direct", "direct"
		return out
	case media.MethodRemux:
		out.Container, out.Video, out.Audio = "HLS", "copy", "copy"
		if dec.StripDolbyVision {
			out.HDR = "HDR10"
		}
		if !dec.AudioCopy && dec.AudioCodec != "" {
			out.Audio, out.AudioCodec = "transcode", dec.AudioCodec
		}
		return out
	}
	out.Container = "HLS"
	if dec.VideoCopy {
		out.Video = "copy"
	} else {
		out.Video = "transcode"
		out.VideoCodec, out.VideoProfile, out.BitDepth, out.HDR = cmp.Or(d.OutCodec, "h264"), "", 8, ""
		out.Width, out.Height, out.Kbps = d.OutWidth, d.OutHeight, d.OutKbps
	}
	if dec.AudioCopy {
		out.Audio = "copy"
	} else if src.AudioCodec != "" {
		out.Audio, out.AudioCodec, out.AudioChannels, out.AudioLayout = "transcode", "aac", 2, "stereo"
	}
	return out
}

func (s *Server) nowPlaying(ctx context.Context) []nowPlaying {
	out := []nowPlaying{}
	names := map[string]string{}
	userName := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		n := id
		if u, err := s.DB.UserByID(ctx, id); err == nil {
			n = firstNonEmpty(u.DisplayName, u.Username, u.Email, u.ID)
		}
		names[id] = n
		return n
	}
	libs := map[string]string{}
	if ls, err := s.DB.ListLibraries(ctx); err == nil {
		for _, l := range ls {
			libs[l.ID] = l.Name
		}
	}
	for _, sess := range s.Streams.List() {
		dec := sess.Decision
		np := nowPlaying{SessionID: sess.ID, UserID: sess.UserID, UserName: userName(sess.UserID), ItemID: sess.ItemID,
			Method: string(dec.Method), Engine: dec.Engine, VideoCopy: dec.VideoCopy, AudioCopy: dec.AudioCopy,
			AudioCodec: dec.AudioCodec, Reasons: dec.Reasons, Started: sess.Started.Unix(), StripDV: dec.StripDolbyVision}
		if it, err := s.DB.GetItem(ctx, sess.ItemID); err == nil {
			np.Kind, np.Title, np.Year, np.PosterPath, np.Backdrop, np.Library = it.Kind, it.Title, it.Year, it.PosterPath, it.BackdropPath, libs[it.LibraryID]
			if it.Kind == "episode" {
				np.Subtitle = it.Title
				np.Backdrop = it.StillPath
				if sh, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
					np.Title, np.PosterPath, np.Year = sh.Title, sh.PosterPath, sh.Year
					if np.Backdrop == "" {
						np.Backdrop = sh.BackdropPath
					}
					np.Subtitle = "S" + strconv.Itoa(it.SeasonNumber) + " · E" + strconv.Itoa(it.EpisodeNumber) + " — " + it.Title
				}
			}
		}
		if f, err := s.DB.GetFile(ctx, sess.FileID); err == nil {
			np.Quality = versionLabel(f)
		}
		var client string
		np.Position, np.Duration, client = s.DB.PlayBySession(ctx, sess.ID)
		from := s.act().from(sess.ID)
		np.Client, np.IP = cmp.Or(from.client, client), from.ip
		np.Local = isLocal(np.IP)
		np.PositionAt = float64(time.Now().Unix())
		np.State = "playing"
		if state, pos, dur, at, ok := s.act().live(sess.UserID, sess.ItemID); ok && !at.Before(sess.Started.Add(-time.Minute)) {
			np.State, np.Position, np.PositionAt = state, pos, float64(at.UnixMilli())/1000
			if dur > 0 {
				np.Duration = dur
			}
		} else if time.Since(sess.Started) > 30*time.Second && sess.Idle() > 30*time.Second {
			np.State = "paused"
		}
		if sess.Info != nil && np.Duration == 0 {
			np.Duration = sess.Info.Duration
		}
		np.Delivery = sess.Delivery()
		np.Source = sourceInfo(sess.Info, dec.AudioIndex)
		np.Stream = streamInfo(np.Source, dec, np.Delivery)
		if dec.Method == media.MethodTranscode {
			np.HW, np.HWDecode, np.Tonemap = np.Delivery.HWKind, np.Delivery.HWDecode, np.Delivery.Tonemap && !dec.VideoCopy
		}
		if np.Delivery.Kbps > 0 && np.Stream.Kbps == 0 {
			np.Stream.Kbps = np.Delivery.Kbps
		}
		out = append(out, np)
	}
	for _, x := range s.LiveTV.Sessions() {
		np := nowPlaying{SessionID: x.ID, UserID: x.UserID, UserName: userName(x.UserID), Kind: "live", Method: "direct",
			State: "playing", Started: x.Started.Unix(), PositionAt: float64(time.Now().Unix()), Title: "Live TV"}
		if x.Remuxed() {
			np.Method = "remux"
		}
		if c := s.LiveTV.Channel(x.ChannelID); c != nil {
			np.Title, np.ChannelLogo = c.Name, c.Logo
			if c.Number != "" {
				np.Subtitle = "Channel " + c.Number
			}
		}
		np.Position = time.Since(x.Started).Seconds()
		out = append(out, np)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	return out
}

// handleActivity is the dashboard's live view, polled every few seconds:
// what's playing, the bandwidth it takes and the machine's load.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	np := s.nowPlaying(r.Context())
	type totals struct {
		Streams   int `json:"streams"`
		Direct    int `json:"direct"`
		Remux     int `json:"remux"`
		Transcode int `json:"transcode"`
		Kbps      int `json:"kbps"`
		LANKbps   int `json:"lanKbps"`
		WANKbps   int `json:"wanKbps"`
	}
	t := totals{Streams: len(np)}
	for _, n := range np {
		switch n.Method {
		case "direct":
			t.Direct++
		case "remux":
			t.Remux++
		default:
			t.Transcode++
		}
		// Bandwidth is what the streams need, as Plex counts it: players
		// fetch in bursts, so the rate of the moment swings to nothing.
		k := cmp.Or(n.Stream.Kbps, n.Delivery.Kbps)
		t.Kbps += k
		if n.Local {
			t.LANKbps += k
		} else {
			t.WANKbps += k
		}
	}
	resp := map[string]any{"serverTime": float64(time.Now().UnixMilli()) / 1000, "sessions": np, "totals": t}
	if s.Sys != nil {
		if x, ok := s.Sys.Latest(); ok {
			resp["system"] = x
		}
		resp["systemInfo"] = s.Sys.Info()
	}
	writeJSON(w, 200, resp)
}

// handleSystem is the machine's CPU and memory over the last hour (every
// five seconds) or day (by minute), with the disks under each library.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := time.Hour
	switch r.URL.Query().Get("range") {
	case "6h":
		span = 6 * time.Hour
	case "24h":
		span = 24 * time.Hour
	}
	type disk struct {
		sysstat.Disk
		Label string `json:"label"`
	}
	disks := []disk{}
	seen := map[uint64]bool{} // one row per volume
	addDisk := func(label, path string) {
		d, ok := sysstat.DiskUsage(path)
		if !ok || d.Total == 0 {
			return
		}
		key := d.Total<<1 ^ d.Free
		if seen[key] {
			for i := range disks {
				if disks[i].Total == d.Total && disks[i].Free == d.Free && !strings.Contains(disks[i].Label, label) {
					disks[i].Label += ", " + label
				}
			}
			return
		}
		seen[key] = true
		disks = append(disks, disk{d, label})
	}
	addDisk("Server data", s.Cfg.DataDir)
	if ls, err := s.DB.ListLibraries(ctx); err == nil {
		for _, l := range ls {
			for _, p := range l.Paths {
				addDisk(l.Name, p)
			}
		}
	}
	resp := map[string]any{"version": s.Version, "hardware": s.Streams.HW(), "disks": disks, "samples": []sysstat.Sample{}}
	if s.Sys != nil {
		resp["info"] = s.Sys.Info()
		resp["samples"] = s.Sys.Since(span)
		resp["uptime"] = int64(s.Sys.Uptime().Seconds())
	}
	writeJSON(w, 200, resp)
}

// handleHistory pages through play history with filters:
// days, user, kind, method, item, library, q, offset and limit.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := db.HistoryFilter{UserID: q.Get("user"), Kind: q.Get("kind"), Method: q.Get("method"), ItemID: q.Get("item"),
		LibraryID: q.Get("library"), Query: q.Get("q"), Offset: queryInt(r, "offset", 0), Limit: min(max(queryInt(r, "limit", 50), 1), 500)}
	if days := queryInt(r, "days", 0); days > 0 {
		f.Since = time.Now().AddDate(0, 0, -days).Unix()
	}
	rows, total, err := s.DB.History(r.Context(), f)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"total": total, "rows": rows})
}

func (s *Server) handleViewers(w http.ResponseWriter, r *http.Request) {
	vs, err := s.DB.Viewers(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, vs)
}

func (s *Server) handleViewer(w http.ResponseWriter, r *http.Request) {
	v, err := s.DB.ViewerDetail(r.Context(), chi.URLParam(r, "id"))
	if err == db.ErrNotFound {
		writeErr(w, 404, "viewer not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, v)
}
