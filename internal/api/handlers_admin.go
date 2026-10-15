package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/direct"
	"github.com/LTAGROUP/wrentv/internal/torrent"
)

// ---------- torrents ----------

func (s *Server) handleListTorrents(w http.ResponseWriter, r *http.Request) {
	ts, err := s.DB.ListTorrents(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, nonNil(ts))
}

func (s *Server) handleGetTorrent(w http.ResponseWriter, r *http.Request) {
	t, err := s.DB.GetTorrent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "torrent not found")
		return
	}
	files, _ := s.DB.FilesForTorrent(r.Context(), t.ID)
	type fileRow struct {
		ItemID string `json:"itemId"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
	}
	rows := []fileRow{}
	for _, f := range files {
		rows = append(rows, fileRow{f.ItemID, displayFileName(f.Path), f.Size})
	}
	writeJSON(w, 200, map[string]any{"torrent": t, "files": rows})
}

// handleAddTorrent accepts JSON {libraryId, magnet, externalRef} or a multipart
// form with fields libraryId and file (.torrent).
func (s *Server) handleAddTorrent(w http.ResponseWriter, r *http.Request) {
	req, err := readTorrentInput(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	req.AddedBy = principal(r).User.ID
	t, err := s.Torrents.Add(r.Context(), req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 202, t)
}

func (s *Server) handleReplaceTorrent(w http.ResponseWriter, r *http.Request) {
	req, err := readTorrentInput(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	t, err := s.Torrents.Replace(r.Context(), chi.URLParam(r, "id"), req.Magnet, req.TorrentFile)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 202, t)
}

func (s *Server) handleDeleteTorrent(w http.ResponseWriter, r *http.Request) {
	if err := s.Torrents.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readTorrentInput(r *http.Request) (torrent.AddRequest, error) {
	var req torrent.AddRequest
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			return req, err
		}
		req.LibraryID = r.FormValue("libraryId")
		req.Magnet = r.FormValue("magnet")
		req.ExternalRef = r.FormValue("externalRef")
		if f, _, err := r.FormFile("file"); err == nil {
			defer f.Close()
			b, err := io.ReadAll(io.LimitReader(f, 8<<20))
			if err != nil {
				return req, err
			}
			req.TorrentFile = b
		}
		return req, nil
	}
	var body struct {
		LibraryID   string `json:"libraryId"`
		Magnet      string `json:"magnet"`
		ExternalRef string `json:"externalRef"`
	}
	if err := decode(r, &body); err != nil {
		return req, err
	}
	req.LibraryID, req.Magnet, req.ExternalRef = body.LibraryID, body.Magnet, body.ExternalRef
	return req, nil
}

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
		"version":          s.Version,
		"hardware":         s.Streams.HW(),
		"sessions":         sessions,
		"torrentProviders": s.Torrents.Providers(),
		"metadata":         s.TMDB.Enabled(),
		"ratings":          s.OMDb.Enabled(),
	})
}

// settingsView is the subset of configuration editable from apps. Secrets are
// write-only: they are reported as set/unset, never echoed back.
type settingsView struct {
	ServerName      string                 `json:"serverName"`
	PublicURL       string                 `json:"publicUrl"`
	HWAccel         string                 `json:"hwaccel"`
	TMDBKeySet      bool                   `json:"tmdbKeySet"`
	OMDbKeySet      bool                   `json:"omdbKeySet"`
	Fingerprint     bool                   `json:"markerFingerprinting"`
	TorrentsEnabled bool                   `json:"torrentsEnabled"`
	Providers       []string               `json:"torrentProviders"`
	DebridService   string                 `json:"debridService"`
	DebridKeySet    bool                   `json:"debridKeySet"`
	Webhooks        []config.WebhookConfig `json:"webhooks"`
	IntegrationKey  string                 `json:"integrationKey"`
	RemoteAccess    *remoteAccessView      `json:"remoteAccess,omitempty"`
	RestartRequired bool                   `json:"restartRequired,omitempty"`
}

type remoteAccessView struct {
	Enabled    bool          `json:"enabled"`
	UPnP       bool          `json:"upnp"`
	PublicPort int           `json:"publicPort"`
	Status     direct.Status `json:"status"`
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
	return settingsView{
		ServerName: c.ServerName, PublicURL: c.PublicURL, HWAccel: c.Transcode.HWAccel, TMDBKeySet: c.Metadata.TMDBAPIKey != "",
		OMDbKeySet: c.Metadata.OMDbAPIKey != "", Fingerprint: c.Markers.Fingerprint,
		TorrentsEnabled: c.Torrents.Enabled, Providers: c.Torrents.Providers, DebridService: c.Torrents.Debrid.Service,
		DebridKeySet: c.Torrents.Debrid.APIKey != "", Webhooks: hooks, IntegrationKey: c.IntegrationKey,
		RemoteAccess: s.remoteAccess(),
	}
}

func (s *Server) remoteAccess() *remoteAccessView {
	if s.Direct == nil {
		return nil
	}
	rc := s.Cfg.RemoteAccess
	return &remoteAccessView{Enabled: rc.Enabled, UPnP: rc.UPnP, PublicPort: rc.PublicPort, Status: s.Direct.Status()}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.settings())
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerName      *string                 `json:"serverName"`
		PublicURL       *string                 `json:"publicUrl"`
		HWAccel         *string                 `json:"hwaccel"`
		TMDBAPIKey      *string                 `json:"tmdbApiKey"`
		OMDbAPIKey      *string                 `json:"omdbApiKey"`
		Fingerprint     *bool                   `json:"markerFingerprinting"`
		TorrentsEnabled *bool                   `json:"torrentsEnabled"`
		Providers       *[]string               `json:"torrentProviders"`
		DebridService   *string                 `json:"debridService"`
		DebridAPIKey    *string                 `json:"debridApiKey"`
		Webhooks        *[]config.WebhookConfig `json:"webhooks"`
		RemoteAccess    *struct {
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
	if req.TorrentsEnabled != nil {
		c.Torrents.Enabled = *req.TorrentsEnabled
		restart = true
	}
	if req.Providers != nil {
		c.Torrents.Providers = *req.Providers
		restart = true
	}
	if req.DebridService != nil {
		c.Torrents.Debrid.Service = *req.DebridService
		restart = true
	}
	if req.DebridAPIKey != nil {
		c.Torrents.Debrid.APIKey = strings.TrimSpace(*req.DebridAPIKey)
		restart = true
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
