package match

import (
	"strings"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

// identityFromRecord turns a persisted team into the matcher's identity,
// including league-scoped aliases that the record's structured fields cannot
// express.
func identityFromRecord(team sports.SportsTeamRecord) teamIdentity {
	return makeIdentity(team.League, team.Name, team.Location, team.Nickname, team.Abbreviation)
}

func sportTeamFromRecord(team sports.SportsTeamRecord) sports.SportsTeam {
	return sports.SportsTeam{
		ID: team.ProviderID, Name: team.Name, Location: team.Location,
		Nickname: team.Nickname, Abbreviation: team.Abbreviation, LogoURL: team.LogoURL,
	}
}

// RankTeamRecordCandidates is the managed-link suggestion ranking for a stored
// team. An explicit query keeps every text match, scored, for manual choice.
func RankTeamRecordCandidates(team sports.SportsTeamRecord, channels []Channel, query string, limit int) []sports.SportsChannelCandidate {
	return rankTeamCandidates(identityFromRecord(team), team.Name, channels, query, limit)
}

// ResolveSportsLink re-matches a stored channel snapshot against the current
// playlist, preferring tvg-id and falling back to the normalized name. The
// stored playlist id is only a tie-breaker: providers regenerate ids. When a
// snapshot matches several channels and the stored source cannot disambiguate
// them, the link stays unresolved rather than silently selecting another feed.
func ResolveSportsLink(link sports.SportsTeamChannelLink, channels []Channel) sports.SportsTeamChannelLink {
	wantTvg := normalize(link.ChannelTvgID)
	wantName := normalize(link.ChannelName)
	tvgAmbiguous := false
	if wantTvg != "" {
		matched := false
		for _, channel := range channels {
			if normalize(channel.TvgID) == wantTvg {
				matched = true
				break
			}
		}
		if channel, ok := pickLinkedChannel(link, channels, func(channel Channel) bool {
			return normalize(channel.TvgID) == wantTvg
		}); ok {
			return resolvedLink(link, channel)
		}
		// A tvg-id that is present but ambiguous must not fall through to a
		// name-only match that could be an entirely different channel.
		tvgAmbiguous = matched
	}
	if wantName != "" && !tvgAmbiguous {
		if channel, ok := pickLinkedChannel(link, channels, func(channel Channel) bool {
			return normalize(channel.Name) == wantName
		}); ok {
			return resolvedLink(link, channel)
		}
	}
	// A unique stored id is still useful when neither name nor tvg id matches
	// (for example a provider that renamed a channel in place).
	if link.ChannelRef != "" {
		for _, channel := range channels {
			if channel.ID == link.ChannelRef {
				return resolvedLink(link, channel)
			}
		}
	}
	link.Resolved = false
	link.ChannelID = ""
	return link
}

// pickLinkedChannel selects one match without guessing. The stored playlist id
// wins when it is still present, then the stored source, then an exact name.
// Anything still ambiguous is left unresolved.
func pickLinkedChannel(link sports.SportsTeamChannelLink, channels []Channel, matches func(Channel) bool) (Channel, bool) {
	found := make([]Channel, 0, 1)
	for _, channel := range channels {
		if matches(channel) {
			found = append(found, channel)
		}
	}
	if len(found) == 0 {
		return Channel{}, false
	}
	if link.ChannelRef != "" {
		for _, channel := range found {
			if channel.ID == link.ChannelRef {
				return channel, true
			}
		}
	}
	if link.SourceID != "" {
		sameSource := make([]Channel, 0, 1)
		for _, channel := range found {
			if channel.SourceID == link.SourceID {
				sameSource = append(sameSource, channel)
			}
		}
		if len(sameSource) == 1 {
			return sameSource[0], true
		}
		if len(sameSource) > 1 {
			return Channel{}, false
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	// Different sources may expose the same tvg-id. The stored name is the last
	// safe discriminator; without a unique one there is no correct answer.
	if wantName := normalize(link.ChannelName); wantName != "" {
		byName := make([]Channel, 0, 1)
		for _, channel := range found {
			if normalize(channel.Name) == wantName {
				byName = append(byName, channel)
			}
		}
		if len(byName) == 1 {
			return byName[0], true
		}
	}
	return Channel{}, false
}

func resolvedLink(link sports.SportsTeamChannelLink, channel Channel) sports.SportsTeamChannelLink {
	link.Resolved = true
	link.ChannelID = channel.ID
	if link.ChannelLogo == "" {
		link.ChannelLogo = channel.Logo
	}
	return link
}

// AutoLinkEvaluation is a preview plus the links that applying it would insert.
type AutoLinkEvaluation struct {
	Preview sports.SportsAutoLinkPreview
	Links   []sports.SportsTeamChannelLink
}

// EvaluateAutoLinks applies MediaStorm's conservative permanent-channel rules:
// a team with a primary link is skipped, only permanent channel brands are
// candidates, and a link needs either 0.95 confidence alone or 0.85 with a
// 0.20 lead over the runner-up. It never writes anything.
func EvaluateAutoLinks(league string, teams []sports.SportsTeamRecord, existing map[string][]sports.SportsTeamChannelLink, channels []Channel) AutoLinkEvaluation {
	evaluation := AutoLinkEvaluation{
		Preview: sports.SportsAutoLinkPreview{League: league, Results: []sports.SportsAutoLinkTeamResult{}},
		Links:   []sports.SportsTeamChannelLink{},
	}
	for _, team := range teams {
		result := sports.SportsAutoLinkTeamResult{Team: team}
		for _, link := range existing[team.ID] {
			if link.Slot == "primary" {
				result.SkipReason = "Primary channel already linked"
				break
			}
		}
		if result.SkipReason == "" {
			candidates := AutoLinkCandidates(sportTeamFromRecord(team), team.League, channels)
			if len(candidates) == 0 {
				result.SkipReason = "No permanent team channel candidate"
			} else {
				best := candidates[0]
				result.Candidate = &best
				if len(candidates) > 1 {
					result.RunnerUpConfidence = candidates[1].Confidence
				}
				result.ConfidenceGap = roundConfidence(best.Confidence - result.RunnerUpConfidence)
				result.Eligible = best.Confidence >= 0.95 ||
					(best.Confidence >= 0.85 && result.ConfidenceGap >= 0.20)
				if !result.Eligible {
					result.SkipReason = "Confidence or lead over runner-up is too low"
				} else {
					evaluation.Links = append(evaluation.Links, sports.SportsTeamChannelLink{
						TeamID: team.ID, ChannelRef: best.ChannelID, ChannelTvgID: best.ChannelTvgID,
						ChannelName: best.ChannelName, ChannelLogo: best.ChannelLogo,
						SourceID: best.SourceID, SourceName: best.SourceName,
						AutoLinked: true, LinkConfidence: best.Confidence, MatchReason: best.MatchReason,
					})
				}
			}
		}
		if result.Eligible {
			evaluation.Preview.EligibleCount++
		} else {
			evaluation.Preview.SkippedCount++
		}
		evaluation.Preview.Results = append(evaluation.Preview.Results, result)
	}
	return evaluation
}

// ExplicitTeamCandidates is a manual search for the link editor. Results are
// scored where possible and labelled manual otherwise; a manual result must
// never claim a strong automatic match.
func ExplicitTeamCandidates(team sports.SportsTeamRecord, channels []Channel, query string, limit int) []sports.SportsChannelCandidate {
	return RankTeamRecordCandidates(team, channels, strings.TrimSpace(query), limit)
}

// SportsChannelsForLinking drops channels that cannot be linked at all: no
// stable identity and no name leaves nothing to resolve later.
func SportsChannelsForLinking(channels []Channel) []Channel {
	out := make([]Channel, 0, len(channels))
	for _, channel := range channels {
		if strings.TrimSpace(channel.Name) == "" && strings.TrimSpace(channel.TvgName) == "" {
			continue
		}
		out = append(out, channel)
	}
	return out
}
