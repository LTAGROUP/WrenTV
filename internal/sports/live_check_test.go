package sports

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveProviderSmoke(t *testing.T) {
	if os.Getenv("WRENTV_LIVE_SPORTS") != "1" {
		t.Skip("set WRENTV_LIVE_SPORTS=1 to contact real providers")
	}
	dir, err := os.MkdirTemp("", "wren-live-sports")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s := New(dir)
	s.SetEnabledLeagueIDs(defaultLeagueIDs)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	races := s.GetRaceBoard(ctx)
	t.Logf("races: %d weekends", len(races.Events))
	for _, l := range races.Leagues {
		t.Logf("race league %s stale=%v unavailable=%v", l.League, l.Stale, l.Unavailable)
	}
	for _, weekend := range races.Events {
		if weekend.League != "f1" {
			continue
		}
		for _, session := range weekend.SubEvents {
			detail, err := s.GetRaceSession(ctx, weekend.League, weekend.ProviderEventID, session.SessionID)
			if err != nil {
				t.Logf("  %s error %v", session.SessionType, err)
				continue
			}
			stats := 0
			positions := 0
			for _, driver := range detail.Participants {
				stats += len(driver.Statistics)
				if driver.Position > 0 {
					positions++
				}
			}
			t.Logf("  %s status=%s participants=%d positions=%d stats=%d", detail.SessionType, detail.Status, len(detail.Participants), positions, stats)
		}
	}
	enrichment := s.GetMotoGPEnrichment(ctx, "")
	t.Logf("motogp standings state=%s rows=%d reason=%q", enrichment.State, len(enrichment.Standings), enrichment.Reason)

	cycling := s.GetCycling(ctx)
	states := map[string]int{}
	for _, coverage := range cycling.Coverage {
		states[coverage.State]++
	}
	t.Logf("cycling state=%s races=%d coverage=%v", cycling.State, len(cycling.Data), states)
	for _, race := range cycling.Data {
		if race.ID != "aso:tour:"+itoa(time.Now().Year()) {
			continue
		}
		t.Logf("  race %s stages=%d", race.ID, len(race.Stages))
		stage, err := s.GetCyclingStage(ctx, "tour", time.Now().Year(), 1)
		if err != nil {
			t.Logf("    stage 1 error %v", err)
			break
		}
		t.Logf("    stage 1 %q status=%s results=%s rows=%d gc=%s gcRows=%d gcReason=%q reason=%q guide=%v", stage.Name, stage.Status, stage.Results.State, len(stage.Results.Data), stage.GeneralClassification.State, len(stage.GeneralClassification.Data), stage.GeneralClassification.Reason, stage.Results.Reason, stage.RouteGuide != nil)
		if len(stage.Results.Data) > 0 {
			t.Logf("    leader %s %s", stage.Results.Data[0].Name, stage.Results.Data[0].Gap)
		}
		break
	}

	boxing, err := s.fetchBoxingDate(ctx, "2026-09-19")
	t.Logf("boxing 2026-09-19: %d cards err=%v", len(boxing), err)
	if len(boxing) > 0 {
		t.Logf("  first %q %s %q", boxing[0].Title, boxing[0].StatusDetail, boxing[0].VenueName)
	}

	board, err := s.GetDatedScoreboard(ctx, time.Now().UTC().Format("2006-01-02"), "")
	bySport := map[string]int{}
	for _, game := range board.Games {
		bySport[game.Sport]++
	}
	t.Logf("dated board games=%d leagues=%d err=%v sports=%v", len(board.Games), len(board.Leagues), err, bySport)

	cfl := s.GetLeagueStandings(ctx, "espn:football:cfl")
	t.Logf("cfl standings state=%s groups=%d rows=%d", cfl.State, len(cfl.Groups), func() int {
		rows := 0
		for _, group := range cfl.Groups {
			rows += len(group.Rows)
		}
		return rows
	}())
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	out := ""
	for value > 0 {
		out = string(rune('0'+value%10)) + out
		value /= 10
	}
	return out
}
