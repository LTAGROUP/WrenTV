package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/livetv"
	"github.com/LTAGROUP/wrentv/internal/sports"
)

type sportsTransport func(*http.Request) (int, string)

func (f sportsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	status, body := f(r)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

const apiScoreboard = `{"events":[{
	"id":"402","date":"2026-10-24T22:00Z","name":"Boston Celtics at Miami Heat",
	"competitions":[{
		"id":"402","status":{"displayClock":"4:12","period":3,"type":{"state":"in","detail":"3rd Quarter","shortDetail":"3rd - 4:12"}},
		"competitors":[
			{"homeAway":"home","id":"14","score":"78","team":{"id":"14","displayName":"Miami Heat","location":"Miami","name":"Heat","abbreviation":"MIA"}},
			{"homeAway":"away","id":"2","score":"81","team":{"id":"2","displayName":"Boston Celtics","location":"Boston","name":"Celtics","abbreviation":"BOS"}}
		]
	}]
}]}`

const apiNBASummary = `{
  "header": {"id":"402","competitions":[{
    "status":{"displayClock":"4:12","period":3,"type":{"state":"in","detail":"3rd Quarter"}},
    "competitors":[
      {"id":"14","homeAway":"home","score":"78","linescores":[{"displayValue":"28"},{"displayValue":"24"},{"displayValue":"26"}]},
      {"id":"2","homeAway":"away","score":"81","linescores":[{"displayValue":"30"},{"displayValue":"25"},{"displayValue":"26"}]}
    ]
  }]},
  "boxscore": {"teams":[
    {"team":{"id":"2"},"statistics":[{"name":"totalRebounds","displayValue":"33"},{"name":"fieldGoalPct","displayValue":"49.0"}]},
    {"team":{"id":"14"},"statistics":[{"name":"totalRebounds","displayValue":"29"},{"name":"fieldGoalPct","displayValue":"45.5"}]}
  ]},
  "plays":[{"id":"p1","text":"Tatum makes layup","scoringPlay":true,"period":{"number":3,"displayValue":"3rd Quarter"},"clock":{"displayValue":"4:12"},"type":{"text":"Made Shot"}}]
}`

const apiRaceScoreboard = `{"events":[{
	"id":"600057442","name":"Pirelli Italian Grand Prix",
	"competitions":[
		{"id":"401839101","date":"2026-09-05T14:00Z","type":{"abbreviation":"Qual"},
		 "status":{"type":{"state":"post","detail":"Final"}},
		 "competitors":[
			{"id":"5498","athlete":{"displayName":"Charles Leclerc","shortName":"C. Leclerc"}},
			{"id":"4665","athlete":{"displayName":"Max Verstappen","shortName":"M. Verstappen"}}
		]},
		{"id":"401839102","date":"2026-09-06T13:00Z","type":{"abbreviation":"Race"},
		 "status":{"type":{"state":"post","detail":"Final"}},
		 "venue":{"fullName":"Autodromo Nazionale Monza"},
		 "broadcasts":[{"names":["Apple TV"]}],
		 "competitors":[
			{"id":"5498","athlete":{"displayName":"Charles Leclerc","shortName":"C. Leclerc"}},
			{"id":"4665","athlete":{"displayName":"Max Verstappen","shortName":"M. Verstappen","winner":true}}
		]}
	]
}]}`

const apiRaceCore = `{"id":"401839102","competitors":[
	{"id":"5498","vehicle":{"number":"16","team":"Ferrari","manufacturer":"Ferrari"}},
	{"id":"4665","vehicle":{"number":"1","team":"Red Bull Racing","manufacturer":"Red Bull"}}
]}`

const apiRaceStatistics = `{"splits":{"categories":[{"stats":[
	{"name":"place","value":1,"displayValue":"1"},
	{"name":"lapsCompleted","value":53,"displayValue":"53"},
	{"name":"totalTime","value":1,"displayValue":"1:20:00.000"},
	{"name":"fastestLap","value":1,"displayValue":"1:22.000"},
	{"name":"pitsTaken","value":0,"displayValue":"0"}
]}]}}`

const apiNBATeams = `{"sports":[{"leagues":[{"teams":[
	{"team":{"id":"2","displayName":"Boston Celtics","location":"Boston","name":"Celtics","abbreviation":"BOS"}},
	{"team":{"id":"14","displayName":"Miami Heat","location":"Miami","name":"Heat","abbreviation":"MIA"}}
]}]}]}`

// apiCricketScoreboard is an IPL fixture: ESPN publishes no /teams catalog for
// cricket, so these participants are the only team identities available.
const apiCricketScoreboard = `{"events":[{
	"id":"1401234","date":"2026-10-25T14:00Z","name":"Mumbai Indians at Chennai Super Kings",
	"competitions":[{
		"id":"1401234",
		"status":{"displayClock":"0:00","period":0,"type":{"state":"pre","detail":"2:00 PM","shortDetail":"2:00 PM"}},
		"competitors":[
			{"homeAway":"home","id":"1","score":"0","team":{"id":"1","displayName":"Chennai Super Kings","location":"Chennai","name":"Super Kings","abbreviation":"CSK"}},
			{"homeAway":"away","id":"2","score":"0","team":{"id":"2","displayName":"Mumbai Indians","location":"Mumbai","name":"Indians","abbreviation":"MI"}}
		]
	}]
}]}`

const apiCricketDropdown = `{"leagues":[
	{"name":"Olympics 2028","id":"1481569","slug":""},
	{"name":"The Ashes 2027","slug":"24627"},
	{"name":"Unpolled Series 2030","slug":"99999"},
	{"name":"Indian Premier League","slug":"8048"},
	{"name":"Indian Premier League","slug":"8048"}
]}`

type sportsFixture struct {
	s   *Server
	svc *sports.Service
	mux http.Handler
	cfg *config.Config
	dir string
}

func newSportsFixture(t *testing.T) *sportsFixture {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sports.Leagues = []string{"nba", "f1"}
	cfg.Sports.Normalize()
	svc := sports.NewWithClient(t.TempDir(), &http.Client{Transport: sportsTransport(func(r *http.Request) (int, string) {
		switch {
		case strings.Contains(r.URL.Path, "/leagues/dropdown"):
			return 200, apiCricketDropdown
		case strings.Contains(r.URL.Path, "/summary"):
			return 200, apiNBASummary
		case strings.Contains(r.URL.Path, "/basketball/nba/"):
			return 200, apiScoreboard
		case strings.Contains(r.URL.Path, "/racing/f1/scoreboard"):
			return 200, apiRaceScoreboard
		case strings.Contains(r.URL.Path, "/competitors/"):
			if strings.HasSuffix(r.URL.Path, "/status") {
				return 200, `{"displayValue":"Finished"}`
			}
			return 200, apiRaceStatistics
		case strings.Contains(r.URL.Path, "/sports/racing/leagues/f1/events/"):
			return 200, apiRaceCore
		default:
			return 200, `{"events":[]}`
		}
	})})
	svc.SetEnabledLeagueIDs(cfg.Sports.PollingLeagueIDs())
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, user := range []*db.User{
		{ID: "member", DisplayName: "Member"},
		{ID: "other", DisplayName: "Other"},
		{ID: "admin", DisplayName: "Admin", IsAdmin: true},
	} {
		if err := database.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{Cfg: cfg, DB: database, Sports: svc, LiveTV: livetv.New(cfg, dir)}
	mux := chi.NewRouter()
	mux.Get("/api/sports/status", s.handleSportsStatus)
	mux.Get("/api/sports/leagues", s.handleSportsLeagues)
	mux.Get("/api/sports/scoreboard", s.handleSportsScoreboard)
	mux.Get("/api/sports/hub", s.handleSportsHub)
	mux.Get("/api/sports/races", s.handleSportsRaces)
	mux.Get("/api/sports/races/{league}/{event}", s.handleSportsRaceSession)
	mux.Get("/api/sports/races/{league}/{event}/{session}", s.handleSportsRaceSession)
	mux.Get("/api/sports/races/{league}/{event}/{session}/archive", s.handleSportsF1Archive)
	mux.Get("/api/sports/cycling", s.handleSportsCycling)
	mux.Get("/api/sports/cycling/{race}/{year}/{stage}", s.handleSportsCyclingStage)
	mux.Get("/api/sports/motogp/standings", s.handleSportsMotoGPStandings)
	mux.Get("/api/sports/motogp/circuit/{event}", s.handleSportsMotoGPCircuit)
	mux.Get("/api/sports/cricket/series", s.handleSportsCricketSeries)
	mux.Get("/api/sports/leagues/{league}/standings", s.handleSportsLeagueStandings)
	mux.Get("/api/sports/events/{id}", s.handleSportsEvent)
	mux.Get("/api/sports/search", s.handleSportsSearch)
	mux.Get("/api/sports/events/{id}/streams", s.handleSportsEventStreams)
	mux.Post("/api/sports/streams/quality", s.handleSportsStreamQuality)
	mux.Get("/api/sports/match", s.handleSportsEventStreamsByQuery)
	mux.Get("/api/sports/logo", s.handleSportsLogo)
	mux.Get("/api/admin/sports", s.handleAdminSports)
	mux.Put("/api/admin/sports", s.handleAdminSportsUpdate)
	mux.Get("/api/sports/preferences", s.handleGetSportsPreferences)
	mux.Put("/api/sports/preferences", s.handlePutSportsPreferences)
	mux.Get("/api/sports/teams", s.handleSportsTeams)
	mux.Get("/api/sports/teams/linked", s.handleSportsLinkedChannels)
	mux.Get("/api/sports/teams/{id}/links", s.handleSportsTeamLinks)
	mux.Get("/api/sports/teams/{id}/suggestions", s.handleSportsTeamSuggestions)
	mux.Get("/api/sports/leagues/{league}/auto-link/preview", s.handleSportsAutoLinkPreview)
	mux.Group(func(r chi.Router) {
		r.Use(adminOnly)
		r.Put("/api/sports/teams/{id}/links/primary", s.handleSportsSetPrimaryLink)
		r.Post("/api/sports/teams/{id}/links", s.handleSportsAddBackupLink)
		r.Delete("/api/sports/teams/{id}/links/{linkId}", s.handleSportsDeleteLink)
		r.Post("/api/sports/teams/{id}/links/order", s.handleSportsReorderLinks)
		r.Post("/api/sports/leagues/{league}/auto-link/apply", s.handleSportsAutoLinkApply)
		r.Get("/api/admin/users/{id}/sports/preferences", s.handleAdminUserSportsPreferences)
		r.Put("/api/admin/users/{id}/sports/preferences", s.handleAdminPutUserSportsPreferences)
	})
	return &sportsFixture{s: s, svc: svc, mux: mux, cfg: cfg, dir: dir}
}

// waitIdle lets a settings-triggered background refresh finish before the
// test removes its temporary data directory.
func (f *sportsFixture) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	idle := 0
	for time.Now().Before(deadline) {
		if f.svc.GetStatus().Refreshing {
			idle = 0
		} else if idle++; idle >= 3 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("sports refresh did not finish")
}

func (f *sportsFixture) do(t *testing.T, method, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

// doAs attaches an authenticated principal, so preference and admin routes run
// with the same identity they would in production.
func (f *sportsFixture) doAs(t *testing.T, userID, role, method, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	user, err := f.s.DB.UserByID(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(context.WithValue(req.Context(), principalKey, &auth.Principal{User: user, Role: role}))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func TestSportsPreferencesRevisionAndIsolation(t *testing.T) {
	f := newSportsFixture(t)
	if rec, _ := f.do(t, "GET", "/api/sports/preferences", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out read: %d", rec.Code)
	}
	rec, body := f.doAs(t, "member", "user", "GET", "/api/sports/preferences", "")
	if rec.Code != 200 || body["revision"] != float64(0) {
		t.Fatalf("initial snapshot: %d %+v", rec.Code, body)
	}
	prefs := body["preferences"].(map[string]any)
	if len(prefs["pinnedSportIds"].([]any)) != 0 || len(prefs["teamIds"].([]any)) != 0 {
		t.Fatalf("initial preferences not empty: %+v", prefs)
	}
	doc := `{"revision":0,"preferences":{"version":1,"pinnedSportIds":["basketball"],"competitionIds":["premier-league"],"teamIds":["nba:2"],"athleteIds":[],"schools":[]}}`
	rec, body = f.doAs(t, "member", "user", "PUT", "/api/sports/preferences", doc)
	if rec.Code != 200 || body["revision"] != float64(1) {
		t.Fatalf("first save: %d %+v", rec.Code, body)
	}
	// The saved follow is returned in the user's own snapshot.
	rec, body = f.doAs(t, "member", "user", "GET", "/api/sports/preferences", "")
	if rec.Code != 200 || body["revision"] != float64(1) {
		t.Fatalf("reload: %d %+v", rec.Code, body)
	}
	if got := body["preferences"].(map[string]any)["pinnedSportIds"].([]any); len(got) != 1 || got[0] != "basketball" {
		t.Fatalf("pins not persisted: %+v", body)
	}
	// Another user is isolated and starts at revision zero.
	rec, body = f.doAs(t, "other", "user", "GET", "/api/sports/preferences", "")
	if rec.Code != 200 || body["revision"] != float64(0) {
		t.Fatalf("other user leaked: %d %+v", rec.Code, body)
	}
	// A stale revision loses with 409 and never overwrites.
	rec, _ = f.doAs(t, "member", "user", "PUT", "/api/sports/preferences", doc)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale revision: %d", rec.Code)
	}
	// Malformed documents are rejected before touching storage.
	for _, invalid := range []string{
		`{"revision":1,"preferences":{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[],"extra":1}}`,
		`{"revision":1,"preferences":{"version":2,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}}`,
		`{"revision":1}`,
	} {
		if rec, _ := f.doAs(t, "member", "user", "PUT", "/api/sports/preferences", invalid); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid document accepted (%s): %d", invalid, rec.Code)
		}
	}
	// The admin editor reads and writes another user's document.
	rec, body = f.doAs(t, "member", "user", "GET", "/api/admin/users/member/sports/preferences", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member read admin editor: %d", rec.Code)
	}
	rec, body = f.doAs(t, "admin", "admin", "GET", "/api/admin/users/member/sports/preferences", "")
	if rec.Code != 200 || body["revision"] != float64(1) {
		t.Fatalf("admin read: %d %+v", rec.Code, body)
	}
	adminDoc := `{"revision":1,"preferences":{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}}`
	rec, body = f.doAs(t, "admin", "admin", "PUT", "/api/admin/users/member/sports/preferences", adminDoc)
	if rec.Code != 200 || body["revision"] != float64(2) {
		t.Fatalf("admin save: %d %+v", rec.Code, body)
	}
	rec, _ = f.doAs(t, "admin", "admin", "PUT", "/api/admin/users/missing/sports/preferences", adminDoc)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing user: %d", rec.Code)
	}
}

func TestSportsTeamLinksAuthorizationAndValidation(t *testing.T) {
	f := newSportsFixture(t)
	ctx := context.Background()
	if _, err := f.s.DB.SyncSportsTeams(ctx, []db.SportsTeamRow{
		{ID: "nba:2", League: "nba", ProviderID: "2", Name: "Boston Celtics", Location: "Boston", Nickname: "Celtics", Abbreviation: "BOS"},
	}); err != nil {
		t.Fatal(err)
	}
	rec, body := f.do(t, "GET", "/api/sports/teams?league=nba", "")
	if rec.Code != 200 || len(body["teams"].([]any)) != 1 {
		t.Fatalf("teams: %d %+v", rec.Code, body)
	}
	rec, body = f.do(t, "GET", "/api/sports/teams/nba:2/links", "")
	if rec.Code != 200 || len(body["links"].([]any)) != 0 {
		t.Fatalf("empty links: %d %+v", rec.Code, body)
	}
	rec, body = f.do(t, "GET", "/api/sports/teams/nba:2/suggestions", "")
	if rec.Code != 200 || len(body["candidates"].([]any)) != 0 {
		t.Fatalf("suggestions without Live TV: %d %+v", rec.Code, body)
	}
	rec, _ = f.do(t, "GET", "/api/sports/teams/nba:2/suggestions?limit=0", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: %d", rec.Code)
	}
	rec, _ = f.do(t, "GET", "/api/sports/teams/missing/links", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing team: %d", rec.Code)
	}
	// A signed-out mutation is refused, a member is forbidden, and only an
	// admin reaches validation.
	bodyJSON := `{"channelId":"c1"}`
	rec, _ = f.do(t, "PUT", "/api/sports/teams/nba:2/links/primary", bodyJSON)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("signed-out mutation: %d", rec.Code)
	}
	rec, _ = f.doAs(t, "member", "user", "PUT", "/api/sports/teams/nba:2/links/primary", bodyJSON)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member mutation: %d", rec.Code)
	}
	rec, _ = f.doAs(t, "admin", "admin", "PUT", "/api/sports/teams/nba:2/links/primary", bodyJSON)
	if rec.Code != http.StatusNotFound || !strings.Contains(fmt.Sprint(mustJSON(t, rec)), "channel not found") {
		t.Fatalf("unknown channel: %d %s", rec.Code, rec.Body.String())
	}
	rec, _ = f.doAs(t, "admin", "admin", "PUT", "/api/sports/teams/nba:2/links/primary", `{"bogus":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown link field: %d", rec.Code)
	}
	rec, _ = f.doAs(t, "admin", "admin", "POST", "/api/sports/teams/nba:2/links/order", `{"linkIds":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty order: %d", rec.Code)
	}
	rec, body = f.do(t, "GET", "/api/sports/leagues/nba/auto-link/preview", "")
	if rec.Code != 200 || body["eligibleCount"] != float64(0) || body["skippedCount"] != float64(1) {
		t.Fatalf("auto preview: %d %+v", rec.Code, body)
	}
	rec, body = f.do(t, "GET", "/api/sports/teams/linked?ids=nba:2,nba:14", "")
	if rec.Code != 200 {
		t.Fatalf("linked batch: %d", rec.Code)
	}
	if links := body["linksByTeam"].(map[string]any); len(links) != 0 {
		t.Fatalf("invented links: %+v", links)
	}
	// Apply records nothing when no candidate is eligible.
	rec, body = f.doAs(t, "admin", "admin", "POST", "/api/sports/leagues/nba/auto-link/apply", "")
	if rec.Code != 200 || body["linkedCount"] != float64(0) {
		t.Fatalf("apply without candidates: %d %+v", rec.Code, body)
	}
	rows, err := f.s.DB.SportsTeamLinks(ctx, "nba:2")
	if err != nil || len(rows) != 0 {
		t.Fatalf("links written without candidates: %+v %v", rows, err)
	}
	// A persisted snapshot that no longer exists in the playlist is reported as
	// unresolved and never carries a playable channel id.
	if _, err := f.s.DB.SetSportsPrimaryLink(ctx, "nba:2", db.SportsLinkRow{ChannelRef: "gone", ChannelTvgID: "gone.us", ChannelName: "Gone Channel"}); err != nil {
		t.Fatal(err)
	}
	rec, body = f.do(t, "GET", "/api/sports/teams/linked?ids=nba:2", "")
	if rec.Code != 200 {
		t.Fatalf("linked unresolved: %d", rec.Code)
	}
	linked := body["linksByTeam"].(map[string]any)["nba:2"].([]any)
	if len(linked) != 1 {
		t.Fatalf("unresolved link omitted: %+v", linked)
	}
	stale := linked[0].(map[string]any)
	if stale["resolved"] != false || stale["channelId"] != nil {
		t.Fatalf("unresolved link offered for playback: %+v", stale)
	}
}

// The web client percent-encodes ids into request paths (espn%3Agolf%3A401).
// chi routes on the escaped path, so handlers must decode before looking an id
// up; otherwise every competition-prefixed event or team id is a 404.
func TestSportsPathParamsDecodeEscapedIDs(t *testing.T) {
	f := newSportsFixture(t)
	if _, err := f.s.DB.SyncSportsTeams(context.Background(), []db.SportsTeamRow{
		{ID: "nba:2", League: "nba", ProviderID: "2", Name: "Boston Celtics"},
	}); err != nil {
		t.Fatal(err)
	}
	rec, body := f.do(t, "GET", "/api/sports/teams/nba%3A2/links", "")
	if rec.Code != 200 || len(body["links"].([]any)) != 0 {
		t.Fatalf("escaped team id: %d %+v", rec.Code, body)
	}
}

func TestPathParamDecodesEscapedID(t *testing.T) {
	mux := chi.NewRouter()
	mux.Get("/api/sports/events/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pathParam(r, "id")))
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sports/events/espn%3Agolf%3Achampions-tour%3A401832052", nil))
	if got := rec.Body.String(); got != "espn:golf:champions-tour:401832052" {
		t.Fatalf("decoded id = %q", got)
	}
}

func TestSportsTeamLinkLifecycleWithLiveTV(t *testing.T) {
	f := newSportsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	playlist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-mpegurl")
		_, _ = io.WriteString(w, `#EXTM3U
#EXTINF:-1 tvg-id="celtics.us" tvg-name="Boston Celtics" group-title="Sports",Boston Celtics
http://channel.example/celtics.m3u8
#EXTINF:-1 tvg-id="celtics-hd.us" group-title="Sports",Celtics HD
http://channel.example/celtics-hd.m3u8
`)
	}))
	defer playlist.Close()
	f.cfg.LiveTV.Enabled = true
	f.cfg.LiveTV.Sources = []config.LiveTVSource{{ID: "fixture", Name: "Fixture", Kind: "m3u", URL: playlist.URL, Enabled: true}}
	f.s.LiveTV.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !f.s.LiveTV.Active() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !f.s.LiveTV.Active() {
		t.Fatal("fixture Live TV did not load")
	}
	if _, err := f.s.DB.SyncSportsTeams(ctx, []db.SportsTeamRow{{
		ID: "nba:2", League: "nba", ProviderID: "2", Name: "Boston Celtics", Location: "Boston", Nickname: "Celtics", Abbreviation: "BOS",
	}}); err != nil {
		t.Fatal(err)
	}
	rec, body := f.do(t, "GET", "/api/sports/teams/nba:2/suggestions?limit=20", "")
	if rec.Code != 200 || !strings.Contains(fmt.Sprint(body["candidates"]), "Boston Celtics") {
		t.Fatalf("suggestions: %d %s", rec.Code, rec.Body.String())
	}
	candidates := body["candidates"].([]any)
	var primaryID, backupID string
	for _, raw := range candidates {
		candidate := raw.(map[string]any)
		switch candidate["channelName"] {
		case "Boston Celtics":
			primaryID = candidate["channelId"].(string)
		case "Celtics HD":
			backupID = candidate["channelId"].(string)
		}
	}
	if primaryID == "" || backupID == "" {
		t.Fatalf("fixture channels missing: %+v", candidates)
	}
	// Automatic linking records confidence and provenance; a later manual choice
	// replaces it and clears that provenance.
	rec, body = f.do(t, "GET", "/api/sports/leagues/nba/auto-link/preview", "")
	if rec.Code != 200 || body["eligibleCount"] != float64(1) {
		t.Fatalf("auto preview with candidates: %d %+v", rec.Code, body)
	}
	rec, body = f.doAs(t, "admin", "admin", "POST", "/api/sports/leagues/nba/auto-link/apply", "")
	if rec.Code != 200 || body["linkedCount"] != float64(1) {
		t.Fatalf("auto apply: %d %+v", rec.Code, body)
	}
	autoLinks := body["links"].([]any)
	autoLink := autoLinks[0].(map[string]any)
	if autoLink["autoLinked"] != true || autoLink["resolved"] != true ||
		autoLink["matchReason"] == "" || autoLink["linkConfidence"].(float64) < 0.95 {
		t.Fatalf("auto link provenance: %+v", autoLink)
	}
	rec, body = f.doAs(t, "admin", "admin", "PUT", "/api/sports/teams/nba:2/links/primary", fmt.Sprintf(`{"channelId":%q}`, primaryID))
	if rec.Code != 200 || body["resolved"] != true || body["slot"] != "primary" || body["autoLinked"] != false || body["linkConfidence"] != nil {
		t.Fatalf("manual correction: %d %+v", rec.Code, body)
	}
	rec, body = f.doAs(t, "admin", "admin", "POST", "/api/sports/teams/nba:2/links", fmt.Sprintf(`{"channelId":%q}`, backupID))
	if rec.Code != 200 || body["slot"] != "backup" || body["position"] != float64(1) {
		t.Fatalf("add backup: %d %+v", rec.Code, body)
	}
	// The same channel cannot occupy a second slot.
	rec, _ = f.doAs(t, "admin", "admin", "POST", "/api/sports/teams/nba:2/links", fmt.Sprintf(`{"channelId":%q}`, backupID))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate backup: %d", rec.Code)
	}
	rec, body = f.do(t, "GET", "/api/sports/teams/nba:2/links", "")
	if rec.Code != 200 || len(body["links"].([]any)) != 2 {
		t.Fatalf("links: %d %+v", rec.Code, body)
	}
	links := body["links"].([]any)
	for _, raw := range links {
		if raw.(map[string]any)["resolved"] != true {
			t.Fatalf("link not resolved: %+v", raw)
		}
	}
	// The team now counts as linked, so auto-link skips it.
	rec, body = f.do(t, "GET", "/api/sports/leagues/nba/auto-link/preview", "")
	if rec.Code != 200 || body["eligibleCount"] != float64(0) || body["skippedCount"] != float64(1) {
		t.Fatalf("auto preview after link: %d %+v", rec.Code, body)
	}
	rec, _ = f.doAs(t, "admin", "admin", "DELETE", "/api/sports/teams/nba:2/links/"+links[0].(map[string]any)["id"].(string), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete link: %d", rec.Code)
	}
	rec, body = f.do(t, "GET", "/api/sports/teams/nba:2/links", "")
	if rec.Code != 200 || len(body["links"].([]any)) != 1 {
		t.Fatalf("links after delete: %d %+v", rec.Code, body)
	}
}

func mustJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestSportsStatusAndCatalogContract(t *testing.T) {
	f := newSportsFixture(t)
	rec, body := f.do(t, "GET", "/api/sports/status", "")
	if rec.Code != 200 || body["enabled"] != true || body["gameCount"].(float64) != 1 {
		t.Fatalf("status: %d %+v", rec.Code, body)
	}
	rec, body = f.do(t, "GET", "/api/sports/leagues", "")
	if rec.Code != 200 {
		t.Fatalf("leagues: %d", rec.Code)
	}
	leagues := body["leagues"].([]any)
	if len(leagues) < 55 {
		t.Fatalf("catalog shrank to %d", len(leagues))
	}
	found := false
	for _, raw := range leagues {
		l := raw.(map[string]any)
		if l["id"] == "nba" {
			found = true
			if l["enabled"] != true || l["sport"] != "basketball" || l["eventKind"] != "matchup" {
				t.Fatalf("nba descriptor wrong: %+v", l)
			}
		}
		if l["id"] == "espn:baseball:dominican-winter-league" {
			t.Fatal("unverified league advertised")
		}
	}
	if !found {
		t.Fatal("nba missing from catalog")
	}
}

func TestSportsScoreboardAndDateWindow(t *testing.T) {
	f := newSportsFixture(t)
	date := time.Now().UTC().Format("2006-01-02")
	rec, body := f.do(t, "GET", "/api/sports/scoreboard?date="+date+"&league=nba", "")
	if rec.Code != 200 {
		t.Fatalf("scoreboard: %d %+v", rec.Code, body)
	}
	games := body["games"].([]any)
	if len(games) != 1 {
		t.Fatalf("games: %+v", games)
	}
	game := games[0].(map[string]any)
	if game["league"] != "nba" || game["status"] != "live" {
		t.Fatalf("game contract wrong: %+v", game)
	}
	home := game["homeTeam"].(map[string]any)
	if home["score"] != "78" {
		t.Fatalf("score contract wrong: %+v", home)
	}
	avail := body["leagues"].([]any)
	if len(avail) != 1 || avail[0].(map[string]any)["league"] != "nba" {
		t.Fatalf("availability contract wrong: %+v", avail)
	}
	if rec, _ := f.do(t, "GET", "/api/sports/scoreboard?date=2020-01-01", ""); rec.Code != 400 {
		t.Fatalf("out-of-window date: %d", rec.Code)
	}
	if rec, _ := f.do(t, "GET", "/api/sports/scoreboard?date=nope", ""); rec.Code != 400 {
		t.Fatalf("malformed date: %d", rec.Code)
	}
}

func TestSportsHubAndEventContract(t *testing.T) {
	f := newSportsFixture(t)
	date := time.Now().UTC().Format("2006-01-02")
	rec, body := f.do(t, "GET", "/api/sports/hub?date="+date, "")
	if rec.Code != 200 {
		t.Fatalf("hub: %d", rec.Code)
	}
	events := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events: %+v", events)
	}
	event := events[0].(map[string]any)
	if event["id"] != "402" || event["league"] != "nba" || event["eventKind"] != "matchup" {
		t.Fatalf("event contract wrong: %+v", event)
	}
	if participants := event["participants"].([]any); len(participants) != 2 {
		t.Fatalf("participants: %+v", participants)
	}
	if _, ok := body["specialTournaments"]; !ok {
		t.Fatal("specialTournaments missing")
	}
	if lanes := body["leagues"].([]any); len(lanes) != 1 || lanes[0].(map[string]any)["league"] != "nba" {
		t.Fatalf("per-league availability missing from hub: %+v", body["leagues"])
	}

	rec, detail := f.do(t, "GET", "/api/sports/events/402", "")
	if rec.Code != 200 {
		t.Fatalf("event: %d", rec.Code)
	}
	panels := detail["detail"].(map[string]any)
	caps := panels["capabilities"].(map[string]any)
	if caps["plays"] != (len(panels["plays"].([]any)) > 0) || caps["stats"] != (len(panels["comparisons"].([]any)) > 0) {
		t.Fatalf("capability flags disagree with verified panels: %+v", caps)
	}
	if caps["winProbability"] != false {
		t.Fatalf("win probability exposed without data: %+v", caps)
	}
	if rec, _ := f.do(t, "GET", "/api/sports/events/missing", ""); rec.Code != 404 {
		t.Fatalf("missing event: %d", rec.Code)
	}
}

func TestSportsLogoRejectsForeignHosts(t *testing.T) {
	f := newSportsFixture(t)
	if rec, _ := f.do(t, "GET", "/api/sports/logo?url=https%3A%2F%2Fevil.example%2Fx.png", ""); rec.Code != 502 {
		t.Fatalf("foreign host accepted: %d", rec.Code)
	}
	if rec, _ := f.do(t, "GET", "/api/sports/logo", ""); rec.Code != 400 {
		t.Fatalf("missing url: %d", rec.Code)
	}
}

func TestAdminSportsSettingsValidation(t *testing.T) {
	f := newSportsFixture(t)
	rec, body := f.do(t, "GET", "/api/admin/sports", "")
	if rec.Code != 200 || body["enabled"] != true {
		t.Fatalf("admin get: %d %+v", rec.Code, body)
	}

	// Strict decoding rejects a typo instead of silently ignoring it.
	if rec, _ := f.do(t, "PUT", "/api/admin/sports", `{"enabledd":false}`); rec.Code != 400 {
		t.Fatalf("unknown field accepted: %d", rec.Code)
	}
	// A non-empty selection that resolves to no known league is rejected.
	if rec, _ := f.do(t, "PUT", "/api/admin/sports", `{"enabledLeagues":["not-a-league"]}`); rec.Code != 400 {
		t.Fatalf("all-unknown leagues accepted: %d", rec.Code)
	}
	// Unknown leagues are dropped while known ones survive.
	rec, body = f.do(t, "PUT", "/api/admin/sports", `{"enabledLeagues":["nba","nfl","not-a-league"]}`)
	if rec.Code != 200 {
		t.Fatalf("valid update rejected: %d %+v", rec.Code, body)
	}
	leagues := body["enabledLeagues"].([]any)
	if len(leagues) != 2 || leagues[0] != "nba" {
		t.Fatalf("league normalization wrong: %+v", leagues)
	}
	if f.svc.GetStatus().Enabled != true {
		t.Fatal("service disabled after a valid update")
	}

	// Disabling stops polling without erasing cached scores.
	rec, body = f.do(t, "PUT", "/api/admin/sports", `{"enabled":false}`)
	if rec.Code != 200 || f.svc.GetStatus().Enabled != false {
		t.Fatalf("disable: %d %+v", rec.Code, f.svc.GetStatus())
	}
	if len(f.svc.GetScoreboard("nba")) != 1 {
		t.Fatal("disabling erased cached scores")
	}
	f.waitIdle(t)
}

func TestSportsRoutesAreRegistered(t *testing.T) {
	// Unauthenticated requests reach the sports routes and are refused by auth
	// (401), rather than falling through to the API's JSON 404.
	s := &Server{Cfg: &config.Config{}}
	h := s.Router()
	for _, path := range []string{
		"/api/sports/status", "/api/sports/leagues", "/api/sports/scoreboard", "/api/sports/hub", "/api/sports/events/1",
		"/api/sports/preferences", "/api/sports/teams", "/api/sports/teams/linked", "/api/sports/teams/nba:2/links",
		"/api/sports/teams/nba:2/suggestions", "/api/sports/leagues/nba/auto-link/preview",
		"/api/sports/races", "/api/sports/races/f1/1/2", "/api/sports/races/f1/1/2/archive",
		"/api/sports/cycling", "/api/sports/cycling/tour/2026/1", "/api/sports/motogp/standings",
		"/api/sports/motogp/circuit/abc", "/api/sports/leagues/nfl/standings",
	} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d, want 401 (route missing?)", path, rec.Code)
		}
	}
	req := httptest.NewRequest("GET", "/api/sports/not-a-route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown sports route: %d", rec.Code)
	}
}

func TestSportsEventEnrichesDetailAndCapabilities(t *testing.T) {
	f := newSportsFixture(t)
	rec, body := f.do(t, "GET", "/api/sports/events/402", "")
	if rec.Code != 200 {
		t.Fatalf("event: %d %s", rec.Code, rec.Body.String())
	}
	detail, ok := body["detail"].(map[string]any)
	if !ok {
		t.Fatalf("missing detail: %+v", body)
	}
	periods := detail["periods"].([]any)
	if len(periods) != 3 {
		t.Fatalf("periods = %+v", periods)
	}
	if detail["plays"].([]any)[0].(map[string]any)["description"] != "Tatum makes layup" {
		t.Fatalf("plays = %+v", detail["plays"])
	}
	capabilities := detail["capabilities"].(map[string]any)
	if capabilities["plays"] != true || capabilities["stats"] != true {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}

func TestSportsLiveSearchContracts(t *testing.T) {
	f := newSportsFixture(t)
	rec, _ := f.do(t, "GET", "/api/sports/search?q=c", "")
	if rec.Code != 400 {
		t.Fatalf("short query status = %d", rec.Code)
	}
	rec, body := f.do(t, "GET", "/api/sports/search?q=celtics", "")
	if rec.Code != 200 {
		t.Fatalf("search status = %d %s", rec.Code, rec.Body.String())
	}
	if events := body["sportsEvents"].([]any); len(events) != 1 || events[0].(map[string]any)["id"] != "402" {
		t.Fatalf("events = %+v", events)
	}
	teams := body["teams"].([]any)
	if len(teams) != 1 || teams[0].(map[string]any)["name"] != "Boston Celtics" {
		t.Fatalf("teams = %+v", teams)
	}
	// A team result carries its competition: provider team ids repeat per league.
	if teams[0].(map[string]any)["league"] != "nba" {
		t.Fatalf("team identity lost its league: %+v", teams[0])
	}
	// Without Live TV there is no channel or EPG data; the endpoint stays honest.
	if len(body["channels"].([]any)) != 0 || len(body["livePrograms"].([]any)) != 0 || len(body["upcomingPrograms"].([]any)) != 0 {
		t.Fatalf("invented live data: %+v", body)
	}
	rec, body = f.do(t, "GET", "/api/sports/search?q=heat", "")
	if rec.Code != 200 || len(body["sportsEvents"].([]any)) != 1 {
		t.Fatalf("heat search: %d %+v", rec.Code, body)
	}
}

func TestSportsStreamQualityContracts(t *testing.T) {
	f := newSportsFixture(t)
	rec, _ := f.do(t, "POST", "/api/sports/streams/quality", `{}`)
	if rec.Code != 400 {
		t.Fatalf("missing channel status = %d", rec.Code)
	}
	rec, _ = f.do(t, "POST", "/api/sports/streams/quality", `{"bogus":1}`)
	if rec.Code != 400 {
		t.Fatalf("unknown field status = %d", rec.Code)
	}
	rec, body := f.do(t, "POST", "/api/sports/streams/quality", `{"channelId":"missing"}`)
	if rec.Code != 404 || !strings.Contains(fmt.Sprint(body["error"]), "not found") {
		t.Fatalf("unknown channel: %d %+v", rec.Code, body)
	}
}

func TestStreamMatchCacheIsBoundedAndExpires(t *testing.T) {
	var c streamMatchCache
	for i := 0; i < streamMatchEntries; i++ {
		c.put(fmt.Sprint(i), streamMatchEntry{})
	}
	if len(c.entries) != streamMatchEntries {
		t.Fatalf("cache size = %d", len(c.entries))
	}
	c.put("overflow", streamMatchEntry{})
	if len(c.entries) != streamMatchEntries {
		t.Fatalf("cache was not bounded: %d", len(c.entries))
	}
	// One answer makes room for the new one; the cache is not emptied
	// wholesale, which would turn every overflow into a provider burst.
	kept := 0
	for i := 0; i < streamMatchEntries; i++ {
		if _, ok := c.entries[fmt.Sprint(i)]; ok {
			kept++
		}
	}
	if kept != streamMatchEntries-1 {
		t.Fatalf("evicted %d entries instead of one", streamMatchEntries-kept)
	}
	if _, ok := c.entries["overflow"]; !ok {
		t.Fatal("the new entry was not stored")
	}
	c.put("expired", streamMatchEntry{})
	c.mu.Lock()
	entry := c.entries["expired"]
	entry.expires = time.Now().Add(-time.Second)
	c.entries["expired"] = entry
	c.mu.Unlock()
	if _, ok := c.get("expired"); ok {
		t.Fatal("expired entry was served")
	}
}

func TestSportsEventStreamsHonestWhenLiveTVOff(t *testing.T) {
	f := newSportsFixture(t)
	rec, body := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != 200 || body["liveTV"] != false {
		t.Fatalf("streams: %d %+v", rec.Code, body)
	}
	if len(body["streams"].([]any)) != 0 || len(body["groups"].([]any)) != 0 {
		t.Fatalf("invented streams without Live TV: %+v", body)
	}
	rec, _ = f.do(t, "GET", "/api/sports/events/missing/streams", "")
	if rec.Code != 404 {
		t.Fatalf("missing event status = %d", rec.Code)
	}
	rec, _ = f.do(t, "GET", "/api/sports/match", "")
	if rec.Code != 400 {
		t.Fatalf("match without event status = %d", rec.Code)
	}
	rec, body = f.do(t, "GET", "/api/sports/match?event=402", "")
	if rec.Code != 200 || body["eventId"] != "402" {
		t.Fatalf("match query: %d %+v", rec.Code, body)
	}
}

func TestSportsRaceRoutesAndSessionDetail(t *testing.T) {
	f := newSportsFixture(t)
	rec, body := f.do(t, "GET", "/api/sports/races", "")
	if rec.Code != 200 {
		t.Fatalf("races: %d %s", rec.Code, rec.Body.String())
	}
	events := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("race events = %+v", events)
	}
	weekend := events[0].(map[string]any)
	if weekend["id"] != "f1:600057442" || weekend["eventKind"] != "race" {
		t.Fatalf("unexpected weekend %+v", weekend)
	}
	sessions := weekend["subEvents"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if weekend["circuit"] == nil {
		t.Fatal("verified circuit geometry not attached")
	}

	rec, session := f.do(t, "GET", "/api/sports/races/f1/600057442/401839102", "")
	if rec.Code != 200 {
		t.Fatalf("session: %d %s", rec.Code, rec.Body.String())
	}
	participants := session["participants"].([]any)
	if len(participants) != 2 {
		t.Fatalf("session drivers = %+v", participants)
	}
	winner := participants[0].(map[string]any)
	if winner["position"] != float64(1) || winner["team"] != "Ferrari" || winner["number"] != "16" {
		t.Fatalf("enriched leader = %+v", winner)
	}
	stats := winner["statistics"].([]any)
	if len(stats) == 0 {
		t.Fatalf("statistics missing: %+v", winner)
	}

	rec, archive := f.do(t, "GET", "/api/sports/races/f1/600057442/401839102/archive", "")
	if rec.Code != 200 || archive["mode"] != "archive" {
		t.Fatalf("archive: %d %+v", rec.Code, archive)
	}
	if archive["state"] != "unavailable" {
		// This transport has no static archive; an unreachable archive must be
		// reported, never fabricated.
		t.Fatalf("archive claimed data without a provider: %+v", archive)
	}

	if rec, _ := f.do(t, "GET", "/api/sports/races/f1/600057442/999", ""); rec.Code != 404 {
		t.Fatalf("unknown session status = %d", rec.Code)
	}
	if rec, _ := f.do(t, "GET", "/api/sports/races/nope/600057442/401839102", ""); rec.Code != 404 {
		t.Fatalf("unsupported league status = %d", rec.Code)
	}
}

// The racing section links a session, which is not an id in the polled
// scoreboard. The watch flow has to name the kind and the weekend it belongs to,
// otherwise the server cannot resolve the id and honestly answers 404.
func TestSportsRaceSessionWatchLookup(t *testing.T) {
	f := newSportsFixture(t)
	const (
		sessionID = "f1:600057442:401839102"
		weekendID = "f1:600057442"
	)
	// The web client percent-encodes the colons, and chi matches the escaped
	// path, so the encoded form is the one that has to work.
	escaped := strings.NewReplacer(":", "%3A").Replace(sessionID)
	path := "/api/sports/events/" + escaped + "/streams"
	if rec, _ := f.do(t, "GET", path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unqualified session id status = %d", rec.Code)
	}
	rec, body := f.do(t, "GET", path+"?kind=racing&parentId="+strings.NewReplacer(":", "%3A").Replace(weekendID), "")
	if rec.Code != 200 {
		t.Fatalf("race session streams: %d %s", rec.Code, rec.Body.String())
	}
	if body["eventId"] != sessionID {
		t.Fatalf("event id = %v", body["eventId"])
	}
	// The fixture has no Live TV configured; the answer says so instead of
	// pretending the session was matched.
	if body["liveTV"] != false {
		t.Fatalf("fixture liveTV = %+v", body["liveTV"])
	}
}

func TestSportsCyclingMotoGPAndStandingsContracts(t *testing.T) {
	f := newSportsFixture(t)
	rec, feed := f.do(t, "GET", "/api/sports/cycling", "")
	if rec.Code != 200 {
		t.Fatalf("cycling: %d %s", rec.Code, rec.Body.String())
	}
	// Cycling is not enabled in this fixture; the feed says so rather than
	// returning invented races.
	if feed["state"] != "unavailable" {
		t.Fatalf("cycling claimed data: %+v", feed)
	}
	if rec, _ := f.do(t, "GET", "/api/sports/cycling/tour/2026/1", ""); rec.Code != 404 {
		t.Fatalf("cycling stage status = %d", rec.Code)
	}
	if rec, _ := f.do(t, "GET", "/api/sports/cycling/tour/nope/1", ""); rec.Code != 400 {
		t.Fatalf("invalid cycling stage year status = %d", rec.Code)
	}

	rec, standings := f.do(t, "GET", "/api/sports/motogp/standings", "")
	if rec.Code != 200 || standings["state"] != "unavailable" {
		t.Fatalf("motogp standings: %d %+v", rec.Code, standings)
	}
	rec, circuit := f.do(t, "GET", "/api/sports/motogp/circuit/f3fd8ba7-2966-46bd-8687-b92047f5e733", "")
	if rec.Code != 200 || circuit["state"] != "unavailable" {
		t.Fatalf("motogp circuit: %d %+v", rec.Code, circuit)
	}
	if rec, _ := f.do(t, "GET", "/api/sports/motogp/circuit/not-a-uuid", ""); rec.Code != 200 {
		t.Fatalf("invalid motogp circuit status = %d", rec.Code)
	}

	rec, table := f.do(t, "GET", "/api/sports/leagues/nfl/standings", "")
	if rec.Code != 200 || table["state"] != "unavailable" || table["league"] != "nfl" {
		t.Fatalf("standings contract: %d %+v", rec.Code, table)
	}
}

func TestSportsCricketSeriesDiscoveryContract(t *testing.T) {
	f := newSportsFixture(t)
	rec, body := f.do(t, "GET", "/api/sports/cricket/series", "")
	if rec.Code != 200 {
		t.Fatalf("cricket series: %d %s", rec.Code, rec.Body.String())
	}
	// Discovery is never claimed exhaustive, and a fresh read is not stale.
	if body["complete"] != false || body["stale"] != false || body["note"] == "" {
		t.Fatalf("discovery flags: %+v", body)
	}
	rows, ok := body["series"].([]any)
	if !ok || len(rows) != 3 {
		t.Fatalf("series rows: %+v", body["series"])
	}
	series, _ := rows[0].(map[string]any)
	if series["providerSeriesId"] != "24627" || series["season"] != "2027" || series["kind"] != "series" {
		t.Fatalf("series row: %+v", series)
	}
	competition, _ := rows[2].(map[string]any)
	// The IPL keeps its polled catalog identity instead of gaining a second one.
	if competition["id"] != "cricket-8048" || competition["kind"] != "competition" {
		t.Fatalf("competition row: %+v", competition)
	}

	// A candidate the catalog does not poll must not leak into the supported
	// league list just because discovery saw it.
	_, leagues := f.do(t, "GET", "/api/sports/leagues", "")
	encoded := fmt.Sprintf("%+v", leagues["leagues"])
	if strings.Contains(encoded, "espn:cricket:99999") || strings.Contains(encoded, "Unpolled Series 2030") {
		t.Fatalf("discovery candidate entered the league catalog: %s", encoded)
	}
}

// liveTVSportsFixture starts a sports fixture with a two-group playlist so
// discovery scope and manual search can be compared on real channel identities.
func liveTVSportsFixture(t *testing.T) *sportsFixture {
	t.Helper()
	f := newSportsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	playlist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-mpegurl")
		_, _ = io.WriteString(w, `#EXTM3U
#EXTINF:-1 tvg-id="celtics.us" tvg-name="Boston Celtics" group-title="Premium",Boston Celtics
http://channel.example/celtics.m3u8
#EXTINF:-1 tvg-id="celtics-hd.us" group-title="Sports",Celtics HD
http://channel.example/celtics-hd.m3u8
`)
	}))
	t.Cleanup(playlist.Close)
	f.cfg.LiveTV.Enabled = true
	f.cfg.LiveTV.Sources = []config.LiveTVSource{{ID: "fixture", Name: "Fixture", Kind: "m3u", URL: playlist.URL, Enabled: true}}
	f.s.LiveTV.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !f.s.LiveTV.Active() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !f.s.LiveTV.Active() {
		t.Fatal("fixture Live TV did not load")
	}
	return f
}

func streamChannelNames(body map[string]any) map[string]bool {
	names := map[string]bool{}
	rows, _ := body["streams"].([]any)
	for _, raw := range rows {
		if stream, ok := raw.(map[string]any); ok {
			names[fmt.Sprint(stream["channelName"])] = true
		}
	}
	return names
}

func TestSportsEventStreamScopeAppliesToDiscoveryOnly(t *testing.T) {
	// No scope: every visible channel is discoverable.
	open := liveTVSportsFixture(t)
	rec, body := open.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != 200 {
		t.Fatalf("unscoped streams: %d %s", rec.Code, rec.Body.String())
	}
	names := streamChannelNames(body)
	if !names["Boston Celtics"] || !names["Celtics HD"] {
		t.Fatalf("unscoped discovery = %+v", names)
	}

	// A "Sports" group scope narrows automatic discovery, even though the
	// Premium channel is the stronger name match.
	scoped := liveTVSportsFixture(t)
	scoped.cfg.Sports.Groups = []string{"Sports"}
	rec, body = scoped.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != 200 {
		t.Fatalf("scoped streams: %d %s", rec.Code, rec.Body.String())
	}
	names = streamChannelNames(body)
	if len(names) != 1 || !names["Celtics HD"] {
		t.Fatalf("scope not applied to discovery: %+v", names)
	}
	// An explicit search stays global: the operator asked for that name.
	rec, body = scoped.do(t, "GET", "/api/sports/events/402/streams?q=Celtics", "")
	if rec.Code != 200 {
		t.Fatalf("manual search: %d %s", rec.Code, rec.Body.String())
	}
	names = streamChannelNames(body)
	if !names["Boston Celtics"] || !names["Celtics HD"] {
		t.Fatalf("scope wrongly narrowed manual search: %+v", names)
	}

	// Hub search returns the same playable channel identities the watch flow
	// uses, even though the event itself is not in the loaded day's feed here.
	rec, results := scoped.do(t, "GET", "/api/sports/search?q=Celtics", "")
	if rec.Code != 200 {
		t.Fatalf("hub search: %d %s", rec.Code, rec.Body.String())
	}
	channels := results["channels"].([]any)
	if len(channels) == 0 {
		t.Fatalf("hub search found no channels: %s", rec.Body.String())
	}
	for _, raw := range channels {
		channel := raw.(map[string]any)
		id, _ := channel["id"].(string)
		if id == "" {
			t.Fatalf("search result without a playable id: %+v", channel)
		}
		if _, ok := scoped.s.LiveTV.MatchChannelByID(id); !ok {
			t.Fatalf("search returned an unplayable channel id %q", id)
		}
	}
}

// sportsTeamSyncFixture builds the minimal server a team-catalog sync needs:
// real config, database and sports service, with a scripted provider.
func sportsTeamSyncFixture(t *testing.T, leagues []string, provider sportsTransport) *sportsFixture {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sports.Leagues = leagues
	cfg.Sports.Normalize()
	svc := sports.NewWithClient(t.TempDir(), &http.Client{Transport: provider})
	svc.SetEnabledLeagueIDs(cfg.Sports.PollingLeagueIDs())
	database, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	s := &Server{Cfg: cfg, DB: database, Sports: svc, LiveTV: livetv.New(cfg, dir)}
	return &sportsFixture{s: s, svc: svc, cfg: cfg, dir: dir}
}

// TestAdminSportsTeamsPersistsPartialSync pins the admin resync contract: one
// league with no team endpoint and one transiently failing league must not
// discard the leagues that succeeded, and must not be reported as clean.
func TestAdminSportsTeamsPersistsPartialSync(t *testing.T) {
	f := sportsTeamSyncFixture(t, []string{"nba", "nfl", "cricket-8048"}, func(r *http.Request) (int, string) {
		switch {
		case strings.Contains(r.URL.Path, "/basketball/nba/teams"):
			return 200, apiNBATeams
		case strings.Contains(r.URL.Path, "/football/nfl/teams"):
			return 500, `{"error":"provider unavailable"}`
		case strings.Contains(r.URL.Path, "/teams"):
			return 404, `{"code":404,"message":"404 NOT_FOUND \"League not found\""}`
		}
		return 200, `{"events":[]}`
	})

	rec := httptest.NewRecorder()
	f.s.handleAdminSportsTeams(rec, httptest.NewRequest("POST", "/api/admin/sports/teams", nil))
	if rec.Code != 200 {
		t.Fatalf("partial sync must not fail as a whole: %d %s", rec.Code, rec.Body.String())
	}
	body := mustJSON(t, rec)
	if changed, _ := body["changed"].(float64); changed != 2 {
		t.Fatalf("successful league was not persisted: changed=%v body=%s", body["changed"], rec.Body.String())
	}
	failed, _ := body["failed"].([]any)
	if len(failed) != 1 {
		t.Fatalf("failed leagues = %+v", body["failed"])
	}
	if row, _ := failed[0].(map[string]any); row["league"] != "nfl" {
		t.Fatalf("failed league not identified: %+v", failed[0])
	}
	unsupported, _ := body["unsupported"].([]any)
	if len(unsupported) != 1 || unsupported[0] != "cricket-8048" {
		t.Fatalf("unsupported leagues = %+v", body["unsupported"])
	}

	rows, err := f.s.DB.ListSportsTeams(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("persisted teams = %+v", rows)
	}
	for _, row := range rows {
		if row.League != "nba" {
			t.Fatalf("unexpected league persisted: %+v", row)
		}
	}
}

// TestAdminSportsTeamsReportsTotalFailure keeps a genuine total failure loud:
// nothing loaded means there is no partial success to return.
func TestAdminSportsTeamsReportsTotalFailure(t *testing.T) {
	f := sportsTeamSyncFixture(t, []string{"nba"}, func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/teams") {
			return 500, `{"error":"provider unavailable"}`
		}
		return 200, `{"events":[]}`
	})

	rec := httptest.NewRecorder()
	f.s.handleAdminSportsTeams(rec, httptest.NewRequest("POST", "/api/admin/sports/teams", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("total failure status = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "nba") {
		t.Fatalf("failure did not name the league: %s", rec.Body.String())
	}
}

// TestCricketHomeAwayMatchingWithoutTeamCatalog pins that IPL events keep their
// home/away participants and still match live channels although ESPN publishes
// no cricket team catalog.
func TestCricketHomeAwayMatchingWithoutTeamCatalog(t *testing.T) {
	f := sportsTeamSyncFixture(t, []string{"cricket-8048"}, func(r *http.Request) (int, string) {
		switch {
		case strings.Contains(r.URL.Path, "/teams"):
			return 404, `{"code":404,"message":"404 NOT_FOUND \"League not found\""}`
		case strings.Contains(r.URL.Path, "/cricket/8048/scoreboard"):
			return 200, apiCricketScoreboard
		}
		return 200, `{"events":[]}`
	})
	if err := f.svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	games := f.svc.GetScoreboard("cricket-8048")
	if len(games) != 1 {
		t.Fatalf("cricket events = %+v", games)
	}
	if games[0].HomeTeam.Name != "Chennai Super Kings" || games[0].AwayTeam.Name != "Mumbai Indians" {
		t.Fatalf("home/away participants lost: %+v", games[0])
	}
	if _, err := f.svc.EnsureTeamCatalog(context.Background()); err != nil {
		t.Fatalf("unsupported catalog reported as failure: %v", err)
	}
	if teams := f.svc.TeamCatalog(""); len(teams) != 0 {
		t.Fatalf("team catalog invented cricket teams: %+v", teams)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	playlist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-mpegurl")
		_, _ = io.WriteString(w, `#EXTM3U
#EXTINF:-1 tvg-id="csk-in.in" group-title="Sports",Chennai Super Kings vs Mumbai Indians
http://channel.example/ipl.m3u8
`)
	}))
	t.Cleanup(playlist.Close)
	f.cfg.LiveTV.Enabled = true
	f.cfg.LiveTV.Sources = []config.LiveTVSource{{ID: "fixture", Name: "Fixture", Kind: "m3u", URL: playlist.URL, Enabled: true}}
	f.s.LiveTV.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !f.s.LiveTV.Active() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !f.s.LiveTV.Active() {
		t.Fatal("fixture Live TV did not load")
	}

	mux := chi.NewRouter()
	mux.Get("/api/sports/events/{id}/streams", f.s.handleSportsEventStreams)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sports/events/"+games[0].ID+"/streams", nil))
	if rec.Code != 200 {
		t.Fatalf("streams: %d %s", rec.Code, rec.Body.String())
	}
	names := streamChannelNames(mustJSON(t, rec))
	if !names["Chennai Super Kings vs Mumbai Indians"] {
		t.Fatalf("cricket home/away matching failed without a team catalog: %+v", names)
	}
}
