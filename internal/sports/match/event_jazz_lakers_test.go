package match

import (
	"testing"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

// A SportsHub team search used to degenerate into an OR of loose words, so a
// music channel named "Jazz" and a "Lakers News" channel looked like feeds for
// Utah Jazz at Los Angeles Lakers. The matchup, EPG and permanent team labels
// below are the evidence the matcher must keep; the unrelated channels must be
// rejected even though they contain one of the team words.
func TestEventStreamsUtahJazzAtLakersKeepsEvidenceAndRejectsUnrelatedChannels(t *testing.T) {
	event := Event{
		ID: "401", League: "nba", Sport: "basketball", EventKind: "matchup",
		Title: "Utah Jazz at Los Angeles Lakers", StartTime: 1_700_000_000,
		Home: sports.SportsTeam{Name: "Los Angeles Lakers", Location: "Los Angeles", Nickname: "Lakers", Abbreviation: "LAL"},
		Away: sports.SportsTeam{Name: "Utah Jazz", Location: "Utah", Nickname: "Jazz", Abbreviation: "UTA"},
	}
	channels := []Channel{
		{ID: "exact", Name: "Utah Jazz vs Los Angeles Lakers"},
		{ID: "nicknames", Name: "Jazz vs Lakers"},
		{ID: "rsn", Name: "Spectrum SportsNet", Now: &Program{
			Title: "NBA: Utah Jazz at Los Angeles Lakers", Start: 1_700_000_000,
		}},
		{ID: "jazz-tv", Name: "Utah Jazz TV"},
		{ID: "lakers-tv", Name: "Lakers TV"},
		// Unrelated channels that share a team word but do not carry the game.
		{ID: "music", Name: "Jazz Music Channel"},
		{ID: "music-group", Name: "Jazz", Group: "Music"},
		{ID: "news", Name: "Lakers News"},
		{ID: "classics", Name: "Lakers Classics"},
		{ID: "la-news", Name: "Los Angeles News"},
		{ID: "utah-news", Name: "Utah News"},
		{ID: "tennis", Name: "Tennis Channel"},
		{ID: "f1", Name: "Sky Sports F1"},
		{ID: "nba", Name: "NBA TV"},
	}

	got := EventStreams(event, channels, "")
	byID := make(map[string]sports.SportsStreamMatch, len(got))
	for _, match := range got {
		byID[match.ChannelID] = match
	}

	for _, id := range []string{"exact", "nicknames", "rsn", "jazz-tv", "lakers-tv"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("valid channel %q missing from %+v", id, got)
		}
	}
	for _, id := range []string{"music", "music-group", "news", "classics", "la-news", "utah-news", "tennis", "f1", "nba"} {
		if _, ok := byID[id]; ok {
			t.Fatalf("unrelated channel %q matched %+v", id, byID[id])
		}
	}

	// Exact matchup names and EPG evidence are strong and rank above a single
	// team's permanent channel.
	for _, id := range []string{"exact", "nicknames", "rsn"} {
		if byID[id].ConfidenceTier != "strong" {
			t.Fatalf("%q tier = %q, want strong: %+v", id, byID[id].ConfidenceTier, byID[id])
		}
	}
	if byID["exact"].Confidence <= byID["jazz-tv"].Confidence {
		t.Fatalf("exact matchup %.2f did not outrank team channel %.2f", byID["exact"].Confidence, byID["jazz-tv"].Confidence)
	}
	if byID["rsn"].MatchedOn != "epg-title" {
		t.Fatalf("RSN matched on %q, want epg-title: %+v", byID["rsn"].MatchedOn, byID["rsn"])
	}
	if byID["jazz-tv"].MatchedOn != "team-name" || byID["jazz-tv"].ConfidenceTier != "possible" {
		t.Fatalf("team channel = %+v, want a possible team-name match", byID["jazz-tv"])
	}
	if got[0].ConfidenceTier != "strong" || got[len(got)-1].ConfidenceTier != "possible" {
		t.Fatalf("matches were not ranked strongest first: %+v", got)
	}
}
