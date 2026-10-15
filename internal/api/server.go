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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/images"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/markers"
	"github.com/LTAGROUP/wrentv/internal/metadata/omdb"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
	"github.com/LTAGROUP/wrentv/internal/stream"
	"github.com/LTAGROUP/wrentv/internal/torrent"
	"github.com/LTAGROUP/wrentv/internal/web"
	"github.com/LTAGROUP/wrentv/internal/webhook"
)

type Server struct {
	Version  string
	Cfg      *config.Config
	DB       *db.DB
	Scanner  *library.Scanner
	Watcher  *library.Watcher
	TMDB     *tmdb.Client
	OMDb     *omdb.Client
	Markers  *markers.Analyzer
	Streams  *stream.Manager
	Torrents *torrent.Manager
	Hub      *library.Hub
	Hooks    *webhook.Sender
	Images   *images.Cache

	Auth  auth.Provider
	Local *auth.Local    // set in local mode
	Supa  *auth.Supabase // set in supabase mode
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
	r.HandleFunc("/stream/{sid}/*", func(w http.ResponseWriter, r *http.Request) {
		sess := s.Streams.Get(chi.URLParam(r, "sid"))
		if sess == nil {
			http.Error(w, "session expired", http.StatusGone)
			return
		}
		sess.ServeHTTP(w, r, chi.URLParam(r, "*"))
	})
	r.Get("/internal/torrent/{token}", func(w http.ResponseWriter, r *http.Request) {
		s.Torrents.ServeInternal(w, r, chi.URLParam(r, "token"))
	})
	r.Head("/internal/torrent/{token}", func(w http.ResponseWriter, r *http.Request) {
		s.Torrents.ServeInternal(w, r, chi.URLParam(r, "token"))
	})

	// Backend services (e.g. a Riven-like source manager), authenticated by the integration key.
	r.Route("/api/integration", func(r chi.Router) {
		r.Use(s.integrationAuth)
		r.Get("/torrents", s.handleListTorrents)
		r.Post("/torrents", s.handleAddTorrent)
		r.Get("/torrents/{id}", s.handleGetTorrent)
		r.Post("/torrents/{id}/replace", s.handleReplaceTorrent)
		r.Delete("/torrents/{id}", s.handleDeleteTorrent)
		r.Get("/libraries", s.handleIntegrationLibraries)
		r.Post("/libraries", s.handleCreateLibrary)
		r.Post("/libraries/{id}/scan", s.handleScanLibrary)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Get("/api/me", s.handleMe)
		r.Get("/api/events", s.handleEvents)
		r.Get("/api/home", s.handleHome)
		r.Get("/api/search", s.handleSearch)
		r.Get("/api/libraries", s.handleLibraries)
		r.Get("/api/libraries/{id}/items", s.handleLibraryItems)
		r.Get("/api/items/{id}", s.handleItem)
		r.Get("/api/items/{id}/children", s.handleChildren)
		r.Get("/api/items/{id}/extras", s.handleExtras)
		r.Get("/api/discover/trending", s.handleTrending)
		r.Post("/api/items/{id}/progress", s.handleProgress)
		r.Post("/api/items/{id}/watched", s.handleWatched)
		r.Post("/api/items/{id}/play", s.handlePlay)
		r.Delete("/api/sessions/{sid}", s.handleStopSession)

		r.Group(func(r chi.Router) {
			r.Use(adminOnly)
			r.Post("/api/libraries", s.handleCreateLibrary)
			r.Patch("/api/libraries/{id}", s.handleUpdateLibrary)
			r.Delete("/api/libraries/{id}", s.handleDeleteLibrary)
			r.Post("/api/libraries/{id}/scan", s.handleScanLibrary)
			r.Post("/api/items/{id}/match", s.handleRematch)
			r.Get("/api/torrents", s.handleListTorrents)
			r.Post("/api/torrents", s.handleAddTorrent)
			r.Get("/api/torrents/{id}", s.handleGetTorrent)
			r.Delete("/api/torrents/{id}", s.handleDeleteTorrent)
			r.Post("/api/torrents/{id}/replace", s.handleReplaceTorrent)
			r.Get("/api/users", s.handleListUsers)
			r.Post("/api/users", s.handleCreateUser)
			r.Patch("/api/users/{id}", s.handleUpdateUser)
			r.Delete("/api/users/{id}", s.handleDeleteUser)
			r.Get("/api/admin/status", s.handleAdminStatus)
			r.Get("/api/admin/settings", s.handleGetSettings)
			r.Get("/api/admin/browse", s.handleBrowse)
			r.Patch("/api/admin/settings", s.handlePatchSettings)
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
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-WrenTV-Token, X-WrenTV-Key, X-Haven-Token, X-Haven-Key, Range")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS, HEAD")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges")
		if r.Method == http.MethodOptions {
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
		if strings.HasPrefix(r.URL.Path, "/stream/") || strings.HasPrefix(r.URL.Path, "/api/images/") || r.URL.Path == "/api/events" {
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
