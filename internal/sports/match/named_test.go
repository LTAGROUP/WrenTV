package match

import "testing"

func TestNamedEventScoringKeepsIdentity(t *testing.T) {
	card := Event{ID: "ufc:1", League: "ufc", Sport: "mma", EventKind: "fight-card", Title: "UFC 331: Van vs. Pantoja 2"}
	if got := scoreNamedEvent("UFC 331", card); got.score <= 0 {
		t.Fatal("numbered card did not match")
	}
	if got := scoreNamedEvent("UFC 332", card); got.score > 0 {
		t.Fatalf("different card matched: %+v", got)
	}
	if got := scoreNamedEvent("UFC 331: Van vs. Pantoja 2", card); got.score < 0.9 {
		t.Fatalf("full card identity scored low: %+v", got)
	}

	boxing := Event{ID: "boxing:1", League: "boxing", Sport: "boxing", EventKind: "fight-card", Title: "Boxing: Canelo Alvarez vs Terence Crawford"}
	if got := scoreNamedEvent("Boxing: Canelo Alvarez vs Terence Crawford", boxing); got.score <= 0.9 {
		t.Fatalf("full boxing card did not match: %+v", got)
	}
	if got := scoreNamedEvent("UFC: Canelo Alvarez vs Terence Crawford", boxing); got.score > 0 {
		t.Fatalf("cross-sport card matched: %+v", got)
	}
	if got := scoreNamedEvent("Boxing: Canelo Alvarez vs Another Boxer", boxing); got.score > 0 {
		t.Fatalf("partial card matched: %+v", got)
	}
}

// A title whose only distinctive token is a number does not identify an event:
// "500" appears in unrelated races, distances and editions.
func TestEventTitleNeedsAWordToBeDistinctive(t *testing.T) {
	if got := scoreEventTitle("Event 500", "Event 500"); got.score > 0 {
		t.Fatalf("numeric-only identity matched: %+v", got)
	}
	if got := scoreEventTitle("Tournament Sprint 500", "Sprint 500"); got.score <= 0 {
		t.Fatalf("named identity did not match: %+v", got)
	}
}

func TestRaceSessionScoringRequiresSeriesAndSession(t *testing.T) {
	race := Event{
		ID: "f1:1:2", League: "f1", Sport: "racing", EventKind: "race-session",
		Title: "Pirelli Italian Grand Prix", Context: "Race",
	}
	if got := scoreNamedEvent("F1 Pirelli Italian Grand Prix", race); got.score <= 0 {
		t.Fatal("race title did not match")
	}
	if got := scoreNamedEvent("MotoGP Pirelli Italian Grand Prix", race); got.score > 0 {
		t.Fatalf("wrong series matched: %+v", got)
	}
	if got := scoreNamedEvent("F1 Pirelli Italian Grand Prix Practice 1", race); got.score > 0 {
		t.Fatalf("wrong session matched: %+v", got)
	}
	if got := scoreNamedEvent("F1 Grand Prix", race); got.score > 0 {
		t.Fatalf("generic race identity matched: %+v", got)
	}
	practice := race
	practice.Context = "FP2"
	if got := scoreNamedEvent("F1 Pirelli Italian GP Practice 2", practice); got.score <= 0 {
		t.Fatalf("explicit matching practice did not match: %+v", got)
	}

	stage := Event{
		ID: "aso:tour:2026:12", League: "aso:tour", Sport: "cycling", EventKind: "cycling-stage",
		Title: "Tour de France", Context: "Stage 12",
	}
	if got := scoreNamedEvent("Cycling: Tour de France Stage 12", stage); got.score <= 0 {
		t.Fatalf("cycling stage did not match: %+v", got)
	}
	if got := scoreNamedEvent("Cycling: Tour de France Stage 13", stage); got.score > 0 {
		t.Fatalf("wrong stage matched: %+v", got)
	}
	if got := scoreNamedEvent("Cycling: Tour de France Femmes Stage 12", stage); got.score > 0 {
		t.Fatalf("women's race matched the men's stage: %+v", got)
	}
	if got := scoreNamedEvent("Golf: Open de France", stage); got.score > 0 {
		t.Fatalf("non-cycling title matched: %+v", got)
	}
}

func TestEventStreamsNeverAutoClaimsANamedEvent(t *testing.T) {
	ev := Event{
		ID: "f1:1:2", League: "f1", Sport: "racing", EventKind: "race-session",
		Title: "Italian Grand Prix", Context: "Race",
	}
	channels := []Channel{{ID: "c1", Name: "F1 Italian Grand Prix"}}
	matches, groups := Resolve(ev, channels, "")
	if len(matches) != 1 || len(groups) != 1 {
		t.Fatalf("unexpected matches %+v groups %+v", matches, groups)
	}
	if matches[0].Confidence > 0.84 || matches[0].ConfidenceTier == "strong" {
		t.Fatalf("race match claimed strong evidence: %+v", matches[0])
	}
	if matches[0].ChannelURL != "livetv:c1" || matches[0].MatchReason == "" {
		t.Fatalf("race match lost its channel reference or reason: %+v", matches[0])
	}
}
