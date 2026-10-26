package sports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// MMA detail comes from the core athlete/competitor endpoints, not the shared
// summary document. Identifiers are validated before they become URL path
// segments so an event id can never address another provider resource.
var espnNumericID = regexp.MustCompile(`^[0-9]+$`)

const mmaDetailBodyLimit = 2 << 20

type mmaAthlete struct {
	ID     string `json:"id"`
	Name   string `json:"displayName"`
	Height string `json:"displayHeight"`
	Reach  string `json:"displayReach"`
	Weight string `json:"displayWeight"`
	Stance struct {
		Text string `json:"text"`
	} `json:"stance"`
	WeightClass struct {
		Text string `json:"text"`
	} `json:"weightClass"`
}

type mmaStatistics struct {
	Splits struct {
		Categories []struct {
			Stats []struct {
				Name         string `json:"name"`
				DisplayValue string `json:"displayValue"`
			} `json:"stats"`
		} `json:"categories"`
	} `json:"splits"`
}

// mmaIdentifiers resolves the provider event and competition ids from the
// canonical game, refusing anything that is not a numeric provider id.
func mmaIdentifiers(game SportsGame) (eventID, competitionID string, ok bool) {
	eventID, competitionID = strings.TrimSpace(game.ParentEventID), strings.TrimSpace(game.ProviderEventID)
	if eventID == "" || competitionID == "" {
		parts := strings.Split(game.ID, ":")
		if len(parts) != 3 || parts[0] != "ufc" {
			return "", "", false
		}
		eventID, competitionID = parts[1], parts[2]
	}
	if !espnNumericID.MatchString(eventID) || !espnNumericID.MatchString(competitionID) {
		return "", "", false
	}
	return eventID, competitionID, true
}

func (s *Service) readProviderJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sports detail HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, mmaDetailBodyLimit)).Decode(out)
}

// enrichMMA ports MediaStorm's fight enrichment: biographical profiles for both
// fighters and a comparison table built only from reported statistics. A
// scheduled bout never shows statistics, because none exist yet.
func (s *Service) enrichMMA(ctx context.Context, game SportsGame) SportsGame {
	eventID, competitionID, ok := mmaIdentifiers(game)
	if !ok {
		return game
	}
	teams := []SportsTeam{game.AwayTeam, game.HomeTeam}
	for _, team := range teams {
		if !espnNumericID.MatchString(team.ID) {
			return game
		}
	}
	cacheID := "mma:" + game.ID
	detailCacheInit.Lock()
	defer detailCacheInit.Unlock()
	now := time.Now()
	cached, exists := s.details[cacheID]
	if exists && now.Before(cached.expires) {
		return cached.game
	}

	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	detail := &SportsGameDetail{
		Source: "espn", UpdatedAt: now,
		Periods: []SportsPeriodScore{}, Plays: []SportsDetailPlay{}, Comparisons: []SportsComparison{},
	}
	// A profile only describes the fighter the provider returned; an id
	// mismatch means the response describes someone else.
	failures := 0
	allStats := map[string]map[string]string{}
	for _, team := range teams {
		var athlete mmaAthlete
		if err := s.readProviderJSON(ctx, "https://sports.core.api.espn.com/v2/sports/mma/athletes/"+team.ID, &athlete); err != nil {
			failures++
		} else if athlete.ID == team.ID {
			profile := SportsPlayerGameStats{ID: team.ID, TeamID: team.ID, Name: team.Name, Category: "Fighter profile"}
			fields := [][2]string{
				{"Height", athlete.Height}, {"Reach", athlete.Reach}, {"Weight", athlete.Weight},
				{"Stance", athlete.Stance.Text}, {"Division", athlete.WeightClass.Text},
			}
			for _, field := range fields {
				if value := strings.TrimSpace(field[1]); value != "" {
					profile.Stats = append(profile.Stats, SportsPlayerStatistic{Label: field[0], Value: value})
				}
			}
			if len(profile.Stats) > 0 {
				detail.FighterProfiles = append(detail.FighterProfiles, profile)
			}
		}
		if game.Status == SportsGameScheduled {
			continue
		}
		var stats mmaStatistics
		endpoint := "https://sports.core.api.espn.com/v2/sports/mma/leagues/ufc/events/" + eventID +
			"/competitions/" + competitionID + "/competitors/" + team.ID + "/statistics"
		if err := s.readProviderJSON(ctx, endpoint, &stats); err != nil {
			failures++
			continue
		}
		values := map[string]string{}
		for _, category := range stats.Splits.Categories {
			for _, stat := range category.Stats {
				if value := strings.TrimSpace(stat.DisplayValue); value != "" {
					values[stat.Name] = value
				}
			}
		}
		allStats[team.ID] = values
	}
	// Both sides must report a value; a one-sided or absent number is not a
	// comparison.
	comparable := [][2]string{
		{"sigStrikesLanded", "Significant strikes landed"},
		{"sigStrikesAttempted", "Significant strikes attempted"},
		{"totalStrikesLanded", "Total strikes landed"},
		{"takedownsLanded", "Takedowns landed"},
		{"takedownsAttempted", "Takedowns attempted"},
		{"timeInControl", "Control time"},
		{"knockDowns", "Knockdowns"},
	}
	for _, field := range comparable {
		away, home := allStats[game.AwayTeam.ID][field[0]], allStats[game.HomeTeam.ID][field[0]]
		if away != "" && home != "" {
			detail.Comparisons = append(detail.Comparisons, SportsComparison{Label: field[1], Away: away, Home: home})
		}
	}
	if failures > 0 && exists && cached.game.Detail != nil {
		game = cached.game
		copy := *game.Detail
		copy.Stale = true
		game.Detail = &copy
		s.cacheDetail(cacheID, detailCacheEntry{game: game, expires: now.Add(15 * time.Second)})
		return game
	}
	detail.Stale = failures > 0
	detail.Capabilities.Stats = len(detail.Comparisons) > 0
	game.Detail = detail
	ttl := 30 * time.Second
	if game.Status == SportsGameFinal {
		ttl = 10 * time.Minute
	}
	if failures > 0 {
		ttl = 15 * time.Second
	}
	s.cacheDetail(cacheID, detailCacheEntry{game: game, expires: now.Add(ttl)})
	return game
}
