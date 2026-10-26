package sports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Boxing is served by TheSportsDB's free boxing league (4445). Its coverage is
// a card schedule only: no live round, punch or judge data is invented.
type boxingSchedule struct {
	Events []boxingEvent `json:"events"`
}

type boxingEvent struct {
	ID        string `json:"idEvent"`
	LeagueID  string `json:"idLeague"`
	Title     string `json:"strEvent"`
	Timestamp string `json:"strTimestamp"`
	Venue     string `json:"strVenue"`
	Status    string `json:"strStatus"`
	Result    string `json:"strResult"`
	Postponed string `json:"strPostponed"`
}

func (s *Service) fetchBoxingDate(ctx context.Context, date string) ([]SportsGame, error) {
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	endpoint := "https://www.thesportsdb.com/api/v1/json/123/eventsday.php?" +
		url.Values{"d": {date}, "l": {"4445"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("boxing schedule HTTP %d", resp.StatusCode)
	}
	var data boxingSchedule
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&data); err != nil {
		return nil, err
	}
	games := []SportsGame{}
	for _, event := range data.Events {
		if game, ok := normalizeBoxingEvent(event); ok {
			games = append(games, game)
		}
	}
	return games, nil
}

func normalizeBoxingEvent(event boxingEvent) (SportsGame, bool) {
	if event.ID == "" || event.LeagueID != "4445" || strings.TrimSpace(event.Title) == "" {
		return SportsGame{}, false
	}
	// TheSportsDB timestamps are UTC even when the ISO string omits Z.
	stamp := event.Timestamp
	if len(stamp) == 19 {
		stamp += "Z"
	}
	start := parseESPNDate(stamp)
	if start.IsZero() {
		return SportsGame{}, false
	}
	state := SportsGameScheduled
	label := "Scheduled · limited coverage"
	if strings.EqualFold(event.Postponed, "yes") {
		label = "Postponed"
	} else if strings.EqualFold(event.Status, "Match Finished") || strings.EqualFold(event.Status, "FT") {
		state = SportsGameFinal
		label = "Final"
		if event.Result != "" {
			label = event.Result
		}
	}
	// A start time in the past is not evidence of a live or completed bout.
	return SportsGame{
		ID: "boxing:" + event.ID, League: "boxing", Sport: "boxing", EventKind: "fight-card",
		Title: event.Title, StartTime: start, Status: state, StatusDetail: label, VenueName: event.Venue,
		Detail: &SportsGameDetail{
			Source: "thesportsdb", UpdatedAt: time.Now(),
			Periods: []SportsPeriodScore{}, Plays: []SportsDetailPlay{}, Comparisons: []SportsComparison{},
			CoverageNote: "Limited boxing schedule coverage. Live round and punch statistics are not supplied.",
		},
	}, true
}
