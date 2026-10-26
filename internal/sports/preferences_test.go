package sports

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func prefDoc() []byte {
	return []byte(`{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}`)
}

func TestParseSportsPreferencesStrictBoundary(t *testing.T) {
	cases := []string{
		`{"version":2,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}`,
		`{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[],"extra":1}`,
		`{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[]}`,
		`{"version":1,"pinnedSportIds":[1],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}`,
		`{"version":1,"pinnedSportIds":[""],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}`,
		`{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":{}}`,
		`{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[{"schoolId":"s","scope":"some"}]}`,
		`{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]} {}`,
	}
	for _, raw := range cases {
		if _, err := ParseSportsPreferences([]byte(raw)); !errors.Is(err, ErrInvalidSportsPreferences) {
			t.Fatalf("accepted invalid document %s: %v", raw, err)
		}
	}
}

func TestParseSportsPreferencesDedupesAndPreservesUnknownIDs(t *testing.T) {
	parsed, err := ParseSportsPreferences([]byte(`{"version":1,"pinnedSportIds":["basketball","basketball","future-sport"],"competitionIds":["premier-league"],"teamIds":["nba:2"],"athleteIds":[],"schools":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.PinnedSportIDs) != 2 || parsed.PinnedSportIDs[1] != "future-sport" {
		t.Fatalf("pins: %+v", parsed.PinnedSportIDs)
	}
	if len(parsed.TeamIDs) != 1 || parsed.TeamIDs[0] != "nba:2" {
		t.Fatalf("teams: %+v", parsed.TeamIDs)
	}
	if len(parsed.CompetitionIDs) != 1 || parsed.CompetitionIDs[0] != "premier-league" {
		t.Fatalf("competitions: %+v", parsed.CompetitionIDs)
	}
}

func TestSchoolFollowMergeAndEmptyProgramList(t *testing.T) {
	raw := []byte(`{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[
		{"schoolId":"duke","scope":"selected-programs","programIds":["mbb"]},
		{"schoolId":"duke","scope":"selected-programs","programIds":["wbb","mbb"]},
		{"schoolId":"unc","scope":"all-supported"},
		{"schoolId":"unc","scope":"selected-programs","programIds":["mbb"]},
		{"schoolId":"empty","scope":"selected-programs","programIds":[]}
	]}`)
	parsed, err := ParseSportsPreferences(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Schools) != 3 {
		t.Fatalf("schools: %+v", parsed.Schools)
	}
	if len(parsed.Schools[0].ProgramIDs) != 2 || parsed.Schools[0].ProgramIDs[1] != "wbb" {
		t.Fatalf("merged programs: %+v", parsed.Schools[0])
	}
	if parsed.Schools[1].Scope != "all-supported" {
		t.Fatalf("all-supported lost: %+v", parsed.Schools[1])
	}
	encoded, err := json.Marshal(parsed.Schools[2])
	if err != nil || !strings.Contains(string(encoded), `"programIds":[]`) {
		t.Fatalf("empty selected-programs lost: %s %v", encoded, err)
	}
}

func TestParseSportsPreferenceSnapshot(t *testing.T) {
	valid := []byte(`{"revision":4,"preferences":` + string(prefDoc()) + `}`)
	snapshot, err := ParseSportsPreferenceSnapshot(valid)
	if err != nil || snapshot.Revision != 4 || snapshot.Preferences.Version != 1 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	for _, raw := range []string{
		`{"revision":-1,"preferences":{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}}`,
		`{"revision":1.5,"preferences":{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}}`,
		`{"revision":9007199254740992,"preferences":{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}}`,
		`{"revision":1,"preferences":{"version":1,"pinnedSportIds":[],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]},"extra":1}`,
	} {
		if _, err := ParseSportsPreferenceSnapshot([]byte(raw)); !errors.Is(err, ErrInvalidSportsPreferences) {
			t.Fatalf("accepted snapshot %s: %v", raw, err)
		}
	}
}
