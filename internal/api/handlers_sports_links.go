package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/livetv"
	"github.com/LTAGROUP/wrentv/internal/sports"
	"github.com/LTAGROUP/wrentv/internal/sports/match"
)

// ---------- channel helpers ----------

// sportsMatchChannels is the current visible playlist in the matcher's shape.
// Nothing here leaves the server with a provider URL attached.
func (s *Server) sportsMatchChannels() []match.Channel {
	if s.LiveTV == nil || !s.LiveTV.Active() {
		return []match.Channel{}
	}
	channels := s.LiveTV.MatchChannels()
	out := make([]match.Channel, 0, len(channels))
	for _, channel := range channels {
		out = append(out, match.Channel{
			ID: channel.ID, Name: channel.Name, TvgName: channel.TvgName, TvgID: channel.TvgID,
			Group: channel.Group, SourceID: channel.SourceID, SourceName: channel.SourceName,
			Logo: channel.Logo, Metadata: channel.Metadata,
			Now: matchProgram(channel.Now), Next: matchProgram(channel.Next),
		})
	}
	return out
}

func (s *Server) sportsChannelByID(id string) (match.Channel, bool) {
	for _, channel := range s.sportsMatchChannels() {
		if channel.ID == id {
			return channel, true
		}
	}
	return match.Channel{}, false
}

// sportsSourceIDs is the configured sports source scope, with the per-league
// override when one exists. An empty list means every provider is eligible.
func (s *Server) sportsSourceIDs(league string) []string {
	if s.Cfg == nil {
		return nil
	}
	if override, ok := s.Cfg.Sports.Overrides[strings.ToLower(league)]; ok && len(override.SourceIDs) > 0 {
		return override.SourceIDs
	}
	return s.Cfg.Sports.Sources
}

// sportsLinkScope applies the configured default source/category scope, with
// the per-league override when one exists. An empty scope means every channel.
func (s *Server) sportsLinkScope(league string, channels []match.Channel) []match.Channel {
	if s.Cfg == nil {
		return channels
	}
	groupIDs := s.Cfg.Sports.Groups
	if override, ok := s.Cfg.Sports.Overrides[strings.ToLower(league)]; ok && len(override.Groups) > 0 {
		groupIDs = override.Groups
	}
	return filterSportsChannelsByScope(channels, s.sportsSourceIDs(league), groupIDs)
}

// sportsScopeEvents applies the sports source scope to addon event entries. A
// group scope names playlist categories, which an addon event's catalog group is
// not, so only the source selection applies here.
func (s *Server) sportsScopeEvents(league string, entries []livetv.EventEntry) []livetv.EventEntry {
	sourceIDs := s.sportsSourceIDs(league)
	if len(sourceIDs) == 0 {
		return entries
	}
	allowed := make(map[string]bool, len(sourceIDs))
	for _, id := range sourceIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	out := make([]livetv.EventEntry, 0, len(entries))
	for _, entry := range entries {
		if allowed[entry.Source] {
			out = append(out, entry)
		}
	}
	return out
}

// filterSportsChannelsByScope matches source ids exactly and category/group
// names case-insensitively, the way playlist groups are presented.
func filterSportsChannelsByScope(channels []match.Channel, sourceIDs, groupIDs []string) []match.Channel {
	if len(sourceIDs) == 0 && len(groupIDs) == 0 {
		return channels
	}
	sources := map[string]bool{}
	for _, id := range sourceIDs {
		sources[strings.TrimSpace(id)] = true
	}
	groups := map[string]bool{}
	for _, id := range groupIDs {
		groups[strings.ToLower(strings.TrimSpace(id))] = true
	}
	out := make([]match.Channel, 0, len(channels))
	for _, channel := range channels {
		if len(sources) > 0 && !sources[channel.SourceID] {
			continue
		}
		if len(groups) > 0 && !groups[strings.ToLower(channel.Group)] {
			continue
		}
		out = append(out, channel)
	}
	return out
}

// ---------- persistence mapping ----------

func sportsLinkModel(row db.SportsLinkRow) sports.SportsTeamChannelLink {
	link := sports.SportsTeamChannelLink{
		ID: row.ID, TeamID: row.TeamID, Slot: row.Slot, Position: row.Position,
		ChannelID: row.ChannelRef, ChannelRef: row.ChannelRef, ChannelTvgID: row.ChannelTvgID,
		ChannelName: row.ChannelName, ChannelLogo: row.ChannelLogo,
		SourceID: row.SourceID, SourceName: row.SourceName,
		AutoLinked: row.AutoLinked, LinkConfidence: row.LinkConfidence, MatchReason: row.MatchReason,
	}
	if row.CreatedAt > 0 {
		link.CreatedAt = time.Unix(row.CreatedAt, 0).UTC()
	}
	if row.UpdatedAt > 0 {
		link.UpdatedAt = time.Unix(row.UpdatedAt, 0).UTC()
	}
	if row.LastVerifiedAt > 0 {
		link.LastVerifiedAt = time.Unix(row.LastVerifiedAt, 0).UTC()
	}
	return link
}

func sportsTeamModel(row db.SportsTeamRow) sports.SportsTeamRecord {
	team := sports.SportsTeamRecord{
		ID: row.ID, League: row.League, ProviderID: row.ProviderID, Name: row.Name,
		Location: row.Location, Nickname: row.Nickname, Abbreviation: row.Abbreviation, LogoURL: row.LogoURL,
	}
	if row.UpdatedAt > 0 {
		team.UpdatedAt = time.Unix(row.UpdatedAt, 0).UTC()
	}
	return team
}

func resolveSportsLinks(links []sports.SportsTeamChannelLink, channels []match.Channel) []sports.SportsTeamChannelLink {
	out := make([]sports.SportsTeamChannelLink, 0, len(links))
	for _, link := range links {
		out = append(out, match.ResolveSportsLink(link, channels))
	}
	return out
}

// ---------- read API ----------

// handleSportsTeams lists persisted team identities, optionally one league,
// with their current links resolved against the caller's playlist.
func (s *Server) handleSportsTeams(w http.ResponseWriter, r *http.Request) {
	league := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("league")))
	rows, err := s.DB.ListSportsTeams(r.Context(), league)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list teams")
		return
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	linksByTeam, err := s.DB.SportsLinksForTeams(r.Context(), ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load channel links")
		return
	}
	channels := s.sportsMatchChannels()
	result := make([]sports.SportsTeamWithLinks, 0, len(rows))
	for _, row := range rows {
		links := []sports.SportsTeamChannelLink{}
		for _, raw := range linksByTeam[row.ID] {
			links = append(links, match.ResolveSportsLink(sportsLinkModel(raw), channels))
		}
		result = append(result, sports.SportsTeamWithLinks{SportsTeamRecord: sportsTeamModel(row), Links: links})
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": result, "liveTV": s.LiveTV != nil && s.LiveTV.Active()})
}

// handleSportsLinkedChannels is the batched hub lookup. Team ids are
// "{league}:{providerTeamId}"; unknown ids simply have no links.
func (s *Server) handleSportsLinkedChannels(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("ids"))
	linksByTeam := map[string][]sports.SportsTeamChannelLink{}
	if raw == "" {
		writeJSON(w, http.StatusOK, map[string]any{"linksByTeam": linksByTeam})
		return
	}
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" || seen[id] || len(ids) >= 100 {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	rowsByTeam, err := s.DB.SportsLinksForTeams(r.Context(), ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load channel links")
		return
	}
	channels := s.sportsMatchChannels()
	for teamID, rows := range rowsByTeam {
		links := make([]sports.SportsTeamChannelLink, 0, len(rows))
		for _, row := range rows {
			links = append(links, match.ResolveSportsLink(sportsLinkModel(row), channels))
		}
		linksByTeam[teamID] = links
	}
	writeJSON(w, http.StatusOK, map[string]any{"linksByTeam": linksByTeam})
}

func (s *Server) requireTeam(w http.ResponseWriter, r *http.Request) (*db.SportsTeamRow, bool) {
	teamID := strings.TrimSpace(pathParam(r, "id"))
	if teamID == "" {
		writeErr(w, http.StatusBadRequest, "team id required")
		return nil, false
	}
	team, err := s.DB.SportsTeamByID(r.Context(), teamID)
	if errors.Is(err, db.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "team not found")
		return nil, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to look up team")
		return nil, false
	}
	return team, true
}

func (s *Server) handleSportsTeamLinks(w http.ResponseWriter, r *http.Request) {
	team, ok := s.requireTeam(w, r)
	if !ok {
		return
	}
	rows, err := s.DB.SportsTeamLinks(r.Context(), team.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load channel links")
		return
	}
	channels := s.sportsMatchChannels()
	links := make([]sports.SportsTeamChannelLink, 0, len(rows))
	for _, row := range rows {
		links = append(links, match.ResolveSportsLink(sportsLinkModel(row), channels))
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

// handleSportsTeamSuggestions ranks channels for manual correction. An
// explicit query keeps every text match available while still scoring it.
func (s *Server) handleSportsTeamSuggestions(w http.ResponseWriter, r *http.Request) {
	team, ok := s.requireTeam(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeErr(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	channels := s.sportsMatchChannels()
	candidates := match.RankTeamRecordCandidates(sportsTeamModel(*team), channels, query, limit)
	writeJSON(w, http.StatusOK, map[string]any{"candidates": candidates})
}

// handleSportsAutoLinkPreview reports exactly which teams would be linked and
// why each other team is skipped. It never writes.
func (s *Server) handleSportsAutoLinkPreview(w http.ResponseWriter, r *http.Request) {
	league := strings.ToLower(strings.TrimSpace(pathParam(r, "league")))
	evaluation, err := s.evaluateSportsAutoLinks(r, league)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Automatic link preview failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, evaluation.Preview)
}

func (s *Server) evaluateSportsAutoLinks(r *http.Request, league string) (match.AutoLinkEvaluation, error) {
	if league == "" {
		return match.AutoLinkEvaluation{}, errors.New("missing league id")
	}
	rows, err := s.DB.ListSportsTeams(r.Context(), league)
	if err != nil {
		return match.AutoLinkEvaluation{}, err
	}
	teams := make([]sports.SportsTeamRecord, 0, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		teams = append(teams, sportsTeamModel(row))
		ids = append(ids, row.ID)
	}
	if len(teams) == 0 {
		return match.AutoLinkEvaluation{
			Preview: sports.SportsAutoLinkPreview{
				League: league, Message: "No teams are available for this league yet. Refresh sports data and try again.",
				Results: []sports.SportsAutoLinkTeamResult{},
			},
			Links: []sports.SportsTeamChannelLink{},
		}, nil
	}
	rowsByTeam, err := s.DB.SportsLinksForTeams(r.Context(), ids)
	if err != nil {
		return match.AutoLinkEvaluation{}, err
	}
	existing := make(map[string][]sports.SportsTeamChannelLink, len(rowsByTeam))
	for teamID, teamRows := range rowsByTeam {
		links := make([]sports.SportsTeamChannelLink, 0, len(teamRows))
		for _, row := range teamRows {
			links = append(links, sportsLinkModel(row))
		}
		existing[teamID] = links
	}
	channels := s.sportsLinkScope(league, s.sportsMatchChannels())
	return match.EvaluateAutoLinks(league, teams, existing, channels), nil
}

// handleSportsAutoLinkApply re-evaluates immediately before inserting, so a
// manual primary added after preview is preserved rather than replaced.
func (s *Server) handleSportsAutoLinkApply(w http.ResponseWriter, r *http.Request) {
	league := strings.ToLower(strings.TrimSpace(pathParam(r, "league")))
	evaluation, err := s.evaluateSportsAutoLinks(r, league)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Automatic link evaluation failed: "+err.Error())
		return
	}
	rows := make([]db.SportsLinkRow, 0, len(evaluation.Links))
	for _, link := range evaluation.Links {
		rows = append(rows, db.SportsLinkRow{
			TeamID: link.TeamID, ChannelRef: link.ChannelRef, ChannelTvgID: link.ChannelTvgID,
			ChannelName: link.ChannelName, ChannelLogo: link.ChannelLogo,
			SourceID: link.SourceID, SourceName: link.SourceName,
			LinkConfidence: link.LinkConfidence, MatchReason: link.MatchReason,
		})
	}
	saved, err := s.DB.InsertSportsAutoPrimaryLinks(r.Context(), rows)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Automatic link save failed: "+err.Error())
		return
	}
	channels := s.sportsMatchChannels()
	links := make([]sports.SportsTeamChannelLink, 0, len(saved))
	for _, row := range saved {
		// Re-resolve against the list used for this request. If the playlist
		// changed between evaluation and insertion, the link is reported as
		// unresolved instead of claiming a channel that is no longer there.
		links = append(links, match.ResolveSportsLink(sportsLinkModel(row), channels))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"linkedCount": len(links), "links": links, "preview": evaluation.Preview,
	})
}

// ---------- admin: link mutations ----------

type sportsLinkRequest struct {
	ChannelID string `json:"channelId"`
}

// readLinkChannel accepts only a current playlist channel id. A client cannot
// invent a snapshot or point a link at a provider URL.
func (s *Server) readLinkChannel(w http.ResponseWriter, r *http.Request) (match.Channel, bool) {
	var req sportsLinkRequest
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return match.Channel{}, false
	}
	id := strings.TrimSpace(req.ChannelID)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "channelId required")
		return match.Channel{}, false
	}
	channel, ok := s.sportsChannelByID(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "channel not found in the current playlist")
		return match.Channel{}, false
	}
	return channel, true
}

// sameLinkedChannel reports whether two snapshots identify one playlist
// channel. tvg-id wins; a name match covers providers without ids.
func sameLinkedChannel(row db.SportsLinkRow, channel match.Channel) bool {
	if row.ChannelRef != "" && row.ChannelRef == channel.ID {
		return true
	}
	if row.ChannelTvgID != "" && channel.TvgID != "" {
		return strings.EqualFold(strings.TrimSpace(row.ChannelTvgID), strings.TrimSpace(channel.TvgID))
	}
	return strings.EqualFold(strings.TrimSpace(row.ChannelName), strings.TrimSpace(channel.Name))
}

// rejectDuplicateLink keeps one channel from occupying several slots for the
// same team. Replacing the primary with the same primary is allowed.
func (s *Server) rejectDuplicateLink(ctx context.Context, teamID string, channel match.Channel, slot string) bool {
	rows, err := s.DB.SportsTeamLinks(ctx, teamID)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if !sameLinkedChannel(row, channel) {
			continue
		}
		if slot == "primary" && row.Slot == "primary" {
			return false
		}
		return true
	}
	return false
}

func linkRowFromChannel(teamID string, channel match.Channel) db.SportsLinkRow {
	return db.SportsLinkRow{
		TeamID: teamID, ChannelRef: channel.ID, ChannelTvgID: channel.TvgID,
		ChannelName: channel.Name, ChannelLogo: channel.Logo,
		SourceID: channel.SourceID, SourceName: channel.SourceName,
	}
}

func (s *Server) handleSportsSetPrimaryLink(w http.ResponseWriter, r *http.Request) {
	team, ok := s.requireTeam(w, r)
	if !ok {
		return
	}
	channel, ok := s.readLinkChannel(w, r)
	if !ok {
		return
	}
	if s.rejectDuplicateLink(r.Context(), team.ID, channel, "primary") {
		writeErr(w, http.StatusConflict, "that channel is already linked to this team")
		return
	}
	saved, err := s.DB.SetSportsPrimaryLink(r.Context(), team.ID, linkRowFromChannel(team.ID, channel))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to save primary channel")
		return
	}
	link := sportsLinkModel(*saved)
	link.Resolved = true
	writeJSON(w, http.StatusOK, link)
}

func (s *Server) handleSportsAddBackupLink(w http.ResponseWriter, r *http.Request) {
	team, ok := s.requireTeam(w, r)
	if !ok {
		return
	}
	channel, ok := s.readLinkChannel(w, r)
	if !ok {
		return
	}
	if s.rejectDuplicateLink(r.Context(), team.ID, channel, "backup") {
		writeErr(w, http.StatusConflict, "that channel is already linked to this team")
		return
	}
	saved, err := s.DB.AddSportsBackupLink(r.Context(), team.ID, linkRowFromChannel(team.ID, channel))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to add backup channel")
		return
	}
	link := sportsLinkModel(*saved)
	link.Resolved = true
	writeJSON(w, http.StatusOK, link)
}

func (s *Server) handleSportsDeleteLink(w http.ResponseWriter, r *http.Request) {
	teamID := strings.TrimSpace(pathParam(r, "id"))
	linkID := strings.TrimSpace(pathParam(r, "linkId"))
	if teamID == "" || linkID == "" {
		writeErr(w, http.StatusBadRequest, "team and link ids required")
		return
	}
	if err := s.DB.DeleteSportsLink(r.Context(), teamID, linkID); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete channel link")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSportsReorderLinks(w http.ResponseWriter, r *http.Request) {
	team, ok := s.requireTeam(w, r)
	if !ok {
		return
	}
	var req struct {
		LinkIDs []string `json:"linkIds"`
	}
	if err := decodeStrict(r, &req); err != nil || len(req.LinkIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.DB.ReorderSportsBackups(r.Context(), team.ID, req.LinkIDs); err != nil {
		if errors.Is(err, db.ErrSportsLinkOrder) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to reorder backup channels")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- shared league view ----------

// sportsLeaguesByID is used by admin views to order and label league choices.
func (s *Server) sportsLeaguesByID() map[string]sports.SportsLeague {
	out := map[string]sports.SportsLeague{}
	for _, league := range s.Sports.Leagues() {
		out[league.ID] = league
	}
	return out
}

// sortSportsTeams orders teams by league then name for stable admin output.
func sortSportsTeams(teams []sports.SportsTeamRecord) {
	sort.SliceStable(teams, func(i, j int) bool {
		if teams[i].League != teams[j].League {
			return teams[i].League < teams[j].League
		}
		return teams[i].Name < teams[j].Name
	})
}
