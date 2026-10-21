package api

import (
	"errors"
	"net/http"
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
		"name":          s.Cfg.ServerName,
		"machineId":     s.Cfg.MachineID,
		"version":       s.Version,
		"authMode":      "supabase",
		"setupRequired": setup,
		"localAccounts": s.Local != nil,
		"streaming":     s.Streaming.Active(),
		"liveTv":        s.LiveTV.Active(),
		"localUrls":     s.advertisedLocalURLs(),
	}
	if s.Supa != nil {
		info["supabase"] = map[string]string{"url": config.SupabaseURL, "publishableKey": config.SupabasePublishableKey}
		info["serverId"] = s.Cfg.Auth.Supabase.ServerID
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
	writeJSON(w, 200, map[string]any{"serverId": s.Cfg.Auth.Supabase.ServerID})
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
	u, err := s.Local.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
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
		_ = s.DB.UpdateUserLimits(r.Context(), u.ID, u.UserLimits)
	}
	if !req.IsAdmin {
		_ = s.DB.SetLibraryAccess(r.Context(), u.ID, req.LibraryIDs)
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
		_ = s.DB.SetLibraryAccess(r.Context(), u.ID, *req.LibraryIDs)
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
		_, _ = s.DB.ExecContext(r.Context(), `UPDATE users SET password_hash=? WHERE id=?`, hash, u.ID)
	}
	writeJSON(w, 200, u)
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
