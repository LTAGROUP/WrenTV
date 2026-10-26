package sports

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Public subscription published at https://inrng.com/calendar/. The publisher
// issues a new calendar each year: an old edition is never rolled forward.
var cyclingSubscriptionURLs = map[int]string{
	2026: "https://calendar.google.com/calendar/ical/5c9dc1a627cf55f1653d17573c2df58075d949559ec87e484b0cf90fa78bbf6d%40group.calendar.google.com/public/basic.ics",
}

type cyclingSubscriptionCache struct {
	FetchedAt time.Time
	Expires   time.Time
	Year      int
	Races     []CyclingRace
	Stale     bool
}

type cyclingSubscriptionDisk struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Body      string    `json:"body"`
}

var cyclingClass = regexp.MustCompile(`\s*\((1|2)\.(UWT|WWT|Pro|1|2)\)$`)

func calendarText(s string) string {
	return strings.NewReplacer(`\n`, " ", `\N`, " ", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}

// This feed is deliberately a date-only calendar, not a timing source. DTEND is
// exclusive in iCalendar. Stage numbers, rest days and live status are never
// synthesized from it.
func parseCyclingSubscription(body string, year int, observed time.Time) ([]CyclingRace, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(body, "BEGIN:VCALENDAR\n") || !strings.Contains(body, "\nEND:VCALENDAR") {
		return nil, fmt.Errorf("invalid cycling calendar envelope")
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\n ", ""), "\n\t", "")
	races := []CyclingRace{}
	seen := map[string]bool{}
	for _, block := range strings.Split(body, "BEGIN:VEVENT\n")[1:] {
		if !strings.Contains(block, "END:VEVENT") {
			return nil, fmt.Errorf("truncated cycling event")
		}
		fields := map[string]string{}
		for _, line := range strings.Split(strings.SplitN(block, "END:VEVENT", 2)[0], "\n") {
			p := strings.SplitN(line, ":", 2)
			if len(p) == 2 {
				fields[p[0]] = p[1]
			}
		}
		// Recurring/timed entries are rejected rather than interpreted as dates.
		if fields["RRULE"] != "" || fields["RECURRENCE-ID"] != "" {
			continue
		}
		start, err := time.Parse("20060102", fields["DTSTART;VALUE=DATE"])
		if err != nil || start.Year() != year {
			continue
		}
		end := start.AddDate(0, 0, 1)
		if value := fields["DTEND;VALUE=DATE"]; value != "" {
			end, err = time.Parse("20060102", value)
			if err != nil {
				continue
			}
		}
		if !end.After(start) || end.Sub(start) > 35*24*time.Hour {
			continue
		}
		uid, title := fields["UID"], strings.TrimSpace(calendarText(fields["SUMMARY"]))
		if uid == "" || title == "" || len(title) > 250 || seen[uid] {
			continue
		}
		seen[uid] = true
		class := cyclingClass.FindStringSubmatch(title)
		category := "unknown"
		lower := strings.ToLower(title)
		if strings.Contains(lower, "women") || strings.Contains(lower, "fem") ||
			strings.Contains(lower, "donne") || strings.Contains(title, ".WWT") {
			category = "women"
		} else if strings.Contains(title, ".UWT") || strings.Contains(lower, "men's") {
			category = "men"
		}
		if len(class) > 0 {
			title = strings.TrimSpace(cyclingClass.ReplaceAllString(title, ""))
		}
		// Make same-named men's/women's events distinguishable in cards and searches.
		if category == "women" && !strings.Contains(lower, "women") && !strings.Contains(lower, "fem") && !strings.Contains(lower, "donne") {
			title += " Women"
		}
		hash := sha256.Sum256([]byte(uid))
		id := fmt.Sprintf("calendar:road-%x:%d", hash[:10], year)
		evidence := cyclingEvidence{Provider: "The Inner Ring · public calendar", ObservedAt: observed}
		if updated, e := time.Parse("20060102T150405Z", fields["LAST-MODIFIED"]); e == nil {
			evidence.UpdatedAt = &updated
		}
		race := CyclingRace{
			ID: id, Name: title, Category: category, RaceKind: "one-day", ScheduleOnly: true,
			CalendarOnly: true, Country: calendarText(fields["LOCATION"]),
			StartDate: start.Format("2006-01-02"), EndDate: end.AddDate(0, 0, -1).Format("2006-01-02"),
			Source: evidence, SourceURL: "https://inrng.com/calendar/",
		}
		if len(class) > 0 {
			race.Classification = class[1] + "." + class[2]
		}
		if race.EndDate != race.StartDate {
			race.RaceKind = "stage-race"
		}
		status := "unknown"
		if race.StartDate > observed.UTC().Format("2006-01-02") {
			status = "scheduled"
		}
		if fields["STATUS"] == "CANCELLED" {
			status = "cancelled"
		}
		race.Stages = []CyclingStage{{
			ID: id + ":1", Name: "Race schedule", Date: race.StartDate, Status: status,
			Results: cyclingResults{
				State: "unavailable", Source: evidence,
				Reason: "Calendar coverage only. Search for streams to watch; live timing and results are not supplied.",
			},
			GeneralClassification: cyclingResults{
				State: "unavailable", Source: evidence,
				Reason: "Standings are not supplied by this calendar.",
			},
		}}
		races = append(races, race)
	}
	if len(races) == 0 {
		return nil, fmt.Errorf("no valid cycling events for %d", year)
	}
	sort.Slice(races, func(i, j int) bool {
		if races[i].StartDate != races[j].StartDate {
			return races[i].StartDate < races[j].StartDate
		}
		return races[i].ID < races[j].ID
	})
	return races, nil
}

// Called while the board mutex is held. A six-hour refresh suits a date
// calendar; failures retain the last fetch for at most seven days.
func (s *Service) cyclingSubscription(ctx context.Context, year int, now time.Time) cyclingSubscriptionCache {
	old := s.cycling.subscription
	if old.Year != year {
		old = cyclingSubscriptionCache{Year: year}
	}
	path := ""
	if s.storageDir != "" {
		path = filepath.Join(s.storageDir, sportsCacheDir, fmt.Sprintf("cycling-calendar-%d.json", year))
	}
	if old.FetchedAt.IsZero() && path != "" {
		if raw, err := os.ReadFile(path); err == nil && len(raw) < 3*1024*1024 {
			var saved cyclingSubscriptionDisk
			if json.Unmarshal(raw, &saved) == nil && !saved.FetchedAt.After(now) && now.Sub(saved.FetchedAt) < 7*24*time.Hour {
				if races, e := parseCyclingSubscription(saved.Body, year, saved.FetchedAt); e == nil {
					old.Races = races
					old.FetchedAt = saved.FetchedAt
					old.Expires = saved.FetchedAt.Add(6 * time.Hour)
				}
			}
		}
	}
	if now.Before(old.Expires) {
		if now.Sub(old.FetchedAt) >= 7*24*time.Hour {
			old.Races = nil
		}
		return old
	}
	old.Stale = true
	old.Expires = now.Add(5 * time.Minute)
	if now.Sub(old.FetchedAt) >= 7*24*time.Hour {
		old.Races = nil
	}
	source := cyclingSubscriptionURLs[year]
	if source == "" || s.client == nil {
		return old
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, source, nil)
	if err != nil {
		return old
	}
	req.Header.Set("Accept", "text/calendar")
	resp, err := s.client.Do(req)
	if err != nil {
		return old
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return old
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return old
	}
	races, err := parseCyclingSubscription(string(raw), year, now)
	if err != nil {
		return old
	}
	next := cyclingSubscriptionCache{Races: races, FetchedAt: now, Expires: now.Add(6 * time.Hour), Year: year}
	if path != "" {
		if data, e := json.Marshal(cyclingSubscriptionDisk{Body: string(raw), FetchedAt: now}); e == nil {
			if os.WriteFile(path+".tmp", data, 0o600) == nil {
				_ = os.Rename(path+".tmp", path)
			}
		}
	}
	return next
}

// Known organizer identities take precedence, including corrected dates.
// Disabled named competitions must not reappear through the supplemental
// calendar.
func calendarOrganizerID(race CyclingRace) string {
	name := strings.ToLower(race.Name)
	if strings.HasPrefix(name, "world championships:") {
		return "road-worlds"
	}
	aliases := map[string]string{
		"tour de france": "tour", "tour de france femmes": "tour-femmes",
		"vuelta a españa": "vuelta", "vuelta españa femenina": "vuelta-femenina",
		"giro d'italia": "giro", "paris-nice": "paris-nice", "paris-roubaix": "paris-roubaix",
		"paris-roubaix women":        "paris-roubaix-femmes",
		"liège-bastogne-liège":       "liege-bastogne-liege",
		"liège-bastogne-liège women": "liege-bastogne-liege-femmes",
		"la flèche wallonne":         "fleche-wallonne",
		"la flèche wallonne women":   "fleche-wallonne-femmes",
		"cro race":                   "cro-race",
	}
	return aliases[name]
}

func mergeCyclingSubscription(feed *CyclingFeed, sub cyclingSubscriptionCache, enabled []cyclingCompetition, year int) {
	allowed := map[string]bool{}
	for _, competition := range enabled {
		allowed[competition.id] = true
	}
	present := map[string]bool{}
	for _, race := range feed.Data {
		present[race.ID] = true
	}
	for _, race := range sub.Races {
		if known := calendarOrganizerID(race); known != "" {
			if !allowed[known] {
				continue
			}
			id := fmt.Sprintf("%s:%d", cyclingLeagueID(known), year)
			if present[id] {
				continue
			}
			// Worlds has several calendar entries; retain their separate identities
			// when its full organizer schedule is unavailable.
			if known != "road-worlds" {
				race.ID = id
				race.Stages = append([]CyclingStage(nil), race.Stages...)
				race.Stages[0].ID = id + ":1"
			}
		}
		feed.Data = append(feed.Data, race)
	}
	state := "available"
	if sub.Stale {
		state = "stale"
	}
	if len(sub.Races) == 0 {
		state = "unavailable"
	}
	feed.Coverage = append(feed.Coverage, CyclingCoverage{ID: "road-calendar", Name: "Road cycling calendar", State: state})
	if sub.Stale && len(sub.Races) > 0 {
		feed.State = "stale"
	}
}
