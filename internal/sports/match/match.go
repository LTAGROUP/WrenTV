// Package match ports MediaStorm's SportsHub stream matcher: token-aware team,
// matchup, broadcast and EPG matching with its confidence thresholds and its
// safeguards against linking an event to the wrong channel.
//
// Matching is heuristic, never authoritative. It deliberately returns plausible
// candidates for explicit selection instead of claiming a broadcast exists.
package match

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

// StrongConfidence is the auto-play threshold. Below it a result is only
// "possible" and must never be auto-selected.
const StrongConfidence = 0.85

// Program is the EPG shape the matcher needs. Times are optional; when present
// they bound how far a title may promote a channel.
type Program struct {
	Title       string
	Description string
	Start       int64
	End         int64
}

// Channel is one Live TV channel with what is on now and next.
type Channel struct {
	ID         string
	Name       string
	TvgName    string
	TvgID      string
	Group      string
	SourceID   string
	SourceName string
	Logo       string
	// Metadata is the provider's own description of the channel (an addon
	// catalogue entry, for example). It never scores evidence; it only rejects a
	// channel whose description names a different sport or series.
	Metadata string
	Now      *Program
	Next     *Program
}

// Event is the subset of a sports game the matcher scores.
type Event struct {
	ID        string
	League    string
	Sport     string
	EventKind string
	StartTime int64 // unix seconds; zero when unknown
	Title     string
	// Context is the event-kind label (race session, stage or round). Named
	// events such as races are matched on it instead of team identities.
	Context string
	Home    sports.SportsTeam
	Away    sports.SportsTeam
}

type teamIdentity struct {
	league       string
	name         string
	location     string
	nickname     string
	abbreviation string
	aliases      []string
}

type evidence struct {
	score     float64
	reason    string
	terms     []string
	team      string
	on        string
	program   string
	lifecycle string
}

const (
	lifecycleLive  = "live"
	lifecycleEnded = "ended"
)

var noiseTokens = map[string]struct{}{
	"4k": {}, "8k": {}, "channel": {}, "feed": {}, "fhd": {}, "hd": {},
	"live": {}, "mlb": {}, "nba": {}, "nfl": {}, "nhl": {}, "premium": {},
	"sd": {}, "sport": {}, "sports": {}, "tv": {}, "uhd": {}, "us": {}, "usa": {},
}

// descriptorTokens say where a game is carried, not who plays in it: a
// network, a carriage label or a streaming product. A side of a matchup may
// carry them without contradicting the teams, so they must not veto a misspelt
// matchup. Team-branded regional networks are deliberately absent: the "tv"
// tokens in broadcastAliases include words like "cardinals" and "rangers" that
// can equally be an opponent, and those keep their veto.
var descriptorTokens = map[string]struct{}{
	"league": {}, "pass": {}, "msg": {}, "msg2": {}, "sny": {}, "spectrum": {},
	"xfinity": {}, "directv": {}, "fubo": {}, "network": {}, "networks": {},
	"broadcast": {}, "stream": {}, "streaming": {}, "app": {}, "plus": {},
	"extra": {}, "alt": {}, "local": {}, "regional": {},
}

var negativeTokens = map[string]struct{}{
	"archive": {}, "classic": {}, "classics": {}, "movie": {}, "movies": {}, "ondemand": {},
	"ppv": {}, "raw": {}, "replay": {}, "series": {}, "vod": {},
}

// nonTeamChannelTokens name a subject other than a team. A single-team match
// can otherwise latch onto a nickname shared with a music, news or comedy
// channel. Matchup and EPG evidence are not affected by this list.
var nonTeamChannelTokens = map[string]struct{}{
	"comedy": {}, "documentary": {}, "documentaries": {}, "education": {},
	"entertainment": {}, "food": {}, "kids": {}, "music": {}, "news": {},
	"reality": {}, "religion": {}, "shopping": {}, "talk": {}, "travel": {},
	"weather": {},
}

// Built-in aliases are deliberately small and league-scoped, matching MediaStorm.
var builtInTeamAliases = map[string]map[string][]string{
	"mlb": {
		"arizona diamondbacks": {"d backs", "dbacks"},
		"boston red sox":       {"bosox", "redsox"},
		"chicago white sox":    {"whitesox"},
		"new york yankees":     {"yanks"},
		"st louis cardinals":   {"cards"},
		"toronto blue jays":    {"jays"},
	},
	"nfl": {
		"new england patriots": {"pats"},
		"philadelphia eagles":  {"philly"},
		"san francisco 49ers":  {"niners", "9ers"},
	},
	"nba": {
		"boston celtics":        {"celts"},
		"golden state warriors": {"dubs"},
		"philadelphia 76ers":    {"sixers"},
	},
	"nhl": {
		"colorado avalanche":  {"avs"},
		"detroit red wings":   {"wings"},
		"montreal canadiens":  {"habs"},
		"pittsburgh penguins": {"pens"},
		"tampa bay lightning": {"bolts"},
		"toronto maple leafs": {"leafs"},
		"washington capitals": {"caps"},
	},
}

var broadcastAliases = map[string][]string{
	"abc":                    {"abc", "abc network"},
	"cbs":                    {"cbs", "cbs network"},
	"fox":                    {"fox", "fox network"},
	"nbc":                    {"nbc", "nbc network"},
	"espn":                   {"espn", "espn hd"},
	"espn unlmtd":            {"espn unlmtd", "espn unlimited"},
	"netflix":                {"netflix"},
	"peacock":                {"peacock", "peacock sports"},
	"mlb tv":                 {"mlb tv", "mlbtv"},
	"yes":                    {"yes", "yes network"},
	"nesn":                   {"nesn", "new england sports network"},
	"masn":                   {"masn", "mid atlantic sports network"},
	"chsn":                   {"chsn", "chicago sports network"},
	"sportsnet la":           {"sportsnet la", "spectrum sportsnet la", "snla"},
	"sportsnet":              {"sportsnet"},
	"tva":                    {"tva", "tva sports"},
	"rangers sports network": {"rangers sports network"},
	"nbc sports ca":          {"nbc sports ca", "nbc sports california", "nbcsca"},
	"nbc sports phil":        {"nbc sports phil", "nbc sports philly", "nbc sports philadelphia", "nbcsp"},
	"angels tv":              {"angels tv", "angelstv"},
	"bravesvision":           {"bravesvision", "braves vision"},
	"brewers tv":             {"brewers tv", "brewertv"},
	"cardinals tv":           {"cardinals tv", "cardinalstv"},
	"cleguardians tv":        {"cleguardians tv", "cle guardians tv", "cleveland guardians tv", "guardians tv", "guardianstv"},
	"mariners tv":            {"mariners tv", "marinerstv"},
	"padres tv":              {"padres tv", "padrestv"},
	"rays tv":                {"rays tv", "raystv"},
	"rockies tv":             {"rockies tv", "rockiestv"},
	"royals tv":              {"royals tv", "royalstv"},
	"tigers tv":              {"tigers tv", "tigerstv"},
	"twins tv":               {"twins tv", "twinstv"},
}

// normalizeCache memoizes the expensive lower/fold pass per distinct input.
// Auto-link previews score every channel against every team, so the same
// channel name is normalized dozens of times.
var normalizeCache sync.Map

func normalize(value string) string {
	if cached, ok := normalizeCache.Load(value); ok {
		return cached.(string)
	}
	result := normalizeUncached(value)
	normalizeCache.Store(value, result)
	return result
}

// foldAccents handles the Latin accents sports providers actually use. It
// replaces MediaStorm's NFD dependency with an explicit table, so Wren TV adds
// no module for character normalization.
var accentFold = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a', 'ā': 'a', 'ă': 'a', 'ą': 'a',
	'ç': 'c', 'ć': 'c', 'č': 'c',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e', 'ē': 'e', 'ė': 'e', 'ę': 'e', 'ě': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i', 'ī': 'i', 'į': 'i',
	'ñ': 'n', 'ń': 'n', 'ň': 'n',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o', 'ō': 'o', 'ő': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ū': 'u', 'ů': 'u', 'ű': 'u',
	'ý': 'y', 'ÿ': 'y', 'š': 's', 'ś': 's', 'ž': 'z', 'ź': 'z', 'ż': 'z',
	'ł': 'l', 'ď': 'd', 'đ': 'd', 'ř': 'r', 'ť': 't', 'þ': 't', 'ß': 's',
}

func normalizeUncached(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	space := false
	for _, r := range value {
		if folded, ok := accentFold[r]; ok {
			r = folded
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func tokens(value string) []string {
	return strings.Fields(normalize(value))
}

func tokenSet(value string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, token := range tokens(value) {
		set[token] = struct{}{}
	}
	return set
}

func containsAllTokens(target string, wanted []string) bool {
	if len(wanted) == 0 {
		return false
	}
	set := tokenSet(target)
	for _, token := range wanted {
		if _, ok := set[token]; !ok {
			return false
		}
	}
	return true
}

func containsMatchupMarker(value string) bool {
	normed := " " + normalize(value) + " "
	return strings.Contains(normed, " vs ") || strings.Contains(normed, " versus ") ||
		strings.Contains(normed, " at ") || strings.Contains(value, "@")
}

// lifecycle recognizes provider-owned state prefixes only when they occupy
// their own leading segment, so "End | ..." counts but "end zone" does not.
func lifecycle(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	first := trimmed
	if index := strings.IndexAny(first, "|:-"); index >= 0 {
		first = first[:index]
	}
	switch normalize(first) {
	case "end", "ended", "finished", "off air", "offline":
		return lifecycleEnded
	case "live":
		return lifecycleLive
	default:
		return ""
	}
}

func matchSegments(value string) []string {
	if !strings.ContainsAny(value, "|:") {
		return []string{value}
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '|' || r == ':' })
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if normalize(part) == "" || lifecycle(part) != "" {
			continue
		}
		segments = append(segments, part)
	}
	if len(segments) == 0 {
		return []string{value}
	}
	return segments
}

func confidenceTier(score float64) string {
	if score >= StrongConfidence {
		return "strong"
	}
	return "possible"
}

func roundConfidence(score float64) float64 {
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return math.Round(score*100) / 100
}

func makeIdentity(league, name, location, nickname, abbreviation string) teamIdentity {
	name = strings.TrimSpace(name)
	location = strings.TrimSpace(location)
	nickname = strings.TrimSpace(nickname)

	// Older rows have no structured identity. Prefer known multi-word suffixes
	// before falling back to the final token.
	if location == "" || nickname == "" {
		normName := normalize(name)
		knownSuffixes := []string{"red sox", "white sox", "blue jays", "maple leafs", "red wings", "trail blazers"}
		for _, suffix := range knownSuffixes {
			if strings.HasSuffix(normName, suffix) {
				nickname = suffix
				location = strings.TrimSpace(strings.TrimSuffix(normName, suffix))
				break
			}
		}
		if nickname == "" {
			parts := strings.Fields(name)
			if len(parts) > 0 {
				nickname = parts[len(parts)-1]
				location = strings.Join(parts[:len(parts)-1], " ")
			}
		}
	}

	identity := teamIdentity{
		league:       strings.ToLower(strings.TrimSpace(league)),
		name:         name,
		location:     location,
		nickname:     nickname,
		abbreviation: abbreviation,
	}
	if aliases := builtInTeamAliases[identity.league][normalize(name)]; len(aliases) > 0 {
		identity.aliases = aliases
	}
	return identity
}

func identityFromTeam(team sports.SportsTeam, league string) teamIdentity {
	return makeIdentity(league, team.Name, team.Location, team.Nickname, team.Abbreviation)
}

func meaningfulExtraTokens(target string, matched []string) []string {
	matchedSet := make(map[string]struct{}, len(matched))
	for _, token := range matched {
		matchedSet[token] = struct{}{}
	}
	var extras []string
	for _, token := range tokens(target) {
		if _, ok := matchedSet[token]; ok {
			continue
		}
		if _, ok := noiseTokens[token]; ok {
			continue
		}
		allDigits := true
		for _, r := range token {
			if !unicode.IsDigit(r) {
				allDigits = false
				break
			}
		}
		if allDigits {
			continue
		}
		extras = append(extras, token)
	}
	return extras
}

func hasNegativeLabel(target string) bool {
	for token := range tokenSet(target) {
		if _, ok := negativeTokens[token]; ok {
			return true
		}
	}
	return false
}

func unknownParticipant(name string) bool {
	switch normalize(name) {
	case "", "tbd", "tba", "unknown", "to be determined", "to be announced":
		return true
	}
	return false
}

func scoreTextForTeam(target string, team teamIdentity, permanentLink bool) evidence {
	if unknownParticipant(team.name) {
		return evidence{}
	}
	normTarget := normalize(target)
	if normTarget == "" {
		return evidence{}
	}
	fullTokens := tokens(team.name)
	locationTokens := tokens(team.location)
	nicknameTokens := tokens(team.nickname)
	abbrTokens := tokens(team.abbreviation)

	if normTarget == normalize(team.name) {
		return evidence{score: 1, reason: "Exact team name", terms: fullTokens, team: team.name}
	}
	if containsAllTokens(target, fullTokens) {
		extras := meaningfulExtraTokens(target, fullTokens)
		score := 0.96
		if len(extras) == 1 {
			score = 0.92
		} else if len(extras) > 1 {
			score = 0.86
		}
		if permanentLink && containsMatchupMarker(target) {
			score = 0.58
		}
		if hasNegativeLabel(target) {
			score -= 0.18
		}
		return evidence{score: score, reason: "Full team name", terms: fullTokens, team: team.name}
	}

	for _, alias := range team.aliases {
		aliasTokens := tokens(alias)
		if containsAllTokens(target, aliasTokens) {
			score := 0.88
			if permanentLink && containsMatchupMarker(target) {
				score = 0.52
			}
			return evidence{score: score, reason: "Team alias", terms: aliasTokens, team: team.name}
		}
	}

	if containsAllTokens(target, nicknameTokens) {
		score := 0.82
		if len(nicknameTokens) == 1 && len(nicknameTokens[0]) < 4 {
			score = 0
		}
		if permanentLink && containsMatchupMarker(target) {
			score = 0.48
		}
		if hasNegativeLabel(target) {
			score -= 0.18
		}
		return evidence{score: score, reason: team.nickname + " nickname", terms: nicknameTokens, team: team.name}
	}

	// A three-letter code identifies a team only when it is the label's own
	// identity, optionally beside carriage noise ("BET HD"). A code buried in
	// another name ("BET Cinema") is a different brand that happens to share the
	// letters: it is not positive evidence and no guide may promote it.
	if len(abbrTokens) == 1 && len(abbrTokens[0]) >= 3 && containsAllTokens(target, abbrTokens) &&
		len(meaningfulExtraTokens(target, abbrTokens)) == 0 {
		score := 0.72
		if normTarget == abbrTokens[0] {
			score = 0.8
		}
		return evidence{score: score, reason: "Team abbreviation", terms: abbrTokens, team: team.name}
	}

	if containsAllTokens(target, locationTokens) {
		// A bare city is weak evidence. If the target has another meaningful
		// word it likely names a different team from the same market.
		if len(meaningfulExtraTokens(target, locationTokens)) > 0 {
			return evidence{}
		}
		return evidence{score: 0.42, reason: "Team location only", terms: locationTokens, team: team.name}
	}
	return evidence{}
}

func scoreMatchupSegment(target string, home, away teamIdentity) evidence {
	if unknownParticipant(home.name) || unknownParticipant(away.name) {
		return evidence{}
	}
	type matchupVariant struct {
		home   []string
		away   []string
		score  float64
		reason string
	}
	variants := []matchupVariant{
		{tokens(home.name), tokens(away.name), 0.98, "Both full team names"},
		{tokens(home.nickname), tokens(away.nickname), 0.9, "Both team nicknames"},
		{tokens(home.location), tokens(away.location), 0.86, "Both team locations"},
		{tokens(home.location), tokens(away.nickname), 0.84, "Location and opponent nickname"},
		{tokens(home.nickname), tokens(away.location), 0.84, "Nickname and opponent location"},
	}
	// Abbreviations require an explicit matchup marker and distinct whole tokens.
	homeAbbr, awayAbbr := tokens(home.abbreviation), tokens(away.abbreviation)
	if containsMatchupMarker(target) {
		if len(homeAbbr) == 1 && len(homeAbbr[0]) >= 2 {
			variants = append(variants, matchupVariant{homeAbbr, tokens(away.nickname), 0.86, "Team abbreviation and opponent nickname"})
		}
		if len(awayAbbr) == 1 && len(awayAbbr[0]) >= 2 {
			variants = append(variants, matchupVariant{tokens(home.nickname), awayAbbr, 0.86, "Team nickname and opponent abbreviation"})
		}
		if len(homeAbbr) == 1 && len(awayAbbr) == 1 && len(homeAbbr[0]) >= 2 && len(awayAbbr[0]) >= 2 && homeAbbr[0] != awayAbbr[0] {
			variants = append(variants, matchupVariant{homeAbbr, awayAbbr, 0.86, "Both team abbreviations"})
		}
	}
	for _, variant := range variants {
		if len(variant.home) == 0 || len(variant.away) == 0 {
			continue
		}
		if containsAllTokens(target, variant.home) && containsAllTokens(target, variant.away) {
			score := variant.score
			if containsMatchupMarker(target) {
				score = math.Min(1, score+0.04)
			}
			return evidence{
				score: score, reason: variant.reason,
				terms: append(append([]string{}, variant.home...), variant.away...), on: "matchup-name",
			}
		}
	}

	homeScore := scoreTextForTeam(target, home, false)
	awayScore := scoreTextForTeam(target, away, false)
	if homeScore.score > 0 && awayScore.score > 0 {
		score := math.Min(homeScore.score, awayScore.score)
		if containsMatchupMarker(target) {
			score = math.Min(1, score+0.08)
		}
		return evidence{
			score:  score,
			reason: "Both teams in matchup",
			terms:  append(append([]string{}, homeScore.terms...), awayScore.terms...),
			on:     "matchup-name",
		}
	}
	// A dedicated team channel is plausible; an explicit matchup with a
	// different opponent is not evidence for this event.
	if containsMatchupMarker(target) || home.league == "atp" || home.league == "wta" {
		return scoreFuzzyMatchup(target, home, away)
	}

	if homeScore.score >= awayScore.score && homeScore.score > 0 {
		homeScore.score = math.Min(homeScore.score, 0.74)
		homeScore.on = "team-name"
		return homeScore
	}
	if awayScore.score > 0 {
		awayScore.score = math.Min(awayScore.score, 0.74)
		awayScore.on = "team-name"
		return awayScore
	}
	return evidence{}
}

// scoreFuzzyMatchup allows exactly one side to differ by a single spelling
// error, and only in an explicit matchup. Two "close enough" sides could
// otherwise match an unrelated game.
func scoreFuzzyMatchup(target string, home, away teamIdentity) evidence {
	if !containsMatchupMarker(target) || hasNonLiveLabel(target) {
		return evidence{}
	}
	ts := tokens(strings.ReplaceAll(target, "@", " vs "))
	split := -1
	for i, token := range ts {
		if token == "vs" || token == "versus" || token == "at" {
			if split >= 0 {
				return evidence{}
			}
			split = i
		}
	}
	if split <= 0 || split >= len(ts)-1 {
		return evidence{}
	}
	matches := 0
	for _, pair := range [][2]teamIdentity{{home, away}, {away, home}} {
		left, le := fuzzySide(ts[:split], pair[0])
		right, re := fuzzySide(ts[split+1:], pair[1])
		if left && right && le+re == 1 {
			matches++
		}
	}
	if matches != 1 {
		return evidence{}
	}
	return evidence{
		score: .70, reason: "Possible matchup: one team-name spelling difference", on: "matchup-name",
		terms: append(tokens(home.nickname), tokens(away.nickname)...),
	}
}

// oneEdit reports whether a and b differ by exactly one insertion, deletion or
// substitution. Short tokens are excluded: "Pens" and "Penu" are different
// names, not spellings of one name, and a two-character code is one edit away
// from half the league. The bounds also keep the comparison cheap on a
// provider-supplied string.
func oneEdit(a, b string) bool {
	x, y := []rune(a), []rune(b)
	if len(x) < 5 || len(y) < 4 || len(x) > 64 || len(y) > 65 || len(x)-len(y) > 1 || len(y)-len(x) > 1 {
		return false
	}
	i, j, edits := 0, 0, 0
	for i < len(x) && j < len(y) {
		if x[i] == y[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		if len(x) >= len(y) {
			i++
		}
		if len(y) >= len(x) {
			j++
		}
	}
	if i < len(x) || j < len(y) {
		edits++
	}
	return edits == 1
}

// fuzzySide compares one side of an explicit matchup with a team. Only the
// nickname may be misspelt, at most one character, and the side may not carry a
// word the team cannot own: an extra opponent or competition name means this is
// a different programme, not a misspelling of this one. A provider descriptor
// (descriptorTokens) is not an opponent, so it does not count as such a word.
func fuzzySide(got []string, team teamIdentity) (bool, int) {
	nickname := tokens(team.nickname)
	if len(nickname) == 0 || len(got) > 24 {
		return false, 0
	}
	used := make([]bool, len(got))
	edits := 0
	for _, wanted := range nickname {
		found := -1
		for i, token := range got {
			if !used[i] && token == wanted {
				found = i
				break
			}
		}
		if found < 0 {
			// Two plausible corrections is ambiguous, so it is no evidence.
			for i, token := range got {
				if !used[i] && oneEdit(wanted, token) {
					if found >= 0 {
						return false, 0
					}
					found = i
				}
			}
			if found >= 0 {
				edits++
			}
		}
		if found < 0 || edits > 1 {
			return false, 0
		}
		used[found] = true
	}
	allowed := make(map[string]struct{})
	for _, token := range tokens(team.name + " " + team.location + " " + team.abbreviation) {
		allowed[token] = struct{}{}
	}
	for i, token := range got {
		if used[i] {
			continue
		}
		if _, ok := allowed[token]; ok {
			continue
		}
		if _, noise := noiseTokens[token]; noise {
			continue
		}
		if _, descriptor := descriptorTokens[token]; descriptor {
			continue
		}
		return false, 0
	}
	return true, edits
}

func scoreMatchupText(target string, home, away teamIdentity) evidence {
	best := evidence{}
	for _, segment := range matchSegments(target) {
		candidate := scoreMatchupSegment(segment, home, away)
		if candidate.score > best.score {
			best = candidate
		}
	}
	best.lifecycle = lifecycle(target)
	return best
}

// Guide titles and descriptions are arbitrary prose: one team token (especially
// CAR, NO or a nickname like Panthers) does not identify the scheduled game.
// Require both opponents in a single guide segment, and evaluate every segment
// so a single-team segment cannot mask a valid fuzzy matchup.
func scoreProgramMatchup(target string, home, away teamIdentity) evidence {
	best := evidence{}
	for _, segment := range matchSegments(target) {
		candidate := scoreMatchupSegment(segment, home, away)
		if candidate.on == "matchup-name" && candidate.score > best.score {
			best = candidate
		}
	}
	return best
}

func canonicalBroadcastVariants(value string) [][]string {
	normed := normalize(value)
	canonical := normed
	for key, values := range broadcastAliases {
		if key == normed {
			canonical = key
			break
		}
		for _, alias := range values {
			if normalize(alias) == normed {
				canonical = key
				break
			}
		}
	}
	aliases := append([]string{}, broadcastAliases[canonical]...)
	if len(aliases) == 0 {
		aliases = []string{value}
	}
	if strings.HasSuffix(normed, " tv") {
		aliases = append(aliases, normed, strings.ReplaceAll(normed, " ", ""))
	}
	seen := make(map[string]struct{})
	var variants [][]string
	for _, alias := range aliases {
		ts := tokens(alias)
		key := strings.Join(ts, " ")
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		variants = append(variants, ts)
	}
	return variants
}

func scoreBroadcastText(target, broadcast string) evidence {
	state := lifecycle(target)
	if state == lifecycleEnded {
		return evidence{lifecycle: state}
	}
	normTarget := normalize(target)
	best := evidence{}
	for _, ts := range canonicalBroadcastVariants(broadcast) {
		if !containsAllTokens(target, ts) {
			continue
		}
		extraCount := len(meaningfulExtraTokens(target, ts))
		score := 0.76
		if normTarget == strings.Join(ts, " ") {
			score = 0.98
		} else if extraCount == 0 {
			score = 0.94
		} else if extraCount == 1 {
			score = 0.86
		} else if extraCount >= 3 {
			score = 0.62
		}
		if hasNegativeLabel(target) {
			score -= 0.18
		}
		if score > best.score {
			reason := broadcast + " channel name"
			if score >= 0.88 {
				reason = "Exact " + broadcast + " network"
			}
			best = evidence{score: score, reason: reason, terms: ts, on: "broadcast", lifecycle: state}
		}
	}
	return best
}

func hasNonLiveLabel(value string) bool {
	// Nuvio uses a rewind prefix for replay catalog entries without "Replay".
	if strings.HasPrefix(strings.TrimSpace(value), "⏪") {
		return true
	}
	for token := range tokenSet(value) {
		switch token {
		case "replay", "archive", "classic", "classics", "movie", "movies", "ondemand", "vod":
			return true
		}
	}
	return false
}

var monthTokens = map[string]struct{}{
	"jan": {}, "january": {}, "feb": {}, "february": {}, "mar": {}, "march": {},
	"apr": {}, "april": {}, "may": {}, "jun": {}, "june": {}, "jul": {}, "july": {},
	"aug": {}, "august": {}, "sep": {}, "sept": {}, "september": {}, "oct": {}, "october": {},
	"nov": {}, "november": {}, "dec": {}, "december": {},
}

func hasDatedLabel(value string) bool {
	for _, token := range tokens(value) {
		if _, month := monthTokens[token]; month {
			return true
		}
		if len(token) == 8 || len(token) == 6 || len(token) == 4 {
			allDigits := true
			for _, r := range token {
				if !unicode.IsDigit(r) {
					allDigits = false
					break
				}
			}
			if allDigits && len(token) >= 4 {
				return true
			}
		}
	}
	return false
}

func hasUnrelatedTeamLabel(channel Channel) bool {
	for _, value := range []string{channel.Name, channel.TvgName, channel.Group} {
		for _, token := range tokens(value) {
			if _, unrelated := nonTeamChannelTokens[token]; unrelated {
				return true
			}
		}
	}
	return false
}

func permanentAutoLinkChannel(channel Channel) bool {
	for _, value := range []string{channel.Name, channel.TvgName} {
		if lifecycle(value) != "" || containsMatchupMarker(value) || hasNegativeLabel(value) || hasDatedLabel(value) {
			return false
		}
	}
	return true
}

func explicitChannelQueryMatches(channel Channel, query string) bool {
	ts := tokens(query)
	return containsAllTokens(channel.Name, ts) || containsAllTokens(channel.TvgName, ts) || containsAllTokens(channel.TvgID, ts)
}

func candidateFromEvidence(channel Channel, ev evidence) sports.SportsChannelCandidate {
	score := roundConfidence(ev.score)
	return sports.SportsChannelCandidate{
		ChannelID: channel.ID, ChannelName: channel.Name, ChannelLogo: channel.Logo,
		ChannelTvgID: channel.TvgID, SourceID: channel.SourceID, SourceName: channel.SourceName,
		Group: channel.Group, Confidence: score, ConfidenceTier: confidenceTier(score),
		MatchReason: ev.reason, MatchedTerms: ev.terms,
	}
}

// RankTeamCandidates ports MediaStorm's Manage Team Channels ranking. An
// explicit query bypasses scoring and keeps every text match for manual choice.
func RankTeamCandidates(team sports.SportsTeam, channels []Channel, query string, limit int) []sports.SportsChannelCandidate {
	identity := identityFromTeam(team, "")
	return rankTeamCandidates(identity, team.Name, channels, query, limit)
}

func rankTeamCandidates(identity teamIdentity, teamName string, channels []Channel, query string, limit int) []sports.SportsChannelCandidate {
	explicit := strings.TrimSpace(query) != ""
	results := make([]sports.SportsChannelCandidate, 0)
	for _, channel := range channels {
		ended := lifecycle(channel.Name) == lifecycleEnded || lifecycle(channel.TvgName) == lifecycleEnded
		if ended && !explicit {
			continue
		}
		if explicit && !explicitChannelQueryMatches(channel, query) {
			continue
		}
		ev := scoreTextForTeam(channel.Name, identity, true)
		if ev.score <= 0 && channel.TvgName != "" {
			ev = scoreTextForTeam(channel.TvgName, identity, true)
		}
		if ev.score <= 0 && !explicit {
			continue
		}
		if ev.score <= 0 {
			ev = evidence{score: 0, reason: "Manual text match", terms: tokens(query), team: teamName}
		}
		if ended {
			ev.score = math.Min(ev.score, 0.2)
			ev.reason = "Ended/offline feed"
			ev.lifecycle = lifecycleEnded
		}
		results = append(results, candidateFromEvidence(channel, ev))
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Confidence != results[j].Confidence {
			return results[i].Confidence > results[j].Confidence
		}
		if results[i].ChannelName != results[j].ChannelName {
			return strings.ToLower(results[i].ChannelName) < strings.ToLower(results[j].ChannelName)
		}
		return results[i].SourceName < results[j].SourceName
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

// AutoLinkCandidates returns only permanent channel-brand matches: no matchup
// names, dated labels, ended feeds, city-only evidence or duplicate brands.
func AutoLinkCandidates(team sports.SportsTeam, league string, channels []Channel) []sports.SportsChannelCandidate {
	identity := identityFromTeam(team, league)
	ranked := rankTeamCandidates(identity, team.Name, channels, "", 0)
	byKey := make(map[string]Channel, len(channels))
	for _, channel := range channels {
		byKey[channel.SourceID+":"+channel.ID] = channel
	}
	results := make([]sports.SportsChannelCandidate, 0, len(ranked))
	seenBrands := make(map[string]struct{})
	for _, candidate := range ranked {
		channel, ok := byKey[candidate.SourceID+":"+candidate.ChannelID]
		if !ok || !permanentAutoLinkChannel(channel) {
			continue
		}
		if candidate.MatchReason == "Team location only" || candidate.MatchReason == "Manual text match" {
			continue
		}
		brand := normalize(candidate.ChannelName)
		if brand == "" {
			brand = candidate.SourceID + ":" + candidate.ChannelID
		}
		if _, exists := seenBrands[brand]; exists {
			continue
		}
		seenBrands[brand] = struct{}{}
		results = append(results, candidate)
	}
	return results
}
