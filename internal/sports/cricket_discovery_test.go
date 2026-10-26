package sports

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCricketDropdownSeparatesSeriesAndPreservesIPL(t *testing.T) {
	rows, err := parseCricketDiscovery([]byte(`{"leagues":[{"name":"Olympics 2028","id":"1481569","slug":""},{"name":"The Ashes 2027","slug":"24627"},{"name":"Indian Premier League","slug":"8048"},{"name":"Duplicate","slug":"8048"},{"name":"Bad","slug":"../all"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("unexpected rows %+v", rows)
	}
	if rows[0].ProviderSeriesID != "24627" || rows[0].Season != "2027" || rows[0].Kind != "series" || rows[0].ID != "espn:cricket:24627" {
		t.Fatalf("series row %+v", rows[0])
	}
	// The IPL keeps its catalog identity so a dropdown row never becomes a
	// second, polled competition.
	if rows[1].ID != "cricket-8048" || rows[1].Kind != "competition" || rows[1].CompetitionID != "cricket-8048" {
		t.Fatalf("competition row %+v", rows[1])
	}
	if _, err := parseCricketDiscovery([]byte("not json")); err == nil {
		t.Fatal("invalid dropdown accepted")
	}
}

func TestCricketDiscoveryCachesAndKeepsLastGood(t *testing.T) {
	calls := 0
	fail := false
	s := NewWithClient(t.TempDir(), &http.Client{Transport: fixtureTransport{body: func(r *http.Request) (int, string) {
		calls++
		if fail {
			return 503, `{}`
		}
		return 200, `{"leagues":[{"name":"The Ashes 2027","slug":"24627"},{"name":"Unpolled Series 2030","slug":"99999"}]}`
	}}})
	rows, err := s.DiscoverCricketSeries(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatalf("discovery: %+v %v", rows, err)
	}
	if rows, err = s.DiscoverCricketSeries(context.Background()); err != nil || len(rows) != 2 || calls != 1 {
		t.Fatalf("fresh cache refetched: %d %v", calls, err)
	}
	path := filepath.Join(s.storageDir, sportsCacheDir, "cricket-series.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"complete":false`) {
		t.Fatal("discovery claimed an exhaustive provider list")
	}
	// Expire the snapshot without touching the catalog, then fail the provider.
	stale := fmt.Sprintf(`{"updatedAt":%q,"series":[{"id":"espn:cricket:24627","providerSeriesId":"24627","kind":"series"}]}`, time.Now().Add(-48*time.Hour).Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	fail = true
	rows, err = s.DiscoverCricketSeries(context.Background())
	// The expired snapshot is the last good one, not a fresh response.
	if err == nil || len(rows) != 1 || rows[0].ProviderSeriesID != "24627" {
		t.Fatalf("lost last good snapshot: %+v %v", rows, err)
	}
	// A candidate the catalog does not already poll stays a candidate:
	// discovery never promotes a dropdown row into the league registry.
	for _, league := range LeagueCatalog {
		if league.ID == "espn:cricket:99999" {
			t.Fatal("discovery candidate entered the league catalog")
		}
	}
}
