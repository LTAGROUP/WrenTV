package match

import "testing"

func realBetisAtCeltaVigo() Event {
	return Event{
		ID: "401882833", League: "soccer-esp.1", Sport: "soccer", EventKind: "matchup",
		Title: "Real Betis at Celta Vigo", StartTime: 1_700_000_000,
		Home: team("Celta Vigo", "Celta Vigo", "Celta Vigo", "CEL"),
		Away: team("Real Betis", "Real Betis", "Real Betis", "BET"),
	}
}

// Real Betis's provider code is "BET", but "BET Cinema" is an unrelated channel
// brand: the code is a word inside another name, not the channel's identity, and
// there is no guide to promote it. A channel may only match on positive team
// evidence, so a stray code inside a longer label is not enough.
func TestEventStreamsRejectsTeamCodeInsideUnrelatedChannelBrand(t *testing.T) {
	event := realBetisAtCeltaVigo()
	for _, name := range []string{"BET Cinema", "BET Cinema HD", "BET Cinema FHD"} {
		if got := EventStreams(event, []Channel{{ID: name, Name: name}}, ""); len(got) != 0 {
			t.Fatalf("channel %q matched %+v", name, got)
		}
	}
	// The same label carried in tvg-name is equally unrelated to the fixture.
	if got := EventStreams(event, []Channel{{ID: "tvg", Name: "Cinema", TvgName: "BET Cinema"}}, ""); len(got) != 0 {
		t.Fatalf("tvg-name matched %+v", got)
	}
	// A label that is only the team's code is positive, if weak, evidence.
	got := EventStreams(event, []Channel{{ID: "code", Name: "BET"}}, "")
	if len(got) != 1 || got[0].ConfidenceTier != "possible" || got[0].MatchedOn != "team-name" {
		t.Fatalf("bare team code = %+v, want one possible team-name match", got)
	}
}

// A dedicated football channel needs no guide: the team brand is positive
// evidence on its own, and both it and a one-spelling matchup stay "possible"
// rather than being promoted to confirmed streams.
func TestEventStreamsKeepsLegitimateFootballChannelsWithoutGuide(t *testing.T) {
	event := realBetisAtCeltaVigo()
	got := EventStreams(event, []Channel{{ID: "betis-tv", Name: "Real Betis TV"}}, "")
	if len(got) != 1 || got[0].ConfidenceTier != "possible" || got[0].MatchedOn != "team-name" {
		t.Fatalf("dedicated channel = %+v, want one possible team-name match", got)
	}
	if got[0].Confidence >= StrongConfidence {
		t.Fatalf("dedicated channel claimed strong: %+v", got[0])
	}

	typo := EventStreams(event, []Channel{{ID: "typo", Name: "Celta Vigo vs Real Betiz"}}, "")
	if len(typo) != 1 || typo[0].Confidence != 0.70 || typo[0].ConfidenceTier != "possible" || typo[0].MatchedOn != "matchup-name" {
		t.Fatalf("one-spelling matchup = %+v, want 0.70 possible matchup-name", typo)
	}

	// Abbreviations remain usable inside an explicit matchup, where both sides
	// are positive evidence.
	abbr := EventStreams(event, []Channel{{ID: "abbr", Name: "CEL vs BET"}}, "")
	if len(abbr) != 1 || abbr[0].ConfidenceTier != "strong" {
		t.Fatalf("abbreviation matchup = %+v, want one strong match", abbr)
	}
}

// A channel label that names a different series, sport or provider category is
// showing something else. The matcher applies this before scoring so a matching
// name cannot outvote a contradicting label.
func TestEventStreamsRejectsContradictingChannelLabels(t *testing.T) {
	race := Event{
		ID: "f1:1:2", League: "f1", Sport: "racing", EventKind: "race-session",
		Title: "Miami Grand Prix", Context: "Race", StartTime: 1_700_000_000,
	}
	channels := []Channel{
		{ID: "f1", Name: "F1 Miami Grand Prix"},
		{ID: "tvg", Name: "F1 Miami Grand Prix", TvgName: "NASCAR Cup Series"},
		{ID: "meta", Name: "F1 Miami Grand Prix", Metadata: "MotoGP coverage"},
	}
	got := EventStreams(race, channels, "")
	if len(got) != 1 || got[0].ChannelID != "f1" {
		t.Fatalf("matches = %#v, want only the F1 channel", got)
	}

	golf := Event{ID: "pga:1", League: "pga", Sport: "golf", EventKind: "tournament", Title: "The Masters", StartTime: 1_700_000_000}
	field := []Channel{
		{ID: "golf", Name: "The Masters", Group: "Golf"},
		{ID: "snooker", Name: "The Masters", Metadata: "Category: Snooker"},
	}
	got = EventStreams(golf, field, "")
	if len(got) != 1 || got[0].ChannelID != "golf" {
		t.Fatalf("tournament matches = %#v, want only the golf channel", got)
	}

	// The same rule must not discard a channel that agrees with the event.
	if got := EventStreams(golf, []Channel{{ID: "same", Name: "The Masters", Metadata: "Category: Golf"}}, ""); len(got) != 1 {
		t.Fatalf("agreeing metadata was rejected: %#v", got)
	}
}

// A channel whose current programme already names a different session or stage
// is not showing this event, whatever its name says.
func TestEventStreamsSkipsChannelWhoseGuideContradictsTheSession(t *testing.T) {
	race := Event{
		ID: "f1:1:2", League: "f1", Sport: "racing", EventKind: "race-session",
		Title: "Miami Grand Prix", Context: "Qualifying", StartTime: 1_700_000_000,
	}
	channels := []Channel{
		{ID: "ok", Name: "F1 Miami Grand Prix", Now: &Program{Title: "F1 Miami Grand Prix Qualifying", Start: 1_700_000_000}},
		{ID: "wrong", Name: "F1 Miami Grand Prix", Now: &Program{Title: "F1 Miami Grand Prix Practice 2", Start: 1_700_000_000}},
	}
	got := EventStreams(race, channels, "")
	if len(got) != 1 || got[0].ChannelID != "ok" {
		t.Fatalf("matches = %#v, want only the qualifying channel", got)
	}

	stage := Event{
		ID: "aso:tour:2026:12", League: "aso:tour", Sport: "cycling", EventKind: "cycling-stage",
		Title: "Tour de France", Context: "Stage 12", StartTime: 1_700_000_000,
	}
	got = EventStreams(stage, []Channel{
		{ID: "ok", Name: "Tour de France Stage 12", Now: &Program{Title: "Cycling: Tour de France Stage 12"}},
		{ID: "wrong", Name: "Tour de France Stage 12", Now: &Program{Title: "Cycling: Tour de France Stage 13"}},
	}, "")
	if len(got) != 1 || got[0].ChannelID != "ok" {
		t.Fatalf("stage matches = %#v, want only stage 12", got)
	}

	// A matchup is unaffected: a guide title naming another game is the guide's
	// business, and the scoreProgramMatchup rules already require both teams.
	game := Event{
		ID: "1", League: "nba", EventKind: "matchup",
		Home: team("Boston Celtics", "Boston", "Celtics", "BOS"),
		Away: team("New York Knicks", "New York", "Knicks", "NYK"),
	}
	if got := EventStreams(game, []Channel{{ID: "c", Name: "Celtics vs Knicks", Now: &Program{Title: "Lakers vs Warriors"}}}, ""); len(got) != 1 {
		t.Fatalf("matchup channel dropped on an unrelated guide title: %#v", got)
	}
}
