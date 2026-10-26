package sports

import (
	"embed"
	"encoding/json"
	"fmt"
	"time"
)

// Official published calendars, not simulated results. Keep the original
// verification date in responses; never roll a calendar into another year or
// promote elapsed races to final/live.
//
//go:embed calendars/organizer-2026.json
var organizerCalendars embed.FS

const organizerCalendarFile = "calendars/organizer-2026.json"

type publishedCyclingCalendar struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Category   string    `json:"category"`
	Kind       string    `json:"kind"`
	Year       int       `json:"year"`
	SourceURL  string    `json:"sourceURL"`
	VerifiedAt time.Time `json:"verifiedAt"`
	RestDays   []string  `json:"restDays"`
	Stages     []struct {
		Name      string `json:"name"`
		Date      string `json:"date"`
		Departure string `json:"departure"`
		Arrival   string `json:"arrival"`
	} `json:"stages"`
}

func publishedCyclingRace(id string, year int, now time.Time) (CyclingRace, error) {
	raw, err := organizerCalendars.ReadFile(organizerCalendarFile)
	if err != nil {
		return CyclingRace{}, err
	}
	var calendars []publishedCyclingCalendar
	if err := json.Unmarshal(raw, &calendars); err != nil {
		return CyclingRace{}, err
	}
	for _, calendar := range calendars {
		if calendar.ID != id || calendar.Year != year {
			continue
		}
		evidence := cyclingEvidence{Provider: "Official organizer · published schedule", ObservedAt: calendar.VerifiedAt}
		race := CyclingRace{
			ID: fmt.Sprintf("%s:%d", cyclingLeagueID(id), year), Name: calendar.Name,
			Category: calendar.Category, RaceKind: calendar.Kind, SourceURL: calendar.SourceURL,
			Source: evidence, ScheduleOnly: true, RestDays: calendar.RestDays, Stages: []CyclingStage{},
		}
		for i, row := range calendar.Stages {
			day, err := time.Parse("2006-01-02", row.Date)
			if err != nil || day.Year() != year {
				return CyclingRace{}, fmt.Errorf("invalid published calendar date")
			}
			state := "unknown"
			if row.Date > now.UTC().Format("2006-01-02") {
				state = "scheduled"
			}
			stage := CyclingStage{
				ID: fmt.Sprintf("%s:%d", race.ID, i+1), Name: row.Name, Date: row.Date,
				Departure: row.Departure, Arrival: row.Arrival, Status: state,
				Results: cyclingResults{
					State: "unavailable", Source: evidence,
					Reason: "Schedule coverage only. Verified results are not connected for this event.",
				},
				GeneralClassification: cyclingResults{
					State: "unavailable", Source: evidence,
					Reason: "Overall classification is not supplied by this schedule source.",
				},
			}
			race.Stages = append(race.Stages, stage)
			if race.StartDate == "" || row.Date < race.StartDate {
				race.StartDate = row.Date
			}
			if row.Date > race.EndDate {
				race.EndDate = row.Date
			}
		}
		return race, nil
	}
	return CyclingRace{}, fmt.Errorf("no verified calendar for %s %d", id, year)
}
