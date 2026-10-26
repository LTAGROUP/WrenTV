package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/livetv"
)

// ---------- viewers ----------

// handleLiveChannels is the one Live TV list. Channels and addon events share
// it so a Stremio-backed provider is used through the ordinary interface: the
// events carry the same source id as channels for the provider filter, but no
// channel number and no guide rows are invented for them.
func (s *Server) handleLiveChannels(w http.ResponseWriter, r *http.Request) {
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	providers := s.LiveTV.Providers()
	status := s.LiveTV.Status()
	// The readiness fields let a caller tell an empty list during the first
	// refresh apart from a provider that genuinely has nothing to show.
	out := map[string]any{
		"channels": []any{}, "events": []any{}, "providers": providers,
		"refreshing": status.Refreshing, "prepared": status.Prepared,
		"revision": s.LiveTV.Revision(),
	}
	if s.LiveTV.Active() {
		out["channels"] = s.LiveTV.ChannelsFor(source)
		out["events"] = s.LiveTV.EventsFor(source)
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleLiveGuide(w http.ResponseWriter, r *http.Request) {
	hours := min(max(queryInt(r, "hours", 6), 1), 36)
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	writeJSON(w, 200, map[string]any{"channels": s.LiveTV.ScheduleFor(hours, source)})
}

func (s *Server) handleLivePlay(w http.ResponseWriter, r *http.Request) {
	if !s.LiveTV.Active() {
		writeErr(w, 404, "live TV is off")
		return
	}
	// Apple's players ask for HLS: they can't read a plain TS stream.
	var req struct {
		HLS bool `json:"hls"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	// A player that retunes after an admin stopped it mustn't just start again.
	if msg, stopped := s.act().refused(principal(r).User.ID, "live:"+chi.URLParam(r, "id")); stopped {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	sess, err := s.LiveTV.Play(r.Context(), principal(r).User.ID, chi.URLParam(r, "id"), req.HLS)
	switch {
	case errors.Is(err, livetv.ErrBusy):
		writeErr(w, 429, err.Error())
		return
	case err != nil:
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"url": sess.URL(), "type": sess.Type(), "sessionId": sess.ID})
}

// ---------- admins ----------

const maskedPassword = "••••••"

type liveTVView struct {
	Enabled      bool                  `json:"enabled"`
	Active       bool                  `json:"active"`
	Sources      []config.LiveTVSource `json:"sources"`
	HiddenGroups []string              `json:"hiddenGroups"`
	MaxStreams   int                   `json:"maxStreams"`
	Status       livetv.Status         `json:"status"`
	Playing      int                   `json:"playing"`
}

func (s *Server) liveTVView() liveTVView {
	c := s.Cfg.LiveTV
	v := liveTVView{Enabled: c.Enabled, Active: s.LiveTV.Active(), Sources: []config.LiveTVSource{}, HiddenGroups: nonNil(c.HiddenGroups),
		MaxStreams: c.MaxStreams, Status: s.LiveTV.Status(), Playing: s.LiveTV.Playing()}
	for _, src := range c.Sources {
		if src.Password != "" {
			src.Password = maskedPassword
		}
		v.Sources = append(v.Sources, src)
	}
	return v
}

type liveTVPatch struct {
	Enabled      *bool                  `json:"enabled"`
	Sources      *[]config.LiveTVSource `json:"sources"`
	HiddenGroups *[]string              `json:"hiddenGroups"`
	MaxStreams   *int                   `json:"maxStreams"`
}

// applyLiveTV validates and applies a live TV patch. It reports whether the
// playlists need fetching again, and whether only the groups changed. Addon
// sources have their capabilities discovered here so they are saved with the
// configuration and survive a restart without asking the addon again.
func (s *Server) applyLiveTV(ctx context.Context, p *liveTVPatch) (refetch, regroup bool, err error) {
	c := &s.Cfg.LiveTV
	if p.Enabled != nil && *p.Enabled != c.Enabled {
		c.Enabled = *p.Enabled
		refetch = true
	}
	if p.MaxStreams != nil {
		if *p.MaxStreams < 0 {
			return false, false, errors.New("streams at once can't be negative")
		}
		c.MaxStreams = *p.MaxStreams
	}
	if p.HiddenGroups != nil && !slices.Equal(*p.HiddenGroups, c.HiddenGroups) {
		c.HiddenGroups = *p.HiddenGroups
		regroup = true
	}
	if p.Sources != nil {
		old := map[string]config.LiveTVSource{}
		for _, src := range c.Sources {
			old[src.ID] = src
		}
		var out []config.LiveTVSource
		seenIDs := map[string]bool{}
		for _, src := range *p.Sources {
			src, err := cleanSource(src, old[src.ID])
			if err != nil {
				return false, false, err
			}
			// IDs key the playlist cache and the channel/guide identities. A
			// duplicated id would make two providers share those things, so a
			// duplicate gets a fresh identity instead of being merged.
			for seenIDs[src.ID] {
				src.ID = db.NewID()[:8]
			}
			seenIDs[src.ID] = true
			out = append(out, src)
		}
		if !sourcesEqual(out, c.Sources) {
			refetch = true
		}
		c.Sources = out
	}
	// Discover addon capabilities that are missing (a new addon, a changed
	// manifest link, or one saved before capabilities were stored). This is
	// best-effort: an addon that is offline right now is still saved, and the
	// next refresh reports its error without discarding the admin's input.
	if s.LiveTV != nil {
		for i := range c.Sources {
			if !livetv.IsAddonKind(c.Sources[i].Kind) || c.Sources[i].Capabilities != nil {
				continue
			}
			dctx, cancel := context.WithTimeout(ctx, addonSaveWait)
			caps, err := s.LiveTV.AddonCapabilities(dctx, c.Sources[i])
			cancel()
			if err == nil {
				c.Sources[i].Capabilities = caps
			}
		}
	}
	return refetch, regroup, nil
}

// addonSaveWait bounds the manifest fetch done while saving settings. It is
// short enough that an offline addon doesn't hold the settings page open.
const addonSaveWait = 6 * time.Second

// sourcesEqual compares sources including their discovered capabilities.
func sourcesEqual(a, b []config.LiveTVSource) bool {
	return reflect.DeepEqual(a, b)
}

func cleanSource(src, old config.LiveTVSource) (config.LiveTVSource, error) {
	src.Name = strings.TrimSpace(src.Name)
	src.URL = strings.TrimSpace(src.URL)
	// Capabilities are the server's discovery, never the client's claim: keep
	// the stored ones only while the manifest link and the source kind match.
	// A changed link (or another kind) invalidates them, and they are
	// discovered again when saved.
	if src.URL == strings.TrimSpace(old.URL) && src.Kind == old.Kind {
		src.Capabilities = old.Capabilities
	} else {
		src.Capabilities = nil
	}
	src.Username = strings.TrimSpace(src.Username)
	src.EPGURL = strings.TrimSpace(src.EPGURL)
	src.UserAgent = strings.TrimSpace(src.UserAgent)
	if src.ID == "" {
		src.ID = db.NewID()[:8]
	}
	if src.Password == maskedPassword {
		src.Password = old.Password
	}
	switch src.Kind {
	case "", "m3u":
		src.Kind = "m3u"
		if !strings.HasPrefix(src.URL, "http://") && !strings.HasPrefix(src.URL, "https://") {
			return src, errors.New("a playlist link starts with http:// or https://")
		}
		src.Username, src.Password = "", ""
		src.Capabilities = nil
	case "stremio", "sport":
		// Both names are Stremio addons. The kind is a legacy hint: what the
		// addon can do comes from its manifest, so a manifest with only sport,
		// only other catalogs, or no catalogs is accepted the same way.
		if !strings.HasPrefix(src.URL, "http://") && !strings.HasPrefix(src.URL, "https://") && !strings.HasPrefix(src.URL, "stremio://") {
			return src, errors.New("an addon link starts with https:// or stremio://")
		}
		src.Username, src.Password = "", ""
	case "xtream":
		if src.URL == "" || src.Username == "" || src.Password == "" {
			return src, errors.New("an Xtream Codes login needs the server, a username and a password")
		}
	default:
		return src, errors.New("unknown source type")
	}
	if src.Name == "" {
		src.Name = "IPTV"
	}
	// A guide is optional, but an explicit one must be fetchable rather than a
	// placeholder. An unavailable host is not rejected here: the refresh
	// reports it beside the provider without disabling its channels.
	if src.EPGURL != "" {
		u, err := url.Parse(src.EPGURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return src, errors.New("a guide link starts with http:// or https://")
		}
	}
	return src, nil
}

func (s *Server) handleLiveTVAdmin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"liveTv": s.liveTVView(), "groups": s.LiveTV.Groups()})
}

func (s *Server) handleLiveTVRefresh(w http.ResponseWriter, r *http.Request) {
	s.LiveTV.Refresh(true)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleLiveTVCheck(w http.ResponseWriter, r *http.Request) {
	var src config.LiveTVSource
	if err := decode(r, &src); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var old config.LiveTVSource
	for _, x := range s.Cfg.LiveTV.Sources {
		if x.ID == src.ID {
			old = x
		}
	}
	src, err := cleanSource(src, old)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// An addon is validated against its own manifest, whatever it declares:
	// tv channels, sport events, another catalog type or no catalogs at all.
	// Checking it as an IPTV provider would reject a perfectly usable addon.
	if livetv.IsAddonKind(src.Kind) {
		s.checkAddon(w, r, src)
		return
	}
	n, guides, err := s.LiveTV.Check(r.Context(), src)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"kind": src.Kind, "channels": n, "guide": len(guides) > 0, "guideCount": len(guides)})
}
