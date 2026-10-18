// Package api is Wren TV's HTTP API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/direct"
	"github.com/LTAGROUP/wrentv/internal/images"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/livetv"
	"github.com/LTAGROUP/wrentv/internal/markers"
	"github.com/LTAGROUP/wrentv/internal/metadata/omdb"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
	"github.com/LTAGROUP/wrentv/internal/stream"
	"github.com/LTAGROUP/wrentv/internal/streaming"
	"github.com/LTAGROUP/wrentv/internal/thumbs"
	"github.com/LTAGROUP/wrentv/internal/tracking"
	"github.com/LTAGROUP/wrentv/internal/web"
	"github.com/LTAGROUP/wrentv/internal/webhook"
)

type Server struct {
	Version   string
	Cfg       *config.Config
	DB        *db.DB
	Scanner   *library.Scanner
	Watcher   *library.Watcher
	TMDB      *tmdb.Client
	OMDb      *omdb.Client
	Markers   *markers.Analyzer
	Streams   *stream.Manager
	Streaming *streaming.Service
	LiveTV    *livetv.Service
	Hub       *library.Hub
	Hooks     *webhook.Sender
	Tracking  *tracking.Service // Simkl and playback webhooks
	Images    *images.Cache
	Thumbs    *thumbs.Manager // seek thumbnails
	// thumbJobs starts a streamed file's thumbnails on first request, by session.
	thumbJobs sync.Map
	Direct    *direct.Manager // automatic HTTPS addresses (supabase mode)

	Auth  auth.Provider
	Local *auth.Local    // set in local mode
	Supa  *auth.Supabase // set in supabase mode
	// Devices signs in devices linked from another device (TV sign-in codes).
	Devices *auth.Devices

	pairs     *pairings // TV sign-in codes waiting for approval
	pairsOnce sync.Once

	tickets     *tickets // device download links
	ticketsOnce sync.Once
}

type ctxKey int

const principalKey ctxKey = 1

func principal(r *http.Request) *auth.Principal {
	p, _ := r.Context().Value(principalKey).(*auth.Principal)
	return p
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	// No RealIP middleware: client-supplied X-Real-IP must never make a
	// proxied request look like loopback (/internal is loopback-only).
	r.Use(middleware.Recoverer, cors)
	r.Use(requestLog)

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/api/server", s.handleServerInfo)
	r.Post("/api/setup/owner", s.handleSetupOwner)
	r.Post("/api/setup/claim", s.handleClaim)
	r.Post("/api/auth/login", s.handleLogin)
	r.Get("/api/images/{size}/{file}", func(w http.ResponseWriter, r *http.Request) {
		s.Images.ServeTMDB(w, r, chi.URLParam(r, "size"), "/"+chi.URLParam(r, "file"))
	})

	// Capability URLs: the session id is the credential (players can't send headers reliably).
	r.Get("/stream/{sid}/thumbs/{n}", s.serveThumb)
	r.HandleFunc("/stream/{sid}/*", func(w http.ResponseWriter, r *http.Request) {
		sess := s.Streams.Get(chi.URLParam(r, "sid"))
		if sess == nil {
			http.Error(w, "session expired", http.StatusGone)
			return
		}
		sess.ServeHTTP(w, r, chi.URLParam(r, "*"))
	})
	r.HandleFunc("/live/{sid}/*", func(w http.ResponseWriter, r *http.Request) {
		sess := s.LiveTV.Get(chi.URLParam(r, "sid"))
		if sess == nil {
			http.Error(w, "session expired", http.StatusGone)
			return
		}
		sess.ServeHTTP(w, r, chi.URLParam(r, "*"))
	})
	r.Get("/internal/stream/{token}", s.serveInternalStream)
	r.Head("/internal/stream/{token}", s.serveInternalStream)
	// Device downloads: the ticket is the credential (download managers resume without headers).
	r.Get("/download/{ticket}/*", s.serveDeviceDownload)
	r.Head("/download/{ticket}/*", s.serveDeviceDownload)

	// TV sign-in: the TV asks for a code and polls; a signed-in device approves it.
	r.Post("/api/pair", s.handleCreatePair)
	r.Get("/api/pair/{code}", s.handlePairInfo)
	r.Post("/api/pair/{code}/poll", s.handlePollPair)

	// Backend services (e.g. a Riven-like source manager), authenticated by the integration key.
	r.Route("/api/integration", func(r chi.Router) {
		r.Use(s.integrationAuth)
		r.Get("/libraries", s.handleIntegrationLibraries)
		r.Post("/libraries", s.handleCreateLibrary)
		r.Post("/libraries/{id}/scan", s.handleScanLibrary)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Get("/api/me", s.handleMe)
		r.Post("/api/pair/{code}/approve", s.handleApprovePair)
		r.Get("/api/events", s.handleEvents)
		r.Get("/api/home", s.handleHome)
		r.Get("/api/search", s.handleSearch)
		r.Get("/api/libraries", s.handleLibraries)
		r.Get("/api/libraries/{id}/items", s.handleLibraryItems)
		r.Get("/api/items/{id}", s.handleItem)
		r.Get("/api/items/{id}/children", s.handleChildren)
		r.Get("/api/items/{id}/extras", s.handleExtras)
		r.Get("/api/items/{id}/theme", s.handleTheme)
		r.Get("/api/discover/home", s.handleDiscoverHome)
		r.Get("/api/discover/browse", s.handleDiscoverBrowse)
		r.Get("/api/discover/trending", s.handleTrending)
		r.Get("/api/discover/search", s.handleDiscoverSearch)
		r.Post("/api/streaming/titles", s.handleAddStreamTitle)
		r.Post("/api/items/{id}/download", s.handleDeviceDownload)
		r.Post("/api/items/{id}/progress", s.handleProgress)
		r.Post("/api/items/{id}/watched", s.handleWatched)
		r.Put("/api/items/{id}/watchlist", s.handleWatchlistAdd)
		r.Delete("/api/items/{id}/watchlist", s.handleWatchlistRemove)
		r.Get("/api/watchlist", s.handleWatchlist)
		r.Post("/api/items/{id}/play", s.handlePlay)
		r.Delete("/api/sessions/{sid}", s.handleStopSession)
		r.Get("/api/livetv/channels", s.handleLiveChannels)
		r.Get("/api/livetv/guide", s.handleLiveGuide)
		r.Post("/api/livetv/channels/{id}/play", s.handleLivePlay)
		r.Get("/api/me/prefs", s.handleGetPrefs)
		r.Patch("/api/me/prefs", s.handlePatchPrefs)
		r.Get("/api/me/simkl", s.handleSimklStatus)
		r.Patch("/api/me/simkl", s.handleSimklPatch)
		r.Post("/api/me/simkl/link", s.handleSimklLink)
		r.Delete("/api/me/simkl/link", s.handleSimklCancelLink)
		r.Delete("/api/me/simkl", s.handleSimklUnlink)
		r.Post("/api/me/simkl/import", s.handleSimklImport)
		r.Get("/api/genres", s.handleGenres)
		r.Get("/api/genres/items", s.handleGenreItems)
		r.Get("/api/collections", s.handleCollections)
		r.Get("/api/collections/{id}", s.handleCollection)

		r.Group(func(r chi.Router) {
			r.Use(adminOnly)
			r.Post("/api/libraries", s.handleCreateLibrary)
			r.Patch("/api/libraries/{id}", s.handleUpdateLibrary)
			r.Delete("/api/libraries/{id}", s.handleDeleteLibrary)
			r.Post("/api/libraries/{id}/scan", s.handleScanLibrary)
			r.Post("/api/items/{id}/match", s.handleRematch)
			r.Get("/api/items/{id}/streams", s.handleItemStreams)
			r.Delete("/api/items/{id}/streams", s.handleForgetStreams)
			r.Post("/api/items/{id}/server-download", s.handleServerDownload)
			r.Get("/api/admin/downloads", s.handleListDownloads)
			r.Delete("/api/admin/downloads/{id}", s.handleCancelDownload)
			r.Post("/api/admin/downloads/{id}/retry", s.handleRetryDownload)
			r.Post("/api/admin/streaming/check-debrid", s.handleCheckDebrid)
			r.Post("/api/admin/streaming/check-addon", s.handleCheckAddon)
			r.Post("/api/admin/streaming/check-usenet", s.handleCheckUsenet)
			r.Get("/api/users", s.handleListUsers)
			r.Post("/api/users", s.handleCreateUser)
			r.Patch("/api/users/{id}", s.handleUpdateUser)
			r.Delete("/api/users/{id}", s.handleDeleteUser)
			r.Get("/api/admin/status", s.handleAdminStatus)
			r.Get("/api/admin/settings", s.handleGetSettings)
			r.Get("/api/admin/browse", s.handleBrowse)
			r.Patch("/api/admin/settings", s.handlePatchSettings)
			r.Get("/api/admin/stats", s.handleStats)
			r.Get("/api/admin/livetv", s.handleLiveTVAdmin)
			r.Post("/api/admin/livetv/refresh", s.handleLiveTVRefresh)
			r.Post("/api/admin/livetv/check", s.handleLiveTVCheck)
			r.Get("/api/admin/history", s.handleHistory)
			r.Post("/api/collections", s.handleCreateCollection)
			r.Post("/api/collections/auto", s.handleAutoFranchises)
			r.Get("/api/collections/franchises", s.handleSearchFranchises)
			r.Patch("/api/collections/{id}", s.handleUpdateCollection)
			r.Delete("/api/collections/{id}", s.handleDeleteCollection)
			r.Post("/api/collections/{id}/items", s.handleCollectionAdd)
			r.Delete("/api/collections/{id}/items/{itemId}", s.handleCollectionRemove)
			r.Post("/api/collections/{id}/order", s.handleCollectionOrder)
			r.Post("/api/collections/{id}/sync", s.handleSyncCollection)
			r.Get("/api/items/{id}/collections", s.handleItemCollections)
		})
	})

	// Unknown API paths are JSON 404s; everything else is the web app.
	r.HandleFunc("/api/*", func(w http.ResponseWriter, r *http.Request) { writeErr(w, 404, "not found") })
	r.Handle("/*", web.Handler())
	return r
}

// ---------- middleware ----------

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := auth.BearerToken(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "sign in required")
			return
		}
		p, err := s.Auth.Authenticate(r.Context(), tok)
		switch {
		case errors.Is(err, auth.ErrUnclaimed):
			writeErr(w, http.StatusConflict, "server not claimed yet")
			return
		case errors.Is(err, auth.ErrForbidden):
			writeErr(w, http.StatusForbidden, "your account doesn't have access to this server")
			return
		case err != nil:
			writeErr(w, http.StatusUnauthorized, "invalid or expired sign-in")
			return
		}
		p = s.withStreamingAccess(r.Context(), p)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

func adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := principal(r); p == nil || !p.IsAdmin() {
			writeErr(w, http.StatusForbidden, "admin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) integrationAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-WrenTV-Key")
		if key == "" {
			key = r.Header.Get("X-Haven-Key") // legacy integrations
		}
		if key == "" {
			key = auth.BearerToken(r)
		}
		if !auth.IntegrationKeyOK(key, s.Cfg.IntegrationKey) {
			writeErr(w, http.StatusUnauthorized, "invalid integration key")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey,
			&auth.Principal{User: &db.User{ID: "integration", DisplayName: "integration"}, Role: "admin"})))
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-WrenTV-Token, X-WrenTV-Key, X-WrenTV-Client, X-Haven-Token, X-Haven-Key, Range")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS, HEAD")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges, X-Subtitle-Complete")
		if r.Method == http.MethodOptions {
			// Private Network Access: lets public HTTPS pages (watch.wrentv.com)
			// reach this server at its LAN address.
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if strings.HasPrefix(r.URL.Path, "/stream/") || strings.HasPrefix(r.URL.Path, "/live/") || strings.HasPrefix(r.URL.Path, "/api/images/") || r.URL.Path == "/api/events" {
			if ww.Status() < 400 {
				return
			}
		}
		slog.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "took", time.Since(start).Round(time.Millisecond))
	})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func queryInt(r *http.Request, key string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil {
		return v
	}
	return def
}

// advertisedLocalURLs is LocalURLs plus the automatic https LAN addresses.
func (s *Server) advertisedLocalURLs() []string {
	urls := LocalURLs(s.Cfg)
	if s.Direct != nil {
		urls = append(append([]string{}, urls...), s.Direct.Status().LocalURLs...)
	}
	return urls
}

// LocalURLs lists LAN addresses clients can use to reach this server.
func LocalURLs(c *config.Config) []string {
	if len(c.LocalURLs) > 0 {
		return c.LocalURLs
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		port = "32500"
	}
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			out = append(out, "http://"+net.JoinHostPort(ipn.IP.String(), port))
		}
	}
	return out
}
