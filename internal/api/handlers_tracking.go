package api

import (
	"net/http"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/db"
)

// ---------- Simkl and Trakt ----------
//
// Both trackers share these handlers: /api/me/simkl/… and /api/me/trakt/….

func (s *Server) trackerReady(w http.ResponseWriter) bool {
	if s.Tracking == nil {
		writeErr(w, http.StatusServiceUnavailable, "tracking isn't running")
		return false
	}
	return true
}

// trackerOf is the tracker a /api/me/{simkl,trakt} route is for.
func trackerOf(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/me/trakt") {
		return db.Trakt
	}
	return db.Simkl
}

func (s *Server) handleTrackerStatus(w http.ResponseWriter, r *http.Request) {
	if !s.trackerReady(w) {
		return
	}
	st, err := s.Tracking.Status(r.Context(), trackerOf(r), principal(r).User.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

// handleTrackerLink starts the sign-in: the user opens the returned URL (or
// enters the code at simkl.com/pin or trakt.tv/activate) and approves;
// GET /api/me/{simkl,trakt} shows when it's linked.
func (s *Server) handleTrackerLink(w http.ResponseWriter, r *http.Request) {
	if !s.trackerReady(w) {
		return
	}
	p, err := s.Tracking.Link(r.Context(), trackerOf(r), principal(r).User.ID)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handleTrackerCancelLink(w http.ResponseWriter, r *http.Request) {
	if !s.trackerReady(w) {
		return
	}
	s.Tracking.CancelLinkFor(trackerOf(r), principal(r).User.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTrackerUnlink(w http.ResponseWriter, r *http.Request) {
	if !s.trackerReady(w) {
		return
	}
	if err := s.Tracking.Unlink(r.Context(), trackerOf(r), principal(r).User.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTrackerPatch(w http.ResponseWriter, r *http.Request) {
	if !s.trackerReady(w) {
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
		if err := s.DB.SetTrackerScrobble(r.Context(), trackerOf(r), uid, *req.Scrobble); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	s.handleTrackerStatus(w, r)
}

func (s *Server) handleTrackerImport(w http.ResponseWriter, r *http.Request) {
	if !s.trackerReady(w) {
		return
	}
	uid := principal(r).User.ID
	if a, err := s.DB.GetTracker(r.Context(), trackerOf(r), uid); err != nil || a == nil {
		writeErr(w, 409, "link the account first")
		return
	}
	s.Tracking.ImportFrom(trackerOf(r), uid)
	w.WriteHeader(http.StatusAccepted)
}
