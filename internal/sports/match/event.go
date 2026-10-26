package match

import (
	"math"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

// EventStreams reproduces MediaStorm's matchGameToChannels: every channel is
// scored through the team/network matcher, current and next EPG programs can
// promote a channel, and only plausible candidates are returned.
//
// ChannelURL carries a channel reference ("livetv:<id>") instead of the
// provider's private stream URL. Playback goes through Wren TV's own live
// session endpoint, so no source credential ever reaches a client.
func EventStreams(event Event, channels []Channel, broadcastFilter string) []sports.SportsStreamMatch {
	home := identityFromTeam(event.Home, event.League)
	away := identityFromTeam(event.Away, event.League)
	matches := make([]sports.SportsStreamMatch, 0)

channelLoop:
	for _, channel := range channels {
		// A label naming another series or sport is a contradiction, not a weak
		// signal: reject the channel before scoring so a matching name cannot
		// outvote it.
		label := channel.Name + " " + channel.TvgName + " " + channel.Metadata
		if conflictingNamedSport(label, event) {
			continue
		}
		if event.EventKind == "race-session" && conflictingSeries(label, event.League) {
			continue
		}
		channelLifecycle := lifecycle(channel.Name)
		if channelLifecycle == "" {
			channelLifecycle = lifecycle(channel.TvgName)
		}
		if channelLifecycle == lifecycleEnded || hasNonLiveLabel(channel.Name) || hasNonLiveLabel(channel.TvgName) {
			continue
		}
		var ev evidence
		if broadcastFilter != "" {
			ev = scoreBroadcastText(channel.Name, broadcastFilter)
			if ev.score <= 0 && channel.TvgName != "" {
				ev = scoreBroadcastText(channel.TvgName, broadcastFilter)
			}
			if ev.score <= 0 {
				continue
			}
		} else if event.EventKind != "" && event.EventKind != "matchup" {
			// Named events (cards, tournaments, races, stage races) are scored on
			// their own identity. A team match never stands in for one.
			ev = scoreNamedEvent(channel.Name, event)
			alternative := scoreNamedEvent(channel.TvgName, event)
			if alternative.score > ev.score {
				ev = alternative
			}
		} else {
			ev = scoreMatchupText(channel.Name, home, away)
			alternate := scoreMatchupText(channel.TvgName, home, away)
			if alternate.score > ev.score {
				ev = alternate
			}
		}

		// A channel whose current programme already names a different session or
		// stage is showing something else, whatever its name claims.
		if channel.Now != nil && conflictingSegment(channel.Now.Title, event) {
			continue
		}

		for _, program := range []*Program{channel.Now, channel.Next} {
			if program == nil {
				continue
			}
			if hasNonLiveLabel(program.Title) || hasNonLiveLabel(program.Description) {
				continue
			}
			programEvidence := scoreProgramMatchup(program.Title, home, away)
			if event.EventKind != "" && event.EventKind != "matchup" {
				programEvidence = scoreNamedEvent(program.Title, event)
			}
			fromDescription := false
			descriptionEvidence := scoreProgramMatchup(program.Description, home, away)
			if event.EventKind != "" && event.EventKind != "matchup" {
				descriptionEvidence = scoreNamedEvent(program.Description, event)
			}
			if descriptionEvidence.score > programEvidence.score {
				programEvidence = descriptionEvidence
				fromDescription = true
			}
			if programEvidence.score <= 0 {
				continue
			}
			// Descriptions without schedule evidence are suggestions, not
			// confirmed broadcasts.
			ceiling := 1.0
			if fromDescription {
				ceiling = 0.84
			}
			if event.StartTime != 0 && program.Start != 0 {
				distance := time.Duration(program.Start-event.StartTime) * time.Second
				if distance < 0 {
					distance = -distance
				}
				if distance > 6*time.Hour {
					continue
				}
				if distance <= 2*time.Hour {
					ceiling = 1
					programEvidence.score = math.Min(ceiling, programEvidence.score+0.04)
				} else {
					ceiling = 0.84
				}
			}
			programEvidence.score = math.Min(ceiling, programEvidence.score)

			if broadcastFilter != "" {
				if programEvidence.score >= StrongConfidence {
					ev.score = math.Max(ev.score, 0.99)
					ev.reason = broadcastFilter + " with both teams in EPG"
					ev.terms = programEvidence.terms
					ev.program = program.Title
					ev.on = "epg-title"
				}
			} else if programEvidence.score > ev.score {
				programEvidence.score = math.Min(ceiling, programEvidence.score+0.04)
				programEvidence.reason = "EPG: " + programEvidence.reason
				programEvidence.program = program.Title
				programEvidence.on = "epg-title"
				if fromDescription {
					programEvidence.on = "epg-description"
				}
				ev = programEvidence
			}
		}

		if ev.score <= 0 {
			continue channelLoop
		}
		// A single team name is weak evidence. A music, news or comedy channel
		// that happens to share a nickname must not become a plausible feed;
		// a matchup name or EPG evidence is stronger and decides on its own.
		if ev.on == "team-name" && hasUnrelatedTeamLabel(channel) {
			continue channelLoop
		}
		score := roundConfidence(ev.score)
		if event.EventKind == "race-session" || event.EventKind == "cycling-stage" || event.EventKind == "tournament" {
			score = math.Min(score, 0.84)
		}
		matches = append(matches, sports.SportsStreamMatch{
			ChannelID: channel.ID, ChannelName: channel.Name, ChannelURL: "livetv:" + channel.ID,
			ChannelLogo: channel.Logo, ChannelTvgID: channel.TvgID, SourceID: channel.SourceID,
			SourceName: channel.SourceName, ProgramTitle: ev.program,
			MatchedOn: ev.on, Broadcast: broadcastFilter, MatchedTeam: ev.team,
			Confidence: score, ConfidenceTier: confidenceTier(score), MatchReason: ev.reason,
			MatchedTerms: ev.terms, LifecycleState: channelLifecycle,
		})
	}
	sortStreamMatches(matches)
	return matches
}

// Resolve returns the selectable flat list and its grouped presentation.
func Resolve(event Event, channels []Channel, broadcastFilter string) ([]sports.SportsStreamMatch, []sports.SportsStreamGroup) {
	return BuildStreamGroups(Selectable(EventStreams(event, channels, broadcastFilter)))
}

// IsTeamChannel reports whether a channel is a permanent team brand rather
// than a one-off matchup listing. Administrative link suggestions use it.
func IsTeamChannel(channel Channel) bool {
	return permanentAutoLinkChannel(channel)
}

// ChannelMatchesText is the shared explicit search: every query token must be
// present in the channel name, tvg name or tvg id.
func ChannelMatchesText(channel Channel, query string) bool {
	return explicitChannelQueryMatches(channel, strings.TrimSpace(query))
}

// SearchChannels is the manual search path: every channel whose name or tvg
// identity contains the query is returned as a selectable candidate. These
// results describe an explicit choice, not event evidence, so they carry a
// "manual" tier that never auto-plays on its own.
func SearchChannels(channels []Channel, query string) []sports.SportsStreamMatch {
	matches := make([]sports.SportsStreamMatch, 0)
	for _, channel := range channels {
		if !explicitChannelQueryMatches(channel, strings.TrimSpace(query)) {
			continue
		}
		matches = append(matches, sports.SportsStreamMatch{
			ChannelID: channel.ID, ChannelName: channel.Name, ChannelURL: "livetv:" + channel.ID,
			ChannelLogo: channel.Logo, ChannelTvgID: channel.TvgID, SourceID: channel.SourceID,
			SourceName: channel.SourceName, Confidence: 1, ConfidenceTier: "manual",
			MatchedOn: "manual-search", MatchReason: "Manual channel search",
		})
	}
	sortStreamMatches(matches)
	return matches
}
