package api

import (
	"net/http"
)

// ---------- Simkl ----------

func (s *Server) simklReady(w http.ResponseWriter) bool {
	if s.Tracking == nil {
		writeErr(w, http.StatusServiceUnavailable, "tracking isn't running")
		return false
	}
	return true
}

func (s *Server) handleSimklStatus(w http.ResponseWriter, r *http.Request) {
	if !s.simklReady(w) {
		return
	}
	st, err := s.Tracking.SimklStatus(r.Context(), principal(r).User.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

// handleSimklLink starts the sign-in: the user opens the returned URL (or
// enters the code at simkl.com/pin) and approves; GET /api/me/simkl shows
// when it's linked.
func (s *Server) handleSimklLink(w http.ResponseWriter, r *http.Request) {
	if !s.simklReady(w) {
		return
	}
	p, err := s.Tracking.LinkSimkl(r.Context(), principal(r).User.ID)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handleSimklCancelLink(w http.ResponseWriter, r *http.Request) {
	if !s.simklReady(w) {
		return
	}
	s.Tracking.CancelLink(principal(r).User.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSimklUnlink(w http.ResponseWriter, r *http.Request) {
	if !s.simklReady(w) {
		return
	}
	if err := s.Tracking.UnlinkSimkl(r.Context(), principal(r).User.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSimklPatch(w http.ResponseWriter, r *http.Request) {
	if !s.simklReady(w) {
		return
	}
	var req struct {
		Scrobble *bool `json:"scrobble"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid := principal(r).User.ID
	if req.Scrobble != nil {
		if err := s.DB.SetSimklScrobble(r.Context(), uid, *req.Scrobble); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	s.handleSimklStatus(w, r)
}

func (s *Server) handleSimklImport(w http.ResponseWriter, r *http.Request) {
	if !s.simklReady(w) {
		return
	}
	uid := principal(r).User.ID
	if a, err := s.DB.GetSimkl(r.Context(), uid); err != nil || a == nil {
		writeErr(w, 409, "link Simkl first")
		return
	}
	s.Tracking.Import(uid)
	w.WriteHeader(http.StatusAccepted)
}
