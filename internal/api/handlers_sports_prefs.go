package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/sports"
)

// ---------- per-user sports preferences ----------

const maxSportsPreferenceBody = 64 << 10

// sportsPreferenceSnapshot reads and validates a user's stored document. An
// unreadable stored document is an error, never silently replaced.
func (s *Server) sportsPreferenceSnapshot(ctx context.Context, userID string) (sports.SportsPreferenceSnapshot, error) {
	revision, raw, err := s.DB.GetSportsPreferences(ctx, userID)
	if err != nil {
		return sports.SportsPreferenceSnapshot{}, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return sports.SportsPreferenceSnapshot{Revision: revision, Preferences: sports.EmptySportsPreferences()}, nil
	}
	// The revision column is authoritative; the document holds only the
	// versioned preference object.
	preferences, err := sports.ParseSportsPreferences(raw)
	if err != nil {
		return sports.SportsPreferenceSnapshot{}, err
	}
	return sports.SportsPreferenceSnapshot{Revision: revision, Preferences: preferences}, nil
}

func writeSportsPreferenceError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readSportsPreferenceBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSportsPreferenceBody))
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			writeSportsPreferenceError(w, http.StatusRequestEntityTooLarge, "sports preferences too large")
		} else {
			writeSportsPreferenceError(w, http.StatusBadRequest, "unable to read sports preferences")
		}
		return nil, false
	}
	return body, true
}

func (s *Server) handleGetSportsPreferences(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p == nil {
		writeSportsPreferenceError(w, http.StatusUnauthorized, "signed out")
		return
	}
	snapshot, err := s.sportsPreferenceSnapshot(r.Context(), p.User.ID)
	if err != nil {
		writeSportsPreferenceError(w, http.StatusInternalServerError, "unable to load sports preferences")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handlePutSportsPreferences(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p == nil {
		writeSportsPreferenceError(w, http.StatusUnauthorized, "signed out")
		return
	}
	s.saveSportsPreferences(w, r, p.User.ID)
}

// saveSportsPreferences is shared by the user endpoint and the admin editor.
func (s *Server) saveSportsPreferences(w http.ResponseWriter, r *http.Request, userID string) {
	body, ok := readSportsPreferenceBody(w, r)
	if !ok {
		return
	}
	input, err := sports.ParseSportsPreferenceSnapshot(body)
	if err != nil {
		writeSportsPreferenceError(w, http.StatusBadRequest, "invalid sports preference document")
		return
	}
	// Re-encode the parsed document so only canonical fields are stored.
	canonical, err := json.Marshal(input.Preferences)
	if err != nil {
		writeSportsPreferenceError(w, http.StatusBadRequest, "invalid sports preference document")
		return
	}
	revision, err := s.DB.SetSportsPreferences(r.Context(), userID, input.Revision, canonical)
	if errors.Is(err, db.ErrSportsPreferenceConflict) {
		writeSportsPreferenceError(w, http.StatusConflict, "sports preferences changed; reload before saving")
		return
	}
	if errors.Is(err, db.ErrSportsPreferenceRevisionExhausted) {
		writeSportsPreferenceError(w, http.StatusConflict, "sports preference revision exhausted")
		return
	}
	if err != nil {
		writeSportsPreferenceError(w, http.StatusInternalServerError, "unable to save sports preferences")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, sports.SportsPreferenceSnapshot{Revision: revision, Preferences: input.Preferences})
}

// ---------- admin: one user's preferences ----------

func (s *Server) handleAdminUserSportsPreferences(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if _, err := s.DB.UserByID(r.Context(), userID); err != nil {
		writeSportsPreferenceError(w, http.StatusNotFound, "user not found")
		return
	}
	snapshot, err := s.sportsPreferenceSnapshot(r.Context(), userID)
	if err != nil {
		writeSportsPreferenceError(w, http.StatusInternalServerError, "unable to load sports preferences")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleAdminPutUserSportsPreferences(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if _, err := s.DB.UserByID(r.Context(), userID); err != nil {
		writeSportsPreferenceError(w, http.StatusNotFound, "user not found")
		return
	}
	s.saveSportsPreferences(w, r, userID)
}
