package sports

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const mmaAthleteFixture = `{"id":"%s","displayName":"Fighter %s","displayHeight":"6' 1\"","displayReach":"75\"","displayWeight":"185 lb","stance":{"text":"Orthodox"},"weightClass":{"text":"Middleweight"}}`

const mmaStatisticsFixture = `{"splits":{"categories":[{"stats":[
	{"name":"sigStrikesLanded","displayValue":"%s"},
	{"name":"sigStrikesAttempted","displayValue":"120"},
	{"name":"totalStrikesLanded","displayValue":"%s"},
	{"name":"takedownsLanded","displayValue":"%s"},
	{"name":"takedownsAttempted","displayValue":"4"},
	{"name":"timeInControl","displayValue":"%s"},
	{"name":"knockDowns","displayValue":"%s"}
]}]}}`

func mmaService(t *testing.T, calls *int) *Service {
	t.Helper()
	return NewWithClient(t.TempDir(), &http.Client{Transport: fixtureTransport{body: func(r *http.Request) (int, string) {
		*calls = *calls + 1
		if strings.Contains(r.URL.Path, "/athletes/") {
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			return 200, fmt.Sprintf(mmaAthleteFixture, id, id)
		}
		// Two different fighters so the comparisons must pair them per id.
		if strings.Contains(r.URL.Path, "/competitors/3166126/") {
			return 200, fmt.Sprintf(mmaStatisticsFixture, "72", "72", "3", "4:10", "1")
		}
		return 200, fmt.Sprintf(mmaStatisticsFixture, "40", "58", "1", "1:05", "0")
	}}})
}

func TestMMADetailBuildsProfilesAndComparisons(t *testing.T) {
	calls := 0
	s := mmaService(t, &calls)
	game := SportsGame{
		ID: "ufc:600054045:401772654", League: "ufc", Sport: "mma", EventKind: "fight-card",
		Status:   SportsGameFinal,
		AwayTeam: SportsTeam{ID: "3166126", Name: "Fighter 3166126"},
		HomeTeam: SportsTeam{ID: "2", Name: "Fighter 2"},
	}
	got := s.EnrichGame(context.Background(), game)
	if got.Detail == nil {
		t.Fatal("no detail")
	}
	if len(got.Detail.FighterProfiles) != 2 {
		t.Fatalf("profiles = %+v", got.Detail.FighterProfiles)
	}
	profile := got.Detail.FighterProfiles[0]
	if profile.Category != "Fighter profile" || len(profile.Stats) != 5 || profile.Stats[0].Label != "Height" {
		t.Fatalf("profile = %+v", profile)
	}
	if len(got.Detail.Comparisons) != 7 || !got.Detail.Capabilities.Stats {
		t.Fatalf("comparisons = %+v", got.Detail.Comparisons)
	}
	// Each side keeps its own numbers, never the other fighter's.
	var strikes *SportsComparison
	for i := range got.Detail.Comparisons {
		if got.Detail.Comparisons[i].Label == "Significant strikes landed" {
			strikes = &got.Detail.Comparisons[i]
		}
	}
	if strikes == nil || strikes.Away != "72" || strikes.Home != "40" {
		t.Fatalf("strikes = %+v", strikes)
	}
	if got.Detail.Stale {
		t.Fatal("a complete read is not stale")
	}

	// A second read is served from the detail cache, not the provider.
	before := calls
	s.EnrichGame(context.Background(), game)
	if calls != before {
		t.Fatalf("cache missed: %d extra calls", calls-before)
	}
}

func TestMMADetailSkipsStatsWhenScheduledAndRefusesUnsafeIDs(t *testing.T) {
	calls := 0
	s := mmaService(t, &calls)
	game := SportsGame{
		ID: "ufc:600054045:401772654", League: "ufc", Sport: "mma", EventKind: "fight-card",
		Status:   SportsGameScheduled,
		AwayTeam: SportsTeam{ID: "3166126"},
		HomeTeam: SportsTeam{ID: "2"},
	}
	got := s.EnrichGame(context.Background(), game)
	if got.Detail == nil || len(got.Detail.FighterProfiles) != 2 {
		t.Fatalf("scheduled profiles = %+v", got.Detail)
	}
	// A scheduled bout has no statistics to report, so the tab stays hidden.
	if len(got.Detail.Comparisons) != 0 || got.Detail.Capabilities.Stats {
		t.Fatalf("scheduled statistics invented: %+v", got.Detail.Comparisons)
	}
	if calls != 2 {
		t.Fatalf("scheduled read made %d calls", calls)
	}

	// A traversal or non-numeric event id must never reach the provider.
	before := calls
	s.EnrichGame(context.Background(), SportsGame{
		ID: "ufc:../../evil:1", League: "ufc", Sport: "mma",
		AwayTeam: SportsTeam{ID: "3166126"}, HomeTeam: SportsTeam{ID: "2"},
	})
	s.EnrichGame(context.Background(), SportsGame{
		ID: "ufc:600054045:401772654", League: "ufc", Sport: "mma",
		AwayTeam: SportsTeam{ID: "../evil"}, HomeTeam: SportsTeam{ID: "2"},
	})
	if calls != before {
		t.Fatalf("unsafe identifier requested %d times", calls-before)
	}
}
