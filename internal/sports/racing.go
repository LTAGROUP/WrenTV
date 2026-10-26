package sports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RaceBoard is the racing hub contract: one entry per race weekend, each with
// its ordered sessions. It is separate from the matchup scoreboard because a
// race has multiple sessions and a field of drivers, not two teams.
type RaceBoard struct {
	Events  []SportsEvent        `json:"events"`
	Leagues []LeagueAvailability `json:"leagues"`
}

type raceBoardEntry struct {
	updated time.Time
	expires time.Time
	events  []SportsEvent
	stale   bool
}

type raceSessionEntry struct {
	event   SportsEvent
	expires time.Time
}

type raceScoreboard struct {
	Events []struct {
		ID           string            `json:"id"`
		Name         string            `json:"name"`
		Competitions []espnCompetition `json:"competitions"`
	} `json:"events"`
}

// racingLeagueSlug resolves the provider slug only for leagues this service
// can actually poll. Dynamic catalog entries carry their own slug.
func (s *Service) racingLeague(id string) (League, bool) {
	league, ok := s.League(id)
	if !ok || !league.active() || league.Sport != "racing" || league.EventKind != "race" {
		return League{}, false
	}
	if league.ID == "motogp" {
		return league, true
	}
	if league.Provider != "espn" || league.Slug == "" {
		return League{}, false
	}
	return league, true
}

func (s *Service) racingJSON(ctx context.Context, endpoint string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("racing provider status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(target)
}

// normalizeRaceBoard converts an ESPN racing scoreboard into a weekend plus
// ordered sessions. Scoreboard order is never accepted as a classification.
func normalizeRaceBoard(raw raceScoreboard, league string, now time.Time) []SportsEvent {
	events := []SportsEvent{}
	for _, event := range raw.Events {
		if event.ID == "" || event.Name == "" {
			continue
		}
		parent := SportsEvent{
			ID: league + ":" + event.ID, ProviderEventID: event.ID, Title: event.Name,
			League: league, Sport: "racing", EventKind: "race", UpdatedAt: now,
			Participants: []SportsParticipant{},
		}
		for _, c := range event.Competitions {
			if c.ID == "" || parseESPNDate(c.Date).IsZero() {
				continue
			}
			label := c.Type.Abbreviation
			if label == "" {
				label = "Race"
			}
			session := SportsEvent{
				ID: parent.ID + ":" + c.ID, ProviderEventID: event.ID, SessionID: c.ID,
				SessionType: label, Title: event.Name, League: league, Sport: "racing",
				EventKind: "race-session", StartTime: parseESPNDate(c.Date),
				Status: espnStatusToGameStatus(c.Status.Type), StatusDetail: c.Status.Type.Detail,
				UpdatedAt: now, Participants: []SportsParticipant{},
			}
			if c.Venue != nil {
				session.VenueName = c.Venue.FullName
			}
			for _, b := range c.Broadcasts {
				session.Broadcasts = append(session.Broadcasts, b.Names...)
			}
			for _, driver := range c.Competitors {
				if driver.Athlete == nil || driver.ID == "" {
					continue
				}
				// Round and sprint entries can share a competitor ID; the card
				// shows the driver once per session.
				session.Participants = append(session.Participants, SportsParticipant{
					ID: driver.ID, Name: driver.Athlete.DisplayName,
					Abbreviation: driver.Athlete.ShortName, Winner: driver.Winner,
				})
			}
			parent.SubEvents = append(parent.SubEvents, session)
		}
		if len(parent.SubEvents) == 0 {
			continue
		}
		sort.SliceStable(parent.SubEvents, func(i, j int) bool {
			return parent.SubEvents[i].StartTime.Before(parent.SubEvents[j].StartTime)
		})
		// The weekend reflects its main race, not an earlier practice session.
		main := parent.SubEvents[len(parent.SubEvents)-1]
		for _, session := range parent.SubEvents {
			if strings.EqualFold(session.SessionType, "Race") {
				main = session
			}
		}
		parent.StartTime = main.StartTime
		parent.Status = main.Status
		parent.StatusDetail = main.StatusDetail
		parent.VenueName = main.VenueName
		attachRaceCircuit(&parent)
		for i := range parent.SubEvents {
			parent.SubEvents[i].Circuit = parent.Circuit
		}
		events = append(events, parent)
	}
	return events
}

// GetRaceBoard returns every enabled racing weekend, cached per league with a
// stale snapshot retained when a provider fails. MotoGP uses its organizer API
// and a longer TTL because its calendar changes rarely.
func (s *Service) GetRaceBoard(ctx context.Context) RaceBoard {
	s.mu.RLock()
	ids := []string{}
	for _, l := range s.leagues {
		if _, ok := s.racingLeague(l.ID); ok {
			ids = append(ids, l.ID)
		}
	}
	s.mu.RUnlock()

	s.raceMu.Lock()
	defer s.raceMu.Unlock()
	if s.raceBoards == nil {
		s.raceBoards = map[string]raceBoardEntry{}
	}
	board := RaceBoard{Events: []SportsEvent{}, Leagues: []LeagueAvailability{}}
	for _, id := range ids {
		league, _ := s.racingLeague(id)
		cached, exists := s.raceBoards[id]
		availability := LeagueAvailability{
			League: id, UpdatedAt: cached.updated, Stale: cached.stale,
			Unavailable: cached.updated.IsZero(),
		}
		if !exists || time.Now().After(cached.expires) {
			var events []SportsEvent
			var err error
			ttl := 30 * time.Second
			if id == "motogp" {
				requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
				events, err = s.fetchMotoGPBoard(requestCtx)
				cancel()
				ttl = 5 * time.Minute
			} else {
				var raw raceScoreboard
				requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				err = s.racingJSON(requestCtx, "https://site.api.espn.com/apis/site/v2/sports/racing/"+league.Slug+"/scoreboard", &raw)
				cancel()
				events = normalizeRaceBoard(raw, id, time.Now())
			}
			if err != nil {
				if cached.updated.IsZero() {
					availability.Unavailable = true
				}
				availability.Stale = true
				cached.stale = true
				cached.expires = time.Now().Add(15 * time.Second)
				if id == "motogp" {
					cached.expires = time.Now().Add(time.Minute)
				}
				s.raceBoards[id] = cached
			} else {
				now := time.Now()
				cached = raceBoardEntry{events: events, updated: now, expires: now.Add(ttl)}
				s.raceBoards[id] = cached
				availability.UpdatedAt = now
				availability.Stale = false
				availability.Unavailable = false
			}
		}
		for _, event := range cached.events {
			event.Stale = availability.Stale
			board.Events = append(board.Events, event)
		}
		board.Leagues = append(board.Leagues, availability)
	}
	sort.SliceStable(board.Events, func(i, j int) bool {
		return board.Events[i].StartTime.Before(board.Events[j].StartTime)
	})
	return board
}

type raceCore struct {
	ID          string `json:"id"`
	Competitors []struct {
		ID      string `json:"id"`
		Vehicle struct {
			Number       string `json:"number"`
			Team         string `json:"team"`
			Manufacturer string `json:"manufacturer"`
		} `json:"vehicle"`
		Status json.RawMessage `json:"status"`
	} `json:"competitors"`
}

type raceStatistics struct {
	Splits struct {
		Categories []struct {
			Stats []struct {
				Name         string  `json:"name"`
				Value        float64 `json:"value"`
				DisplayValue string  `json:"displayValue"`
			} `json:"stats"`
		} `json:"categories"`
	} `json:"splits"`
}

func isRaceCountSession(sessionType string) bool {
	kind := strings.ToLower(strings.TrimSpace(sessionType))
	return kind == "race" || kind == "sprint" || kind == "sprint race"
}

// applyRaceStatistics maps only verified ESPN split names and drops placeholder
// zero times while keeping genuine zero counts.
func applyRaceStatistics(driver *SportsParticipant, data raceStatistics, sessionType string) {
	for _, category := range data.Splits.Categories {
		for _, stat := range category.Stats {
			if stat.Name == "place" {
				if stat.Value >= 1 && stat.Value <= 1000 && float64(int(stat.Value)) == stat.Value {
					driver.Position = int(stat.Value)
				}
				continue
			}
			label := ""
			switch stat.Name {
			case "lapsCompleted":
				label = "Laps"
			case "totalTime":
				if isRaceCountSession(sessionType) {
					label = "Elapsed time"
				} else {
					label = "Session time"
				}
			case "behindTime":
				if stat.Value < 0 {
					continue
				}
				label = "Time behind"
			case "behindLaps":
				if stat.Value < 0 || float64(int(stat.Value)) != stat.Value {
					continue
				}
				label = "Laps behind"
			case "lapsLead", "pitsTaken":
				if !isRaceCountSession(sessionType) {
					continue
				}
				maxCount := 100.0
				if stat.Name == "lapsLead" {
					maxCount = 1000
				}
				if stat.Value < 0 || stat.Value > maxCount || float64(int(stat.Value)) != stat.Value {
					continue
				}
				label = "Pit stops"
				if stat.Name == "lapsLead" {
					label = "Laps led"
				}
			case "fastestLapNum":
				if stat.Value < 1 || stat.Value > 1000 || float64(int(stat.Value)) != stat.Value {
					continue
				}
				label = "Fastest lap number"
			case "fastestLap":
				label = "Fastest lap"
			case "qual1TimeMS":
				label = "Q1"
			case "qual2TimeMS":
				label = "Q2"
			case "qual3TimeMS":
				label = "Q3"
			}
			if label == "" || stat.DisplayValue == "" {
				continue
			}
			if stat.Name != "lapsCompleted" && stat.Name != "pitsTaken" && stat.Name != "lapsLead" && stat.Value == 0 {
				continue
			}
			driver.Statistics = append(driver.Statistics, SportsRaceStatistic{
				Name: stat.Name, Label: label, Value: stat.DisplayValue,
			})
		}
	}
}

// GetRaceSession enriches one session with driver identities, statistics and
// status. A failed refresh keeps the last good snapshot and marks it stale.
func (s *Service) GetRaceSession(ctx context.Context, league, eventID, sessionID string) (SportsEvent, error) {
	if league == "motogp" {
		return s.getMotoGPSession(ctx, eventID, sessionID)
	}
	leagueMeta, ok := s.racingLeague(league)
	if !ok || leagueMeta.Provider != "espn" {
		return SportsEvent{}, fmt.Errorf("unsupported race league")
	}
	for _, id := range []string{eventID, sessionID} {
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return SportsEvent{}, fmt.Errorf("invalid race identity")
		}
	}
	board := s.GetRaceBoard(ctx)
	var session SportsEvent
	for _, event := range board.Events {
		if event.League == league && event.ProviderEventID == eventID {
			for _, candidate := range event.SubEvents {
				if candidate.SessionID == sessionID {
					session = candidate
				}
			}
		}
	}
	if session.ID == "" {
		return session, fmt.Errorf("race session not found in current provider window")
	}
	s.raceDetailMu.Lock()
	defer s.raceDetailMu.Unlock()
	if s.raceDetails == nil {
		s.raceDetails = map[string]raceSessionEntry{}
	}
	old, exists := s.raceDetails[session.ID]
	if exists && time.Now().Before(old.expires) {
		return old.event, nil
	}
	if session.Status == SportsGameScheduled {
		return session, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	base := "https://sports.core.api.espn.com/v2/sports/racing/leagues/" + leagueMeta.Slug +
		"/events/" + eventID + "/competitions/" + sessionID
	var core raceCore
	if err := s.racingJSON(ctx, base, &core); err != nil || core.ID != sessionID {
		if exists {
			copy := old.event
			copy.Stale = true
			s.raceDetails[session.ID] = raceSessionEntry{event: copy, expires: time.Now().Add(15 * time.Second)}
			return copy, nil
		}
		session.Stale = true
		s.raceDetails[session.ID] = raceSessionEntry{event: session, expires: time.Now().Add(15 * time.Second)}
		return session, nil
	}
	// Copy before enriching so cached scoreboard participant slices stay immutable.
	session.Participants = append([]SportsParticipant{}, session.Participants...)
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	failed := make([]bool, len(session.Participants))
	for i := range session.Participants {
		if i >= 64 {
			failed[i] = true
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				failed[i] = true
				return
			}
			defer func() { <-slots }()
			driver := &session.Participants[i]
			found := false
			hasStatus := false
			for _, c := range core.Competitors {
				if c.ID == driver.ID {
					found = true
					driver.Number = c.Vehicle.Number
					driver.Team = c.Vehicle.Team
					if driver.Team == "" {
						driver.Team = c.Vehicle.Manufacturer
					}
					hasStatus = len(c.Status) > 0 && string(c.Status) != "null"
					break
				}
			}
			if !found {
				failed[i] = true
				return
			}
			// IDs come from a validated session snapshot; provider $ref URLs
			// are never followed.
			if _, err := strconv.ParseUint(driver.ID, 10, 64); err != nil {
				failed[i] = true
				return
			}
			endpoint := base + "/competitors/" + driver.ID
			var stats raceStatistics
			if err := s.racingJSON(ctx, endpoint+"/statistics/0", &stats); err != nil {
				failed[i] = true
			} else {
				applyRaceStatistics(driver, stats, session.SessionType)
			}
			if hasStatus {
				var state struct {
					DisplayValue string `json:"displayValue"`
				}
				if err := s.racingJSON(ctx, endpoint+"/status", &state); err == nil {
					driver.Result = state.DisplayValue
				} else {
					failed[i] = true
				}
			}
		}(i)
	}
	wg.Wait()
	for _, failure := range failed {
		if failure {
			session.Stale = true
		}
	}
	if session.Stale && exists {
		copy := old.event
		copy.Stale = true
		s.raceDetails[session.ID] = raceSessionEntry{event: copy, expires: time.Now().Add(15 * time.Second)}
		return copy, nil
	}
	sort.SliceStable(session.Participants, func(i, j int) bool {
		a, b := session.Participants[i].Position, session.Participants[j].Position
		if a == 0 {
			return false
		}
		if b == 0 {
			return true
		}
		return a < b
	})
	session.UpdatedAt = time.Now()
	ttl := 30 * time.Second
	if session.Status == SportsGameFinal {
		ttl = 10 * time.Minute
	}
	if session.Stale {
		ttl = 15 * time.Second
	}
	if len(s.raceDetails) >= 32 {
		oldestKey := ""
		var earliest time.Time
		for key, entry := range s.raceDetails {
			if oldestKey == "" || entry.expires.Before(earliest) {
				oldestKey = key
				earliest = entry.expires
			}
		}
		delete(s.raceDetails, oldestKey)
	}
	s.raceDetails[session.ID] = raceSessionEntry{event: session, expires: time.Now().Add(ttl)}
	return session, nil
}

// FindRaceSession resolves a session (or a weekend) straight from the board
// without enriching it, for the event page and stream discovery.
func (s *Service) FindRaceSession(ctx context.Context, id, parentID string) (SportsEvent, SportsEvent, bool) {
	for _, parent := range s.GetRaceBoard(ctx).Events {
		sessions := parent.SubEvents
		if parent.EventKind == "race-session" {
			sessions = []SportsEvent{parent}
		}
		if parentID != "" && parent.ID != parentID {
			continue
		}
		for _, session := range sessions {
			if session.ID != id {
				continue
			}
			if parent.Stale || session.Stale {
				return SportsEvent{}, SportsEvent{}, false
			}
			return parent, session, true
		}
		if parent.ID == id {
			return parent, SportsEvent{}, true
		}
	}
	return SportsEvent{}, SportsEvent{}, false
}

// raceLeagueIDs lists the racing leagues the board serves; the hub uses it to
// decide whether a racing section is meaningful for the selected date.
func raceLeagueIDs() []string {
	ids := []string{}
	for _, league := range LeagueCatalog {
		if league.Sport == "racing" && league.EventKind == "race" && league.active() {
			ids = append(ids, league.ID)
		}
	}
	return ids
}

// raceQueryValues keeps the provider URL construction deterministic in tests.
func raceQueryValues(values url.Values) string { return values.Encode() }
