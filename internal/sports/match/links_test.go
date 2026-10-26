package match

import (
	"testing"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

func linkTeam() sports.SportsTeamRecord {
	return sports.SportsTeamRecord{
		ID: "nba:2", League: "nba", ProviderID: "2", Name: "Boston Celtics",
		Location: "Boston", Nickname: "Celtics", Abbreviation: "BOS",
	}
}

func linkChannels() []Channel {
	return []Channel{
		{ID: "c1", Name: "Celtics", TvgID: "celtics.us", SourceID: "src", SourceName: "Provider"},
		{ID: "c2", Name: "Boston Celtics", TvgID: "boston-celtics.us", SourceID: "src", SourceName: "Provider"},
		{ID: "c3", Name: "NBC Sports Boston", TvgID: "", SourceID: "src", SourceName: "Provider"},
		{ID: "c4", Name: "Celtics vs Heat", TvgID: "", SourceID: "src", SourceName: "Provider"},
		{ID: "c5", Name: "End | Celtics", TvgID: "", SourceID: "src", SourceName: "Provider"},
		{ID: "c6", Name: "NBA League Pass", TvgID: "", SourceID: "src", SourceName: "Provider"},
	}
}

func TestResolveSportsLinkSelfHealsAcrossPlaylistIDs(t *testing.T) {
	link := sports.SportsTeamChannelLink{ChannelRef: "old-id", ChannelTvgID: "Celtics.US", ChannelName: "Old name"}
	got := ResolveSportsLink(link, linkChannels())
	if !got.Resolved || got.ChannelID != "c1" {
		t.Fatalf("tvg-id resolution: %+v", got)
	}
	byName := sports.SportsTeamChannelLink{ChannelRef: "old-id", ChannelName: "boston celtics"}
	got = ResolveSportsLink(byName, linkChannels())
	if !got.Resolved || got.ChannelID != "c2" {
		t.Fatalf("name resolution: %+v", got)
	}
	// The stored id is a last resort only when the snapshot no longer matches.
	byID := sports.SportsTeamChannelLink{ChannelRef: "c3", ChannelName: "Gone"}
	got = ResolveSportsLink(byID, linkChannels())
	if !got.Resolved || got.ChannelID != "c3" {
		t.Fatalf("id fallback: %+v", got)
	}
	missing := ResolveSportsLink(sports.SportsTeamChannelLink{ChannelRef: "gone", ChannelName: "Also gone"}, linkChannels())
	if missing.Resolved || missing.ChannelID != "" {
		t.Fatalf("missing link resolved: %+v", missing)
	}
}

func TestResolveSportsLinkNeverGuessesBetweenSources(t *testing.T) {
	channels := []Channel{
		{ID: "a1", Name: "NBA TV", TvgID: "nba-tv.us", SourceID: "src-a", SourceName: "A"},
		{ID: "b1", Name: "NBA TV", TvgID: "nba-tv.us", SourceID: "src-b", SourceName: "B"},
	}
	stored := sports.SportsTeamChannelLink{ChannelTvgID: "nba-tv.us", ChannelName: "NBA TV", SourceID: "src-b"}
	got := ResolveSportsLink(stored, channels)
	if !got.Resolved || got.ChannelID != "b1" {
		t.Fatalf("source disambiguation: %+v", got)
	}
	// The stored channel's old playlist id still identifies it exactly.
	stored.ChannelRef = "a1"
	got = ResolveSportsLink(stored, channels)
	if !got.Resolved || got.ChannelID != "a1" {
		t.Fatalf("stored id precedence: %+v", got)
	}
	// With neither source nor id present, an ambiguous tvg-id is not guessed.
	ambiguous := sports.SportsTeamChannelLink{ChannelTvgID: "nba-tv.us", ChannelName: "NBA TV"}
	got = ResolveSportsLink(ambiguous, channels)
	if got.Resolved || got.ChannelID != "" {
		t.Fatalf("ambiguous tvg-id resolved: %+v", got)
	}
	// A stale source must not select the other source's different channel.
	stale := sports.SportsTeamChannelLink{ChannelTvgID: "nba-tv.us", ChannelName: "NBA TV", SourceID: "src-gone"}
	got = ResolveSportsLink(stale, channels)
	if got.Resolved || got.ChannelID != "" {
		t.Fatalf("stale source guessed: %+v", got)
	}
	// A unique stored name can disambiguate when the id is shared.
	unique := sports.SportsTeamChannelLink{ChannelTvgID: "nba-tv.us", ChannelName: "NBA TV"}
	channels[1].Name = "NBA TV Canada"
	got = ResolveSportsLink(unique, channels)
	if !got.Resolved || got.ChannelID != "a1" {
		t.Fatalf("unique name disambiguation: %+v", got)
	}
}

func TestRankTeamRecordCandidatesKeepsExplicitMatchesManual(t *testing.T) {
	got := RankTeamRecordCandidates(linkTeam(), linkChannels(), "MSG", 20)
	if len(got) != 0 {
		t.Fatalf("unrelated explicit search returned %+v", got)
	}
	got = RankTeamRecordCandidates(linkTeam(), linkChannels(), "NBC", 20)
	if len(got) != 1 || got[0].MatchReason != "Manual text match" || got[0].ConfidenceTier == "strong" {
		t.Fatalf("explicit candidates: %+v", got)
	}
	// Ranked automatic suggestions never label a city-only or dynamic feed as
	// a permanent team channel.
	ranked := RankTeamRecordCandidates(linkTeam(), linkChannels(), "", 20)
	if len(ranked) == 0 || ranked[0].ChannelID != "c2" || ranked[0].Confidence != 1 {
		t.Fatalf("ranked suggestions: %+v", ranked)
	}
	auto := AutoLinkCandidates(sportTeamFromRecord(linkTeam()), "nba", linkChannels())
	if len(auto) != 2 || auto[0].ChannelID != "c2" || auto[1].ChannelID != "c1" {
		t.Fatalf("auto candidates: %+v", auto)
	}
	for _, candidate := range auto {
		if candidate.ChannelID == "c3" || candidate.ChannelID == "c4" || candidate.ChannelID == "c5" || candidate.ChannelID == "c6" {
			t.Fatalf("dynamic/non-team feed auto-linked: %+v", candidate)
		}
	}
}

func TestEvaluateAutoLinksRequiresConfidenceOrLead(t *testing.T) {
	teams := []sports.SportsTeamRecord{
		linkTeam(),
		{ID: "nba:14", League: "nba", ProviderID: "14", Name: "Miami Heat", Location: "Miami", Nickname: "Heat", Abbreviation: "MIA"},
	}
	channels := append(linkChannels(),
		Channel{ID: "m1", Name: "Miami", SourceID: "src"},
		Channel{ID: "m2", Name: "Heat", SourceID: "src"},
	)
	evaluation := EvaluateAutoLinks("nba", teams, map[string][]sports.SportsTeamChannelLink{}, channels)
	if evaluation.Preview.EligibleCount != 1 || evaluation.Preview.SkippedCount != 1 {
		t.Fatalf("preview counts: %+v", evaluation.Preview)
	}
	if len(evaluation.Links) != 1 || evaluation.Links[0].TeamID != "nba:2" || !evaluation.Links[0].AutoLinked {
		t.Fatalf("links: %+v", evaluation.Links)
	}
	for _, result := range evaluation.Preview.Results {
		if result.Team.ID == "nba:14" {
			if result.Eligible || result.SkipReason != "Confidence or lead over runner-up is too low" {
				t.Fatalf("ambiguous team linked: %+v", result)
			}
		}
	}
}

func TestEvaluateAutoLinksSkipsExistingPrimaryWithoutWritingOthers(t *testing.T) {
	teams := []sports.SportsTeamRecord{linkTeam()}
	existing := map[string][]sports.SportsTeamChannelLink{
		"nba:2": {{TeamID: "nba:2", Slot: "primary", ChannelName: "User choice"}},
	}
	evaluation := EvaluateAutoLinks("nba", teams, existing, linkChannels())
	if evaluation.Preview.EligibleCount != 0 || len(evaluation.Links) != 0 {
		t.Fatalf("existing primary replaced: %+v", evaluation)
	}
	if evaluation.Preview.Results[0].SkipReason != "Primary channel already linked" {
		t.Fatalf("skip reason: %+v", evaluation.Preview.Results[0])
	}
}
