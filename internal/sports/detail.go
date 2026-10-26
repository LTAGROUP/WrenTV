package sports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// detailCacheEntry caches a normalized game, including failures, so a broken
// provider cannot be hammered by every client opening the same event.
type detailCacheEntry struct {
	game    SportsGame
	expires time.Time
}

// detailMu and details are initialized lazily so a zero Service in tests still
// enriches correctly.
var detailCacheInit sync.Mutex

const (
	summaryBodyLimit = 8 << 20
	summaryTimeout   = 8 * time.Second
	detailCacheSize  = 64
)

type mlbRunner struct {
	PlayerID json.RawMessage `json:"playerId"`
}

type mlbSummary struct {
	Situation *struct {
		OnFirst  *mlbRunner `json:"onFirst"`
		OnSecond *mlbRunner `json:"onSecond"`
		OnThird  *mlbRunner `json:"onThird"`
		Balls    *int       `json:"balls"`
		Strikes  *int       `json:"strikes"`
		Outs     *int       `json:"outs"`
		Pitcher  struct {
			PlayerID json.RawMessage `json:"playerId"`
		} `json:"pitcher"`
		Batter struct {
			PlayerID json.RawMessage `json:"playerId"`
		} `json:"batter"`
	} `json:"situation"`
	Rosters []struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
		Roster []struct {
			Athlete struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
			} `json:"athlete"`
		} `json:"roster"`
	} `json:"rosters"`

	Header struct {
		ID           string `json:"id"`
		Competitions []struct {
			Status      espnStatus `json:"status"`
			Competitors []struct {
				HomeAway   string          `json:"homeAway"`
				ID         string          `json:"id"`
				Score      json.RawMessage `json:"score"`
				Hits       *int            `json:"hits"`
				Errors     *int            `json:"errors"`
				Linescores []struct {
					DisplayValue string `json:"displayValue"`
				} `json:"linescores"`
			} `json:"competitors"`
		} `json:"competitions"`
	} `json:"header"`
	Plays []struct {
		ID      string `json:"id"`
		Text    string `json:"text"`
		Scoring bool   `json:"scoringPlay"`
		Type    struct {
			Text string `json:"text"`
		} `json:"type"`
		Period struct {
			Type   string `json:"type"`
			Number int    `json:"number"`
		} `json:"period"`
	} `json:"plays"`
	Boxscore struct {
		Players []playerBoxscoreTeam `json:"players"`

		Teams []struct {
			Team struct {
				ID string `json:"id"`
			} `json:"team"`
			Statistics []struct {
				Name  string `json:"name"`
				Stats []struct {
					Name         string `json:"name"`
					DisplayValue string `json:"displayValue"`
				} `json:"stats"`
			} `json:"statistics"`
		} `json:"teams"`
	} `json:"boxscore"`
}

// playerHeadshot accepts both a URL string and the {"href": ...} object shape.
type playerHeadshot string

func (h *playerHeadshot) UnmarshalJSON(data []byte) error {
	var value string
	if json.Unmarshal(data, &value) != nil {
		var object struct {
			Href string `json:"href"`
		}
		_ = json.Unmarshal(data, &object)
		value = object.Href
	}
	*h = ""
	if u, err := url.Parse(value); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
		*h = playerHeadshot(value)
	}
	return nil
}

// EnrichGame fetches the selected ESPN summary, with a bounded cache and
// timeout. Failures keep the last good detail and mark it stale; unknown data
// is never fabricated.
func (s *Service) EnrichGame(ctx context.Context, game SportsGame) SportsGame {
	league, known := s.League(game.League)
	if !known || !league.active() || league.Provider != "espn" {
		return game
	}
	if strings.HasPrefix(league.ID, "espn:") && !league.hasCapability("summary") {
		return game
	}
	// Combat cards are not matchups in the dated scoreboard sense: their
	// profiles and fight statistics live on the core athlete endpoints.
	if league.Sport == "mma" {
		return s.enrichMMA(ctx, game)
	}
	if league.EventKind != "matchup" || league.Slug == "" {
		return game
	}
	cacheID := game.ID
	if game.League != "mlb" {
		cacheID = game.League + ":" + game.ID
	}

	detailCacheInit.Lock()
	defer detailCacheInit.Unlock()
	now := time.Now()
	cached, exists := s.details[cacheID]
	if exists && now.Before(cached.expires) {
		return cached.game
	}

	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	endpoint := "https://site.api.espn.com/apis/site/v2/sports/" + league.Sport + "/" + league.Slug +
		"/summary?event=" + url.QueryEscape(providerEventID(game))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	var result SportsGame
	if err == nil {
		var response *http.Response
		response, err = s.client.Do(req)
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = fmt.Errorf("sports summary HTTP %d", response.StatusCode)
			} else {
				decoder := json.NewDecoder(io.LimitReader(response.Body, summaryBodyLimit))
				var raw json.RawMessage
				err = decoder.Decode(&raw)
				switch {
				case err != nil:
				case league.Sport == "cricket":
					result, err = normalizeCricketDetail(game, raw, now)
				case game.League == "mlb":
					var payload mlbSummary
					err = json.Unmarshal(raw, &payload)
					if err == nil {
						result, err = normalizeMLBDetail(game, payload, now)
					}
				default:
					var payload teamSportSummary
					err = json.Unmarshal(raw, &payload)
					if err == nil {
						result, err = normalizeTeamDetail(game, payload, now)
					}
				}
				if err == nil && result.Detail != nil {
					result.Detail.Pregame = normalizePregame(result, raw, now)
				}
			}
		}
	}
	if err != nil {
		if exists && cached.game.Detail != nil {
			game = cached.game
			d := *game.Detail
			d.Stale = true
			game.Detail = &d
		}
		s.cacheDetail(cacheID, detailCacheEntry{game: game, expires: now.Add(15 * time.Second)})
		return game
	}
	ttl := 30 * time.Second
	if result.Status == SportsGameFinal {
		ttl = 10 * time.Minute
	}
	s.cacheDetail(cacheID, detailCacheEntry{game: result, expires: now.Add(ttl)})
	return result
}

// cacheDetail is called with the detail cache held. It bounds successes and
// failures alike, dropping the soonest-expiring entry.
func (s *Service) cacheDetail(id string, entry detailCacheEntry) {
	if s.details == nil {
		s.details = map[string]detailCacheEntry{}
	}
	if _, exists := s.details[id]; !exists && len(s.details) >= detailCacheSize {
		var oldestID string
		var oldest time.Time
		for key, value := range s.details {
			if oldestID == "" || value.expires.Before(oldest) {
				oldestID, oldest = key, value.expires
			}
		}
		delete(s.details, oldestID)
	}
	s.details[id] = entry
}

func validSummaryCount(value *int, maximum int) *int {
	if value == nil || *value < 0 || *value > maximum {
		return nil
	}
	return value
}

func espnScoreText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

// espnPlayerID accepts the numeric-string and numeric provider shapes.
func espnPlayerID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func scoreBoardStatValue(raw json.RawMessage) string {
	return espnScoreText(raw)
}

func periodInt(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}
