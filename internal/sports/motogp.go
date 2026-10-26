package sports

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MotoGP publishes its calendar and results through the organizer API rather
// than ESPN. Only verified lifecycle values are accepted; a reachable payload
// never implies an active session.
const motoGPBase = "https://api.motogp.pulselive.com/motogp/v1"

var motoGPUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type motoGPSeason struct {
	ID   string `json:"id"`
	Year int    `json:"year"`
}

type motoGPCategory struct {
	ID       string `json:"id"`
	LegacyID int    `json:"legacy_id"`
}

type motoGPEvent struct {
	ID           string `json:"id"`
	Name         string `json:"sponsored_name"`
	FallbackName string `json:"name"`
	Test         bool   `json:"test"`
	Circuit      struct {
		Name string `json:"name"`
	} `json:"circuit"`
}

type motoGPSession struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Number *int   `json:"number"`
	Date   string `json:"date"`
	Status string `json:"status"`
	Event  struct {
		ID string `json:"id"`
	} `json:"event"`
}

type motoGPClassification struct {
	Classification []struct {
		Position *int `json:"position"`
		Rider    struct {
			ID     string `json:"id"`
			Name   string `json:"full_name"`
			Number *int   `json:"number"`
		} `json:"rider"`
		Team struct {
			Name string `json:"name"`
		} `json:"team"`
		TotalLaps *int   `json:"total_laps"`
		Time      string `json:"time"`
		Status    string `json:"status"`
		BestLap   struct {
			Time string `json:"time"`
		} `json:"best_lap"`
		Gap struct {
			First string `json:"first"`
			Lap   string `json:"lap"`
		} `json:"gap"`
		Points *float64 `json:"points"`
	} `json:"classification"`
}

func motoGPStatus(raw string) (SportsGameStatus, bool) {
	switch strings.ToUpper(raw) {
	case "NOT-STARTED":
		return SportsGameScheduled, true
	case "FINISHED", "OFFICIAL":
		return SportsGameFinal, true
	default:
		return "", false
	}
}

func normalizeMotoGPSessions(event motoGPEvent, raw []motoGPSession, now time.Time) SportsEvent {
	title := event.Name
	if title == "" {
		title = event.FallbackName
	}
	parent := SportsEvent{
		ID: "motogp:" + event.ID, ProviderEventID: event.ID, Title: title,
		League: "motogp", Sport: "racing", EventKind: "race",
		VenueName: event.Circuit.Name, UpdatedAt: now, Participants: []SportsParticipant{},
	}
	for _, session := range raw {
		status, known := motoGPStatus(session.Status)
		if !motoGPUUID.MatchString(session.ID) || !known || parseESPNDate(session.Date).IsZero() {
			continue
		}
		label := map[string]string{"RAC": "Race", "SPR": "Sprint", "PR": "Practice", "WUP": "Warm-up", "FP": "FP", "Q": "Q"}[session.Type]
		if label == "" {
			label = session.Type
		}
		if session.Number != nil {
			label += strconv.Itoa(*session.Number)
		}
		parent.SubEvents = append(parent.SubEvents, SportsEvent{
			ID: parent.ID + ":" + session.ID, ProviderEventID: event.ID, SessionID: session.ID,
			SessionType: label, Title: label, League: "motogp", Sport: "racing",
			EventKind: "race-session", StartTime: parseESPNDate(session.Date),
			Status: status, StatusDetail: session.Status, VenueName: event.Circuit.Name,
			UpdatedAt: now, Participants: []SportsParticipant{},
		})
	}
	sort.SliceStable(parent.SubEvents, func(i, j int) bool {
		return parent.SubEvents[i].StartTime.Before(parent.SubEvents[j].StartTime)
	})
	if len(parent.SubEvents) > 0 {
		main := parent.SubEvents[len(parent.SubEvents)-1]
		for _, session := range parent.SubEvents {
			if strings.HasPrefix(session.SessionType, "Race") {
				main = session
			}
		}
		parent.StartTime = main.StartTime
		parent.Status = main.Status
		parent.StatusDetail = main.StatusDetail
	}
	return parent
}

// fetchMotoGPBoard reads the current season, the premier class category and
// every non-test event's sessions. One failed event fails the board so a
// partial calendar is never presented as the full season.
func (s *Service) fetchMotoGPBoard(ctx context.Context) ([]SportsEvent, error) {
	seasonID, categoryID, err := s.motoGPSeasonIDs(ctx, time.Now().UTC().Year())
	if err != nil {
		return nil, err
	}
	var events []motoGPEvent
	if err := s.racingJSON(ctx, motoGPBase+"/results/events?seasonUuid="+seasonID, &events); err != nil {
		return nil, err
	}
	if len(events) == 0 || len(events) > 64 {
		return nil, fmt.Errorf("unexpected MotoGP calendar size")
	}
	result := make([]SportsEvent, len(events))
	failures := make([]error, len(events))
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, event := range events {
		if event.Test || !motoGPUUID.MatchString(event.ID) {
			continue
		}
		wg.Add(1)
		go func(i int, event motoGPEvent) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				failures[i] = ctx.Err()
				return
			}
			defer func() { <-slots }()
			var sessions []motoGPSession
			if err := s.racingJSON(ctx, motoGPBase+"/results/sessions?eventUuid="+event.ID+"&categoryUuid="+categoryID, &sessions); err != nil {
				failures[i] = err
				return
			}
			result[i] = normalizeMotoGPSessions(event, sessions, time.Now())
			if len(sessions) == 0 || len(result[i].SubEvents) != len(sessions) {
				failures[i] = fmt.Errorf("MotoGP session schedule incomplete or lifecycle unsupported")
			}
		}(i, event)
	}
	wg.Wait()
	out := []SportsEvent{}
	for i, event := range result {
		if failures[i] != nil {
			return nil, failures[i]
		}
		if len(event.SubEvents) > 0 {
			out = append(out, event)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("MotoGP sessions unavailable")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartTime.Before(out[j].StartTime) })
	return out, nil
}

// applyMotoGPClassification keeps source positions, including missing ones, and
// never infers rank from array order.
func applyMotoGPClassification(session *SportsEvent, raw motoGPClassification) {
	session.Participants = []SportsParticipant{}
	for _, row := range raw.Classification {
		if !motoGPUUID.MatchString(row.Rider.ID) || row.Rider.Name == "" {
			continue
		}
		participant := SportsParticipant{ID: "motogp:" + row.Rider.ID, Name: row.Rider.Name, Team: row.Team.Name}
		if row.Rider.Number != nil {
			participant.Number = strconv.Itoa(*row.Rider.Number)
		}
		if row.Position != nil && *row.Position > 0 && *row.Position <= 1000 {
			participant.Position = *row.Position
		}
		participant.Winner = participant.Position == 1 && session.Status == SportsGameFinal
		// INSTND means classified, not a lap-time value.
		if row.Status != "INSTND" {
			participant.Result = row.Status
		}
		add := func(name, label, value string) {
			if value != "" {
				participant.Statistics = append(participant.Statistics, SportsRaceStatistic{Name: name, Label: label, Value: value})
			}
		}
		if row.TotalLaps != nil {
			add("lapsCompleted", "Laps", strconv.Itoa(*row.TotalLaps))
		}
		add("totalTime", "Elapsed time", row.Time)
		add("fastestLap", "Best lap", row.BestLap.Time)
		if row.Gap.First != "0.000" {
			add("behindTime", "Time behind", row.Gap.First)
		}
		if row.Gap.Lap != "0" {
			add("behindLaps", "Laps behind", row.Gap.Lap)
		}
		if row.Points != nil {
			add("points", "Points", strconv.FormatFloat(*row.Points, 'f', -1, 64))
		}
		session.Participants = append(session.Participants, participant)
	}
}

func (s *Service) getMotoGPSession(ctx context.Context, eventID, sessionID string) (SportsEvent, error) {
	if !motoGPUUID.MatchString(eventID) || !motoGPUUID.MatchString(sessionID) {
		return SportsEvent{}, fmt.Errorf("invalid MotoGP identity")
	}
	board := s.GetRaceBoard(ctx)
	var session SportsEvent
	for _, event := range board.Events {
		if event.League == "motogp" && event.ProviderEventID == eventID {
			for _, candidate := range event.SubEvents {
				if candidate.SessionID == sessionID {
					session = candidate
					session.Stale = event.Stale
				}
			}
		}
	}
	if session.ID == "" {
		return session, fmt.Errorf("MotoGP session not found in enabled calendar")
	}
	if session.Status == SportsGameScheduled {
		return session, nil
	}
	s.raceDetailMu.Lock()
	defer s.raceDetailMu.Unlock()
	if s.raceDetails == nil {
		s.raceDetails = map[string]raceSessionEntry{}
	}
	old, exists := s.raceDetails[session.ID]
	if exists && time.Now().Before(old.expires) {
		out := old.event
		out.Stale = out.Stale || session.Stale
		return out, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var raw motoGPClassification
	err := s.racingJSON(requestCtx, motoGPBase+"/results/session/"+sessionID+"/classification?seasonYear="+
		strconv.Itoa(session.StartTime.Year())+"&test=false", &raw)
	if err == nil && len(raw.Classification) > 0 {
		applyMotoGPClassification(&session, raw)
	}
	ttl := 10 * time.Minute
	if err != nil || len(session.Participants) == 0 {
		if exists {
			session = old.event
		}
		session.Stale = true
		ttl = time.Minute
	} else {
		session.UpdatedAt = time.Now()
	}
	if len(s.raceDetails) >= 32 {
		for key := range s.raceDetails {
			delete(s.raceDetails, key)
			break
		}
	}
	s.raceDetails[session.ID] = raceSessionEntry{event: session, expires: time.Now().Add(ttl)}
	return session, nil
}

// ---------- MotoGP championship standings and circuit guide ----------

type MotoGPStanding struct {
	Number      *int     `json:"number,omitempty"`
	Position    *int     `json:"position,omitempty"`
	Name        string   `json:"name"`
	RiderID     string   `json:"riderId"`
	Team        string   `json:"team"`
	Constructor string   `json:"constructor"`
	Points      *float64 `json:"points,omitempty"`
	Wins        *int     `json:"wins,omitempty"`
	Podiums     *int     `json:"podiums,omitempty"`
}

type MotoGPCircuit struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	MapURL                string `json:"mapUrl,omitempty"`
	LengthMeters          *int   `json:"lengthMeters,omitempty"`
	LeftCorners           *int   `json:"leftCorners,omitempty"`
	RightCorners          *int   `json:"rightCorners,omitempty"`
	LongestStraightMeters *int   `json:"longestStraightMeters,omitempty"`
}

type MotoGPEnrichment struct {
	UpdatedAt *time.Time       `json:"updatedAt,omitempty"`
	Circuit   *MotoGPCircuit   `json:"circuit,omitempty"`
	State     string           `json:"state"`
	Source    string           `json:"source"`
	SourceURL string           `json:"sourceUrl"`
	Reason    string           `json:"reason,omitempty"`
	Standings []MotoGPStanding `json:"standings,omitempty"`
	Season    int              `json:"season"`
}

type motoGPEnrichmentEntry struct {
	expires time.Time
	value   MotoGPEnrichment
}

type motoGPEnrichmentCache struct {
	entries map[string]motoGPEnrichmentEntry
	mu      sync.Mutex
}

type motoGPBroadcast struct {
	ID      string `json:"id"`
	Circuit *struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Track *struct {
			Length   string `json:"lenght"`
			Left     string `json:"left_corners"`
			Right    string `json:"right_corners"`
			Straight string `json:"longest_straight"`
			Assets   struct {
				Simple struct {
					Path string `json:"path"`
				} `json:"simple"`
			} `json:"assets"`
		} `json:"track"`
	} `json:"circuit"`
}

func officialMotoGPMap(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "photos.motogp.com" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/events-admin/") ||
		!strings.HasSuffix(strings.ToLower(parsed.Path), ".png") {
		return ""
	}
	return raw
}

func motoGPPositive(raw string, zero bool) *int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || (!zero && n == 0) {
		return nil
	}
	return &n
}

func joinedMotoGPCircuit(eventID string, events []struct {
	ID          string `json:"id"`
	BroadcastID string `json:"toad_api_uuid"`
}, broadcasts []motoGPBroadcast) *MotoGPCircuit {
	broadcastID := ""
	for _, event := range events {
		if event.ID == eventID {
			broadcastID = event.BroadcastID
			break
		}
	}
	if !motoGPUUID.MatchString(broadcastID) {
		return nil
	}
	for _, broadcast := range broadcasts {
		if broadcast.ID != broadcastID || broadcast.Circuit == nil || broadcast.Circuit.Track == nil {
			continue
		}
		c, t := broadcast.Circuit, broadcast.Circuit.Track
		return &MotoGPCircuit{
			ID: c.ID, Name: c.Name, MapURL: officialMotoGPMap(t.Assets.Simple.Path),
			LengthMeters: motoGPPositive(t.Length, false), LeftCorners: motoGPPositive(t.Left, true),
			RightCorners: motoGPPositive(t.Right, true), LongestStraightMeters: motoGPPositive(t.Straight, false),
		}
	}
	return nil
}

func (s *Service) motoGPSeasonIDs(ctx context.Context, year int) (string, string, error) {
	var seasons []motoGPSeason
	if err := s.racingJSON(ctx, motoGPBase+"/results/seasons", &seasons); err != nil {
		return "", "", err
	}
	seasonID := ""
	for _, season := range seasons {
		if season.Year == year && motoGPUUID.MatchString(season.ID) {
			seasonID = season.ID
			break
		}
	}
	if seasonID == "" {
		return "", "", fmt.Errorf("current season unavailable")
	}
	var categories []motoGPCategory
	if err := s.racingJSON(ctx, motoGPBase+"/results/categories?seasonUuid="+seasonID, &categories); err != nil {
		return "", "", err
	}
	for _, category := range categories {
		if category.LegacyID == 3 && motoGPUUID.MatchString(category.ID) {
			return seasonID, category.ID, nil
		}
	}
	return "", "", fmt.Errorf("MotoGP category unavailable")
}

// GetMotoGPEnrichment returns either the current championship table (empty
// event id) or one event's official circuit guide. Both are optional extras and
// keep the last good snapshot as stale when a refresh fails.
func (s *Service) GetMotoGPEnrichment(ctx context.Context, eventID string) MotoGPEnrichment {
	year := time.Now().UTC().Year()
	value := MotoGPEnrichment{
		State: "unavailable", Season: year, Source: "MotoGP",
		SourceURL: "https://www.motogp.com/en/world-standing",
		Reason:    "Current-season championship data unavailable",
	}
	if eventID != "" {
		value.SourceURL = motoGPBase + "/events?seasonYear=" + strconv.Itoa(year)
		value.Reason = "Official circuit guide unavailable"
		if !motoGPUUID.MatchString(eventID) {
			return value
		}
	}
	s.motoGPExtra.mu.Lock()
	defer s.motoGPExtra.mu.Unlock()
	key := strconv.Itoa(year) + ":" + eventID
	if s.motoGPExtra.entries == nil {
		s.motoGPExtra.entries = map[string]motoGPEnrichmentEntry{}
	}
	old := s.motoGPExtra.entries[key]
	if time.Now().Before(old.expires) {
		return old.value
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	seasonID, categoryID, err := s.motoGPSeasonIDs(ctx, year)
	if err == nil && eventID == "" {
		var raw struct {
			Classification []struct {
				Position *int     `json:"position"`
				Points   *float64 `json:"points"`
				Wins     *int     `json:"race_wins"`
				Podiums  *int     `json:"podiums"`
				Rider    struct {
					ID     string `json:"id"`
					Name   string `json:"full_name"`
					Number *int   `json:"number"`
				} `json:"rider"`
				Team struct {
					Name string `json:"name"`
				} `json:"team"`
				Constructor struct {
					Name string `json:"name"`
				} `json:"constructor"`
			} `json:"classification"`
		}
		err = s.racingJSON(ctx, motoGPBase+"/results/standings?seasonUuid="+seasonID+"&categoryUuid="+categoryID, &raw)
		if err == nil {
			if len(raw.Classification) == 0 || len(raw.Classification) > 100 {
				err = fmt.Errorf("unexpected standings size")
			} else {
				for _, row := range raw.Classification {
					if !motoGPUUID.MatchString(row.Rider.ID) || strings.TrimSpace(row.Rider.Name) == "" {
						err = fmt.Errorf("invalid rider")
						value.Standings = nil
						break
					}
					value.Standings = append(value.Standings, MotoGPStanding{
						RiderID: row.Rider.ID, Name: row.Rider.Name, Number: row.Rider.Number,
						Team: row.Team.Name, Constructor: row.Constructor.Name,
						Position: row.Position, Points: row.Points, Wins: row.Wins, Podiums: row.Podiums,
					})
				}
			}
		}
	} else if err == nil {
		var events []struct {
			ID          string `json:"id"`
			BroadcastID string `json:"toad_api_uuid"`
		}
		var broadcasts []motoGPBroadcast
		err = s.racingJSON(ctx, motoGPBase+"/results/events?seasonUuid="+seasonID, &events)
		if err == nil {
			err = s.racingJSON(ctx, value.SourceURL, &broadcasts)
		}
		if err == nil {
			value.Circuit = joinedMotoGPCircuit(eventID, events, broadcasts)
			if value.Circuit == nil {
				err = fmt.Errorf("circuit identity unavailable")
			}
		}
	}
	ttl := 15 * time.Minute
	if err == nil {
		now := time.Now().UTC()
		value.State = "available"
		value.Reason = ""
		value.UpdatedAt = &now
	} else {
		value.Standings = nil
		value.Circuit = nil
		ttl = time.Minute
		if old.value.UpdatedAt != nil {
			value = old.value
			value.State = "stale"
			value.Reason = "Showing previously retrieved MotoGP data; refresh unavailable"
		}
	}
	// Only current-season entries, with a fixed maximum independent of input.
	if len(s.motoGPExtra.entries) >= 64 {
		for key := range s.motoGPExtra.entries {
			delete(s.motoGPExtra.entries, key)
			break
		}
	}
	s.motoGPExtra.entries[key] = motoGPEnrichmentEntry{value: value, expires: time.Now().Add(ttl)}
	return value
}
