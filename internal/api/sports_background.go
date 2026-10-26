package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/sports"
)

// sportsStageBudget bounds one stage of the cycle. It exceeds the paced time for
// the largest catalog so a cold start is not cut short, and stays under the 90 s
// interval so a warm cycle keeps its cadence.
const sportsStageBudget = 75 * time.Second

// RunSportsBackground keeps scoreboards, team identities and the persisted
// catalog fresh. Each stage re-reads the settings so disabling the hub stops
// the cycle without a restart, and a failing provider never erases last-good
// scores. The interval matches MediaStorm's 90-second sports cycle.
func (s *Server) RunSportsBackground(ctx context.Context) {
	interval := 90 * time.Second
	s.sportsCycle(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sportsCycle(ctx)
		}
	}
}

func (s *Server) sportsCycle(ctx context.Context) {
	if s.Sports == nil || !s.Cfg.Sports.IsEnabled() {
		return
	}
	s.Sports.SetEnabledLeagueIDs(s.Cfg.Sports.PollingLeagueIDs())
	// Each stage gets more than one paced pass needs: on a cold cache the whole
	// catalog is fetched, which is bounded by the network pacing rather than by
	// the provider. A warm cycle finishes in seconds because cached scoreboards
	// are reused, so these budgets only shape startup and recovery.
	refreshCtx, cancel := context.WithTimeout(ctx, sportsStageBudget)
	err := s.Sports.Refresh(refreshCtx)
	cancel()
	if err != nil {
		slog.Warn("sports scoreboard cycle", "err", err)
	}
	if ctx.Err() != nil || !s.Cfg.Sports.IsEnabled() {
		return
	}
	teamCtx, cancel := context.WithTimeout(ctx, sportsStageBudget)
	report, err := s.Sports.EnsureTeamCatalog(teamCtx)
	cancel()
	if err != nil {
		slog.Warn("sports team catalog cycle", "err", err)
	}
	if len(report.Teams) > 0 {
		if _, err := s.syncSportsTeams(ctx, report.Teams); err != nil {
			slog.Warn("sports team catalog persist", "err", err)
		}
	}
}

// syncSportsTeams persists provider team identities with change detection. It
// writes whatever leagues loaded, so one failing league never discards the work
// the others completed.
func (s *Server) syncSportsTeams(ctx context.Context, teams []sports.SportsTeamRecord) (int, error) {
	rows := make([]db.SportsTeamRow, 0, len(teams))
	for _, team := range teams {
		rows = append(rows, db.SportsTeamRow{
			ID: team.ID, League: team.League, ProviderID: team.ProviderID, Name: team.Name,
			Location: team.Location, Nickname: team.Nickname,
			Abbreviation: team.Abbreviation, LogoURL: team.LogoURL,
		})
	}
	return s.DB.SyncSportsTeams(ctx, rows)
}

// decodeStrict decodes a JSON body into v, rejecting unknown fields and
// trailing data. Admin settings use it so a typo never silently does nothing.
func decodeStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errTrailingJSON
	}
	return nil
}
