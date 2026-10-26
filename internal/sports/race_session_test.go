package sports

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const raceSessionScoreboard = `{"events":[{"id":"1","name":"X Grand Prix","competitions":[{"id":"2","date":"2026-09-06T13:00Z","type":{"abbreviation":"Race"},"status":{"type":{"state":"post","detail":"Final"}},"competitors":[{"id":"5498","athlete":{"displayName":"A Driver","shortName":"A. Driver"}},{"id":"4665","athlete":{"displayName":"B Driver","shortName":"B. Driver"}}]}]}]}`

const raceSessionCore = `{"id":"2","competitors":[{"id":"5498","vehicle":{"number":"16","team":"Ferrari","manufacturer":"Ferrari"}},{"id":"4665","vehicle":{"number":"1","team":"Red Bull Racing","manufacturer":"Red Bull"}}]}`

const raceSessionStatistics = `{"splits":{"categories":[{"stats":[{"name":"place","value":1,"displayValue":"1"},{"name":"lapsCompleted","value":53,"displayValue":"53"},{"name":"pitsTaken","value":0,"displayValue":"0"}]}]}}`

func TestRaceSessionEnrichesDriversAndStatistics(t *testing.T) {
	s := testService(t, func(r *http.Request) (int, string) {
		switch {
		case strings.Contains(r.URL.Path, "/racing/f1/scoreboard"):
			return 200, raceSessionScoreboard
		case strings.Contains(r.URL.Path, "/competitors/") && strings.HasSuffix(r.URL.Path, "/status"):
			return 200, `{"displayValue":"Finished"}`
		case strings.Contains(r.URL.Path, "/competitors/"):
			return 200, raceSessionStatistics
		default:
			return 200, raceSessionCore
		}
	})
	session, err := s.GetRaceSession(context.Background(), "f1", "1", "2")
	if err != nil {
		t.Fatal(err)
	}
	if session.Stale {
		t.Fatalf("session marked stale despite complete provider data: %+v", session)
	}
	if len(session.Participants) != 2 {
		t.Fatalf("drivers = %+v", session.Participants)
	}
	leader := session.Participants[0]
	if leader.Position != 1 || leader.Team != "Ferrari" || leader.Number != "16" {
		t.Fatalf("leader not enriched: %+v", leader)
	}
	if len(leader.Statistics) == 0 || leader.Statistics[0].Label == "" {
		t.Fatalf("statistics missing: %+v", leader)
	}
	if session.UpdatedAt.IsZero() {
		t.Fatal("session timestamp missing")
	}
}
