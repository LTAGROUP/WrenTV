package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/livetv"
)

// ---------- viewers ----------

func (s *Server) handleLiveChannels(w http.ResponseWriter, r *http.Request) {
	if !s.LiveTV.Active() {
		writeJSON(w, 200, map[string]any{"channels": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"channels": s.LiveTV.Channels()})
}

func (s *Server) handleLiveGuide(w http.ResponseWriter, r *http.Request) {
	hours := min(max(queryInt(r, "hours", 6), 1), 36)
	writeJSON(w, 200, map[string]any{"channels": s.LiveTV.Schedule(hours)})
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
// playlists need fetching again, and whether only the groups changed.
func (s *Server) applyLiveTV(p *liveTVPatch) (refetch, regroup bool, err error) {
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
		for _, src := range *p.Sources {
			src, err := cleanSource(src, old[src.ID])
			if err != nil {
				return false, false, err
			}
			out = append(out, src)
		}
		if !slices.Equal(out, c.Sources) {
			refetch = true
		}
		c.Sources = out
	}
	return refetch, regroup, nil
}

func cleanSource(src, old config.LiveTVSource) (config.LiveTVSource, error) {
	src.Name = strings.TrimSpace(src.Name)
	src.URL = strings.TrimSpace(src.URL)
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
	case "stremio":
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
	n, guides, err := s.LiveTV.Check(r.Context(), src)
	if err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"channels": n, "guide": len(guides) > 0})
}
