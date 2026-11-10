package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
)

func (s *Server) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	// Every server uses Wren TV accounts; setupRequired means it's unclaimed.
	setup := s.Supa != nil && !s.Supa.Claimed()
	info := map[string]any{
		"name":          s.Cfg.Snapshot().ServerName,
		"machineId":     s.Cfg.MachineID,
		"version":       s.Version,
		"authMode":      "supabase",
		"setupRequired": setup,
		"localAccounts": s.Local != nil,
		"streaming":     s.Streaming.Active(),
		"liveTv":        s.LiveTV.Active(),
		"localUrls":     s.advertisedLocalURLs(),
		// Apps sign in with a server ticket (POST /api/auth/exchange), not
		// by sending their account token.
		"tokenExchange": s.Supa != nil && s.Devices != nil,
	}
	if s.Supa != nil {
		info["supabase"] = map[string]string{"url": config.SupabaseURL, "publishableKey": config.SupabasePublishableKey}
		info["serverId"] = s.Cfg.Snapshot().Auth.Supabase.ServerID
	}
	writeJSON(w, 200, info)
}

type userResponse struct {
	*db.User
	Role string `json:"role"`
}

func (s *Server) createLocalUser(r *http.Request, username, password, display string, admin bool) (*db.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("username required")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	if display == "" {
		display = username
	}
	u := &db.User{Username: username, DisplayName: display, PasswordHash: hash, IsAdmin: admin}
	if err := s.DB.CreateUser(r.Context(), u); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("username taken")
		}
		return nil, err
	}
	return u, nil
}

// handleClaim links this server to the caller's Wren TV account (Supabase mode).
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	if s.Supa == nil {
		writeErr(w, http.StatusBadRequest, "this server uses local accounts")
		return
	}
	tok := auth.BearerToken(r)
	u, err := s.Supa.Identify(r.Context(), tok)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "sign in to your Wren TV account first")
		return
	}
	if s.Supa.Claimed() {
		// Re-claiming is allowed only by the owner (e.g. to rotate the secret).
		if p, err := s.Supa.Authenticate(r.Context(), tok); err != nil || p.Role != "owner" {
			writeErr(w, http.StatusConflict, "server is already claimed by another account")
			return
		}
	} else {
		var req struct {
			ClaimCode string `json:"claimCode"`
		}
		_ = decode(r, &req)
		if !s.Supa.ClaimCodeOK(req.ClaimCode) {
			writeErr(w, http.StatusForbidden, "wrong claim code (it's printed in the server log: docker logs wrentv)")
			return
		}
	}
	if err := s.Supa.Claim(r.Context(), tok); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.DB.UpdateUserProfile(r.Context(), u.ID, u.DisplayName, u.Email, true)
	go s.heartbeat()
	if s.Direct != nil {
		s.Direct.Refresh()
	}
	writeJSON(w, 200, map[string]any{"serverId": s.Cfg.Snapshot().Auth.Supabase.ServerID})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.Local == nil {
		writeErr(w, http.StatusBadRequest, "sign in with your Wren TV account")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Device   string `json:"device"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ip := clientIP(r)
	th := s.loginThrottle()
	if wait := th.wait(req.Username, ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, waitMessage(wait))
		return
	}
	u, err := s.Local.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		th.failed(req.Username, ip)
		slog.Warn("failed sign-in", "username", req.Username, "ip", ip)
		writeErr(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	th.succeeded(req.Username, ip)
	tok, err := s.Local.Issue(u, req.Device)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	role := "user"
	if u.IsAdmin {
		role = "admin"
	}
	writeJSON(w, 200, map[string]any{"token": tok, "user": userResponse{u, role}})
}

// handleExchange signs an app in with a server ticket. The app gets the
// ticket from the account service, for this server only, and never sends
// this server its account token; it gets back a token only this server
// accepts. The account's access is still checked with the account service on
// every request, so unsharing the server ends it.
func (s *Server) handleExchange(w http.ResponseWriter, r *http.Request) {
	if s.Supa == nil || s.Devices == nil {
		writeErr(w, http.StatusBadRequest, "this server uses local accounts")
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
		Device string `json:"device"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// A wrong ticket costs a call to the account service: slow guessing down
	// the same way as wrong passwords.
	ip := clientIP(r)
	th := s.loginThrottle()
	if wait := th.wait("ticket", ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, waitMessage(wait))
		return
	}
	p, err := s.Supa.Redeem(r.Context(), strings.TrimSpace(req.Ticket))
	switch {
	case errors.Is(err, auth.ErrBadTicket):
		th.failed("ticket", ip)
		writeErr(w, http.StatusUnauthorized, "that sign-in has expired; try again")
		return
	case errors.Is(err, auth.ErrUnclaimed):
		writeErr(w, http.StatusConflict, "server not claimed yet")
		return
	case errors.Is(err, auth.ErrForbidden):
		writeErr(w, http.StatusForbidden, "your account doesn't have access to this server")
		return
	case err != nil:
		slog.Warn("sign-in ticket", "err", err)
		writeErr(w, http.StatusBadGateway, "couldn't check the sign-in with the account service; try again")
		return
	}
	device := strings.TrimSpace(req.Device)
	if device == "" {
		device = clientName(r)
	}
	if len(device) > 60 {
		device = device[:60]
	}
	tok, err := s.Devices.Issue(p.User, device)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok, "user": userResponse{p.User, p.Role}})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	writeJSON(w, 200, map[string]any{"user": userResponse{p.User, p.Role}, "libraryIds": p.LibraryIDs, "limits": s.limitsFor(p.User)})
}

// ---------- local user management (admin) ----------

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.DB.ListUsers(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type row struct {
		*db.User
		LibraryIDs []string `json:"libraryIds"`
		Effective  limits   `json:"effective"`
	}
	out := []row{}
	for _, u := range users {
		libs, _ := s.DB.LibraryAccess(r.Context(), u.ID)
		if libs == nil {
			libs = []string{}
		}
		out = append(out, row{u, libs, s.limitsFor(u)})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if s.Local == nil {
		writeErr(w, http.StatusBadRequest, "local accounts are turned off on this server")
		return
	}
	var req struct {
		Username    string         `json:"username"`
		Password    string         `json:"password"`
		DisplayName string         `json:"displayName"`
		IsAdmin     bool           `json:"isAdmin"`
		LibraryIDs  []string       `json:"libraryIds"`
		Limits      *db.UserLimits `json:"limits"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	u, err := s.createLocalUser(r, req.Username, req.Password, req.DisplayName, req.IsAdmin)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Limits != nil {
		if err := validLimits(*req.Limits); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		u.UserLimits = *req.Limits
		if err := s.DB.UpdateUserLimits(r.Context(), u.ID, u.UserLimits); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if !req.IsAdmin {
		if err := s.DB.SetLibraryAccess(r.Context(), u.ID, req.LibraryIDs); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 201, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := s.DB.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "user not found")
		return
	}
	var req struct {
		DisplayName *string        `json:"displayName"`
		IsAdmin     *bool          `json:"isAdmin"`
		LibraryIDs  *[]string      `json:"libraryIds"`
		Password    *string        `json:"password"`
		Limits      *db.UserLimits `json:"limits"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.DisplayName != nil {
		u.DisplayName = *req.DisplayName
	}
	if req.IsAdmin != nil && u.ID != principal(r).User.ID {
		u.IsAdmin = *req.IsAdmin
	}
	if err := s.DB.UpdateUserProfile(r.Context(), u.ID, u.DisplayName, u.Email, u.IsAdmin); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if req.LibraryIDs != nil {
		if err := s.DB.SetLibraryAccess(r.Context(), u.ID, *req.LibraryIDs); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if req.Limits != nil {
		if err := validLimits(*req.Limits); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		u.UserLimits = *req.Limits
		if err := s.DB.UpdateUserLimits(r.Context(), u.ID, u.UserLimits); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if req.Password != nil {
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		// A new password signs the user out everywhere on this server.
		if err := s.DB.SetPassword(r.Context(), u.ID, hash); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		// Whoever changed their own password stays signed in on this device.
		if u.ID == principal(r).User.ID {
			if tok := s.reissue(r, u); tok != "" {
				writeJSON(w, 200, struct {
					*db.User
					Token string `json:"token"`
				}{u, tok})
				return
			}
		}
	}
	writeJSON(w, 200, u)
}

// reissue gives the caller a fresh token of the kind they signed in with
// (a local sign-in or a linked device), or "" for a Wren TV account sign-in,
// which this server doesn't issue.
func (s *Server) reissue(r *http.Request, u *db.User) string {
	tok := auth.BearerToken(r)
	if s.Devices != nil {
		if device, ok := s.Devices.Owns(tok); ok {
			t, _ := s.Devices.Issue(u, device)
			return t
		}
	}
	if s.Local != nil {
		if device, ok := s.Local.Owns(tok); ok {
			t, _ := s.Local.Issue(u, device)
			return t
		}
	}
	return ""
}

// handleRevokeMine signs the caller out everywhere on this server: every
// local sign-in and linked device (TVs signed in with a code). The device
// asking gets a fresh token and stays signed in.
func (s *Server) handleRevokeMine(w http.ResponseWriter, r *http.Request) {
	u := principal(r).User
	if err := s.DB.RevokeTokens(r.Context(), u.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"token": s.reissue(r, u)})
}

// handleRevokeUser signs a user out everywhere on this server (admin).
func (s *Server) handleRevokeUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.DB.UserByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "user not found")
		return
	}
	if err := s.DB.RevokeTokens(r.Context(), u.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := map[string]any{}
	if u.ID == principal(r).User.ID {
		out["token"] = s.reissue(r, u)
	}
	writeJSON(w, 200, out)
}

func (s *Server) loginThrottle() *loginThrottle {
	s.throttleOnce.Do(func() { s.throttle = newLoginThrottle() })
	return s.throttle
}

// validLimits checks per-user limits: -1 is the server default, 0 no limit.
func validLimits(l db.UserLimits) error {
	if l.AllowDownloads < -1 || l.AllowDownloads > 1 || l.MaxStreams < -1 || l.MaxDownloads < -1 {
		return errors.New("invalid limits")
	}
	return nil
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == principal(r).User.ID {
		writeErr(w, 400, "you can't remove yourself")
		return
	}
	if err := s.DB.DeleteUser(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
