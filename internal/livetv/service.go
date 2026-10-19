package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

const (
	playlistEvery = 12 * time.Hour
	guideEvery    = 6 * time.Hour
	// Guide window kept in memory.
	guideBehind = 2 * time.Hour
	guideAhead  = 36 * time.Hour
	// DefaultUserAgent is what IPTV providers expect to see: most only serve players.
	DefaultUserAgent = "VLC/3.0.20 LibVLC/3.0.20"
)

// Status is how the last refresh went, for the dashboard.
type Status struct {
	Refreshing     bool      `json:"refreshing"`
	Channels       int       `json:"channels"`
	Visible        int       `json:"visible"`
	GuideChannels  int       `json:"guideChannels"`
	Refreshed      time.Time `json:"refreshed,omitzero"`
	GuideRefreshed time.Time `json:"guideRefreshed,omitzero"`
	Errors         []string  `json:"errors,omitempty"`
}

// Group is a playlist group and how many channels it has.
type Group struct {
	Name     string `json:"name"`
	Channels int    `json:"channels"`
	Hidden   bool   `json:"hidden"`
}

type Service struct {
	// FFmpeg repackages TS channels as HLS for Apple players; empty turns that off.
	FFmpeg string

	cfg    *config.Config
	dir    string
	client *http.Client

	mu      sync.RWMutex
	all     []*Channel // every channel, in playlist order
	visible []*Channel // numbered, without hidden groups
	byID    map[string]*Channel
	guide   Guide
	status  Status

	kick chan bool // true: refetch even if fresh

	sessions sessions
}

func New(cfg *config.Config, dataDir string) *Service {
	return &Service{
		cfg:    cfg,
		dir:    filepath.Join(dataDir, "cache", "livetv"),
		client: &http.Client{Timeout: 5 * time.Minute},
		byID:   map[string]*Channel{},
		guide:  Guide{},
		kick:   make(chan bool, 1),
	}
}

// Active: turned on and has channels to show.
func (s *Service) Active() bool {
	if !s.cfg.LiveTV.Enabled {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.visible) > 0
}

// Start loads what was cached and keeps playlists and guides fresh.
func (s *Service) Start(ctx context.Context) {
	go s.run(ctx)
	go s.sessions.reap(ctx)
}

// Refresh fetches playlists and guides now (after a settings change, say).
func (s *Service) Refresh(force bool) {
	select {
	case s.kick <- force:
	default:
	}
}

func (s *Service) run(ctx context.Context) {
	s.refresh(ctx, false)
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case force := <-s.kick:
			s.refresh(ctx, force)
		case <-t.C:
			s.refresh(ctx, false)
		}
	}
}

func (s *Service) refresh(ctx context.Context, force bool) {
	lc := s.cfg.LiveTV
	if !lc.Enabled || len(lc.Sources) == 0 {
		s.mu.Lock()
		s.all, s.visible, s.byID, s.guide = nil, nil, map[string]*Channel{}, Guide{}
		s.status = Status{}
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.status.Refreshing = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.status.Refreshing = false
		s.mu.Unlock()
	}()
	_ = os.MkdirAll(s.dir, 0o755)

	var errs []string
	var all []*Channel
	var guideURLs []string
	guideUA := map[string]string{}
	newest := time.Time{}
	for _, src := range lc.Sources {
		if !src.Enabled {
			continue
		}
		file := filepath.Join(s.dir, "playlist-"+src.ID+".m3u")
		var fetched time.Time
		var err error
		if src.Kind == "stremio" {
			fetched, err = s.saveStremio(ctx, src, file, playlistEvery, force)
		} else {
			fetched, err = s.fetch(ctx, PlaylistURL(src), file, ua(src.UserAgent), playlistEvery, force)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", src.Name, err))
		}
		f, err := os.Open(file)
		if err != nil {
			continue // nothing fetched now or before
		}
		pl, err := ParseM3U(f, src.ID)
		f.Close()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", src.Name, err))
			continue
		}
		if len(pl.Channels) == 0 {
			errs = append(errs, src.Name+": the playlist has no channels")
		}
		for _, c := range pl.Channels {
			if c.UserAgent == "" {
				c.UserAgent = ua(src.UserAgent)
			}
			c.stremio = src.Kind == "stremio"
		}
		all = append(all, pl.Channels...)
		for _, u := range GuideURLs(src, pl) {
			if !contains(guideURLs, u) {
				guideURLs = append(guideURLs, u)
				guideUA[u] = ua(src.UserAgent)
			}
		}
		if fetched.After(newest) {
			newest = fetched
		}
	}

	s.mu.Lock()
	s.all = all
	s.byID = make(map[string]*Channel, len(all))
	for _, c := range all {
		s.byID[c.ID] = c
	}
	s.number()
	s.status.Channels = len(all)
	s.status.Refreshed = newest
	s.mu.Unlock()

	// The guide.
	guide := Guide{}
	var guideTime time.Time
	now := time.Now()
	for i, u := range guideURLs {
		file := filepath.Join(s.dir, "guide-"+shortHash(u)+".xml")
		fetched, err := s.fetch(ctx, u, file, guideUA[u], guideEvery, force)
		if err != nil {
			errs = append(errs, fmt.Sprintf("guide %d: %v", i+1, err))
		}
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		if err := ParseXMLTV(f, all, now.Add(-guideBehind), now.Add(guideAhead), guide); err != nil {
			errs = append(errs, fmt.Sprintf("guide %d: %v", i+1, err))
		}
		f.Close()
		if fetched.After(guideTime) {
			guideTime = fetched
		}
	}
	guide.Tidy()
	s.mu.Lock()
	s.guide = guide
	s.status.GuideChannels = len(guide)
	s.status.GuideRefreshed = guideTime
	s.status.Errors = errs
	s.mu.Unlock()
	s.pruneCache(lc.Sources, guideURLs)
	slog.Info("live tv refreshed", "channels", len(all), "guide", len(guide), "errors", len(errs))
}

// number leaves out hidden groups and numbers what's left: the playlist's
// own numbers where it has them, the next free number otherwise.
func (s *Service) number() {
	hidden := map[string]bool{}
	for _, g := range s.cfg.LiveTV.HiddenGroups {
		hidden[g] = true
	}
	var vis []*Channel
	taken := map[string]bool{}
	for _, c := range s.all {
		if hidden[groupName(c)] {
			continue
		}
		vis = append(vis, c)
		if c.numbered {
			taken[c.Number] = true
		}
	}
	n := 0
	for _, c := range vis {
		if c.numbered {
			continue
		}
		for {
			n++
			if !taken[strconv.Itoa(n)] {
				break
			}
		}
		c.Number = strconv.Itoa(n)
	}
	sort.SliceStable(vis, func(i, j int) bool { return numLess(vis[i].Number, vis[j].Number) })
	s.visible = vis
	s.status.Visible = len(vis)
}

// Regroup applies a change to hidden groups without fetching anything.
func (s *Service) Regroup() {
	s.mu.Lock()
	s.number()
	s.mu.Unlock()
}

// fetch saves url to file unless the file is younger than maxAge. It returns
// when the file was last fetched.
func (s *Service) fetch(ctx context.Context, url, file, userAgent string, maxAge time.Duration, force bool) (time.Time, error) {
	if st, err := os.Stat(file); err == nil && !force && time.Since(st.ModTime()) < maxAge {
		return st.ModTime(), nil
	}
	old := time.Time{}
	if st, err := os.Stat(file); err == nil {
		old = st.ModTime()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return old, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return old, errors.New(redact(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return old, fmt.Errorf("the provider answered %s", resp.Status)
	}
	tmp := file + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return old, err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 2<<30))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return old, errors.New(redact(err.Error()))
	}
	if err := os.Rename(tmp, file); err != nil {
		return old, err
	}
	return time.Now(), nil
}

// pruneCache removes files for sources and guides no longer used.
func (s *Service) pruneCache(sources []config.LiveTVSource, guides []string) {
	keep := map[string]bool{}
	for _, src := range sources {
		keep["playlist-"+src.ID+".m3u"] = true
	}
	for _, u := range guides {
		keep["guide-"+shortHash(u)+".xml"] = true
	}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if !keep[e.Name()] {
			os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

// ChannelView is a channel with what's on.
type ChannelView struct {
	Channel
	Now  *Program `json:"now,omitempty"`
	Next *Program `json:"next,omitempty"`
}

// Channels lists the visible channels with what's on now and next.
func (s *Service) Channels() []ChannelView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := time.Now().Unix()
	out := make([]ChannelView, 0, len(s.visible))
	for _, c := range s.visible {
		v := ChannelView{Channel: *c}
		v.Now, v.Next = s.guide.NowNext(c.ID, t)
		out = append(out, v)
	}
	return out
}

// GuideView is a channel's programmes in a window.
type GuideView struct {
	ID       string    `json:"id"`
	Programs []Program `json:"programs"`
}

// Schedule lists each visible channel's programmes from now for [hours].
func (s *Service) Schedule(hours int) []GuideView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	from := time.Now().Unix()
	to := from + int64(hours)*3600
	out := make([]GuideView, 0, len(s.visible))
	for _, c := range s.visible {
		ps := []Program{}
		for _, p := range s.guide[c.ID] {
			if p.End > from && p.Start < to {
				ps = append(ps, p)
			}
		}
		out = append(out, GuideView{ID: c.ID, Programs: ps})
	}
	return out
}

// Channel finds a visible channel.
func (s *Service) Channel(id string) *Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.byID[id]
	if c == nil {
		return nil
	}
	for _, v := range s.visible {
		if v == c {
			return c
		}
	}
	return nil
}

func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.status
	st.Errors = append([]string(nil), st.Errors...)
	return st
}

// Groups lists every playlist group, in playlist order.
func (s *Service) Groups() []Group {
	hidden := map[string]bool{}
	for _, g := range s.cfg.LiveTV.HiddenGroups {
		hidden[g] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx := map[string]int{}
	out := []Group{}
	for _, c := range s.all {
		g := groupName(c)
		i, ok := idx[g]
		if !ok {
			i = len(out)
			idx[g] = i
			out = append(out, Group{Name: g, Hidden: hidden[g]})
		}
		out[i].Channels++
	}
	return out
}

// Check fetches a source's playlist without saving anything: for the
// dashboard's Check button.
func (s *Service) Check(ctx context.Context, src config.LiveTVSource) (channels int, guides []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if src.Kind == "stremio" {
		chans, err := s.StremioChannels(ctx, src)
		if err != nil {
			return 0, nil, err
		}
		if len(chans) == 0 {
			return 0, nil, errors.New("the addon lists no channels")
		}
		return len(chans), GuideURLs(src, nil), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PlaylistURL(src), nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", ua(src.UserAgent))
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, errors.New(redact(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, nil, fmt.Errorf("the provider answered %s", resp.Status)
	}
	pl, err := ParseM3U(io.LimitReader(resp.Body, 512<<20), src.ID)
	if err != nil {
		return 0, nil, err
	}
	if len(pl.Channels) == 0 {
		return 0, nil, errors.New("that isn't a playlist with channels")
	}
	return len(pl.Channels), GuideURLs(src, pl), nil
}

func groupName(c *Channel) string {
	if g := strings.TrimSpace(c.Group); g != "" {
		return g
	}
	return "Other"
}

func ua(u string) string {
	if u = strings.TrimSpace(u); u != "" {
		return u
	}
	return DefaultUserAgent
}

// numLess orders "2" before "10" and "5.1" after "5".
func numLess(a, b string) bool {
	fa, ea := strconv.ParseFloat(a, 64)
	fb, eb := strconv.ParseFloat(b, 64)
	switch {
	case ea == nil && eb == nil:
		return fa < fb
	case ea == nil:
		return true
	case eb == nil:
		return false
	}
	return a < b
}

// redact keeps logins in Xtream URLs out of errors shown and logged.
func redact(s string) string {
	for _, k := range []string{"password=", "username="} {
		for i := strings.Index(s, k); i >= 0; {
			j := i + len(k)
			end := strings.IndexAny(s[j:], "&\" ")
			if end < 0 {
				end = len(s) - j
			}
			s = s[:j] + "…" + s[j+end:]
			next := strings.Index(s[j+1:], k)
			if next < 0 {
				break
			}
			i = j + 1 + next
		}
	}
	return s
}
