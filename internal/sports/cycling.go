package sports

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cycling is served by the race organizers' own feeds. Every module carries its
// own state and evidence, so a published schedule is never presented as a
// result and a cancelled or future stage is never presented as live.

type cyclingEvidence struct {
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
	Provider   string     `json:"provider"`
	ObservedAt time.Time  `json:"observedAt"`
}

type cyclingResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Team        string `json:"team,omitempty"`
	Time        string `json:"time,omitempty"`
	Gap         string `json:"gap,omitempty"`
	Nationality string `json:"nationality,omitempty"`
	Bib         int    `json:"bib,omitempty"`
	Rank        int    `json:"rank,omitempty"`
}

type cyclingResults struct {
	Source cyclingEvidence `json:"source"`
	State  string          `json:"state"`
	Reason string          `json:"reason,omitempty"`
	Data   []cyclingResult `json:"data,omitempty"`
}

type cyclingTeamResult struct {
	ID     string `json:"id"`
	TeamID string `json:"teamId"`
	Name   string `json:"name"`
	Time   string `json:"time,omitempty"`
	Gap    string `json:"gap,omitempty"`
	Rank   int    `json:"rank,omitempty"`
}

type cyclingTeamResults struct {
	Source cyclingEvidence     `json:"source"`
	State  string              `json:"state"`
	Reason string              `json:"reason,omitempty"`
	Data   []cyclingTeamResult `json:"data,omitempty"`
}

// CyclingRouteGuide is the organizer's own static route artwork. It is never
// generated or approximated locally.
type CyclingRouteGuide struct {
	Source     cyclingEvidence `json:"source"`
	State      string          `json:"state"`
	SourceURL  string          `json:"sourceUrl"`
	MapURL     string          `json:"mapUrl,omitempty"`
	ProfileURL string          `json:"profileUrl,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

type CyclingStage struct {
	TeamResults                *cyclingTeamResults `json:"teamResults,omitempty"`
	RouteGuide                 *CyclingRouteGuide  `json:"routeGuide,omitempty"`
	ID                         string              `json:"id"`
	Name                       string              `json:"name"`
	Terrain                    string              `json:"terrain,omitempty"`
	Date                       string              `json:"date,omitempty"`
	Departure                  string              `json:"departure,omitempty"`
	Arrival                    string              `json:"arrival,omitempty"`
	Distance                   string              `json:"distance,omitempty"`
	Status                     string              `json:"status"`
	GeneralClassificationLabel string              `json:"generalClassificationLabel,omitempty"`
	Results                    cyclingResults      `json:"results"`
	GeneralClassification      cyclingResults      `json:"generalClassification"`
}

type CyclingRace struct {
	Source         cyclingEvidence `json:"source"`
	RaceKind       string          `json:"raceKind"`
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Category       string          `json:"category"`
	StartDate      string          `json:"startDate,omitempty"`
	EndDate        string          `json:"endDate,omitempty"`
	Country        string          `json:"country,omitempty"`
	Classification string          `json:"classification,omitempty"`
	SourceURL      string          `json:"sourceUrl"`
	Stages         []CyclingStage  `json:"stages"`
	RestDays       []string        `json:"restDays,omitempty"`
	CalendarOnly   bool            `json:"calendarOnly,omitempty"`
	ScheduleOnly   bool            `json:"scheduleOnly,omitempty"`
}

type CyclingCoverage struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type CyclingFeed struct {
	Coverage []CyclingCoverage `json:"coverage"`
	State    string            `json:"state"`
	Source   cyclingEvidence   `json:"source"`
	Reason   string            `json:"reason,omitempty"`
	Data     []CyclingRace     `json:"data"`
}

type cyclingCompetition struct {
	id, name, category, host string
	oneDay                   bool
}

var cyclingCompetitions = []cyclingCompetition{
	{"tour", "Tour de France", "men", "racecenter.letour.fr", false},
	{"vuelta", "La Vuelta", "men", "racecenter.lavuelta.es", false},
	{"tour-femmes", "Tour de France Femmes", "women", "racecenter.letourfemmes.fr", false},
	{"paris-nice", "Paris-Nice", "men", "racecenter.paris-nice.fr", false},
	{"vuelta-femenina", "La Vuelta Femenina", "women", "racecenter.lavueltafemenina.es", false},
	{"paris-roubaix", "Paris-Roubaix", "men", "racecenter.paris-roubaix.fr", true},
	{"paris-roubaix-femmes", "Paris-Roubaix Femmes", "women", "racecenter.paris-roubaix-femmes.fr", true},
	{"liege-bastogne-liege", "Liège-Bastogne-Liège", "men", "racecenter.liege-bastogne-liege.be", true},
	{"liege-bastogne-liege-femmes", "Liège-Bastogne-Liège Femmes", "women", "racecenter.liege-bastogne-liege-femmes.be", true},
	{"fleche-wallonne", "La Flèche Wallonne", "men", "racecenter.la-fleche-wallonne.be", true},
	{"fleche-wallonne-femmes", "La Flèche Wallonne Femmes", "women", "racecenter.la-fleche-wallonne-femmes.be", true},
	{"giro", "Giro d’Italia", "men", "www.giroditalia.it", false},
	{"cro-race", "CRO Race", "men", "crorace.com", false},
	{"road-worlds", "UCI Road World Championships", "unknown", "www.montreal2026.org", false},
}

type cyclingBoardCache struct {
	expires time.Time
	race    CyclingRace
	stale   bool
}

type cyclingStageCache struct {
	expires time.Time
	stage   CyclingStage
}

type cyclingGuideCache struct {
	expires time.Time
	guide   CyclingRouteGuide
}

type cyclingCache struct {
	boards       map[string]cyclingBoardCache
	stages       map[string]cyclingStageCache
	guides       map[string]cyclingGuideCache
	subscription cyclingSubscriptionCache
	mu           sync.Mutex
	detailMu     sync.Mutex
}

type asoStage struct {
	Type      string  `json:"type"`
	ID        string  `json:"_id"`
	Date      string  `json:"date"`
	Length    float64 `json:"lengthDisplay"`
	Stage     int     `json:"stage"`
	UpdatedAt int64   `json:"_updatedAt"`
	Cancelled bool    `json:"isCancelled"`
	Departure struct {
		Label string `json:"label"`
	} `json:"departureCity"`
	Arrival struct {
		Label string `json:"label"`
	} `json:"arrivalCity"`
}

type asoRanking struct {
	ID              string   `json:"_id"`
	Bind            string   `json:"_bind"`
	Type            string   `json:"type"`
	Types           []string `json:"types"`
	Checkpoint      string   `json:"$cp"`
	Firstname       string   `json:"firstname"`
	Lastname        string   `json:"lastname"`
	Name            string   `json:"name"`
	Team            string   `json:"$team"`
	Rider           string   `json:"$rider"`
	UpdatedAt       int64    `json:"_updatedAt"`
	Bib             int      `json:"bib"`
	Nationality     string   `json:"nationality"`
	CheckpointTypes []struct {
		Type string `json:"type"`
		Code string `json:"code"`
	} `json:"checkpointTypes"`
	Rankings []struct {
		Rider    string   `json:"$rider"`
		Absolute *float64 `json:"absolute"`
		Relative *float64 `json:"relative"`
		Position int      `json:"position"`
	} `json:"rankings"`
}

func cyclingSource(now time.Time, updated int64) cyclingEvidence {
	evidence := cyclingEvidence{Provider: "ASO race center", ObservedAt: now}
	if updated > 0 {
		stamp := time.UnixMilli(updated)
		evidence.UpdatedAt = &stamp
	}
	return evidence
}

func cyclingPending(evidence cyclingEvidence, reason string) cyclingResults {
	return cyclingResults{State: "pending", Source: evidence, Reason: reason}
}

func normalizeCyclingSchedule(raw []asoStage, competition cyclingCompetition, year int, now time.Time) CyclingRace {
	race := CyclingRace{
		RaceKind: "stage-race", ID: fmt.Sprintf("aso:%s:%d", competition.id, year), Name: competition.name,
		Category: competition.category, Stages: []CyclingStage{}, Source: cyclingSource(now, 0),
		SourceURL: "https://" + competition.host + "/en/",
	}
	if competition.oneDay {
		race.RaceKind = "one-day"
	}
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].Stage < raw[j].Stage })
	seen := map[int]bool{}
	latest := int64(0)
	for _, item := range raw {
		if item.Stage < 1 || item.Stage > 40 || item.ID == "" || len(item.Date) < 10 || seen[item.Stage] {
			continue
		}
		date := item.Date[:10]
		if _, err := time.Parse("2006-01-02", date); err != nil || !strings.HasPrefix(date, strconv.Itoa(year)+"-") {
			continue
		}
		seen[item.Stage] = true
		evidence := cyclingSource(now, item.UpdatedAt)
		terrain := map[string]string{"PLN": "Flat", "VAL": "Hilly", "HMG": "Mountain", "PAS": "Individual time trial", "EQU": "Team time trial", "REP": "Rest day"}[item.Type]
		stage := CyclingStage{
			Terrain: terrain, ID: race.ID + ":" + strconv.Itoa(item.Stage), Name: fmt.Sprintf("Stage %d", item.Stage),
			Date: date, Departure: item.Departure.Label, Arrival: item.Arrival.Label, Status: "unknown",
			Results:                    cyclingPending(evidence, "Select this stage to load published results."),
			GeneralClassification:      cyclingPending(evidence, "Select this stage to load the overall classification."),
			GeneralClassificationLabel: fmt.Sprintf("After stage %d", item.Stage),
		}
		if site, ok := asoGuideSites[competition.id]; ok {
			stage.RouteGuide = &CyclingRouteGuide{
				State: "pending", Source: evidence,
				SourceURL: fmt.Sprintf("https://%s/en/stage-%d", site.host, item.Stage),
			}
		}
		if competition.oneDay {
			stage.Name = "Race"
			markCyclingOneDay(&stage)
		}
		if item.Cancelled {
			stage.Status = "cancelled"
		} else if date > now.UTC().Format("2006-01-02") {
			stage.Status = "scheduled"
		}
		if item.Length > 0 && item.Length < 1000 {
			stage.Distance = strconv.FormatFloat(item.Length, 'f', -1, 64) + " km"
		}
		if item.UpdatedAt > latest {
			latest = item.UpdatedAt
		}
		race.Stages = append(race.Stages, stage)
	}
	if len(race.Stages) > 0 {
		race.StartDate = race.Stages[0].Date
		race.EndDate = race.Stages[len(race.Stages)-1].Date
	}
	race.Source = cyclingSource(now, latest)
	return race
}

func cyclingLeagueID(id string) string {
	switch id {
	case "cro-race":
		return "cro:cro-race"
	case "road-worlds":
		return "uci:road-worlds"
	case "giro":
		return "rcs:" + id
	}
	return "aso:" + id
}

func (s *Service) enabledCyclingCompetitions() []cyclingCompetition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// A zero-value service is used by provider tests; production always has a
	// league list.
	if s.leagues == nil {
		return cyclingCompetitions
	}
	enabled := map[string]bool{}
	for _, league := range s.leagues {
		enabled[league.ID] = true
	}
	out := []cyclingCompetition{}
	for _, competition := range cyclingCompetitions {
		if enabled[cyclingLeagueID(competition.id)] {
			out = append(out, competition)
		}
	}
	return out
}

// GetCycling returns every enabled organizer race for the current season.
// Boards are cached for five minutes; a failed refresh keeps the last verified
// schedule and marks it stale.
func (s *Service) GetCycling(ctx context.Context) CyclingFeed {
	competitions := s.enabledCyclingCompetitions()
	now := time.Now()
	year := now.UTC().Year()
	cache := &s.cycling
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.boards == nil {
		cache.boards = map[string]cyclingBoardCache{}
	}
	feed := CyclingFeed{
		State: "available", Source: cyclingEvidence{Provider: "Cycling organizers", ObservedAt: now},
		Data: []CyclingRace{}, Coverage: []CyclingCoverage{},
	}
	// A shared network budget keeps slow organizers from serializing timeouts.
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	boards := make([]cyclingBoardCache, len(competitions))
	var workers sync.WaitGroup
	slots := make(chan struct{}, 4)
	var subscription cyclingSubscriptionCache
	if len(competitions) > 0 {
		workers.Add(1)
		slots <- struct{}{}
		go func() {
			defer workers.Done()
			defer func() { <-slots }()
			subscription = s.cyclingSubscription(requestCtx, year, now)
		}()
	}
	for i, competition := range competitions {
		key := fmt.Sprintf("%s:%d", competition.id, year)
		old, exists := cache.boards[key]
		boards[i] = old
		if exists && now.Before(old.expires) {
			continue
		}
		// Locally published calendars must not wait behind remote timeouts.
		if competition.id == "cro-race" || competition.id == "road-worlds" {
			if race, err := publishedCyclingRace(competition.id, year, now); err == nil {
				old = cyclingBoardCache{race: race, expires: now.Add(5 * time.Minute)}
			} else {
				old.stale = true
				old.expires = now.Add(30 * time.Second)
			}
			boards[i] = old
			continue
		}
		workers.Add(1)
		go func(i int, competition cyclingCompetition, old cyclingBoardCache) {
			defer workers.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-requestCtx.Done():
				old.stale = true
				old.expires = now.Add(30 * time.Second)
				boards[i] = old
				return
			}
			providerCtx, providerCancel := context.WithTimeout(requestCtx, 6*time.Second)
			var race CyclingRace
			var err error
			if competition.id == "giro" {
				var doc *htmlDoc
				doc, err = s.giroHTML(providerCtx, "the-route/")
				if err == nil {
					race, err = normalizeGiroSchedule(doc, year, now)
				}
			} else {
				var raw []asoStage
				err = s.racingJSON(providerCtx, fmt.Sprintf("https://%s/api/stage-%d", competition.host, year), &raw)
				if err == nil {
					race = normalizeCyclingSchedule(raw, competition, year, now)
				}
			}
			providerCancel()
			if err == nil {
				if len(race.Stages) == 0 {
					err = fmt.Errorf("no valid stages")
				} else {
					old = cyclingBoardCache{race: race, expires: now.Add(5 * time.Minute)}
				}
			}
			if err != nil {
				old.stale = true
				old.expires = now.Add(30 * time.Second)
			}
			boards[i] = old
		}(i, competition, old)
	}
	workers.Wait()
	for i, competition := range competitions {
		old := boards[i]
		cache.boards[fmt.Sprintf("%s:%d", competition.id, year)] = old
		state := "available"
		if old.race.ID == "" {
			// Nothing was ever verified for this competition: it is unavailable,
			// not stale, and it must not make the whole feed look delayed.
			state = "unavailable"
		} else {
			if old.stale {
				state = "stale"
				feed.State = "stale"
			}
			feed.Data = append(feed.Data, old.race)
		}
		feed.Coverage = append(feed.Coverage, CyclingCoverage{ID: competition.id, Name: competition.name, State: state})
	}
	if len(competitions) > 0 {
		cache.subscription = subscription
		mergeCyclingSubscription(&feed, subscription, competitions, year)
	}
	if len(feed.Data) == 0 {
		feed.State = "unavailable"
		feed.Reason = "Cycling race schedules could not be reached."
		if len(competitions) == 0 {
			feed.Reason = "Cycling is disabled in server league availability."
		}
	} else if feed.State == "stale" {
		feed.Reason = "Some organizer schedules could not be refreshed. The last verified calendar is shown."
	}
	sort.SliceStable(feed.Data, func(i, j int) bool {
		if feed.Data[i].StartDate != feed.Data[j].StartDate {
			return feed.Data[i].StartDate < feed.Data[j].StartDate
		}
		return feed.Data[i].Name < feed.Data[j].Name
	})
	// Retain current-season cache entries only.
	for key := range cache.boards {
		if !strings.HasSuffix(key, ":"+strconv.Itoa(year)) {
			delete(cache.boards, key)
		}
	}
	return feed
}

func markCyclingOneDay(stage *CyclingStage) {
	stage.GeneralClassification = cyclingResults{
		State: "unavailable", Source: stage.Results.Source,
		Reason: "This one-day race has no overall stage classification.",
	}
	stage.GeneralClassificationLabel = ""
}

func cyclingTime(ms *float64, gap bool) string {
	if ms == nil || *ms < 0 || math.IsNaN(*ms) || math.IsInf(*ms, 0) || *ms > 1e12 || (!gap && *ms == 0) {
		return ""
	}
	seconds := int64(*ms / 1000)
	h := seconds / 3600
	m := (seconds % 3600) / 60
	sec := seconds % 60
	value := fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	if gap {
		if h == 0 {
			value = fmt.Sprintf("%d:%02d", m, sec)
		}
		value = "+" + value
	}
	if remainder := int64(*ms) % 1000; remainder != 0 {
		value += fmt.Sprintf(".%03d", remainder)
	}
	return value
}

// normalizeCyclingResults accepts only a single arrival checkpoint per
// classification. Conflicting checkpoints leave the module pending rather than
// guessing between them.
func normalizeCyclingResults(raw []asoRanking, stage *CyclingStage, year, number int, now time.Time) {
	records := map[string]asoRanking{}
	for _, record := range raw {
		records[record.Bind+":"+record.ID] = record
	}
	for _, kind := range []string{"ite", "itg", "ttt"} {
		if kind == "ttt" && stage.Terrain != "Team time trial" {
			continue
		}
		result := cyclingResults{State: "pending", Source: cyclingSource(now, 0), Reason: "No finish classification was found in this feed capture."}
		matches := []asoRanking{}
		for _, record := range raw {
			if record.Bind != fmt.Sprintf("rankingType-%d-%d", year, number) || record.Type != kind {
				continue
			}
			checkpoint, ok := records[record.Checkpoint]
			if !ok {
				continue
			}
			arrival := false
			for _, t := range checkpoint.CheckpointTypes {
				if t.Type == "arrival" && t.Code == "A" {
					arrival = true
				}
			}
			hasArrivalType := false
			for _, t := range record.Types {
				if t == "A" {
					hasArrivalType = true
				}
			}
			if arrival && hasArrivalType {
				matches = append(matches, record)
			}
		}
		if len(matches) == 1 {
			ranking := matches[0]
			result.Source = cyclingSource(now, ranking.UpdatedAt)
			result.Data = []cyclingResult{}
			seen := map[string]bool{}
			partial := false
			for _, entry := range ranking.Rankings {
				rider, ok := records[entry.Rider]
				identity := entry.Rider
				if kind == "ttt" {
					// ASO TTT rows reference a representative rider; resolve the team
					// without presenting the representative as an individual result.
					team, teamOK := records[rider.Team]
					identity = rider.Team
					if !ok || !teamOK || team.Bind != fmt.Sprintf("team-%d", year) || team.Name == "" || seen[identity] {
						partial = true
						continue
					}
					rider.Firstname, rider.Lastname = team.Name, ""
				}
				if !ok || rider.Firstname == "" || (kind != "ttt" && rider.Lastname == "") || seen[identity] {
					partial = true
					continue
				}
				seen[identity] = true
				rank := entry.Position
				if rank < 0 {
					rank = 0
				}
				gap := ""
				if rank > 0 {
					gap = cyclingTime(entry.Relative, true)
				}
				bib, nationality := 0, ""
				if kind != "ttt" {
					if rider.Bib > 0 && rider.Bib < 10000 {
						bib = rider.Bib
					}
					code := strings.ToUpper(strings.TrimSpace(rider.Nationality))
					if len(code) == 3 && strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
						nationality = code
					}
				}
				result.Data = append(result.Data, cyclingResult{
					Bib: bib, Nationality: nationality, ID: stage.ID + ":" + identity,
					Name: strings.TrimSpace(rider.Firstname + " " + rider.Lastname),
					Team: records[rider.Team].Name, Rank: rank,
					Time: cyclingTime(entry.Absolute, false), Gap: gap,
				})
			}
			if len(result.Data) > 0 {
				result.State = "available"
				result.Reason = ""
				if partial {
					result.State = "stale"
					result.Reason = "Some riders could not be resolved."
				}
				sort.SliceStable(result.Data, func(i, j int) bool {
					a, b := result.Data[i].Rank, result.Data[j].Rank
					if a == 0 {
						return false
					}
					if b == 0 {
						return true
					}
					return a < b
				})
			}
		}
		switch kind {
		case "ite":
			stage.Results = result
		case "ttt":
			stage.TeamResults = &cyclingTeamResults{State: result.State, Source: result.Source, Reason: result.Reason}
			for _, row := range result.Data {
				stage.TeamResults.Data = append(stage.TeamResults.Data, cyclingTeamResult{
					ID: row.ID, TeamID: strings.TrimPrefix(row.ID, stage.ID+":"), Name: row.Name,
					Rank: row.Rank, Time: row.Time, Gap: row.Gap,
				})
			}
			if result.State == "stale" {
				stage.TeamResults.Reason = "Some teams could not be resolved."
			}
		default:
			stage.GeneralClassification = result
		}
	}
}

// GetCyclingStage returns one stage with its published results, overall
// classification and organizer route guide. Scheduled and cancelled stages only
// load the guide; no result is invented.
func (s *Service) GetCyclingStage(ctx context.Context, raceID string, year, number int) (CyclingStage, error) {
	if year != time.Now().UTC().Year() || number < 1 || number > 40 {
		return CyclingStage{}, fmt.Errorf("unsupported cycling date or stage")
	}
	// Resolve calendar-only entries from the trusted board before organizer lookup.
	if number == 1 {
		for _, race := range s.GetCycling(ctx).Data {
			parts := strings.Split(race.ID, ":")
			if len(parts) == 3 && parts[1] == raceID && race.CalendarOnly && len(race.Stages) > 0 {
				return race.Stages[0], nil
			}
		}
	}
	var competition cyclingCompetition
	for _, candidate := range cyclingCompetitions {
		if candidate.id == raceID {
			competition = candidate
		}
	}
	if competition.id == "" {
		return CyclingStage{}, fmt.Errorf("unsupported cycling race")
	}
	board := s.GetCycling(ctx)
	id := fmt.Sprintf("%s:%d:%d", cyclingLeagueID(raceID), year, number)
	var stage CyclingStage
	for _, race := range board.Data {
		for _, candidate := range race.Stages {
			if candidate.ID == id {
				stage = candidate
			}
		}
	}
	if stage.ID == "" {
		return stage, fmt.Errorf("stage not found")
	}
	if raceID == "cro-race" || raceID == "road-worlds" {
		return stage, nil
	}
	cache := &s.cycling
	cache.detailMu.Lock()
	defer cache.detailMu.Unlock()
	if cache.stages == nil {
		cache.stages = map[string]cyclingStageCache{}
	}
	now := time.Now()
	old, exists := cache.stages[id]
	if exists && now.Before(old.expires) {
		return old.stage, nil
	}
	if stage.Status == "cancelled" || stage.Status == "scheduled" {
		s.enrichCyclingGuide(ctx, &stage, year, number, now)
		return stage, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var err error
	if competition.id == "giro" {
		err = s.enrichGiroStage(requestCtx, &stage, year, number, now)
	} else {
		var raw []asoRanking
		err = s.racingJSON(requestCtx, fmt.Sprintf("https://%s/api/rankingType-%d-%d", competition.host, year, number), &raw)
		if err == nil {
			normalizeCyclingResults(raw, &stage, year, number, now)
		}
	}
	ttl := 5 * time.Minute
	if err != nil {
		ttl = 30 * time.Second
		if exists {
			stage = old.stage
		}
		if stage.TeamResults != nil {
			if len(stage.TeamResults.Data) > 0 {
				stage.TeamResults.State = "stale"
			} else {
				stage.TeamResults.State = "unavailable"
			}
			stage.TeamResults.Reason = "The cycling results feed could not be reached."
		}
		for _, module := range []*cyclingResults{&stage.Results, &stage.GeneralClassification} {
			if len(module.Data) > 0 {
				module.State = "stale"
			} else {
				module.State = "unavailable"
			}
			module.Reason = "The cycling results feed could not be reached."
		}
	}
	if competition.id == "giro" && err == nil && stage.GeneralClassification.State == "unavailable" && exists && len(old.stage.GeneralClassification.Data) > 0 {
		reason := stage.GeneralClassification.Reason
		stage.GeneralClassification = old.stage.GeneralClassification
		stage.GeneralClassification.State = "stale"
		stage.GeneralClassification.Reason = reason
		ttl = 30 * time.Second
	}
	if competition.oneDay {
		markCyclingOneDay(&stage)
	}
	s.enrichCyclingGuide(ctx, &stage, year, number, now)
	if stage.RouteGuide != nil && (stage.RouteGuide.State == "stale" || stage.RouteGuide.State == "unavailable") {
		ttl = 30 * time.Second
	}
	if len(cache.stages) >= 32 {
		oldest := ""
		var expiry time.Time
		for key, value := range cache.stages {
			if oldest == "" || value.expires.Before(expiry) {
				oldest = key
				expiry = value.expires
			}
		}
		delete(cache.stages, oldest)
	}
	cache.stages[id] = cyclingStageCache{stage: stage, expires: now.Add(ttl)}
	return stage, nil
}

// FindCyclingStage resolves a stage directly from the board for watch links.
func (s *Service) FindCyclingStage(ctx context.Context, id, parentID string) (CyclingRace, CyclingStage, bool) {
	for _, race := range s.GetCycling(ctx).Data {
		if parentID != "" && race.ID != parentID {
			continue
		}
		for _, stage := range race.Stages {
			if stage.ID == id && stage.Status != "cancelled" {
				return race, stage, true
			}
		}
	}
	return CyclingRace{}, CyclingStage{}, false
}
