package sports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const cricketDropdownURL = "https://site.api.espn.com/apis/site/v2/leagues/dropdown?region=us&lang=en&sport=cricket"

const cricketDiscoveryTTL = 24 * time.Hour
const cricketDiscoveryMaxBytes = 4 << 20

// CricketSeries is a discovery candidate, not a validated League. A trophy or
// tournament identifier is never turned into a polling endpoint: it is shown so
// an admin can see what the provider publishes, and nothing else.
type CricketSeries struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ProviderSeriesID string `json:"providerSeriesId"`
	CompetitionID    string `json:"competitionId,omitempty"`
	Season           string `json:"season,omitempty"`
	Kind             string `json:"kind"`
}

type cricketDiscoverySnapshot struct {
	UpdatedAt time.Time       `json:"updatedAt"`
	Series    []CricketSeries `json:"series"`
	// The provider dropdown is capped, so discovery is never claimed complete.
	Complete bool `json:"complete"`
}

var cricketSeriesIDPattern = regexp.MustCompile(`^[0-9]+$`)
var cricketSeasonPattern = regexp.MustCompile(`\s+(20[0-9]{2}(?:/[0-9]{2,4})?)$`)

func parseCricketDiscovery(data []byte) ([]CricketSeries, error) {
	var payload struct {
		Leagues []struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"leagues"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode cricket dropdown: %w", err)
	}
	result := make([]CricketSeries, 0, len(payload.Leagues))
	seen := map[string]bool{}
	for _, row := range payload.Leagues {
		slug := strings.TrimSpace(row.Slug)
		name := strings.TrimSpace(row.Name)
		// The real feed contains empty and non-numeric slugs; they must never
		// become root/all or arbitrary provider URLs.
		if !cricketSeriesIDPattern.MatchString(slug) || name == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		series := CricketSeries{ID: "espn:cricket:" + slug, Name: name, ProviderSeriesID: slug, Kind: "series"}
		if match := cricketSeasonPattern.FindStringSubmatch(name); len(match) > 1 {
			series.Season = match[1]
		}
		// The IPL is the one cricket competition this service already polls, so
		// the dropdown row keeps its catalog identity instead of a new one.
		if slug == "8048" {
			series.ID = "cricket-8048"
			series.CompetitionID = "cricket-8048"
			series.Kind = "competition"
		}
		result = append(result, series)
	}
	return result, nil
}

// DiscoverCricketSeries persists provider identities beside the other sports
// caches. It never mutates the league registry or promotes a dropdown row to
// live coverage, and a network failure returns the last good snapshot with the
// error so callers can report staleness honestly.
func (s *Service) DiscoverCricketSeries(ctx context.Context) ([]CricketSeries, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(s.storageDir, sportsCacheDir, "cricket-series.json")
	var cached cricketDiscoverySnapshot
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cached)
	}
	if len(cached.Series) > 0 && time.Since(cached.UpdatedAt) >= 0 && time.Since(cached.UpdatedAt) < cricketDiscoveryTTL {
		return cached.Series, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cricketDropdownURL, nil)
	if err != nil {
		return cached.Series, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return cached.Series, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return cached.Series, fmt.Errorf("cricket discovery HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, cricketDiscoveryMaxBytes+1))
	if err != nil {
		return cached.Series, err
	}
	if len(data) > cricketDiscoveryMaxBytes {
		return cached.Series, fmt.Errorf("cricket dropdown exceeds response limit")
	}
	rows, err := parseCricketDiscovery(data)
	if err != nil {
		return cached.Series, err
	}
	if len(rows) == 0 {
		return cached.Series, fmt.Errorf("cricket dropdown contains no usable series")
	}
	snapshot := cricketDiscoverySnapshot{UpdatedAt: time.Now(), Series: rows, Complete: false}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return rows, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return rows, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "cricket-series-*.tmp")
	if err != nil {
		return rows, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(encoded); err != nil {
		tmp.Close()
		return rows, err
	}
	if err = tmp.Close(); err != nil {
		return rows, err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return rows, err
	}
	return rows, nil
}
