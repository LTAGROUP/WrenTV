package match

import (
	"regexp"
	"strings"
)

// Named events (fight cards, golf tournaments, race sessions and cycling
// stages) need their own evidence rules: a team match must never stand in for
// one, and a race/session title is plausible evidence, never proof of a live
// broadcast.

var (
	namedYear       = regexp.MustCompile(`^20[0-9]{2}$`)
	namedCardNumber = regexp.MustCompile(`^[0-9]{1,4}$`)
	namedStage      = regexp.MustCompile(`(?i)\b(?:stage|etape|étape)\s*([0-9]+)\b`)
	namedPractice   = regexp.MustCompile(`(?i)\b(?:fp|(?:free\s+)?practice\s*)([1-3])\b`)
	namedSeries     = regexp.MustCompile(`(?i)\b(f[123]|formula\s*(?:[123]|one|two|three|e)|motogp|moto2|moto3|nascar|indycar)\b`)
)

// namedSportWords are unambiguous sport exclusions. Generic categories such as
// "motorsport" are not reliable evidence and stay out of the list.
var namedSportWords = map[string]string{
	"golf": "golf", "tennis": "tennis", "snooker": "snooker", "darts": "darts",
	"boxing": "boxing", "mma": "mma", "ufc": "mma", "cycling": "cycling", "basketball": "basketball",
	"baseball": "baseball", "cricket": "cricket", "hockey": "hockey",
}

func namedEventSport(event Event) string {
	if event.Sport != "" {
		return event.Sport
	}
	return map[string]string{"pga": "golf", "ufc": "mma", "boxing": "boxing"}[event.League]
}

func conflictingNamedSport(value string, event Event) bool {
	if event.EventKind != "tournament" && event.EventKind != "fight-card" {
		return false
	}
	wanted := namedEventSport(event)
	if wanted == "" {
		return false
	}
	for _, token := range tokens(value) {
		if sport, ok := namedSportWords[token]; ok && sport != wanted {
			return true
		}
	}
	return false
}

func conflictingSeries(value, league string) bool {
	normalizedLeague := league
	if strings.HasPrefix(league, "espn:racing:nascar-") {
		normalizedLeague = "nascar"
	}
	for _, raw := range namedSeries.FindAllString(value, -1) {
		series := strings.ReplaceAll(strings.ToLower(raw), " ", "")
		switch series {
		case "formula1", "formulaone":
			series = "f1"
		case "formula2", "formulatwo":
			series = "f2"
		case "formula3", "formulathree":
			series = "f3"
		}
		if series != normalizedLeague {
			return true
		}
	}
	return false
}

func namedSession(value string) string {
	v := strings.ToLower(value)
	if m := namedPractice.FindStringSubmatch(v); len(m) > 1 {
		return "practice" + m[1]
	}
	if v == "qual" {
		return "qualifying"
	}
	for _, name := range []string{"qualifying", "practice", "sprint", "race"} {
		if strings.Contains(v, name) {
			return name
		}
	}
	return ""
}

func conflictingSegment(value string, event Event) bool {
	if event.EventKind == "race-session" {
		actual, wanted := namedSession(value), namedSession(event.Context)
		if actual == "" || wanted == "" {
			return false
		}
		a, w := strings.TrimRight(actual, "123"), strings.TrimRight(wanted, "123")
		return a != w || (actual != a && wanted != w && actual != wanted)
	}
	if event.EventKind == "cycling-stage" {
		expected, actual := namedStage.FindStringSubmatch(event.Context), namedStage.FindStringSubmatch(value)
		return len(actual) > 1 && len(expected) > 1 && actual[1] != expected[1]
	}
	return false
}

// stripEventLabels removes only known leading category labels, never arbitrary
// sponsor or event words.
func stripEventLabels(title string, event Event) string {
	title = normalize(title)
	labels := map[string][]string{
		"f1": {"formula 1", "formula one", "f1"}, "motogp": {"motogp"},
		"nascar": {"nascar"}, "indycar": {"indycar"},
		"espn:racing:nascar-secondary": {"nascar", "xfinity", "oreilly"},
		"espn:racing:nascar-truck":     {"nascar", "trucks", "truck"},
		"pga":                          {"pga tour", "pga", "golf"},
		"cycling":                      {"cycling"}, "boxing": {"boxing"}, "ufc": {"mma"},
	}
	for _, label := range labels[event.League] {
		title = strings.TrimSpace(strings.TrimPrefix(title, label+" "))
	}
	return title
}

// raceTitle normalizes sponsor prefixes explicitly. Dropping arbitrary leading
// words could turn the Azerbaijan Grand Prix into the Qatar Grand Prix.
func raceTitle(value string) string {
	list := tokens(value)
	if len(list) >= 2 && list[0] == "qatar" && list[1] == "airways" {
		list = list[2:]
	}
	expanded := make([]string, 0, len(list))
	for _, token := range list {
		if token == "gp" {
			expanded = append(expanded, "grand", "prix")
		} else {
			expanded = append(expanded, token)
		}
	}
	return strings.Join(expanded, " ")
}

func scoreNamedEvent(value string, event Event) evidence {
	if conflictingNamedSport(value, event) {
		return evidence{}
	}
	if event.EventKind != "race-session" && event.EventKind != "cycling-stage" {
		// Preserve colon-separated card/bout identities while removing category labels.
		parts := strings.Split(event.Title, ":")
		for i, part := range parts {
			parts[i] = stripEventLabels(part, event)
		}
		result := scoreEventTitle(value, strings.Join(parts, ":"))
		if event.EventKind != "fight-card" && result.score > 0.78 {
			result.score = 0.78
		}
		return result
	}
	if conflictingSegment(value, event) {
		return evidence{}
	}
	title := stripEventLabels(event.Title, event)
	// A race name must remain a phrase: scattered Tour / de / France words also
	// occur in golf's DP World Tour Open de France.
	if event.EventKind == "cycling-stage" && !strings.Contains(" "+normalize(value)+" ", " "+normalize(title)+" ") {
		return evidence{}
	}
	if event.EventKind == "race-session" {
		if conflictingSeries(value, event.League) {
			return evidence{}
		}
		value, title = raceTitle(value), raceTitle(title)
	}
	set := map[string]struct{}{}
	for _, token := range tokens(value) {
		set[token] = struct{}{}
	}
	meaningful := 0
	for _, token := range tokens(title) {
		switch token {
		case "the", "and", "de", "d", "a", "championship":
			continue
		}
		if len(token) < 3 || namedYear.MatchString(token) {
			continue
		}
		meaningful++
		if _, ok := set[token]; !ok {
			return evidence{}
		}
	}
	generic := strings.ToLower(strings.TrimSpace(title))
	if meaningful == 0 || generic == "race" || generic == "grand prix" || generic == "qualifying" || generic == "practice" {
		return evidence{}
	}
	if event.EventKind == "race-session" {
		v := normalize(value)
		series := map[string][]string{
			"f1": {"formula1", "formulaone"}, "motogp": {"motogp"},
			"nascar": {"nascar"}, "indycar": {"indycar"},
			"espn:racing:nascar-secondary": {"nascar", "xfinity", "oreilly"},
			"espn:racing:nascar-truck":     {"nascar", "craftsman", "trucks"},
		}
		_, hasF1 := set["f1"]
		found := event.League == "f1" && hasF1
		for _, alias := range series[event.League] {
			if strings.Contains(v, alias) {
				found = true
			}
		}
		if !found {
			// Addons often omit the series. Require both the specific race
			// identity above and an explicit matching session.
			actual, wanted := namedSession(value), namedSession(event.Context)
			if actual == "" || actual != wanted {
				return evidence{}
			}
		}
	} else {
		expected, actual := namedStage.FindStringSubmatch(event.Context), namedStage.FindStringSubmatch(value)
		if len(actual) > 1 && len(expected) > 1 && actual[1] != expected[1] {
			return evidence{}
		}
		women := func(v string) bool {
			v = strings.ToLower(v)
			return strings.Contains(v, "femmes") || strings.Contains(v, "femenina") || strings.Contains(v, "women")
		}
		if women(value) != women(event.Title) {
			return evidence{}
		}
	}
	// Race identity is plausible evidence, not proof of a live broadcast.
	return evidence{
		score: 0.78, reason: "Race / session title match — verify broadcast",
		terms: tokens(event.Title), on: "event-title",
	}
}

// scoreEventTitle requires a full named event (or full parent card/bout). Two
// shared words could otherwise match a different opponent or tournament.
func scoreEventTitle(value, title string) evidence {
	valueTokens := tokenSet(value)
	var best evidence
	for _, part := range strings.Split(title, ":") {
		terms := []string{}
		distinctive := false
		matched := true
		for _, token := range tokens(part) {
			switch token {
			case "the", "and", "vs", "v", "at", "de", "of":
				continue
			}
			if namedYear.MatchString(token) {
				continue
			}
			if _, ok := valueTokens[token]; !ok {
				matched = false
				break
			}
			terms = append(terms, token)
			switch token {
			case "golf", "boxing", "mma", "ufc", "tour", "round", "event", "championship",
				"championships", "world", "open", "cup", "grand", "prix", "race", "live", "men", "women":
			default:
				// A bare number is shared by unrelated events, so only a word
				// identifies one. Numbered cards are handled separately above.
				if len(token) >= 3 && strings.IndexFunc(token, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
					distinctive = true
				}
			}
		}
		// A promotion and its numbered card identify a broadcast without fighter names.
		partTokens := tokens(part)
		numberedCard := len(partTokens) == 2 && partTokens[0] == "ufc" && namedCardNumber.MatchString(partTokens[1])
		if !matched || len(terms) == 0 || (!distinctive && !numberedCard) {
			continue
		}
		score := 0.78
		if len(terms) >= 2 {
			score = 0.82 + float64(len(terms))*0.04
			if score > 0.98 {
				score = 0.98
			}
		}
		if score > best.score {
			best = evidence{score: score, reason: "Event title match", terms: terms, on: "event-title"}
		}
	}
	return best
}
