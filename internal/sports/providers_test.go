package sports

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func jsonUnmarshal(raw string, target any) error { return json.Unmarshal([]byte(raw), target) }

func mustParseHTML(t *testing.T, raw string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

func TestBoxingScheduleKeepsCardContract(t *testing.T) {
	fixture := readFixture(t, "boxing-day.json")
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Host, "thesportsdb") {
			if r.URL.Query().Get("d") != "2026-09-19" || r.URL.Query().Get("l") != "4445" {
				return 200, `{"events":[]}`
			}
			return 200, fixture
		}
		return 200, `{"events":[]}`
	})
	games, err := s.fetchBoxingDate(context.Background(), "2026-09-19")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) == 0 {
		t.Fatal("fixture produced no boxing cards")
	}
	for _, game := range games {
		if !strings.HasPrefix(game.ID, "boxing:") || game.League != "boxing" || game.EventKind != "fight-card" {
			t.Fatalf("unexpected card %+v", game)
		}
		if game.StartTime.IsZero() {
			t.Fatalf("card %s lost its start time", game.ID)
		}
		if game.Detail == nil || !strings.Contains(game.Detail.CoverageNote, "Live round") {
			t.Fatalf("card %s does not disclose limited coverage", game.ID)
		}
		if game.Status == SportsGameLive {
			t.Fatalf("boxing schedule must never claim a live bout: %+v", game)
		}
	}
	// TheSportsDB timestamps omit Z; they are still UTC.
	if got := games[0].StartTime.UTC().Hour(); got != 16 && got != 15 {
		t.Fatalf("unexpected start hour %d", got)
	}
	// A foreign league id is not boxing 4445.
	if game, ok := normalizeBoxingEvent(boxingEvent{ID: "1", LeagueID: "4328", Title: "Other", Timestamp: "2026-09-19T16:00:00"}); ok {
		t.Fatalf("accepted a non-boxing league: %+v", game)
	}
	// A past start time is not evidence of a final result.
	if game, _ := normalizeBoxingEvent(boxingEvent{ID: "2", LeagueID: "4445", Title: "A vs B", Timestamp: "2020-01-01T00:00:00"}); game.Status != SportsGameScheduled {
		t.Fatalf("past bout promoted to %s", game.Status)
	}
}

func TestCFLFixturesTeamsAndStandings(t *testing.T) {
	fixtures := readFixture(t, "cfl-fixtures-2026.json")
	teams := readFixture(t, "cfl-teams.json")
	standings := readFixture(t, "cfl-standings-2026.json")
	s := testService(t, func(r *http.Request) (int, string) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			return 200, teams
		case strings.HasPrefix(r.URL.Path, "/fixtures/"):
			return 200, fixtures
		case strings.HasPrefix(r.URL.Path, "/standings/"):
			return 200, standings
		case strings.HasSuffix(r.URL.Path, "/seasons"):
			return 200, `[2025,2026]`
		default:
			return 404, `{}`
		}
	})
	games, err := s.fetchCFLScoreboardDate(context.Background(), "2026-06-04")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) == 0 {
		t.Fatal("no CFL fixtures for the captured day")
	}
	final := 0
	for _, game := range games {
		if game.League != cflLeagueID || game.Sport != "football" || game.EventKind != "matchup" {
			t.Fatalf("unexpected CFL game %+v", game)
		}
		if game.HomeTeam.Name == "" || game.AwayTeam.Name == "" || strings.Contains(game.Title, " at  at ") {
			t.Fatalf("CFL team identity incomplete: %+v", game)
		}
		if logo := game.HomeTeam.LogoURL; logo != "" && !strings.HasPrefix(logo, "https://content.cfl.ca/") {
			t.Fatalf("CFL promoted an untrusted logo %q", logo)
		}
		if game.Status == SportsGameFinal {
			final++
			if game.HomeTeam.Score == "" || game.AwayTeam.Score == "" {
				t.Fatalf("finished CFL game without scores: %+v", game)
			}
			if game.HomeTeam.Winner == game.AwayTeam.Winner {
				t.Fatalf("winner flag inconsistent: %+v", game)
			}
		}
	}
	if final == 0 {
		t.Fatal("fixture contains a finished game but none normalised as final")
	}

	records, err := s.fetchCFLTeams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 9 {
		t.Fatalf("CFL team catalog = %d", len(records))
	}
	for _, record := range records {
		if record.League != cflLeagueID || !strings.HasPrefix(record.ID, cflLeagueID+":cfl:") {
			t.Fatalf("unexpected CFL identity %+v", record)
		}
	}

	table := s.GetLeagueStandings(context.Background(), cflLeagueID)
	if table.State != "available" || len(table.Groups) == 0 {
		t.Fatalf("CFL standings unavailable: %+v", table)
	}
	for _, group := range table.Groups {
		if group.Season != 2026 || group.Title == "" || len(group.Rows) == 0 || len(group.Columns) == 0 {
			t.Fatalf("incomplete standings group %+v", group)
		}
		for _, column := range group.Columns {
			if column.Key == "rank" && column.Label != "Rank" {
				t.Fatalf("column label changed: %+v", column)
			}
		}
	}
	if other := s.GetLeagueStandings(context.Background(), "nfl"); other.State != "unavailable" || len(other.Groups) != 0 {
		t.Fatalf("unsupported league produced a table: %+v", other)
	}
}

func TestRaceBoardNormalizesWeekendAndCircuit(t *testing.T) {
	fixture := readFixture(t, "f1-racing-captured.json")
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/racing/f1/scoreboard") {
			return 200, fixture
		}
		return 200, `{"events":[]}`
	})
	board := s.GetRaceBoard(context.Background())
	if len(board.Events) != 1 {
		t.Fatalf("race board events = %d", len(board.Events))
	}
	weekend := board.Events[0]
	if weekend.League != "f1" || weekend.EventKind != "race" || weekend.ProviderEventID != "600057442" {
		t.Fatalf("unexpected weekend %+v", weekend)
	}
	if weekend.Title != "Pirelli Italian Grand Prix" {
		t.Fatalf("weekend identity lost: %+v", weekend)
	}
	if len(weekend.SubEvents) < 2 {
		t.Fatalf("weekend sessions = %d", len(weekend.SubEvents))
	}
	for i := 1; i < len(weekend.SubEvents); i++ {
		if weekend.SubEvents[i].StartTime.Before(weekend.SubEvents[i-1].StartTime) {
			t.Fatal("race sessions are not ordered")
		}
	}
	var race *SportsEvent
	for i := range weekend.SubEvents {
		if strings.EqualFold(weekend.SubEvents[i].SessionType, "Race") {
			race = &weekend.SubEvents[i]
		}
	}
	if race == nil {
		t.Fatal("weekend has no race session")
	}
	if !weekend.StartTime.Equal(race.StartTime) || weekend.Status != race.Status {
		t.Fatal("weekend does not reflect its main race")
	}
	if weekend.Circuit == nil || weekend.Circuit.ID != "it-1922" || len(weekend.Circuit.Coordinates) < 3 {
		t.Fatalf("verified circuit geometry missing: %+v", weekend.Circuit)
	}
	if weekend.Circuit.Attribution == "" || !strings.HasPrefix(weekend.Circuit.SourceURL, "https://") {
		t.Fatal("circuit geometry lacks provenance")
	}
	if race.Circuit == nil || race.Circuit.ID != weekend.Circuit.ID {
		t.Fatal("session lost the circuit reference")
	}
	// Provider order is not a classification.
	for _, participant := range race.Participants {
		if participant.Position != 0 {
			t.Fatalf("scoreboard order became a race position: %+v", participant)
		}
	}
}

func TestRaceStatisticsOnlyUseVerifiedColumns(t *testing.T) {
	var stats raceStatistics
	stats.Splits.Categories = append(stats.Splits.Categories, struct {
		Stats []struct {
			Name         string  `json:"name"`
			Value        float64 `json:"value"`
			DisplayValue string  `json:"displayValue"`
		} `json:"stats"`
	}{Stats: []struct {
		Name         string  `json:"name"`
		Value        float64 `json:"value"`
		DisplayValue string  `json:"displayValue"`
	}{
		{Name: "place", Value: 3, DisplayValue: "3"},
		{Name: "lapsCompleted", Value: 0, DisplayValue: "0"},
		{Name: "pitsTaken", Value: 0, DisplayValue: "0"},
		{Name: "fastestLap", Value: 0, DisplayValue: ""},
		{Name: "behindTime", Value: 0, DisplayValue: "0.000"},
		{Name: "behindLaps", Value: 1, DisplayValue: "1"},
		{Name: "unknownStat", Value: 5, DisplayValue: "5"},
	}})
	driver := SportsParticipant{ID: "1", Name: "Driver"}
	applyRaceStatistics(&driver, stats, "Race")
	if driver.Position != 3 {
		t.Fatalf("place not applied: %+v", driver)
	}
	labels := map[string]string{}
	for _, stat := range driver.Statistics {
		labels[stat.Label] = stat.Value
	}
	if labels["Laps"] != "0" || labels["Pit stops"] != "0" {
		t.Fatalf("real zero counts dropped: %+v", driver.Statistics)
	}
	if _, ok := labels["Time behind"]; ok {
		t.Fatal("placeholder zero gap was shown")
	}
	if len(driver.Statistics) != 3 {
		t.Fatalf("unexpected statistics %+v", driver.Statistics)
	}
	// Practice sessions do not carry race-count columns.
	driver = SportsParticipant{ID: "2", Name: "Driver"}
	applyRaceStatistics(&driver, stats, "Practice 3")
	for _, stat := range driver.Statistics {
		if stat.Label == "Pit stops" || stat.Label == "Laps led" {
			t.Fatalf("race count shown in practice: %+v", driver.Statistics)
		}
	}
}

func TestMotoGPSessionsAndClassification(t *testing.T) {
	var sessions []motoGPSession
	raw := readFixture(t, "motogp-sessions.json")
	if err := jsonUnmarshal(raw, &sessions); err != nil {
		t.Fatal(err)
	}
	event := motoGPEvent{
		ID:   "f3fd8ba7-2966-46bd-8687-b92047f5e733",
		Name: "Aragon Grand Prix",
	}
	event.Circuit.Name = "MotorLand Aragón"
	weekend := normalizeMotoGPSessions(event, sessions, time.Now())
	if weekend.League != "motogp" || weekend.EventKind != "race" || len(weekend.SubEvents) == 0 {
		t.Fatalf("unexpected MotoGP weekend %+v", weekend)
	}
	for _, session := range weekend.SubEvents {
		if !motoGPUUID.MatchString(session.SessionID) || session.SessionType == "" {
			t.Fatalf("invalid MotoGP session %+v", session)
		}
		if session.Status != SportsGameScheduled && session.Status != SportsGameFinal {
			t.Fatalf("unsupported lifecycle leaked: %s", session.Status)
		}
	}
	var classification motoGPClassification
	if err := jsonUnmarshal(readFixture(t, "motogp-classification.json"), &classification); err != nil {
		t.Fatal(err)
	}
	if len(classification.Classification) == 0 {
		t.Fatal("classification fixture empty")
	}
	target := SportsEvent{ID: "motogp:event:session", Status: SportsGameFinal}
	applyMotoGPClassification(&target, classification)
	if len(target.Participants) == 0 {
		t.Fatal("no classified riders")
	}
	first := target.Participants[0]
	if first.Position != 1 || !first.Winner || first.Name == "" || first.ID == "" {
		t.Fatalf("winner missing: %+v", first)
	}
	for _, participant := range target.Participants {
		for _, stat := range participant.Statistics {
			if stat.Label == "" || stat.Value == "" {
				t.Fatalf("empty statistic: %+v", participant)
			}
		}
	}
}

func TestF1ArchiveRequiresExactSessionMatch(t *testing.T) {
	index := `{"Meetings":[{"Sessions":[
		{"Name":"Qualifying","StartDate":"2026-09-05T14:00:00","GmtOffset":"00:00:00","Path":"2026/ITALY/Qualifying/"},
		{"Name":"Race","StartDate":"2026-09-06T13:00:00","GmtOffset":"00:00:00","Path":"2026/ITALY/Race/"}
	]}]}`
	s := testService(t, func(r *http.Request) (int, string) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/Index.json"):
			return 200, "\ufeff" + index
		case strings.HasPrefix(r.URL.Path, "/static/2026/ITALY/Race/"):
			return 200, "\ufeff{\"ok\":true}"
		default:
			return 404, `{}`
		}
	})
	start := time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
	archive := s.GetF1Archive(context.Background(), SportsEvent{
		ID: "f1:600057442:401839101", League: "f1", SessionType: "Race",
		StartTime: start, Status: SportsGameFinal,
	})
	if archive.State != "available" || archive.Mode != "archive" || archive.SessionName != "Race" {
		t.Fatalf("archive not resolved: %+v", archive)
	}
	if !strings.Contains(archive.SourceURL, "2026/ITALY/Race/") || len(archive.Channels) == 0 {
		t.Fatalf("archive channels missing: %+v", archive)
	}
	// A live or scheduled session never resolves to an archive.
	if live := s.GetF1Archive(context.Background(), SportsEvent{ID: "x", League: "f1", SessionType: "Race", StartTime: start, Status: SportsGameLive}); live.State != "unavailable" {
		t.Fatalf("live session returned an archive: %+v", live)
	}
	// A different start time must not reuse the nearest weekend.
	if wrong := s.GetF1Archive(context.Background(), SportsEvent{
		ID: "f1:y", League: "f1", SessionType: "Race", Status: SportsGameFinal,
		StartTime: start.Add(48 * time.Hour),
	}); wrong.State == "available" {
		t.Fatalf("archive matched the wrong session: %+v", wrong)
	}
}

func TestCyclingScheduleAndResults(t *testing.T) {
	var stages []asoStage
	if err := jsonUnmarshal(readFixture(t, "aso-paris-nice-stages.json"), &stages); err != nil {
		t.Fatal(err)
	}
	competition := cyclingCompetition{id: "paris-nice", name: "Paris-Nice", category: "men", host: "racecenter.paris-nice.fr"}
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	race := normalizeCyclingSchedule(stages, competition, 2026, now)
	if race.ID != "aso:paris-nice:2026" || race.RaceKind != "stage-race" || len(race.Stages) != len(stages) {
		t.Fatalf("unexpected race %+v", race)
	}
	if race.StartDate == "" || race.EndDate == "" || race.StartDate > race.EndDate {
		t.Fatalf("race window invalid: %+v", race)
	}
	for _, stage := range race.Stages {
		if !strings.HasPrefix(stage.ID, race.ID+":") || stage.Date == "" || stage.Status == "" {
			t.Fatalf("invalid stage %+v", stage)
		}
		if stage.Departure == "" || stage.Arrival == "" {
			t.Fatalf("stage geography lost: %+v", stage)
		}
		if stage.RouteGuide == nil || stage.RouteGuide.SourceURL == "" {
			t.Fatalf("ASO stage lost its organizer guide link: %+v", stage)
		}
	}

	var rankings []asoRanking
	if err := jsonUnmarshal(readFixture(t, "aso-paris-nice-results.json"), &rankings); err != nil {
		t.Fatal(err)
	}
	target := race.Stages[len(race.Stages)-1]
	normalizeCyclingResults(rankings, &target, 2026, len(race.Stages), now)
	if target.Results.State != "available" || len(target.Results.Data) == 0 {
		t.Fatalf("finish classification missing: %+v", target.Results)
	}
	if target.GeneralClassification.State != "available" || len(target.GeneralClassification.Data) == 0 {
		t.Fatalf("general classification missing: %+v", target.GeneralClassification)
	}
	if target.Results.Data[0].Rank != 1 {
		t.Fatalf("results not ranked: %+v", target.Results.Data[0])
	}
	for _, row := range target.Results.Data {
		if row.ID == "" || row.Name == "" {
			t.Fatalf("invalid rider %+v", row)
		}
		if row.Gap != "" && !strings.HasPrefix(row.Gap, "+") {
			t.Fatalf("gap format changed: %q", row.Gap)
		}
	}

	oneDay := cyclingCompetition{id: "paris-roubaix", name: "Paris-Roubaix", category: "men", host: "racecenter.paris-roubaix.fr", oneDay: true}
	single := normalizeCyclingSchedule(stages[:1], oneDay, 2026, now)
	if single.RaceKind != "one-day" || single.Stages[0].Name != "Race" {
		t.Fatalf("one-day race mislabeled: %+v", single)
	}
	if single.Stages[0].GeneralClassification.State != "unavailable" {
		t.Fatal("one-day race invented an overall classification")
	}
}

func TestPublishedCyclingCalendarAndSubscription(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	race, err := publishedCyclingRace("cro-race", 2026, now)
	if err != nil {
		t.Fatal(err)
	}
	if !race.ScheduleOnly || race.ID != "cro:cro-race:2026" || len(race.Stages) < 5 {
		t.Fatalf("unexpected published calendar %+v", race)
	}
	for _, stage := range race.Stages {
		if stage.Results.State != "unavailable" || stage.GeneralClassification.State != "unavailable" {
			t.Fatalf("published calendar invented results: %+v", stage)
		}
	}
	if _, err := publishedCyclingRace("cro-race", 2025, now); err == nil {
		t.Fatal("calendar rolled into another year")
	}

	ics := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:a\nSUMMARY:Tour de France (2.UWT)\nDTSTART;VALUE=DATE:20260704\nDTEND;VALUE=DATE:20260727\nLOCATION:France\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:b\nSUMMARY:Tour de France Femmes (2.WWT)\nDTSTART;VALUE=DATE:20260725\nDTEND;VALUE=DATE:20260803\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:c\nSUMMARY:Recurring race\nDTSTART;VALUE=DATE:20260710\nRRULE:FREQ=YEARLY\nEND:VEVENT\n" +
		"END:VCALENDAR\n"
	races, err := parseCyclingSubscription(ics, 2026, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(races) != 2 {
		t.Fatalf("calendar accepted %d races", len(races))
	}
	if races[0].RaceKind != "stage-race" || races[0].Category != "men" || races[0].Classification != "2.UWT" {
		t.Fatalf("men's stage race misparsed: %+v", races[0])
	}
	if races[1].Category != "women" || !strings.Contains(races[1].Name, "Femmes") {
		t.Fatalf("women's race misparsed: %+v", races[1])
	}
	if races[0].Stages[0].Results.State != "unavailable" {
		t.Fatal("calendar invented a result")
	}
	if _, err := parseCyclingSubscription("not a calendar", 2026, now); err == nil {
		t.Fatal("invalid calendar accepted")
	}
}

func TestGiroScheduleAndClassification(t *testing.T) {
	doc := mustParseHTML(t, readFixture(t, "giro-route.html"))
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	race, err := normalizeGiroSchedule(doc, 2026, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(race.Stages) != 21 || race.Stages[0].IDNumber() != 1 || race.Stages[20].IDNumber() != 21 {
		t.Fatalf("Giro schedule incomplete: %d stages", len(race.Stages))
	}
	if race.Stages[0].Terrain == "" || race.Stages[0].RouteGuide == nil {
		t.Fatalf("Giro stage context missing: %+v", race.Stages[0])
	}
	if _, err := normalizeGiroSchedule(doc, 2025, now); err == nil {
		t.Fatal("Giro edition mismatch accepted")
	}

	resultDoc := mustParseHTML(t, readFixture(t, "giro-stage21.html"))
	results, err := normalizeGiroClassification(resultDoc, "js-tab-classifica-ORARR", "rcs:giro:2026:21", 2026, now)
	if err != nil {
		t.Fatal(err)
	}
	if results.State != "available" || len(results.Data) == 0 {
		t.Fatalf("Giro stage classification missing: %+v", results)
	}
	if results.Data[0].Rank != 1 || !strings.HasPrefix(results.Data[0].Gap, "+") {
		t.Fatalf("unexpected Giro result %+v", results.Data[0])
	}
}

// Every active catalog row must resolve to an ingestion path this service
// actually implements. A documented row with no adapter would sit in the
// catalog as an empty lane forever, so routing is asserted per provider rather
// than trusted from the JSON.
func TestActiveCatalogResolvesToImplementedProvider(t *testing.T) {
	s := testService(t, func(*http.Request) (int, string) { return 200, `{"events":[]}` })
	cycling := map[string]bool{}
	for _, competition := range cyclingCompetitions {
		cycling[cyclingLeagueID(competition.id)] = true
	}
	kinds := map[string]bool{"matchup": true, "fight-card": true, "tournament": true, "race": true}
	providers := map[string]bool{"espn": true, "cfl": true, "thesportsdb": true, "organizer": true}
	checked := 0
	for _, league := range LeagueCatalog {
		if !league.active() {
			continue
		}
		checked++
		if !kinds[league.EventKind] {
			t.Errorf("%s has an unknown event kind %q", league.ID, league.EventKind)
		}
		if !providers[league.Provider] {
			t.Errorf("%s names an unimplemented provider %q", league.ID, league.Provider)
		}
		switch league.Provider {
		case "espn":
			if league.Slug == "" {
				t.Errorf("%s has no provider slug", league.ID)
			}
			if league.EventKind == "race" {
				// Racing is served by the race board, never the dated
				// scoreboard, and must be pollable there.
				if _, ok := s.racingLeague(league.ID); !ok {
					t.Errorf("%s is a race the race board cannot poll", league.ID)
				}
				if supportsHubLeague(league.ID) {
					t.Errorf("%s would be flattened into a hub matchup", league.ID)
				}
				continue
			}
			if !supportsHubLeague(league.ID) {
				t.Errorf("%s is not routable to the hub", league.ID)
			}
		case "cfl":
			if league.ID != cflLeagueID {
				t.Errorf("cfl league id changed: %s", league.ID)
			}
			if !supportsHubLeague(league.ID) {
				t.Errorf("%s is not routable to the hub", league.ID)
			}
		case "thesportsdb":
			if league.ID != "boxing" {
				t.Errorf("unexpected TheSportsDB league %s", league.ID)
			}
		case "organizer":
			if league.Sport == "cycling" {
				if !cycling[league.ID] {
					t.Errorf("%s has no organizer schedule adapter", league.ID)
				}
				continue
			}
			if _, ok := s.racingLeague(league.ID); !ok {
				t.Errorf("%s has no organizer race adapter", league.ID)
			}
		}
	}
	if checked < 260 {
		t.Fatalf("only %d active rows were checked", checked)
	}
	// Organizer competitions without a catalog league would never be enabled.
	seen := map[string]bool{}
	for _, league := range LeagueCatalog {
		seen[league.ID] = true
	}
	for _, competition := range cyclingCompetitions {
		if id := cyclingLeagueID(competition.id); !seen[id] {
			t.Errorf("cycling competition %s has no catalog row", id)
		}
	}
}

// A competition that has never loaded is unavailable, not stale; only a
// previously verified schedule that failed to refresh marks the feed delayed.
func TestCyclingFeedSeparatesUnavailableFromStale(t *testing.T) {
	year := time.Now().UTC().Year()
	stages := fmt.Sprintf(`[{"_id":"s1","stage":1,"date":"%d-03-08","lengthDisplay":120,"departureCity":{"label":"Nice"},"arrivalCity":{"label":"Isola"}}]`, year)
	newService := func(fail *bool) *Service {
		s := NewWithClient(t.TempDir(), &http.Client{Transport: fixtureTransport{body: func(r *http.Request) (int, string) {
			if *fail {
				return 503, `{}`
			}
			if strings.Contains(r.URL.Host, "racecenter.paris-nice.fr") && strings.Contains(r.URL.Path, "/api/stage-") {
				return 200, stages
			}
			return 200, `[]`
		}}})
		s.SetEnabledLeagueIDs([]string{"aso:paris-nice"})
		return s
	}
	fail := true
	broken := newService(&fail)
	feed := broken.GetCycling(context.Background())
	if feed.State != "unavailable" || len(feed.Data) != 0 {
		t.Fatalf("a first-load failure was not unavailable: %+v", feed.State)
	}
	if len(feed.Coverage) == 0 || feed.Coverage[0].ID != "paris-nice" || feed.Coverage[0].State != "unavailable" {
		t.Fatalf("first-load coverage state = %+v", feed.Coverage)
	}

	fail = false
	s := newService(&fail)
	feed = s.GetCycling(context.Background())
	if feed.State != "available" || len(feed.Data) != 1 || feed.Data[0].ID != "aso:paris-nice:"+fmt.Sprint(year) {
		t.Fatalf("live schedule not loaded: %+v", feed)
	}
	fail = true
	s.cycling.mu.Lock()
	key := fmt.Sprintf("paris-nice:%d", year)
	entry := s.cycling.boards[key]
	entry.expires = time.Now().Add(-time.Second)
	s.cycling.boards[key] = entry
	s.cycling.mu.Unlock()
	feed = s.GetCycling(context.Background())
	if feed.State != "stale" || feed.Reason == "" {
		t.Fatalf("refresh failure was not reported as stale: %q %q", feed.State, feed.Reason)
	}
	if len(feed.Data) != 1 || feed.Coverage[0].State != "stale" {
		t.Fatalf("last verified schedule was not retained: %+v %+v", feed.Data, feed.Coverage)
	}
}
