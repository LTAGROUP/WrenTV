package match

import (
	"testing"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

func team(name, location, nickname, abbreviation string) sports.SportsTeam {
	return sports.SportsTeam{Name: name, Location: location, Nickname: nickname, Abbreviation: abbreviation}
}

func TestNormalizeText(t *testing.T) {
	got := normalize("  Montréal_Canadiens-vs.N.Y.  ")
	if got != "montreal canadiens vs n y" {
		t.Fatalf("normalize() = %q", got)
	}
}

func TestWholeTokenBroadcastMatching(t *testing.T) {
	tests := []struct {
		name      string
		channel   string
		broadcast string
		wantMatch bool
		wantTier  string
	}{
		{name: "yes network", channel: "YES Network HD", broadcast: "YES", wantMatch: true, wantTier: "strong"},
		{name: "no substring", channel: "Yesterday Classics", broadcast: "YES", wantMatch: false},
		{name: "nesn", channel: "NESN", broadcast: "NESN", wantMatch: true, wantTier: "strong"},
		{name: "peacock movie stays possible", channel: "Peacock Movies VOD", broadcast: "Peacock", wantMatch: true, wantTier: "possible"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := scoreBroadcastText(tt.channel, tt.broadcast)
			if (ev.score > 0) != tt.wantMatch {
				t.Fatalf("score = %.2f, want match=%v", ev.score, tt.wantMatch)
			}
			if tt.wantMatch && confidenceTier(ev.score) != tt.wantTier {
				t.Fatalf("tier = %q, want %q (score %.2f)", confidenceTier(ev.score), tt.wantTier, ev.score)
			}
		})
	}
}

func TestBroadcastAliasCatalog(t *testing.T) {
	tests := []struct{ broadcast, channel string }{
		{"ESPN Unlmtd", "ESPN Unlimited HD"},
		{"NBC Sports CA", "NBCSCA"},
		{"NBC Sports Phil", "NBC Sports Philadelphia"},
		{"SportsNet LA", "Spectrum SportsNet LA"},
		{"CHSN", "Chicago Sports Network"},
		{"MASN", "Mid Atlantic Sports Network"},
		{"MLB.TV", "MLBTV"},
		{"Angels.TV", "AngelsTV"},
		{"CLEGuardians.TV", "Cleveland Guardians TV"},
		{"TVA", "TVA Sports"},
	}
	for _, tt := range tests {
		t.Run(tt.broadcast+"_"+tt.channel, func(t *testing.T) {
			if ev := scoreBroadcastText(tt.channel, tt.broadcast); ev.score <= 0 {
				t.Fatalf("%q did not match ESPN label %q", tt.channel, tt.broadcast)
			}
		})
	}
	for _, unsafe := range []struct{ channel, broadcast string }{
		{"Yesterday Classics", "YES"},
		{"Turner RSN Documentary", "Rangers Sports Network"},
		{"Sports News", "Sportsnet"},
	} {
		if ev := scoreBroadcastText(unsafe.channel, unsafe.broadcast); ev.score > 0 {
			t.Fatalf("unsafe substring %q matched %q at %.2f", unsafe.channel, unsafe.broadcast, ev.score)
		}
	}
}

func TestLifecycleFiltering(t *testing.T) {
	if got := lifecycle("End | Phillies at Angels"); got != lifecycleEnded {
		t.Fatalf("End lifecycle = %q", got)
	}
	if got := lifecycle("Off Air: Phillies at Angels"); got != lifecycleEnded {
		t.Fatalf("Off Air lifecycle = %q", got)
	}
	if got := lifecycle("Live | Phillies at Angels"); got != lifecycleLive {
		t.Fatalf("Live lifecycle = %q", got)
	}
	if got := lifecycle("Weekend Baseball"); got != "" {
		t.Fatalf("Weekend incorrectly marked %q", got)
	}
	if ev := scoreBroadcastText("End | Peacock 001", "Peacock"); ev.score != 0 {
		t.Fatalf("ended Peacock score = %.2f", ev.score)
	}
}

func TestTeamChannelSuggestionScores(t *testing.T) {
	nationals := team("Washington Nationals", "Washington", "Nationals", "WSH")
	channels := []Channel{
		{ID: "dedicated", Name: "MLB Washington Nationals HD"},
		{ID: "matchup", Name: "Mets vs Nationals"},
		{ID: "other-team", Name: "Washington Capitals"},
	}
	got := RankTeamCandidates(nationals, channels, "", 100)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
	}
	if got[0].ChannelID != "dedicated" || got[0].ConfidenceTier != "strong" {
		t.Fatalf("first candidate = %+v, want strong dedicated channel", got[0])
	}
	if got[1].ChannelID != "matchup" || got[1].ConfidenceTier != "possible" || got[1].Confidence >= 0.60 {
		t.Fatalf("second candidate = %+v, want sub-0.60 possible matchup", got[1])
	}
}

func TestSelectableMatches(t *testing.T) {
	matches := []sports.SportsStreamMatch{
		{ChannelID: "strong", ChannelURL: "livetv:strong", Confidence: 0.85, ConfidenceTier: "strong"},
		{ChannelID: "possible", ChannelURL: "livetv:possible", Confidence: 0.74, ConfidenceTier: "possible"},
		{ChannelID: "weak", ChannelURL: "livetv:weak", Confidence: 0.42, ConfidenceTier: "possible"},
		{ChannelID: "empty", Confidence: 0.98, ConfidenceTier: "strong"},
		{ChannelID: "replay", ChannelURL: "livetv:replay", ChannelName: "Replay: Finals", Confidence: 0.95, ConfidenceTier: "strong"},
	}
	got := Selectable(matches)
	if len(got) != 2 || got[0].ChannelID != "strong" || got[1].ChannelID != "possible" {
		t.Fatalf("selectable matches = %#v", got)
	}
}

func TestManualSearchKeepsZeroConfidenceResult(t *testing.T) {
	nationals := team("Washington Nationals", "Washington", "Nationals", "WSH")
	channels := []Channel{{ID: "manual", Name: "My Custom Baseball Feed"}}
	got := RankTeamCandidates(nationals, channels, "custom", 100)
	if len(got) != 1 || got[0].Confidence != 0 || got[0].ConfidenceTier != "possible" {
		t.Fatalf("manual result = %+v, want visible 0%% possible match", got[0:])
	}
}

func TestMatchupVariantsAndShortCodes(t *testing.T) {
	home := makeIdentity("nfl", "Kansas City Chiefs", "Kansas City", "Chiefs", "KC")
	away := makeIdentity("nfl", "Baltimore Ravens", "Baltimore", "Ravens", "BAL")
	for _, title := range []string{"Kansas City vs Baltimore", "Ravens @ Chiefs", "Baltimore Kansas City", "KC vs BAL", "KC vs Ravens", "BAL @ Chiefs"} {
		if ev := scoreMatchupText(title, home, away); ev.score < StrongConfidence {
			t.Fatalf("%q score = %.2f, want strong matchup", title, ev.score)
		}
	}
	if ev := scoreTextForTeam("Tennessee Sports", home, false); ev.score != 0 {
		t.Fatalf("KC/short-code false positive score = %.2f", ev.score)
	}
	newEngland := makeIdentity("nfl", "New England Patriots", "New England", "Patriots", "NE")
	if ev := scoreTextForTeam("Tennessee Sports", newEngland, false); ev.score != 0 {
		t.Fatalf("NE matched inside Tennessee with score %.2f", ev.score)
	}
}

func TestMatchupRequiresBothTeamsInOneSegment(t *testing.T) {
	home := makeIdentity("soccer", "United States", "United States", "United States", "USA")
	away := makeIdentity("soccer", "Australia", "Australia", "Australia", "AUS")
	ev := scoreMatchupText("USA Soccer07: Australia vs Turkey", home, away)
	if ev.score >= StrongConfidence {
		t.Fatalf("cross-segment evidence = %.2f, want non-strong", ev.score)
	}
}

func TestStreamGroupingPreservesDistinctFeeds(t *testing.T) {
	matches := []sports.SportsStreamMatch{
		{ChannelID: "1", ChannelName: "Live | Phillies at Angels HD", ChannelURL: "livetv:one", Confidence: .94, ConfidenceTier: "strong", MatchReason: "Both team nicknames"},
		{ChannelID: "2", ChannelName: "Phillies at Angels FHD", ChannelURL: "livetv:two", Confidence: .94, ConfidenceTier: "strong", MatchReason: "Both team nicknames"},
		{ChannelID: "3", ChannelName: "Phillies at Angels FHD", ChannelURL: "livetv:two", Confidence: .94, ConfidenceTier: "strong", MatchReason: "Both team nicknames"},
	}
	flat, groups := BuildStreamGroups(matches)
	if len(flat) != 2 {
		t.Fatalf("flat len = %d, want exact URL dedupe to 2", len(flat))
	}
	if len(groups) != 1 || groups[0].FeedCount != 2 || len(groups[0].Alternatives) != 1 {
		t.Fatalf("groups = %+v, want one group with two feeds", groups)
	}
}

func TestSearchChannelsIsExplicitAndNeverStrong(t *testing.T) {
	channels := []Channel{
		{ID: "1", Name: "NBA TV"},
		{ID: "2", Name: "NBA TV HD", SourceName: "Provider"},
		{ID: "3", Name: "Celtics vs Knicks", TvgName: "NBA TV"},
		{ID: "4", Name: "Tennis Channel"},
	}
	matches := SearchChannels(channels, "nba")
	if len(matches) != 3 {
		t.Fatalf("matches = %+v", matches)
	}
	for _, m := range matches {
		if m.ConfidenceTier != "manual" || m.MatchedOn != "manual-search" || m.Confidence != 1 {
			t.Fatalf("manual match = %+v", m)
		}
		if m.ChannelURL != "livetv:"+m.ChannelID {
			t.Fatalf("channel reference leaked or missing: %q", m.ChannelURL)
		}
	}
	_, groups := BuildStreamGroups(matches)
	if len(groups) != 3 {
		t.Fatalf("manual groups = %+v", groups)
	}
	for _, group := range groups {
		if group.ConfidenceTier == "strong" {
			t.Fatalf("manual search must not claim a strong match: %+v", group)
		}
	}
}

func TestEventStreamsRequiresMatchupAndMatchesEPG(t *testing.T) {
	event := Event{
		ID: "1", League: "mlb", EventKind: "matchup", StartTime: 1_700_000_000,
		Home: team("Los Angeles Angels", "Los Angeles", "Angels", "LAA"),
		Away: team("Philadelphia Phillies", "Philadelphia", "Phillies", "PHI"),
	}
	channels := []Channel{
		{ID: "dedicated", Name: "MLB Baseball"},
		{ID: "epg", Name: "Sports Channel 2", Now: &Program{
			Title: "Phillies at Angels", Start: 1_700_000_000, End: 1_700_010_000,
		}},
		{ID: "ended", Name: "End | Phillies at Angels"},
		{ID: "replay", Name: "Replay: Phillies at Angels"},
	}
	got := EventStreams(event, channels, "")
	if len(got) != 1 || got[0].ChannelID != "epg" {
		t.Fatalf("matches = %#v, want only the EPG channel", got)
	}
	if got[0].ConfidenceTier != "strong" || got[0].MatchedOn != "epg-title" {
		t.Fatalf("EPG match = %+v, want strong epg-title", got[0])
	}

	racing := event
	racing.EventKind = "race-session"
	if got := EventStreams(racing, channels, ""); len(got) != 0 {
		t.Fatalf("non-matchup event matched channels: %#v", got)
	}
}

func TestEventStreamsEndedAndArchiveExcluded(t *testing.T) {
	event := Event{
		ID: "1", League: "nba", EventKind: "matchup",
		Home: team("Boston Celtics", "Boston", "Celtics", "BOS"),
		Away: team("New York Knicks", "New York", "Knicks", "NYK"),
	}
	channels := []Channel{
		{ID: "live", Name: "Celtics vs Knicks"},
		{ID: "ended", Name: "End | Celtics vs Knicks"},
		{ID: "archive", Name: "Archive: Celtics vs Knicks"},
	}
	got := EventStreams(event, channels, "")
	if len(got) != 1 || got[0].ChannelID != "live" {
		t.Fatalf("matches = %#v, want only the live channel", got)
	}
}

func TestBroadcastFilterIsScoped(t *testing.T) {
	event := Event{
		ID: "1", League: "nhl", EventKind: "matchup",
		Home: team("Toronto Maple Leafs", "Toronto", "Maple Leafs", "TOR"),
		Away: team("Montreal Canadiens", "Montreal", "Canadiens", "MTL"),
	}
	channels := []Channel{
		{ID: "sportsnet", Name: "Sportsnet HD"},
		{ID: "other", Name: "Leafs vs Canadiens"},
	}
	got := EventStreams(event, channels, "Sportsnet")
	if len(got) != 1 || got[0].ChannelID != "sportsnet" || got[0].MatchedOn != "broadcast" {
		t.Fatalf("broadcast filter matches = %#v", got)
	}
}
