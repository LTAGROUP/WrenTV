package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/direct"
	"github.com/LTAGROUP/wrentv/internal/streaming"
)

func (s *Server) handleIntegrationLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.DB.ListLibraries(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, nonNil(libs))
}

// ---------- status & settings ----------

func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	type sess struct {
		ID      string   `json:"id"`
		UserID  string   `json:"userId"`
		ItemID  string   `json:"itemId"`
		Engine  string   `json:"engine"`
		Method  string   `json:"method"`
		Reasons []string `json:"reasons"`
		Started int64    `json:"started"`
	}
	sessions := []sess{}
	for _, x := range s.Streams.List() {
		sessions = append(sessions, sess{x.ID, x.UserID, x.ItemID, x.Decision.Engine, string(x.Decision.Method), x.Decision.Reasons, x.Started.Unix()})
	}
	writeJSON(w, 200, map[string]any{
		"version":   s.Version,
		"hardware":  s.Streams.HW(),
		"sessions":  sessions,
		"streaming": s.streamingStatus(),
		"metadata":  s.TMDB.Enabled(),
		"ratings":   s.OMDb.Enabled(),
	})
}

// settingsView is the subset of configuration editable from apps. Secrets are
// write-only: they are reported as set/unset, never echoed back.
type settingsView struct {
	ServerName      string                  `json:"serverName"`
	PublicURL       string                  `json:"publicUrl"`
	HWAccel         string                  `json:"hwaccel"`
	TMDBKeySet      bool                    `json:"tmdbKeySet"`
	OMDbKeySet      bool                    `json:"omdbKeySet"`
	Fingerprint     bool                    `json:"markerFingerprinting"`
	Streaming       streamingView           `json:"streaming"`
	UserDefaults    config.UserLimitsConfig `json:"userDefaults"`
	Webhooks        []config.WebhookConfig  `json:"webhooks"`
	IntegrationKey  string                  `json:"integrationKey"`
	SimklClientID   string                  `json:"simklClientId"`
	RemoteAccess    *remoteAccessView       `json:"remoteAccess,omitempty"`
	LiveTV          liveTVView              `json:"liveTv"`
	RestartRequired bool                    `json:"restartRequired,omitempty"`
}

type streamingView struct {
	Enabled bool `json:"enabled"`
	// Active: enabled with a debrid key and TMDB, so titles can be played.
	Active          bool                   `json:"active"`
	Debrid          []debridView           `json:"debrid"`
	Addons          []config.AddonConfig   `json:"addons"`
	Qualities       []string               `json:"qualities"`
	PerQuality      int                    `json:"perQuality"`
	Usenet          bool                   `json:"usenet"`
	MaxSizeGB       map[string]float64     `json:"maxSizeGB"`
	Prefetch        bool                   `json:"prefetch"`
	Mode            string                 `json:"mode"`
	MovieLibrary    string                 `json:"movieLibrary"`
	ShowLibrary     string                 `json:"showLibrary"`
	DownloadQuality string                 `json:"downloadQuality"`
	Limits          config.StreamingLimits `json:"limits"`
}

// debridView is one debrid service, in the order they're tried.
type debridView struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	KeySet   bool   `json:"keySet"`
	// KeyHint is the key's last characters, so owners can tell keys apart.
	KeyHint string `json:"keyHint,omitempty"`
}

var debridNames = map[string]string{"torbox": "TorBox", "alldebrid": "AllDebrid", "realdebrid": "Real-Debrid"}

func (s *Server) debridKeys() map[string]*string {
	c := &s.Cfg.Streaming
	return map[string]*string{"torbox": &c.TorBoxKey, "alldebrid": &c.AllDebridKey, "realdebrid": &c.RealDebridKey}
}

// providerOrder is the configured order with any missing services appended.
func providerOrder(order []string) []string {
	out := []string{}
	for _, p := range append(slices.Clone(order), "torbox", "alldebrid", "realdebrid") {
		if debridNames[p] != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) settings() settingsView {
	c := s.Cfg
	hooks := make([]config.WebhookConfig, len(c.Webhooks))
	for i, h := range c.Webhooks {
		hooks[i] = config.WebhookConfig{URL: h.URL, Events: h.Events}
		if h.Secret != "" {
			hooks[i].Secret = "••••••"
		}
	}
	st := c.Streaming
	sv := streamingView{Enabled: st.Enabled, Active: s.Streaming.Active(), Addons: nonNil(st.Addons), Qualities: nonNil(st.Qualities),
		PerQuality: max(st.PerQuality, 1), Usenet: st.Usenet, MaxSizeGB: st.MaxSizeGB, Prefetch: st.Prefetch, Mode: st.Mode, MovieLibrary: st.MovieLibrary, ShowLibrary: st.ShowLibrary,
		DownloadQuality: st.DownloadQuality, Limits: st.Limits, Debrid: []debridView{}}
	if sv.MaxSizeGB == nil {
		sv.MaxSizeGB = map[string]float64{}
	}
	keys := s.debridKeys()
	for _, p := range providerOrder(st.ProviderOrder) {
		k := *keys[p]
		v := debridView{Provider: p, Name: debridNames[p], KeySet: k != ""}
		if len(k) > 8 {
			v.KeyHint = "…" + k[len(k)-4:]
		}
		sv.Debrid = append(sv.Debrid, v)
	}
	return settingsView{
		ServerName: c.ServerName, PublicURL: c.PublicURL, HWAccel: c.Transcode.HWAccel, TMDBKeySet: c.Metadata.TMDBAPIKey != "",
		OMDbKeySet: c.Metadata.OMDbAPIKey != "", Fingerprint: c.Markers.Fingerprint, Streaming: sv, UserDefaults: c.Users,
		Webhooks: hooks, IntegrationKey: c.IntegrationKey, SimklClientID: c.Simkl.ClientID, RemoteAccess: s.remoteAccess(), LiveTV: s.liveTVView(),
	}
}

func (s *Server) streamingStatus() map[string]any {
	names := []string{}
	for _, p := range s.Streaming.Providers() {
		names = append(names, p.Name())
	}
	return map[string]any{"active": s.Streaming.Active(), "providers": names, "activeStreams": s.Streaming.ActiveStreams()}
}

func (s *Server) remoteAccess() *remoteAccessView {
	if s.Direct == nil {
		return nil
	}
	rc := s.Cfg.RemoteAccess
	return &remoteAccessView{Enabled: rc.Enabled, UPnP: rc.UPnP, PublicPort: rc.PublicPort, Status: s.Direct.Status()}
}

type remoteAccessView struct {
	Enabled    bool          `json:"enabled"`
	UPnP       bool          `json:"upnp"`
	PublicPort int           `json:"publicPort"`
	Status     direct.Status `json:"status"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.settings())
}

type streamingPatch struct {
	Enabled         *bool                   `json:"enabled"`
	TorBoxKey       *string                 `json:"torboxKey"`
	AllDebridKey    *string                 `json:"alldebridKey"`
	RealDebridKey   *string                 `json:"realdebridKey"`
	ProviderOrder   *[]string               `json:"providerOrder"`
	Addons          *[]config.AddonConfig   `json:"addons"`
	Qualities       *[]string               `json:"qualities"`
	PerQuality      *int                    `json:"perQuality"`
	Usenet          *bool                   `json:"usenet"`
	MaxSizeGB       *map[string]float64     `json:"maxSizeGB"`
	Prefetch        *bool                   `json:"prefetch"`
	Mode            *string                 `json:"mode"`
	MovieLibrary    *string                 `json:"movieLibrary"`
	ShowLibrary     *string                 `json:"showLibrary"`
	DownloadQuality *string                 `json:"downloadQuality"`
	Limits          *config.StreamingLimits `json:"limits"`
}

// apply validates and applies a streaming patch.
func (s *Server) applyStreaming(r *http.Request, p *streamingPatch) error {
	c := &s.Cfg.Streaming
	if p.Enabled != nil {
		c.Enabled = *p.Enabled
	}
	for name, v := range map[string]*string{"torbox": p.TorBoxKey, "alldebrid": p.AllDebridKey, "realdebrid": p.RealDebridKey} {
		if v != nil {
			*s.debridKeys()[name] = strings.TrimSpace(*v)
		}
	}
	if p.ProviderOrder != nil {
		c.ProviderOrder = providerOrder(*p.ProviderOrder)
	}
	if p.Addons != nil {
		var addons []config.AddonConfig
		for _, a := range *p.Addons {
			ad, err := streaming.ParseAddon(a.Name, a.URL)
			if err != nil {
				return err
			}
			name := strings.TrimSpace(a.Name)
			if name == "" {
				name = ad.Name
			}
			addons = append(addons, config.AddonConfig{Name: name, URL: ad.BaseURL + "/manifest.json", Enabled: a.Enabled})
		}
		c.Addons = addons
	}
	if p.Qualities != nil {
		var qs []string
		for _, q := range *p.Qualities {
			if (q == "2160p" || q == "1080p" || q == "720p") && !slices.Contains(qs, q) {
				qs = append(qs, q)
			}
		}
		if len(qs) == 0 {
			return fmt.Errorf("pick at least one quality")
		}
		c.Qualities = qs
	}
	if p.MaxSizeGB != nil {
		for q, v := range *p.MaxSizeGB {
			if v < 0 {
				return fmt.Errorf("size limits can't be negative")
			}
			if c.MaxSizeGB == nil {
				c.MaxSizeGB = map[string]float64{}
			}
			c.MaxSizeGB[q] = v
		}
	}
	if p.PerQuality != nil {
		if *p.PerQuality < 1 || *p.PerQuality > 5 {
			return fmt.Errorf("keep between 1 and 5 streams per quality")
		}
		c.PerQuality = *p.PerQuality
	}
	if p.Usenet != nil {
		c.Usenet = *p.Usenet
	}
	if p.Prefetch != nil {
		c.Prefetch = *p.Prefetch
	}
	if p.Mode != nil {
		if *p.Mode != "stream" && *p.Mode != "download" {
			return fmt.Errorf("mode must be stream or download")
		}
		c.Mode = *p.Mode
	}
	for _, lib := range []struct {
		v   *string
		dst *string
		typ string
	}{{p.MovieLibrary, &c.MovieLibrary, "movies"}, {p.ShowLibrary, &c.ShowLibrary, "shows"}} {
		if lib.v == nil {
			continue
		}
		if *lib.v != "" {
			l, err := s.DB.GetLibrary(r.Context(), *lib.v)
			if err != nil || l.Source != "files" || l.Type != lib.typ || len(l.Paths) == 0 {
				return fmt.Errorf("downloads need a %s library with a folder", lib.typ)
			}
		}
		*lib.dst = *lib.v
	}
	if p.DownloadQuality != nil {
		c.DownloadQuality = *p.DownloadQuality
	}
	if p.Limits != nil {
		l := *p.Limits
		if l.MaxStreams < 0 || l.MaxDownloads < 0 || l.PrefetchWorkers < 0 || l.BandwidthMbps < 0 || l.PerStreamMbps < 0 ||
			l.DownloadMbps < 0 || l.DownloadQuotaGB < 0 || l.MinFreeGB < 0 {
			return fmt.Errorf("limits can't be negative")
		}
		c.Limits = l
	}
	if c.Mode == "download" && (c.MovieLibrary == "" || c.ShowLibrary == "") {
		return fmt.Errorf("download mode needs a movie library and a show library")
	}
	return nil
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerName   *string                  `json:"serverName"`
		PublicURL    *string                  `json:"publicUrl"`
		HWAccel      *string                  `json:"hwaccel"`
		TMDBAPIKey   *string                  `json:"tmdbApiKey"`
		OMDbAPIKey   *string                  `json:"omdbApiKey"`
		Fingerprint  *bool                    `json:"markerFingerprinting"`
		Streaming    *streamingPatch          `json:"streaming"`
		LiveTV       *liveTVPatch             `json:"liveTv"`
		UserDefaults *config.UserLimitsConfig `json:"userDefaults"`
		Webhooks     *[]config.WebhookConfig  `json:"webhooks"`
		SimklID      *string                  `json:"simklClientId"`
		RemoteAccess *struct {
			Enabled    *bool `json:"enabled"`
			UPnP       *bool `json:"upnp"`
			PublicPort *int  `json:"publicPort"`
		} `json:"remoteAccess"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	c := s.Cfg
	restart := false
	if req.ServerName != nil && strings.TrimSpace(*req.ServerName) != "" {
		c.ServerName = strings.TrimSpace(*req.ServerName)
	}
	if req.PublicURL != nil {
		c.PublicURL = strings.TrimSpace(*req.PublicURL)
	}
	if req.HWAccel != nil {
		c.Transcode.HWAccel = *req.HWAccel
		restart = true
	}
	if req.TMDBAPIKey != nil {
		c.Metadata.TMDBAPIKey = strings.TrimSpace(*req.TMDBAPIKey)
		restart = true
	}
	if req.OMDbAPIKey != nil {
		c.Metadata.OMDbAPIKey = strings.TrimSpace(*req.OMDbAPIKey) // read per request: no restart
	}
	if req.Fingerprint != nil {
		c.Markers.Fingerprint = *req.Fingerprint
		if c.Markers.Fingerprint && s.Markers != nil {
			s.Markers.Kick()
		}
	}
	if req.Streaming != nil {
		if err := s.applyStreaming(r, req.Streaming); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if s.Streaming.Active() {
			_, _ = s.Streaming.Libraries(r.Context())
		}
	}
	var liveRefetch, liveRegroup bool
	if req.LiveTV != nil {
		var err error
		if liveRefetch, liveRegroup, err = s.applyLiveTV(req.LiveTV); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if u := req.UserDefaults; u != nil {
		if u.MaxStreams < 0 || u.MaxDownloads < 0 {
			writeErr(w, 400, "limits can't be negative")
			return
		}
		c.Users = *u
	}
	if req.Webhooks != nil {
		// Keep existing secrets when the client sends the masked placeholder.
		old := map[string]string{}
		for _, h := range c.Webhooks {
			old[h.URL] = h.Secret
		}
		hooks := *req.Webhooks
		for i := range hooks {
			if hooks[i].Secret == "••••••" {
				hooks[i].Secret = old[hooks[i].URL]
			}
		}
		c.Webhooks = hooks
	}
	if req.SimklID != nil {
		c.Simkl.ClientID = strings.TrimSpace(*req.SimklID)
	}
	if ra := req.RemoteAccess; ra != nil {
		if ra.Enabled != nil {
			c.RemoteAccess.Enabled = *ra.Enabled
		}
		if ra.UPnP != nil {
			c.RemoteAccess.UPnP = *ra.UPnP
		}
		if ra.PublicPort != nil {
			if *ra.PublicPort < 0 || *ra.PublicPort > 65535 {
				writeErr(w, 400, "public port must be between 1 and 65535 (0 for automatic)")
				return
			}
			c.RemoteAccess.PublicPort = *ra.PublicPort
		}
		if s.Direct != nil {
			s.Direct.Refresh()
		}
	}
	if err := c.Save(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if liveRefetch {
		s.LiveTV.Refresh(true)
	} else if liveRegroup {
		s.LiveTV.Regroup()
	}
	v := s.settings()
	v.RestartRequired = restart
	go s.heartbeat()
	writeJSON(w, 200, v)
}

// ---------- background ----------

// heartbeat publishes this server's addresses and libraries to Wren TV accounts.
func (s *Server) heartbeat() {
	if s.Supa == nil || !s.Supa.Claimed() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	libs, _ := s.DB.ListLibraries(ctx)
	if libs == nil {
		libs = []*db.Library{}
	}
	remote := ""
	if s.Direct != nil {
		remote = s.Direct.Status().RemoteURL
	}
	if err := s.Supa.Heartbeat(ctx, s.advertisedLocalURLs(), remote, s.Version, libs); err != nil {
		slog.Warn("heartbeat failed", "err", err)
	}
}

// HeartbeatNow publishes immediately (e.g. when the server's addresses change).
func (s *Server) HeartbeatNow() { s.heartbeat() }

// RunHeartbeat keeps the account service informed while the server runs.
func (s *Server) RunHeartbeat(ctx context.Context) {
	s.heartbeat()
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.heartbeat()
		}
	}
}
