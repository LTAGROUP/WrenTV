package sports

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxSportsPreferenceRevision keeps the revision an exact JavaScript safe
// integer; clients compare it as a number.
const MaxSportsPreferenceRevision uint64 = 9007199254740991

var ErrInvalidSportsPreferences = errors.New("invalid sports preferences")

// SchoolFollow is a college/program follow. Its custom marshaller preserves an
// explicitly empty selected-programs list, which otherwise disappears behind
// omitempty and would turn "no programmes selected" into "all supported".
type SchoolFollow struct {
	SchoolID string `json:"schoolId"`
	Scope    string `json:"scope"`
	// MarshalJSON preserves an explicitly empty selected-programs list.
	ProgramIDs []string `json:"programIds,omitempty"`
}

func (s SchoolFollow) MarshalJSON() ([]byte, error) {
	if s.Scope == "selected-programs" {
		return json.Marshal(struct {
			SchoolID   string   `json:"schoolId"`
			Scope      string   `json:"scope"`
			ProgramIDs []string `json:"programIds"`
		}{s.SchoolID, s.Scope, s.ProgramIDs})
	}
	type plain SchoolFollow
	return json.Marshal(plain(s))
}

// SportsPreferences is MediaStorm's versioned preference document. Unknown IDs
// survive; unknown schema fields are rejected. Wren TV has authenticated users
// rather than profiles, so one document belongs to each user.
type SportsPreferences struct {
	Version        int            `json:"version"`
	PinnedSportIDs []string       `json:"pinnedSportIds"`
	CompetitionIDs []string       `json:"competitionIds"`
	TeamIDs        []string       `json:"teamIds"`
	AthleteIDs     []string       `json:"athleteIds"`
	Schools        []SchoolFollow `json:"schools"`
}

type SportsPreferenceSnapshot struct {
	Revision    uint64            `json:"revision"`
	Preferences SportsPreferences `json:"preferences"`
}

func EmptySportsPreferences() SportsPreferences {
	return SportsPreferences{
		Version: 1, PinnedSportIDs: []string{}, CompetitionIDs: []string{},
		TeamIDs: []string{}, AthleteIDs: []string{}, Schools: []SchoolFollow{},
	}
}

// sportsObject reads an object with exactly the given keys, rejecting unknown,
// duplicate and missing keys as well as trailing JSON.
func sportsObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalidSportsPreferences
	}
	fields := make(map[string]json.RawMessage)
	allowed := make(map[string]bool)
	for _, key := range keys {
		allowed[key] = true
	}
	for dec.More() {
		token, err = dec.Token()
		if err != nil {
			return nil, ErrInvalidSportsPreferences
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || fields[key] != nil {
			return nil, ErrInvalidSportsPreferences
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, ErrInvalidSportsPreferences
		}
		fields[key] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, ErrInvalidSportsPreferences
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, ErrInvalidSportsPreferences
	}
	if len(fields) != len(keys) {
		return nil, ErrInvalidSportsPreferences
	}
	return fields, nil
}

func sportsID(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", ErrInvalidSportsPreferences
	}
	return strings.TrimSpace(value), nil
}

func sportsIDs(raw json.RawMessage) ([]string, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, ErrInvalidSportsPreferences
	}
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		id, err := sportsID(value)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result, nil
}

// ParseSportsPreferences enforces MediaStorm's document boundary: version 1,
// exact fields, non-empty IDs, de-duplication and school merge rules.
func ParseSportsPreferences(raw []byte) (SportsPreferences, error) {
	empty := SportsPreferences{}
	fields, err := sportsObject(raw, "version", "pinnedSportIds", "competitionIds", "teamIds", "athleteIds", "schools")
	if err != nil {
		return empty, err
	}
	var version int
	if err := json.Unmarshal(fields["version"], &version); err != nil || version != 1 {
		return empty, ErrInvalidSportsPreferences
	}
	result := EmptySportsPreferences()
	for key, target := range map[string]*[]string{
		"pinnedSportIds": &result.PinnedSportIDs, "competitionIds": &result.CompetitionIDs,
		"teamIds": &result.TeamIDs, "athleteIds": &result.AthleteIDs,
	} {
		ids, err := sportsIDs(fields[key])
		if err != nil {
			return empty, err
		}
		*target = ids
	}
	var schools []json.RawMessage
	if err := json.Unmarshal(fields["schools"], &schools); err != nil || schools == nil {
		return empty, ErrInvalidSportsPreferences
	}
	indices := map[string]int{}
	for _, rawSchool := range schools {
		var hint struct {
			Scope string `json:"scope"`
		}
		if err := json.Unmarshal(rawSchool, &hint); err != nil {
			return empty, ErrInvalidSportsPreferences
		}
		keys := []string{"schoolId", "scope"}
		if hint.Scope == "selected-programs" {
			keys = append(keys, "programIds")
		} else if hint.Scope != "all-supported" {
			return empty, ErrInvalidSportsPreferences
		}
		school, err := sportsObject(rawSchool, keys...)
		if err != nil {
			return empty, err
		}
		id, err := sportsID(school["schoolId"])
		if err != nil {
			return empty, err
		}
		next := SchoolFollow{SchoolID: id, Scope: hint.Scope}
		if hint.Scope == "selected-programs" {
			next.ProgramIDs, err = sportsIDs(school["programIds"])
			if err != nil {
				return empty, err
			}
		}
		if i, ok := indices[id]; ok {
			prev := result.Schools[i]
			if prev.Scope == "all-supported" {
				continue
			}
			if next.Scope == "selected-programs" {
				joined, _ := json.Marshal(append(prev.ProgramIDs, next.ProgramIDs...))
				next.ProgramIDs, _ = sportsIDs(joined)
			}
			result.Schools[i] = next
		} else {
			indices[id] = len(result.Schools)
			result.Schools = append(result.Schools, next)
		}
	}
	return result, nil
}

// ParseSportsPreferenceSnapshot validates the optimistic-concurrency envelope.
func ParseSportsPreferenceSnapshot(raw []byte) (SportsPreferenceSnapshot, error) {
	var result SportsPreferenceSnapshot
	fields, err := sportsObject(raw, "revision", "preferences")
	if err != nil {
		return result, err
	}
	var revision *uint64
	if err := json.Unmarshal(fields["revision"], &revision); err != nil || revision == nil || *revision > MaxSportsPreferenceRevision {
		return result, ErrInvalidSportsPreferences
	}
	preferences, err := ParseSportsPreferences(fields["preferences"])
	if err != nil {
		return result, fmt.Errorf("%w: document", err)
	}
	return SportsPreferenceSnapshot{Revision: *revision, Preferences: preferences}, nil
}
