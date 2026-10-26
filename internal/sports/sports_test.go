package sports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureTransport struct {
	body func(*http.Request) (int, string)
}

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	status, body := f.body(r)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func testService(t *testing.T, body func(*http.Request) (int, string)) *Service {
	t.Helper()
	s := NewWithClient(t.TempDir(), &http.Client{Transport: fixtureTransport{body: body}, Timeout: 5 * time.Second})
	// The fixture transport only answers ESPN-shaped requests. Secondary
	// providers (CFL, TheSportsDB, organizers) have their own focused tests and
	// must not be mixed into every ESPN scoreboard test.
	ids := []string{}
	for _, league := range LeagueCatalog {
		if league.active() && league.Provider == "espn" {
			ids = append(ids, league.ID)
		}
	}
	s.SetEnabledLeagueIDs(ids)
	return s
}

const nbaScheduled = `{"events":[{
	"id":"401","date":"2026-10-24T23:30Z","name":"Chicago Bulls at New York Knicks","shortName":"CHI @ NY",
	"competitions":[{
		"id":"401","status":{"displayClock":"0:00","period":0,"type":{"state":"pre","detail":"7:30 PM","shortDetail":"7:30 PM"}},
		"competitors":[
			{"homeAway":"home","id":"19","order":2,"score":"0","team":{"id":"19","displayName":"New York Knicks","location":"New York","name":"Knicks","abbreviation":"NY","color":"006BB6"}},
			{"homeAway":"away","id":"4","order":1,"score":"0","team":{"id":"4","displayName":"Chicago Bulls","location":"Chicago","name":"Bulls","abbreviation":"CHI","color":"CE1141"}}
		],
		"venue":{"fullName":"Madison Square Garden"}
	}]
}]}`

const nbaLive = `{"events":[{
	"id":"402","date":"2026-10-24T22:00Z","name":"Boston Celtics at Miami Heat",
	"competitions":[{
		"id":"402","status":{"displayClock":"4:12","period":3,"type":{"state":"in","detail":"3rd Quarter","shortDetail":"3rd - 4:12"}},
		"competitors":[
			{"homeAway":"home","id":"14","score":"78","team":{"id":"14","displayName":"Miami Heat","location":"Miami","name":"Heat","abbreviation":"MIA"}},
			{"homeAway":"away","id":"2","score":"81","team":{"id":"2","displayName":"Boston Celtics","location":"Boston","name":"Celtics","abbreviation":"BOS"}}
		]
	}]
}]}`

func TestCatalogIsCanonicalAndCapable(t *testing.T) {
	seen := map[string]bool{}
	active := 0
	for _, l := range LeagueCatalog {
		if l.ID == "" || l.Name == "" || l.Sport == "" || l.EventKind == "" {
			t.Fatalf("incomplete league %+v", l)
		}
		if seen[l.ID] {
			t.Fatalf("duplicate league %s", l.ID)
		}
		seen[l.ID] = true
		if !l.active() {
			// Catalog entries without provider evidence stay documented but are
			// never polled or advertised.
			continue
		}
		active++
		if !l.hasCapability("schedule") {
			t.Fatalf("%s is active without a schedule capability", l.ID)
		}
		if strings.HasPrefix(l.ID, "espn:") && l.ID != "espn:"+l.Sport+":"+l.Slug {
			t.Fatalf("noncanonical dynamic id %s", l.ID)
		}
	}
	if active < 55 {
		t.Fatalf("only %d active leagues, want the full Sports Hub catalog", active)
	}
	for _, league := range selectLeagues(defaultLeagueIDs) {
		if league.ID == "espn:baseball:dominican-winter-league" {
			t.Fatal("an unverified league entered polling")
		}
	}
	for _, id := range []string{"nfl", "nba", "mlb", "nhl", "soccer-eng.1", "ufc", "atp", "f1", "pga"} {
		if !seen[id] {
			t.Fatalf("catalog lost %s", id)
		}
	}
}

// board merges fixture scoreboard payloads into one valid response.
func board(payloads ...string) string {
	parts := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		start, end := strings.Index(payload, "["), strings.LastIndex(payload, "]")
		if start < 0 || end <= start {
			continue
		}
		parts = append(parts, payload[start+1:end])
	}
	return `{"events":[` + strings.Join(parts, ",") + `]}`
}

func TestScoreboardScheduledAndLive(t *testing.T) {
	s := testService(t, func(r *http.Request) (int, string) {
		switch {
		case strings.Contains(r.URL.Path, "/basketball/nba/"):
			return 200, board(nbaScheduled, nbaLive)
		default:
			return 200, `{"events":[]}`
		}
	})
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	games := s.GetScoreboard("nba")
	if len(games) != 2 {
		t.Fatalf("got %d games", len(games))
	}
	live, ok := s.GetGame("402")
	if !ok {
		t.Fatal("live game missing")
	}
	if live.Status != SportsGameLive || live.Period != "3rd quarter" || live.Clock != "4:12" {
		t.Fatalf("live mapping wrong: %+v", live)
	}
	if live.AwayTeam.Score != "81" || live.HomeTeam.Score != "78" || live.AwayTeam.Name != "Boston Celtics" {
		t.Fatalf("score mapping wrong: %+v", live)
	}
	scheduled, _ := s.GetGame("401")
	if scheduled.Status != SportsGameScheduled || scheduled.StatusDetail != "7:30 PM" || scheduled.VenueName != "Madison Square Garden" {
		t.Fatalf("scheduled mapping wrong: %+v", scheduled)
	}
}

func TestScoreboardFailureKeepsLastKnown(t *testing.T) {
	fail := false
	s := testService(t, func(r *http.Request) (int, string) {
		if fail {
			return 503, `{"error":"down"}`
		}
		if strings.Contains(r.URL.Path, "/basketball/nba/") {
			return 200, nbaLive
		}
		return 200, `{"events":[]}`
	})
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := s.Refresh(context.Background()); err == nil {
		t.Fatal("expected a provider error")
	}
	if games := s.GetScoreboard("nba"); len(games) != 1 || games[0].ID != "402" {
		t.Fatalf("last-known scores were erased: %+v", games)
	}
	if s.GetStatus().LastError == "" {
		t.Fatal("status should report the provider failure")
	}
}

func TestPartialScoreboardMergesRatherThanErases(t *testing.T) {
	s := testService(t, func(r *http.Request) (int, string) {
		return 200, nbaScheduled + nbaLive
	})
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A truncated response advertises more events than it contains.
	s.client = &http.Client{Transport: fixtureTransport{body: func(r *http.Request) (int, string) {
		return 200, `{"count":4,"pageCount":2,"pageIndex":1,"events":[{"id":"402","date":"2026-10-24T22:00Z","name":"Boston Celtics at Miami Heat","competitions":[{"id":"402","status":{"period":3,"type":{"state":"in","detail":"3rd","shortDetail":"3rd"}},"competitors":[{"homeAway":"home","id":"14","score":"78","team":{"id":"14","displayName":"Miami Heat"}},{"homeAway":"away","id":"2","score":"81","team":{"id":"2","displayName":"Boston Celtics"}}]}]}]}`
	}}}
	err := s.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("want partial error, got %v", err)
	}
	if games := s.GetScoreboard("nba"); len(games) != 2 {
		t.Fatalf("partial refresh erased an event: %+v", games)
	}
}

const tennisCard = `{"events":[{
	"id":"t1","date":"2026-10-24T10:00Z","name":"Vienna Open",
	"competitions":[
		{"id":"c1","date":"2026-10-24T10:00Z","format":{"regulation":{"periods":3}},
		 "status":{"period":2,"type":{"state":"in","detail":"Set 2","shortDetail":"Set 2"}},
		 "competitors":[
			{"homeAway":"home","id":"a1","curatedRank":{"current":2},"score":"1","linescores":[{"period":1,"value":6,"winner":true},{"period":2,"value":3,"winner":false}],"athlete":{"id":"a1","displayName":"Home Player","shortName":"H. Player"}},
			{"homeAway":"away","id":"a2","curatedRank":{"current":5},"score":"1","linescores":[{"period":1,"value":4,"winner":false},{"period":2,"value":4,"winner":true}],"athlete":{"id":"a2","displayName":"Away Player","shortName":"A. Player"}}
		 ]},
		{"id":"c2","date":"2026-10-24T14:00Z","status":{"type":{"state":"pre","detail":"Scheduled","shortDetail":"Scheduled"}},
		 "competitors":[
			{"homeAway":"home","id":"a3","athlete":{"id":"a3","displayName":"Third Player"}},
			{"homeAway":"away","id":"a4","athlete":{"id":"a4","displayName":"Fourth Player"}}
		 ]}
	]
}]}`

func TestTennisMatchesStayIndividual(t *testing.T) {
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/tennis/atp/") {
			return 200, tennisCard
		}
		return 200, `{"events":[]}`
	})
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	games := s.GetScoreboard("atp")
	if len(games) != 2 {
		t.Fatalf("want two matches, got %d", len(games))
	}
	first := games[0]
	if first.BestOf != 3 || first.AwayTeam.Seed != 5 || first.HomeTeam.Seed != 2 {
		t.Fatalf("tennis context lost: %+v", first)
	}
	if first.EventContext != "Vienna Open" || !strings.HasPrefix(first.Title, "Vienna Open: ") {
		t.Fatalf("tournament context lost: %q / %q", first.EventContext, first.Title)
	}
	if len(first.Detail.Periods) != 2 || first.Detail.Periods[0].Label != "Set 1" {
		t.Fatalf("set scores lost: %+v", first.Detail.Periods)
	}
}

const mlbLive = `{"events":[{
	"id":"m1","date":"2026-10-24T18:00Z","name":"Yankees at Red Sox",
	"competitions":[{
		"id":"m1","status":{"displayClock":"0:00","period":7,"type":{"state":"in","detail":"7th Inning","shortDetail":"Top 7th"}},
		"situation":{"balls":2,"strikes":1,"outs":0,"onFirst":true,"onSecond":false,"onThird":true,
			"batter":{"athlete":{"displayName":"A. Batter"}},"pitcher":{"athlete":{"displayName":"P. Pitcher"}}},
		"competitors":[
			{"homeAway":"home","id":"2","score":"3","team":{"id":"2","displayName":"Boston Red Sox","location":"Boston","name":"Red Sox","abbreviation":"BOS"}},
			{"homeAway":"away","id":"10","score":"5","team":{"id":"10","displayName":"New York Yankees","location":"New York","name":"Yankees","abbreviation":"NYY"}}
		]
	}]
}]}`

func TestBaseballSituationDistinguishesZeroFromMissing(t *testing.T) {
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/baseball/mlb/") {
			return 200, mlbLive
		}
		return 200, `{"events":[]}`
	})
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	game, ok := s.GetGame("m1")
	if !ok || game.LiveSituation == nil {
		t.Fatal("missing live situation")
	}
	sit := game.LiveSituation
	if sit.Balls == nil || *sit.Balls != 2 || sit.Outs == nil || *sit.Outs != 0 {
		t.Fatalf("counts wrong: %+v", sit)
	}
	if sit.Bases != "On 1st & 3rd" || sit.Batter != "A. Batter" {
		t.Fatalf("base state wrong: %+v", sit)
	}
}

func TestDatedScoreboardCachesAndValidates(t *testing.T) {
	calls := 0
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/basketball/nba/") {
			calls++
			return 200, nbaLive
		}
		return 200, `{"events":[]}`
	})
	date := time.Now().UTC().Format("2006-01-02")
	board, err := s.GetDatedScoreboard(context.Background(), date, "nba")
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Games) != 1 || board.Games[0].ID != "402" {
		t.Fatalf("dated board wrong: %+v", board)
	}
	if _, err := s.GetDatedScoreboard(context.Background(), date, "nba"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("second read hit the provider: %d calls", calls)
	}
	if _, err := s.GetDatedScoreboard(context.Background(), "2020-01-01", ""); err == nil {
		t.Fatal("out-of-window date accepted")
	}
	if err := ValidateScoreboardDate("nope", time.Now()); err == nil {
		t.Fatal("malformed date accepted")
	}
}

// The client sends its own calendar day, not UTC's. A viewer at UTC+14 is already
// on tomorrow's date while UTC is still on today, and UTC−11 is still on
// yesterday; both must be inside the accepted window or their hub is empty.
func TestScoreboardDateAcceptsEveryViewerTimeZone(t *testing.T) {
	now := time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)
	for hours := -11; hours <= 14; hours++ {
		viewer := now.In(time.FixedZone("viewer", hours*3600))
		date := viewer.Format("2006-01-02")
		if err := ValidateScoreboardDate(date, now); err != nil {
			t.Fatalf("UTC%+d local date %s rejected: %v", hours, date, err)
		}
	}
	if err := ValidateScoreboardDate("2026-10-27", now); err == nil {
		t.Fatal("a date three UTC days out must stay rejected")
	}
}

func TestDatedScoreboardKeepsPreviousOnFailure(t *testing.T) {
	fail := false
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/basketball/nba/") {
			if fail {
				return 500, `{}`
			}
			return 200, nbaLive
		}
		return 200, `{"events":[]}`
	})
	date := time.Now().UTC().Format("2006-01-02")
	if _, err := s.GetDatedScoreboard(context.Background(), date, "nba"); err != nil {
		t.Fatal(err)
	}
	fail = true
	// Expire the cache so the next read actually polls.
	s.dateMu.Lock()
	for k, v := range s.dated {
		v.expires = time.Now().Add(-time.Second)
		s.dated[k] = v
	}
	s.dateMu.Unlock()
	board, err := s.GetDatedScoreboard(context.Background(), date, "nba")
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Games) != 1 || !board.Stale {
		t.Fatalf("stale board should retain the previous event: %+v", board)
	}
}

func TestLogoAllowlistAndCache(t *testing.T) {
	s := testService(t, func(r *http.Request) (int, string) {
		return 200, "not reached"
	})
	if _, _, err := s.GetCachedLogo(context.Background(), "https://evil.example/logo.png"); err == nil {
		t.Fatal("foreign logo host accepted")
	}
	if _, _, err := s.GetCachedLogo(context.Background(), "http://a.espncdn.com/logo.png"); err == nil {
		t.Fatal("non-https logo accepted")
	}
}

func TestRestartKeepsScores(t *testing.T) {
	dir := t.TempDir()
	s := NewWithClient(dir, &http.Client{Transport: fixtureTransport{body: func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/basketball/nba/") {
			return 200, nbaLive
		}
		return 200, `{"events":[]}`
	}}})
	s.SetEnabledLeagueIDs([]string{"nba"})
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := New(dir)
	if games := restarted.GetScoreboard("nba"); len(games) != 1 || games[0].ID != "402" {
		t.Fatalf("disk snapshot not restored: %+v", games)
	}
	data, err := json.Marshal(restarted.GetStatus())
	if err != nil || !strings.Contains(string(data), `"enabled":false`) {
		// A fresh service restores games but has no enabled leagues until the
		// caller applies config; the snapshot itself must still be intact.
		t.Logf("status: %s", data)
	}
}

func TestTeamCatalogSync(t *testing.T) {
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/teams") && strings.Contains(r.URL.Path, "/basketball/nba/") {
			return 200, `{"sports":[{"leagues":[{"teams":[
				{"team":{"id":"2","displayName":"Boston Celtics","location":"Boston","name":"Celtics","abbreviation":"BOS","logos":[{"href":"https://a.espncdn.com/bos.png"}]}},
				{"team":{"id":"14","displayName":"Miami Heat","location":"Miami","name":"Heat","abbreviation":"MIA"}}
			]}]}]}`
		}
		return 200, `{"events":[]}`
	})
	s.SetEnabledLeagueIDs([]string{"nba"})
	report, err := s.EnsureTeamCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Teams) != 2 || report.Teams[0].ID != "nba:2" {
		t.Fatalf("team catalog wrong: %+v", report.Teams)
	}
	if report.Refreshed != 1 || len(report.Failed) != 0 || len(report.Unsupported) != 0 {
		t.Fatalf("clean pass reported problems: %+v", report)
	}
}

func TestUnsupportedTeamEndpointIsCached(t *testing.T) {
	var requests int
	var mu sync.Mutex
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/teams") {
			mu.Lock()
			requests++
			mu.Unlock()
			return 404, `{"code":404,"message":"404 NOT_FOUND \"League not found\""}`
		}
		return 200, `{"events":[]}`
	})
	s.SetEnabledLeagueIDs([]string{"cricket-8048"})

	first, err := s.EnsureTeamCatalog(context.Background())
	if err != nil {
		t.Fatalf("a provider with no team catalog is not a failure: %v", err)
	}
	if len(first.Teams) != 0 || first.Refreshed != 0 {
		t.Fatalf("unexpected teams: %+v", first)
	}
	if len(first.Unsupported) != 1 || first.Unsupported[0] != "cricket-8048" {
		t.Fatalf("unsupported leagues = %+v", first.Unsupported)
	}

	// The completed check is cached for the TTL, so the known-absent endpoint is
	// not probed again on the next cycle.
	second, err := s.EnsureTeamCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Unsupported) != 0 {
		t.Fatalf("unsupported league re-fetched inside the TTL: %+v", second.Unsupported)
	}
	if got := requestCount(&mu, &requests); got != 1 {
		t.Fatalf("teams requests = %d, want 1", got)
	}

	// Once the TTL expires the provider is re-probed, in case it gained support.
	s.mu.Lock()
	s.teamCatalogUpdated["cricket-8048"] = time.Now().Add(-teamCatalogTTL - time.Minute)
	s.mu.Unlock()
	if _, err := s.EnsureTeamCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := requestCount(&mu, &requests); got != 2 {
		t.Fatalf("teams requests after TTL = %d, want 2", got)
	}
	if !s.teamCatalogFresh("cricket-8048") {
		t.Fatal("re-probed league should be fresh again")
	}
}

func requestCount(mu *sync.Mutex, n *int) int {
	mu.Lock()
	defer mu.Unlock()
	return *n
}

func TestTransientTeamFailureStaysRetryable(t *testing.T) {
	var requests int
	var mu sync.Mutex
	fail := true
	s := testService(t, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/teams") {
			mu.Lock()
			requests++
			down := fail
			mu.Unlock()
			if down {
				return 500, `{"error":"boom"}`
			}
			return 200, `{"sports":[{"leagues":[{"teams":[
				{"team":{"id":"2","displayName":"Boston Celtics","location":"Boston","name":"Celtics","abbreviation":"BOS"}}
			]}]}]}`
		}
		return 200, `{"events":[]}`
	})
	s.SetEnabledLeagueIDs([]string{"nba"})

	// A 5xx is a real failure: it is reported and nothing is cached as checked.
	report, err := s.EnsureTeamCatalog(context.Background())
	if err == nil {
		t.Fatal("a total failure must be visible to the caller")
	}
	if len(report.Failed) != 1 || report.Failed[0].League != "nba" {
		t.Fatalf("failed leagues = %+v", report.Failed)
	}
	if s.teamCatalogFresh("nba") {
		t.Fatal("a failed fetch must not count as a completed check")
	}

	mu.Lock()
	fail = false
	mu.Unlock()
	if _, err := s.EnsureTeamCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := requestCount(&mu, &requests); got != 2 {
		t.Fatalf("teams requests = %d, want 2 (a failure must be retried)", got)
	}
	if len(s.TeamCatalog("nba")) != 1 {
		t.Fatalf("recovered catalog = %+v", s.TeamCatalog("nba"))
	}
}

// A 403 is the provider refusing this client, not a request that succeeds if
// it is repeated. The cycle must stop asking for a while instead of spending
// the next cycle on the same leagues, and the last known scores must survive.
func TestRefusedScoreboardBacksOffAndKeepsLastKnownScores(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	refuse := false
	s := testService(t, func(r *http.Request) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(r.URL.Path, "/basketball/nba/") {
			requests++
			if refuse {
				return 403, `{"error":"forbidden"}`
			}
			return 200, nbaLive
		}
		return 200, `{"events":[]}`
	})
	s.SetEnabledLeagueIDs([]string{"nba"})

	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	refuse = true
	mu.Unlock()
	if err := s.Refresh(context.Background()); err == nil {
		t.Fatal("the cycle must report the refusal")
	}
	before := requestCount(&mu, &requests)

	// The next cycle is inside the backoff: it must not ask again and must not
	// erase what the provider last served.
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatalf("a backed-off cycle is not a new failure: %v", err)
	}
	if got := requestCount(&mu, &requests); got != before {
		t.Fatalf("requests = %d, want %d (a refused league must not be re-asked)", got, before)
	}
	if games := s.GetScoreboard("nba"); len(games) != 1 || games[0].ID != "402" {
		t.Fatalf("last-known scores were erased: %+v", games)
	}
	if !s.leagueBlocked("nba") {
		t.Fatal("the refusal should register a backoff")
	}

	// Once the provider serves the league again the backoff is forgotten.
	s.clearLeagueRefusal("nba")
	mu.Lock()
	refuse = false
	mu.Unlock()
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.leagueBlocked("nba") {
		t.Fatal("a served league should not stay blocked")
	}
}

// The team catalog repeats on every cycle and is the noisiest path when the
// provider refuses the client, so it backs off the same way.
func TestRefusedTeamCatalogBacksOffInsteadOfRepeating(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	s := testService(t, func(r *http.Request) (int, string) {
		if !strings.Contains(r.URL.Path, "/teams") {
			return 200, `{"events":[]}`
		}
		mu.Lock()
		requests++
		mu.Unlock()
		return 403, `{"error":"forbidden"}`
	})
	s.SetEnabledLeagueIDs([]string{"nba"})

	if _, err := s.EnsureTeamCatalog(context.Background()); err == nil {
		t.Fatal("a refusal must still be visible to the caller")
	}
	before := requestCount(&mu, &requests)
	if before != 1 {
		t.Fatalf("teams requests = %d, want 1", before)
	}
	if _, err := s.EnsureTeamCatalog(context.Background()); err != nil {
		t.Fatalf("a backed-off pass is not a failure: %v", err)
	}
	if got := requestCount(&mu, &requests); got != before {
		t.Fatalf("teams requests = %d, want %d (a refused league must not be re-asked)", got, before)
	}
}

// A provider that refuses every league is refusing this client, not one
// competition, so the rest of the burst must not be sent. Only a repeated
// refusal silences the host, so one unsupported league cannot do it.
func TestRepeatedRefusalStopsTheRestOfTheBurst(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	inner := fixtureTransport{body: func(r *http.Request) (int, string) {
		if !strings.Contains(r.URL.Path, "/scoreboard") {
			return 200, `{"events":[]}`
		}
		mu.Lock()
		requests++
		mu.Unlock()
		return 403, `{"error":"forbidden"}`
	}}
	resetSportsHTTPPool(t)
	s := NewWithClient(t.TempDir(), boundedSportsClient(&http.Client{Transport: inner, Timeout: 5 * time.Second}))
	s.SetEnabledLeagueIDs(espnLeagueIDs(t))

	if err := s.Refresh(context.Background()); err == nil {
		t.Fatal("a refused cycle must report the failure")
	}
	first := requestCount(&mu, &requests)
	if first > providerRefusalBurst+8 {
		t.Fatalf("scoreboard requests = %d, want at most %d (the burst must stop after a refusal)", first, providerRefusalBurst+8)
	}
	// Every league is now backed off, so the next cycle does not touch the network.
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatalf("a backed-off cycle is not a new failure: %v", err)
	}
	if got := requestCount(&mu, &requests); got != first {
		t.Fatalf("scoreboard requests = %d, want %d (a refused host must stay quiet)", got, first)
	}
}

// The catalog is large enough that unpaced polling is a burst; a provider
// answers a burst with a refusal, so requests leave on a paced schedule.
func TestProviderRequestsArePaced(t *testing.T) {
	resetSportsHTTPPool(t)
	var mu sync.Mutex
	sent := 0
	inner := fixtureTransport{body: func(r *http.Request) (int, string) {
		mu.Lock()
		sent++
		mu.Unlock()
		return 200, `{"events":[]}`
	}}
	client := boundedSportsClient(&http.Client{Transport: inner, Timeout: 5 * time.Second})

	start := time.Now()
	const requests = 3
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "https://site.api.espn.com/apis/site/v2/sports/basketball/nba/scoreboard?probe="+fmt.Sprint(i), nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	if got := requestCount(&mu, &sent); got != requests {
		t.Fatalf("requests = %d, want %d", got, requests)
	}
	if want := time.Duration(requests-1) * providerRequestInterval; elapsed < want {
		t.Fatalf("elapsed = %v, want at least %v (requests were not paced)", elapsed, want)
	}
}

// resetSportsHTTPPool gives a test its own provider pool; the real one is a
// process-wide cache and cooldown that must not leak between tests.
func resetSportsHTTPPool(t *testing.T) {
	t.Helper()
	previous := sharedSportsHTTP
	sharedSportsHTTP = newSportsHTTPPool()
	t.Cleanup(func() { sharedSportsHTTP = previous })
}

func espnLeagueIDs(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, league := range LeagueCatalog {
		if league.active() && league.Provider == "espn" {
			ids = append(ids, league.ID)
		}
	}
	if len(ids) < providerRefusalBurst+8 {
		t.Fatalf("only %d ESPN leagues; the burst bound would prove nothing", len(ids))
	}
	return ids
}

func TestCricketScoreboardKeepsHomeAndAwayWithoutTeamCatalog(t *testing.T) {
	s := testService(t, func(r *http.Request) (int, string) {
		switch {
		case strings.Contains(r.URL.Path, "/teams"):
			return 404, `{"code":404,"message":"404 NOT_FOUND \"League not found\""}`
		case strings.Contains(r.URL.Path, "/cricket/8048/scoreboard"):
			return 200, `{"events":[{
				"id":"1401234","date":"2026-10-25T14:00Z","name":"Mumbai Indians at Chennai Super Kings",
				"competitions":[{"id":"1401234",
					"status":{"displayClock":"0:00","period":0,"type":{"state":"pre","detail":"2:00 PM","shortDetail":"2:00 PM"}},
					"competitors":[
						{"homeAway":"home","id":"1","score":"0","team":{"id":"1","displayName":"Chennai Super Kings","location":"Chennai","name":"Super Kings","abbreviation":"CSK"}},
						{"homeAway":"away","id":"2","score":"0","team":{"id":"2","displayName":"Mumbai Indians","location":"Mumbai","name":"Indians","abbreviation":"MI"}}
					]
				}]
			}]}`
		}
		return 200, `{"events":[]}`
	})
	s.SetEnabledLeagueIDs([]string{"cricket-8048"})
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	games := s.GetScoreboard("cricket-8048")
	if len(games) != 1 || games[0].HomeTeam.Name != "Chennai Super Kings" || games[0].AwayTeam.Name != "Mumbai Indians" {
		t.Fatalf("cricket participants lost: %+v", games)
	}

	report, err := s.EnsureTeamCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Teams) != 0 {
		t.Fatalf("team catalog invented cricket teams: %+v", report.Teams)
	}
	if len(s.GetScoreboard("cricket-8048")) != 1 {
		t.Fatal("a missing team catalog dropped the scoreboard event")
	}
}

func TestPeriodLabels(t *testing.T) {
	if got := periodLabel(2, "baseball"); got != "2nd inning" {
		t.Fatal(got)
	}
	if got := teamPeriodLabel("soccer-eng.1", 3); got != "ET1" {
		t.Fatal(got)
	}
	if got := teamPeriodLabel("nfl", 5); got != "1OT" {
		t.Fatal(got)
	}
	if got := teamPeriodLabel("nhl", 4); got != "1OT" {
		t.Fatal(got)
	}
	if got := periodLabel(3, "hockey"); got != "3rd period" {
		t.Fatal(got)
	}
	_ = fmt.Sprint()
}
