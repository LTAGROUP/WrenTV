package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

// handleSportsRaces returns every enabled racing weekend with ordered sessions
// and per-league freshness. A race is never flattened into a matchup.
func (s *Server) handleSportsRaces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Sports.GetRaceBoard(r.Context()))
}

// handleSportsRaceSession returns one enriched session (drivers, statistics,
// status). An unknown session is a 404; a provider failure keeps the last good
// snapshot and marks it stale rather than erroring.
func (s *Server) handleSportsRaceSession(w http.ResponseWriter, r *http.Request) {
	league := strings.ToLower(strings.TrimSpace(pathParam(r, "league")))
	eventID := strings.TrimSpace(pathParam(r, "event"))
	sessionID := strings.TrimSpace(pathParam(r, "session"))
	if league == "" || eventID == "" || sessionID == "" {
		writeErr(w, http.StatusBadRequest, "league, event and session required")
		return
	}
	session, err := s.Sports.GetRaceSession(r.Context(), league, eventID, sessionID)
	if err != nil {
		if session.ID == "" {
			writeErr(w, http.StatusNotFound, "Race session unavailable")
			return
		}
		writeErr(w, http.StatusBadGateway, "Race session unavailable")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

// handleSportsF1Archive resolves the session first, then reads the archive.
// Archive data is always labelled as archive, never as a live feed.
func (s *Server) handleSportsF1Archive(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimSpace(pathParam(r, "event"))
	sessionID := strings.TrimSpace(pathParam(r, "session"))
	session, err := s.Sports.GetRaceSession(r.Context(), "f1", eventID, sessionID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Race session unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.Sports.GetF1Archive(r.Context(), session))
}

func (s *Server) handleSportsCycling(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Sports.GetCycling(r.Context()))
}

func (s *Server) handleSportsCyclingStage(w http.ResponseWriter, r *http.Request) {
	race := strings.ToLower(strings.TrimSpace(pathParam(r, "race")))
	year, errYear := strconv.Atoi(pathParam(r, "year"))
	stage, errStage := strconv.Atoi(pathParam(r, "stage"))
	if errYear != nil || errStage != nil || race == "" {
		writeErr(w, http.StatusBadRequest, "invalid cycling stage")
		return
	}
	detail, err := s.Sports.GetCyclingStage(r.Context(), race, year, stage)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Cycling stage unavailable")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleSportsMotoGPStandings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Sports.GetMotoGPEnrichment(r.Context(), ""))
}

func (s *Server) handleSportsMotoGPCircuit(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimSpace(pathParam(r, "event"))
	if eventID == "" {
		writeErr(w, http.StatusBadRequest, "event required")
		return
	}
	writeJSON(w, http.StatusOK, s.Sports.GetMotoGPEnrichment(r.Context(), eventID))
}

// handleSportsCricketSeries exposes provider discovery candidates separately
// from supported leagues. A candidate is never polled and never enters the
// catalog until its scoreboard has been validated, so `complete` stays false.
func (s *Server) handleSportsCricketSeries(w http.ResponseWriter, r *http.Request) {
	series, err := s.Sports.DiscoverCricketSeries(r.Context())
	if err != nil && len(series) == 0 {
		writeErr(w, http.StatusBadGateway, "cricket series discovery unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"series": series, "complete": false, "stale": err != nil,
		"note": "Discovery candidates require scoreboard validation before activation; the provider list is capped.",
	})
}

// handleSportsLeagueStandings exposes provider tables. Only competitions whose
// official table is ported return rows; the rest answer "unavailable" rather
// than an invented table.
func (s *Server) handleSportsLeagueStandings(w http.ResponseWriter, r *http.Request) {
	league := strings.ToLower(strings.TrimSpace(pathParam(r, "league")))
	if league == "" {
		writeErr(w, http.StatusBadRequest, "league required")
		return
	}
	writeJSON(w, http.StatusOK, s.Sports.GetLeagueStandings(r.Context(), league))
}

// sportsWatchEvent resolves race sessions and cycling stages for the stream
// matcher without a client-supplied title. It returns false for unknown IDs so
// a deep link cannot invent an event.
func (s *Server) sportsWatchEvent(r *http.Request, id string) (sports.SportsGame, bool) {
	if game, ok := s.Sports.GetGame(id); ok {
		return game, true
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	parentID := strings.TrimSpace(r.URL.Query().Get("parentId"))
	if kind == "racing" {
		parent, session, ok := s.Sports.FindRaceSession(r.Context(), id, parentID)
		if !ok || session.ID == "" {
			return sports.SportsGame{}, false
		}
		return sports.SportsGame{
			ID: session.ID, Title: parent.Title, EventContext: session.SessionType,
			League: parent.League, Sport: "racing", EventKind: "race-session",
			StartTime: session.StartTime, Status: session.Status, StatusDetail: session.StatusDetail,
			VenueName: session.VenueName, Broadcasts: session.Broadcasts,
		}, true
	}
	if kind == "cycling" {
		race, stage, ok := s.Sports.FindCyclingStage(r.Context(), id, parentID)
		if !ok {
			return sports.SportsGame{}, false
		}
		// Date-only stages deliberately have no invented start time.
		return sports.SportsGame{
			ID: stage.ID, Title: race.Name, EventContext: stage.Name,
			League: "cycling", Sport: "cycling", EventKind: "cycling-stage",
			Status: sports.SportsGameStatus("scheduled"),
		}, true
	}
	return sports.SportsGame{}, false
}
