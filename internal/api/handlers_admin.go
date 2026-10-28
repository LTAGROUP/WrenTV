package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/direct"
	"github.com/LTAGROUP/wrentv/internal/streaming"
	"github.com/LTAGROUP/wrentv/internal/usenet"
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
	Community       bool                    `json:"markerCommunity"`
	Subtitles       subtitlesView           `json:"subtitles"`
	Streaming       streamingView           `json:"streaming"`
	UserDefaults    config.UserLimitsConfig `json:"userDefaults"`
	Webhooks        []config.WebhookConfig  `json:"webhooks"`
	IntegrationKey  string                  `json:"integrationKey"`
	SimklClientID   string                  `json:"simklClientId"`
	TraktClientID   string                  `json:"traktClientId"`
	TraktSecret     string                  `json:"traktClientSecret"`
	RemoteAccess    *remoteAccessView       `json:"remoteAccess,omitempty"`
	LiveTV          liveTVView              `json:"liveTv"`
	RestartRequired bool                    `json:"restartRequired,omitempty"`
}

type subtitlesView struct {
	OpenSubtitlesKeySet      bool   `json:"openSubtitlesKeySet"`
	OpenSubtitlesUsername    string `json:"openSubtitlesUsername"`
	OpenSubtitlesPasswordSet bool   `json:"openSubtitlesPasswordSet"`
	SubDLKeySet              bool   `json:"subdlKeySet"`
}

type subtitlesPatch struct {
	OpenSubtitlesAPIKey   *string `json:"openSubtitlesApiKey"`
	OpenSubtitlesUsername *string `json:"openSubtitlesUsername"`
	OpenSubtitlesPassword *string `json:"openSubtitlesPassword"`
	SubDLAPIKey           *string `json:"subdlApiKey"`
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
	LookupOnOpen    bool                   `json:"lookupOnOpen"`
	CheckAddonLinks bool                   `json:"checkAddonLinks"`
	LinkTTLMinutes  map[string]int         `json:"linkTTLMinutes"`
	Mode            string                 `json:"mode"`
	MovieLibrary    string                 `json:"movieLibrary"`
	ShowLibrary     string                 `json:"showLibrary"`
	DownloadQuality string                 `json:"downloadQuality"`
	Limits          config.StreamingLimits `json:"limits"`
	Indexers        []indexerView          `json:"indexers"`
	NewsServers     []newsServerView       `json:"newsServers"`
	UsenetCacheMB   int                    `json:"usenetCacheMB"`
	UsenetDiskGB    float64                `json:"usenetDiskCacheGB"`
	ExcludeWords    []string               `json:"excludeWords"`
}

// indexerView is a Newznab indexer without its API key.
type indexerView struct {
	config.IndexerConfig
	KeySet  bool   `json:"keySet"`
	KeyHint string `json:"keyHint,omitempty"`
}

// newsServerView is a news server account without its password.
type newsServerView struct {
	config.NewsServerConfig
	PasswordSet bool `json:"passwordSet"`
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
		PerQuality: max(st.PerQuality, 1), Usenet: st.Usenet, MaxSizeGB: st.MaxSizeGB, Prefetch: st.Prefetch, LookupOnOpen: st.LookupOnOpen, CheckAddonLinks: st.CheckAddonLinks, LinkTTLMinutes: nonNilMap(st.LinkTTLMinutes), Mode: st.Mode, MovieLibrary: st.MovieLibrary, ShowLibrary: st.ShowLibrary,
		DownloadQuality: st.DownloadQuality, Limits: st.Limits, Debrid: []debridView{},
		UsenetCacheMB: st.UsenetCacheMB, UsenetDiskGB: st.UsenetDiskCacheGB, ExcludeWords: nonNil(st.ExcludeWords)}
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
	sv.Indexers, sv.NewsServers = []indexerView{}, []newsServerView{}
	for _, ix := range st.Indexers {
		v := indexerView{IndexerConfig: ix, KeySet: ix.APIKey != ""}
		if len(ix.APIKey) > 8 {
			v.KeyHint = "…" + ix.APIKey[len(ix.APIKey)-4:]
		}
		sv.Indexers = append(sv.Indexers, v)
	}
	for _, ns := range st.NewsServers {
		sv.NewsServers = append(sv.NewsServers, newsServerView{NewsServerConfig: ns, PasswordSet: ns.Password != ""})
	}
	return settingsView{
		ServerName: c.ServerName, PublicURL: c.PublicURL, HWAccel: c.Transcode.HWAccel, TMDBKeySet: c.Metadata.TMDBAPIKey != "",
		OMDbKeySet: c.Metadata.OMDbAPIKey != "", Fingerprint: c.Markers.Fingerprint, Community: c.Markers.Community, Streaming: sv,
		Subtitles: subtitlesView{OpenSubtitlesKeySet: c.Subtitles.OpenSubtitles.APIKey != "", OpenSubtitlesUsername: c.Subtitles.OpenSubtitles.Username,
			OpenSubtitlesPasswordSet: c.Subtitles.OpenSubtitles.Password != "", SubDLKeySet: c.Subtitles.SubDL.APIKey != ""}, UserDefaults: c.Users,
		Webhooks: hooks, IntegrationKey: c.IntegrationKey, SimklClientID: c.Simkl.ClientID,
		TraktClientID: c.Trakt.ClientID, TraktSecret: maskSecret(c.Trakt.ClientSecret), RemoteAccess: s.remoteAccess(), LiveTV: s.liveTVView(),
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
	LookupOnOpen    *bool                   `json:"lookupOnOpen"`
	CheckAddonLinks *bool                   `json:"checkAddonLinks"`
	LinkTTLMinutes  *map[string]int         `json:"linkTTLMinutes"`
	Mode            *string                 `json:"mode"`
	MovieLibrary    *string                 `json:"movieLibrary"`
	ShowLibrary     *string                 `json:"showLibrary"`
	DownloadQuality *string                 `json:"downloadQuality"`
	Limits          *config.StreamingLimits `json:"limits"`
	Indexers        *[]indexerPatch         `json:"indexers"`
	NewsServers     *[]newsServerPatch      `json:"newsServers"`
	UsenetCacheMB   *int                    `json:"usenetCacheMB"`
	UsenetDiskGB    *float64                `json:"usenetDiskCacheGB"`
	ExcludeWords    *[]string               `json:"excludeWords"`
}

// indexerPatch and newsServerPatch leave the saved key or password alone
// when it's sent empty.
type indexerPatch struct {
	config.IndexerConfig
	APIKey string `json:"apiKey"`
}

type newsServerPatch struct {
	config.NewsServerConfig
	Password string `json:"password"`
}

// usenetFromPatch turns the settings page's indexers and servers into
// config, keeping saved secrets that weren't retyped.
func usenetFromPatch(c *config.StreamingConfig, ixs *[]indexerPatch, nss *[]newsServerPatch) ([]config.IndexerConfig, []config.NewsServerConfig, error) {
	indexers, servers := c.Indexers, c.NewsServers
	if ixs != nil {
		indexers = nil
		for _, p := range *ixs {
			ix := p.IndexerConfig
			u, err := usenet.Endpoint(ix.URL)
			if err != nil {
				return nil, nil, err
			}
			ix.URL, ix.Name, ix.APIKey = u, strings.TrimSpace(ix.Name), strings.TrimSpace(p.APIKey)
			if ix.Name == "" {
				if pu, err := url.Parse(u); err == nil {
					ix.Name = strings.TrimPrefix(strings.TrimPrefix(pu.Hostname(), "api."), "www.")
				}
			}
			if ix.APIKey == "" {
				// Keep the saved key: the same address, else the same site
				// (an edited address), else the same name.
				for _, same := range []func(config.IndexerConfig) bool{
					func(old config.IndexerConfig) bool { return old.URL == ix.URL },
					func(old config.IndexerConfig) bool { return (usenet.Indexer{URL: old.URL}).Owns(ix.URL) },
					func(old config.IndexerConfig) bool { return strings.EqualFold(old.Name, ix.Name) },
				} {
					for _, old := range c.Indexers {
						if ix.APIKey == "" && old.APIKey != "" && same(old) {
							ix.APIKey = old.APIKey
						}
					}
				}
			}
			indexers = append(indexers, ix)
		}
	}
	if nss != nil {
		servers = nil
		for _, p := range *nss {
			ns := p.NewsServerConfig
			ns.Host, ns.Username, ns.Password = strings.TrimSpace(ns.Host), strings.TrimSpace(ns.Username), p.Password
			if ns.Host == "" {
				return nil, nil, fmt.Errorf("enter the news server's address")
			}
			if ns.Port < 0 || ns.Port > 65535 {
				return nil, nil, fmt.Errorf("news server port must be between 1 and 65535")
			}
			if ns.Connections < 1 || ns.Connections > 100 {
				return nil, nil, fmt.Errorf("news server connections must be between 1 and 100")
			}
			if ns.Password == "" {
				for _, old := range c.NewsServers {
					if old.Host == ns.Host && old.Username == ns.Username {
						ns.Password = old.Password
					}
				}
			}
			servers = append(servers, ns)
		}
	}
	return indexers, servers, nil
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
	if p.Indexers != nil || p.NewsServers != nil {
		ixs, nss, err := usenetFromPatch(c, p.Indexers, p.NewsServers)
		if err != nil {
			return err
		}
		c.Indexers, c.NewsServers = ixs, nss
	}
	if p.UsenetCacheMB != nil {
		if *p.UsenetCacheMB < 0 || *p.UsenetCacheMB > 16384 {
			return fmt.Errorf("keep the article memory cache between 0 and 16384 MB")
		}
		c.UsenetCacheMB = *p.UsenetCacheMB
	}
	if p.UsenetDiskGB != nil {
		if *p.UsenetDiskGB < 0 {
			return fmt.Errorf("the article disk cache can't be negative")
		}
		c.UsenetDiskCacheGB = *p.UsenetDiskGB
	}
	if p.ExcludeWords != nil {
		var ws []string
		for _, w := range *p.ExcludeWords {
			if w = strings.TrimSpace(w); w != "" {
				if _, err := streaming.ExcludeMatcher([]string{w}); err != nil {
					return err
				}
				ws = append(ws, w)
			}
		}
		c.ExcludeWords = ws
	}
	if p.Prefetch != nil {
		c.Prefetch = *p.Prefetch
	}
	if p.LookupOnOpen != nil {
		c.LookupOnOpen = *p.LookupOnOpen
	}
	if p.CheckAddonLinks != nil {
		c.CheckAddonLinks = *p.CheckAddonLinks
	}
	if p.LinkTTLMinutes != nil {
		ttl := map[string]int{}
		for k, v := range *p.LinkTTLMinutes {
			if v < 0 || v > 7*24*60 {
				return fmt.Errorf("a link lifetime must be between 0 and a week")
			}
			if v > 0 {
				ttl[k] = v
			}
		}
		c.LinkTTLMinutes = ttl
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
		Community    *bool                    `json:"markerCommunity"`
		Subtitles    *subtitlesPatch          `json:"subtitles"`
		Streaming    *streamingPatch          `json:"streaming"`
		LiveTV       *liveTVPatch             `json:"liveTv"`
		UserDefaults *config.UserLimitsConfig `json:"userDefaults"`
		Webhooks     *[]config.WebhookConfig  `json:"webhooks"`
		SimklID      *string                  `json:"simklClientId"`
		TraktID      *string                  `json:"traktClientId"`
		TraktSecret  *string                  `json:"traktClientSecret"`
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
	if req.Community != nil {
		c.Markers.Community = *req.Community
	}
	if p := req.Subtitles; p != nil {
		oc := &c.Subtitles.OpenSubtitles
		for _, f := range []struct {
			from *string
			to   *string
		}{{p.OpenSubtitlesAPIKey, &oc.APIKey}, {p.OpenSubtitlesUsername, &oc.Username}, {p.SubDLAPIKey, &c.Subtitles.SubDL.APIKey}} {
			if f.from != nil {
				*f.to = strings.TrimSpace(*f.from)
			}
		}
		if p.OpenSubtitlesPassword != nil {
			oc.Password = *p.OpenSubtitlesPassword
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
	if req.TraktID != nil {
		c.Trakt.ClientID = strings.TrimSpace(*req.TraktID)
	}
	if req.TraktSecret != nil && *req.TraktSecret != maskedPassword {
		c.Trakt.ClientSecret = strings.TrimSpace(*req.TraktSecret)
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

// maskSecret hides a saved secret from the settings page.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return maskedPassword
}
