package match

import "testing"

// A one-letter spelling difference inside an explicit matchup is the only
// spelling tolerance allowed, and only when each side is otherwise the team's
// own words. See scoreFuzzyMatchup.
func TestFuzzyMatchupToleratesOneSpellingDifference(t *testing.T) {
	event := Event{
		ID: "1", League: "nba", EventKind: "matchup",
		Home: team("Boston Celtics", "Boston", "Celtics", "BOS"),
		Away: team("New York Knicks", "New York", "Knicks", "NYK"),
	}
	got := EventStreams(event, []Channel{{ID: "typo", Name: "Celtics vs Knickz"}}, "")
	if len(got) != 1 {
		t.Fatalf("matches = %#v, want the one misspelt matchup", got)
	}
	if got[0].Confidence != 0.7 || got[0].ConfidenceTier != "possible" {
		t.Fatalf("typo match = %+v, want 0.70 possible", got[0])
	}
	if got[0].MatchedOn != "matchup-name" {
		t.Fatalf("typo match evidence = %q, want matchup-name", got[0].MatchedOn)
	}

	// Two misspelt sides could each be a different game.
	if got := EventStreams(event, []Channel{{ID: "two", Name: "Celtix vs Knickz"}}, ""); len(got) != 0 {
		t.Fatalf("two spelling differences matched: %#v", got)
	}
}

// An unrelated word on one side means the listing is a different programme
// (another opponent or competition, or a second matchup), so it must not
// become a possible feed. A different team is the case this rule exists for.
func TestFuzzyMatchupRejectsUnrelatedSideWords(t *testing.T) {
	event := Event{
		ID: "1", League: "nba", EventKind: "matchup",
		Home: team("Boston Celtics", "Boston", "Celtics", "BOS"),
		Away: team("New York Knicks", "New York", "Knicks", "NYK"),
	}
	for _, name := range []string{
		"Celtics vs Knickz Lakers",
		"Celtics vs Knickz vs Nets",
		// A local RSN is branded with its team, so its name is also a team
		// word: "Cardinals TV" may not turn into evidence against the Knicks.
		"Celtics vs Knickz Cardinals",
	} {
		if got := EventStreams(event, []Channel{{ID: name, Name: name}}, ""); len(got) != 0 {
			t.Fatalf("channel %q matched with an unrelated word: %#v", name, got)
		}
	}

	// Noise and the teams' own words stay allowed.
	got := EventStreams(event, []Channel{{ID: "ok", Name: "NBA HD Celtics vs Knickz New York"}}, "")
	if len(got) != 1 {
		t.Fatalf("matches = %#v, want the tolerated matchup", got)
	}
}

// A provider, network or carriage product says where the game is shown, not
// who plays in it, so it must not veto an otherwise sound misspelt matchup.
// MediaStorm's identical rule drops these feeds; that is a false rejection we
// deliberately do not inherit.
func TestFuzzyMatchupAllowsProviderDescriptors(t *testing.T) {
	event := Event{
		ID: "1", League: "nba", EventKind: "matchup",
		Home: team("Boston Celtics", "Boston", "Celtics", "BOS"),
		Away: team("New York Knicks", "New York", "Knicks", "NYK"),
	}
	for _, name := range []string{
		"Celtics vs Knickz League Pass",
		"Celtics vs Knickz League Pass HD",
		"Celtics vs Knickz MSG",
		"Celtics vs Knickz MSG Network HD",
		"Celtics vs Knickz Spectrum",
	} {
		got := EventStreams(event, []Channel{{ID: name, Name: name}}, "")
		if len(got) != 1 {
			t.Fatalf("channel %q was rejected for a provider descriptor: %#v", name, got)
		}
		if got[0].Confidence != 0.7 || got[0].ConfidenceTier != "possible" {
			t.Fatalf("channel %q = %+v, want 0.70 possible", name, got[0])
		}
		if got[0].MatchedOn != "matchup-name" {
			t.Fatalf("channel %q evidence = %q, want matchup-name", name, got[0].MatchedOn)
		}
	}

	// A provider prefix before the matchup leaves the split alone only when the
	// teams are not misspelt: the strict path already reads that shape.
	if got := EventStreams(event, []Channel{{ID: "prefix", Name: "NBA League Pass: Celtics vs Knicks"}}, ""); len(got) != 1 {
		t.Fatalf("prefixed matchup matched = %#v, want the strict path to keep it", got)
	}
}

// Short tokens are different names, not typos: one edit on a four-letter
// nickname would put any three-letter neighbour one character away.
func TestFuzzyMatchupIgnoresShortTokenEdits(t *testing.T) {
	event := Event{
		ID: "1", League: "nhl", EventKind: "matchup",
		Home: team("Pittsburgh Penguins", "Pittsburgh", "Penguins", "PIT"),
		Away: team("Montreal Canadiens", "Montreal", "Canadiens", "MTL"),
	}
	if got := EventStreams(event, []Channel{{ID: "long", Name: "Penguinz vs Canadiens"}}, ""); len(got) != 1 {
		t.Fatalf("single edit on a long nickname was rejected: %#v", got)
	}
	// "Pens" is four letters; "Penu" must not become a Penguins feed.
	thirteen := event
	thirteen.Home = team("Pittsburgh Pens", "Pittsburgh", "Pens", "PIT")
	if got := EventStreams(thirteen, []Channel{{ID: "short", Name: "Penu vs Canadiens"}}, ""); len(got) != 0 {
		t.Fatalf("short token was treated as a typo: %#v", got)
	}
}
