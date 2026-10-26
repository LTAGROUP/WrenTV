package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	// Guide window kept in memory. Seven days matches the sports search
	// horizon: sports search can find a weekend's fixtures that the old 36-hour
	// window dropped. It costs programme memory proportional to the channels a
	// provider actually publishes, so a short feed stays short.
	guideBehind = 2 * time.Hour
	guideAhead  = 7 * 24 * time.Hour
	// DefaultUserAgent is what IPTV providers expect to see: most only serve players.
	DefaultUserAgent = "VLC/3.0.20 LibVLC/3.0.20"
)

// Status is how the last refresh went, for the dashboard.
type Status struct {
	Refreshing    bool `json:"refreshing"`
	Channels      int  `json:"channels"`
	Visible       int  `json:"visible"`
	GuideChannels int  `json:"guideChannels"`
	// SportSources counts enabled sports-event addons. They are browsed on
	// demand and deliberately do not become channels or guide rows.
	SportSources int `json:"sportSources,omitempty"`
	// AddonSources counts enabled addons that are browsed as catalogs or
	// events rather than imported as channels, whatever their content types.
	AddonSources int `json:"addonSources,omitempty"`
	// Events counts the addon catalog items offered as Live TV entries. They
	// are not channels and carry no guide rows.
	Events int `json:"events,omitempty"`
	// Prepared reports that at least one refresh has finished. Before that,
	// an empty list means the provider data is still loading, not that the
	// provider has nothing to offer.
	Prepared       bool           `json:"prepared"`
	Refreshed      time.Time      `json:"refreshed,omitzero"`
	GuideRefreshed time.Time      `json:"guideRefreshed,omitzero"`
	Errors         []string       `json:"errors,omitempty"`
	Sources        []SourceStatus `json:"sources,omitempty"`
}

// SourceStatus is the last refresh result for one configured provider.
// Playlist errors mean the provider could not supply channels; guide errors
// are optional and do not stop its channels from being usable.
type SourceStatus struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	Channels      int      `json:"channels"`
	Visible       int      `json:"visible"`
	GuideChannels int      `json:"guideChannels"`
	Error         string   `json:"error,omitempty"`
	GuideErrors   []string `json:"guideErrors,omitempty"`
	// Kind and Note describe an addon source: what model it is browsed with,
	// and why it contributes no channels (which is not a failure).
	Kind string `json:"kind,omitempty"`
	Note string `json:"note,omitempty"`
	// Events is how many of the addon's catalog items are selectable Live TV
	// entries after this refresh.
	Events int `json:"events,omitempty"`
	// Warning reports something the viewer should know that is not a failure:
	// an event catalog that couldn't be read just now while its last known
	// entries stay listed, for instance.
	Warning string `json:"warning,omitempty"`
	// Capabilities is the addon's manifest as discovered by this refresh, so
	// the settings page is current even before the next save persists it.
	Capabilities *config.AddonCapabilities `json:"capabilities,omitempty"`
}

// Provider is a configured IPTV source as the viewer's provider picker sees
// it. Channel counts are from the last refresh.
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Addon   bool   `json:"addon,omitempty"`
	Enabled bool   `json:"enabled"`
	// Channels is how many visible channels this source contributes. A Stremio
	// addon can be zero and still offer events, which the picker shows beside
	// the channels.
	Channels int `json:"channels"`
	Events   int `json:"events,omitempty"`
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
	// FFprobe measures live stream quality without starting playback.
	FFprobe string
	// AddonCheckWait bounds the request that proves an addon feed starts, so a
	// dead link can't stall automatic playback. Zero means the default.
	AddonCheckWait time.Duration

	cfg    *config.Config
	dir    string
	client *http.Client

	mu      sync.RWMutex
	all     []*Channel // every channel, in playlist order
	visible []*Channel // numbered, without hidden groups
	byID    map[string]*Channel
	// events are addon catalog items exposed in the same Live TV list. They
	// are not channels: no number, no playlist entry and no guide row.
	events []EventEntry
	guide  Guide
	status Status
	// revision advances after every completed refresh. Callers that cache
	// derived data include it in their key, so a playlist that changed is never
	// answered from a match computed against the old one.
	revision uint64

	kick     chan bool // true: refetch even if fresh
	probeSem chan struct{}

	// addonCache is each configured addon's manifest, briefly reused: one viewer
	// action (open an item, then have automatic playback prove each feed) asks
	// for the same manifest several times, and a provider that counts requests
	// should see one. Only a manifest that was read is stored: a failure is
	// never remembered.
	addonMu    sync.Mutex
	addonCache map[string]*addonManifestEntry

	sessions sessions
}

func New(cfg *config.Config, dataDir string) *Service {
	return &Service{
		cfg:      cfg,
		dir:      filepath.Join(dataDir, "cache", "livetv"),
		client:   &http.Client{Timeout: 5 * time.Minute},
		byID:     map[string]*Channel{},
		guide:    Guide{},
		kick:     make(chan bool, 1),
		probeSem: make(chan struct{}, qualityProbeSlots),
	}
}

// Active: turned on and has something to show: channels, or addon events in
// the same list. An addon with no events and no channels is not visible.
func (s *Service) Active() bool {
	if !s.cfg.LiveTV.Enabled {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.visible) > 0 || len(s.events) > 0 {
		return true
	}
	for _, src := range s.cfg.LiveTV.Sources {
		if src.Enabled && IsAddonKind(src.Kind) && addonHasBrowsableContent(SportSource{ID: src.ID, Capabilities: src.Capabilities}) {
			return true
		}
	}
	return false
}

// Start loads what was cached and keeps playlists and guides fresh.
func (s *Service) Start(ctx context.Context) {
	go s.run(ctx)
	go s.sessions.reap(ctx)
}

// Refresh fetches playlists and guides now (after a settings change, say).
// The status says so immediately: the settings page reloads as soon as the
// save returns, before the background refresh has had time to start.
//
// A forced refresh also drops the cached addon manifests: the admin is asking
// again, so the addons' answers are read again rather than reused.
func (s *Service) Refresh(force bool) {
	if force {
		s.ForgetAddonManifests()
	}
	s.mu.Lock()
	if s.cfg.LiveTV.Enabled && len(s.cfg.LiveTV.Sources) > 0 {
		s.status.Refreshing = true
	}
	s.mu.Unlock()
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
		s.all, s.visible, s.byID, s.events, s.guide = nil, nil, map[string]*Channel{}, nil, Guide{}
		s.status = Status{Prepared: true}
		s.revision++
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
	var events []EventEntry
	sportSources := 0
	addonSources := 0
	// The entries the last refresh listed, by source: a catalog that cannot be
	// read right now (a rate limit, an addon that is briefly down) keeps its
	// provider's last known entries instead of reporting itself as empty.
	previousEvents := map[string][]EventEntry{}
	s.mu.RLock()
	for _, event := range s.events {
		previousEvents[event.Source] = append(previousEvents[event.Source], event)
	}
	s.mu.RUnlock()
	// A guide belongs to the source that named it. Parsing it against only
	// that source's channels is what keeps two providers with the same tvg-id
	// or channel name from borrowing each other's programmes.
	type guideTarget struct {
		url       string
		userAgent string
		sourceID  string
		channels  []*Channel
	}
	var targets []guideTarget
	guideFiles := map[string]bool{}
	statuses := make([]SourceStatus, 0, len(lc.Sources))
	statusIndex := map[string]int{}
	newest := time.Time{}
	for _, src := range lc.Sources {
		statuses = append(statuses, SourceStatus{ID: src.ID, Name: src.Name, Enabled: src.Enabled})
		idx := len(statuses) - 1
		statusIndex[src.ID] = idx
		if !src.Enabled {
			continue
		}
		label := src.Name
		if strings.TrimSpace(label) == "" {
			label = src.ID
		}

		file := filepath.Join(s.dir, "playlist-"+src.ID+".m3u")
		var fetched time.Time
		var err error
		// Addons are capability-driven. Their manifest is read on every
		// refresh so the settings page reflects what the addon offers now,
		// and a valid addon with no television channels is browsed as events
		// or catalog items instead of failing as a channel provider.
		if IsAddonKind(src.Kind) {
			var caps *config.AddonCapabilities
			fetched, caps, err = s.saveStremio(ctx, src, file, playlistEvery, force)
			statuses[idx].Kind = "addon"
			statuses[idx].Capabilities = caps
			if err != nil {
				statuses[idx].Error = err.Error()
				errs = append(errs, fmt.Sprintf("%s: %v", label, err))
				continue
			}
			// Event catalogs go into the same Live TV list as channels. This is
			// independent of whether the addon also publishes TV channels.
			found, problems := s.addonEventEntries(ctx, src, caps)
			for _, problem := range problems {
				// An unreadable event shelf is reported, but it does not make the
				// provider fail: its channels and the other shelves still work.
				errs = append(errs, fmt.Sprintf("%s: optional event catalog: %v", label, problem))
			}
			if len(found) == 0 && addonEventsTemporary(problems) {
				// The addon is refusing or unreachable, not empty: a rate limit or
				// an outage must not read as "this provider has nothing", so the
				// entries it listed last time stay in the list.
				if kept := previousEvents[src.ID]; len(kept) > 0 {
					found = kept
					statuses[idx].Warning = fmt.Sprintf("its event catalog couldn't be read just now, so the last known %d entries are still listed", len(kept))
				}
			}
			if len(found) > 0 {
				events = append(events, found...)
				statuses[idx].Events = len(found)
			}
			if caps != nil && !caps.HasTVChannels() {
				addonSources++
				if caps.SportCatalogs() > 0 {
					sportSources++
				}
				statuses[idx].Note = addonBrowseNote(caps)
				if fetched.After(newest) {
					newest = fetched
				}
				continue
			}
		} else {
			fetched, err = s.fetch(ctx, PlaylistURL(src), file, ua(src.UserAgent), playlistEvery, force)
		}
		if err != nil {
			statuses[idx].Error = err.Error()
			errs = append(errs, fmt.Sprintf("%s: %v", label, err))
		}
		f, openErr := os.Open(file)
		if openErr != nil {
			continue // nothing fetched now or before
		}
		pl, parseErr := ParseM3U(f, src.ID)
		f.Close()
		if parseErr != nil {
			statuses[idx].Error = parseErr.Error()
			errs = append(errs, fmt.Sprintf("%s: %v", label, parseErr))
			continue
		}
		if len(pl.Channels) == 0 {
			statuses[idx].Error = "the playlist has no channels"
			errs = append(errs, label+": the playlist has no channels")
		}
		for _, c := range pl.Channels {
			if c.UserAgent == "" {
				c.UserAgent = ua(src.UserAgent)
			}
			c.stremio = IsAddonKind(src.Kind)
		}
		all = append(all, pl.Channels...)
		statuses[idx].Channels = len(pl.Channels)
		for _, u := range GuideURLs(src, pl) {
			targets = append(targets, guideTarget{url: u, userAgent: ua(src.UserAgent), sourceID: src.ID, channels: pl.Channels})
			guideFiles[u] = true
		}
		if fetched.After(newest) {
			newest = fetched
		}
	}

	// The guide. A failed guide is reported beside its provider and never
	// prevents that provider's channels from being used.
	guide := Guide{}
	var guideTime time.Time
	now := time.Now()
	for _, target := range targets {
		label := target.sourceID
		if idx, ok := statusIndex[target.sourceID]; ok && statuses[idx].Name != "" {
			label = statuses[idx].Name
		}
		file := filepath.Join(s.dir, "guide-"+shortHash(target.url)+".xml")
		fetched, fetchErr := s.fetch(ctx, target.url, file, target.userAgent, guideEvery, force)
		if fetchErr != nil {
			msg := guideFailure(fetchErr)
			if idx, ok := statusIndex[target.sourceID]; ok {
				statuses[idx].GuideErrors = append(statuses[idx].GuideErrors, msg)
			}
			errs = append(errs, label+": "+msg)
		}
		f, openErr := os.Open(file)
		if openErr != nil {
			continue
		}
		sourceGuide := Guide{}
		if parseErr := ParseXMLTV(f, target.channels, now.Add(-guideBehind), now.Add(guideAhead), sourceGuide); parseErr != nil {
			msg := fmt.Sprintf("guide could not be read: %v", parseErr)
			if idx, ok := statusIndex[target.sourceID]; ok {
				statuses[idx].GuideErrors = append(statuses[idx].GuideErrors, msg)
			}
			errs = append(errs, label+": "+msg)
		}
		f.Close()
		for id, programs := range sourceGuide {
			guide[id] = append(guide[id], programs...)
		}
		if fetched.After(guideTime) {
			guideTime = fetched
		}
	}
	guide.Tidy()
	s.mu.Lock()
	s.all = all
	s.events = events
	s.byID = make(map[string]*Channel, len(all))
	for _, c := range all {
		s.byID[c.ID] = c
	}
	s.number()
	visibleCounts := map[string]int{}
	for _, c := range s.visible {
		visibleCounts[c.Source]++
	}
	for i := range statuses {
		statuses[i].Visible = visibleCounts[statuses[i].ID]
		count := 0
		for _, c := range all {
			if c.Source == statuses[i].ID && len(guide[c.ID]) > 0 {
				count++
			}
		}
		statuses[i].GuideChannels = count
	}
	s.guide = guide
	s.status.Channels = len(all)
	s.status.Events = len(events)
	s.status.SportSources = sportSources
	s.status.AddonSources = addonSources
	s.status.Refreshed = newest
	s.status.GuideChannels = len(guide)
	s.status.GuideRefreshed = guideTime
	s.status.Errors = errs
	s.status.Sources = statuses
	s.status.Prepared = true
	s.revision++
	s.mu.Unlock()
	guideURLs := make([]string, 0, len(guideFiles))
	for u := range guideFiles {
		guideURLs = append(guideURLs, u)
	}
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
		return old, redactedError{err}
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
		return old, redactedError{err}
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

// Channels lists every visible channel with what's on now and next.
func (s *Service) Channels() []ChannelView {
	return s.ChannelsFor("")
}

// ChannelsFor lists visible channels from one provider, or every provider
// when source is empty. The source id is kept on every channel, so selecting a
// provider never changes channel identity or playback.
func (s *Service) ChannelsFor(source string) []ChannelView {
	source = strings.TrimSpace(source)
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := time.Now().Unix()
	out := make([]ChannelView, 0, len(s.visible))
	for _, c := range s.visible {
		if source != "" && c.Source != source {
			continue
		}
		v := ChannelView{Channel: *c}
		v.Now, v.Next = s.guide.NowNext(c.ID, t)
		out = append(out, v)
	}
	return out
}

// Providers lists every configured source in configuration order, with the
// visible channel and event counts from the last refresh. Disabled providers
// stay in the list so their identity does not disappear from the picker.
//
// A Stremio addon is not a separate category: it is a provider like any other,
// whether it contributes channels, events, or both.
func (s *Service) Providers() []Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := map[string]int{}
	for _, c := range s.visible {
		counts[c.Source]++
	}
	out := make([]Provider, 0, len(s.cfg.LiveTV.Sources))
	eventCounts := map[string]int{}
	for _, event := range s.events {
		eventCounts[event.Source]++
	}
	// Every configured source is listed, addon or not: the picker is one list
	// and an addon with events but no channels is a normal choice in it.
	for _, src := range s.cfg.LiveTV.Sources {
		out = append(out, Provider{
			ID: src.ID, Name: src.Name, Kind: src.Kind, Addon: IsAddonKind(src.Kind),
			Enabled: src.Enabled, Channels: counts[src.ID], Events: eventCounts[src.ID],
		})
	}
	return out
}

// ProgramHit is one guide programme with its channel identity, for search
// results that must stay playable.
type ProgramHit struct {
	Program
	ChannelID   string `json:"channelId"`
	ChannelName string `json:"channelName"`
}

// SearchPrograms finds programmes matching the predicate in the retained
// guide window, split into live and upcoming. Results are capped and ordered
// by start time so a search is deterministic.
func (s *Service) SearchPrograms(match func(title, description string) bool, limit int) (live, upcoming []ProgramHit) {
	now := time.Now().Unix()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.visible {
		for _, p := range s.guide[c.ID] {
			if !match(p.Title, p.Description) {
				continue
			}
			hit := ProgramHit{Program: p, ChannelID: c.ID, ChannelName: c.Name}
			switch {
			case p.Start <= now && p.End > now:
				if len(live) < limit {
					live = append(live, hit)
				}
			case p.End <= now:
			case len(upcoming) < limit:
				upcoming = append(upcoming, hit)
			}
		}
	}
	sortHits(live)
	sortHits(upcoming)
	return live, upcoming
}

func sortHits(hits []ProgramHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Start != hits[j].Start {
			return hits[i].Start < hits[j].Start
		}
		return hits[i].ChannelName < hits[j].ChannelName
	})
}

// GuideView is a channel's programmes in a window.
type GuideView struct {
	ID       string    `json:"id"`
	Programs []Program `json:"programs"`
}

// Schedule lists each visible channel's programmes from now for [hours].
func (s *Service) Schedule(hours int) []GuideView {
	return s.ScheduleFor(hours, "")
}

// ScheduleFor lists the guide from one provider, or every provider when
// source is empty. Guide rows are keyed by the channel's provider-scoped ID,
// so duplicate channel names cannot mix their programmes.
func (s *Service) ScheduleFor(hours int, source string) []GuideView {
	source = strings.TrimSpace(source)
	s.mu.RLock()
	defer s.mu.RUnlock()
	from := time.Now().Unix()
	to := from + int64(hours)*3600
	out := make([]GuideView, 0, len(s.visible))
	for _, c := range s.visible {
		if source != "" && c.Source != source {
			continue
		}
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

// Revision identifies the current channel and event data. Derived caches use
// it so a refresh invalidates them without waiting for their own TTL.
func (s *Service) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
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
	// Acceptance for an addon is its manifest, not the presence of TV
	// channels: a valid addon with no channel catalogs checks successfully
	// and reports zero channels. The UI shows its capabilities separately.
	if IsAddonKind(src.Kind) {
		caps, err := s.AddonCapabilities(ctx, src)
		if err != nil {
			return 0, nil, err
		}
		// The channel count is best-effort: a catalog that fails to list is a
		// usability detail, not a reason to call a valid manifest invalid.
		if !caps.HasTVChannels() {
			return 0, nil, nil
		}
		chans, err := s.StremioChannels(ctx, src)
		if err != nil {
			return 0, nil, nil
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

// guideFailure explains an optional guide fetch failure in terms the owner can
// act on. A DNS or connection problem is temporary; an HTTP status or a parse
// error needs the guide link corrected. The provider URL and credentials stay
// out of the message either way.
func guideFailure(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return fmt.Sprintf("guide host %q could not be found (DNS: %v); check the guide link and the server's DNS", dnsErr.Name, dnsErr.Err)
		}
		return fmt.Sprintf("guide host %q could not be resolved (DNS: %v)", dnsErr.Name, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Sprintf("guide source timed out: %v", err)
	}
	return fmt.Sprintf("guide unavailable: %v", err)
}

// redactedError keeps errors.As working (DNS and timeout checks) while hiding
// credentials that may appear in a failing URL.
type redactedError struct{ err error }

func (e redactedError) Error() string { return redact(e.err.Error()) }
func (e redactedError) Unwrap() error { return e.err }

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
