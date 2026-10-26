package match

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

var resolutionLabel = regexp.MustCompile(`(?i)\b(4320[pi]|2160[pi]|1080[pi]|720[pi]|480[pi]|8k|4k|uhd|fhd)\b`)
var bitrateLabel = regexp.MustCompile(`(?i)\b([0-9]+(?:\.[0-9]+)?)\s*(mbps|kbps)\b`)

// ReportedQuality interprets only explicit labels. No stream or network
// probing occurs, and conflicting labels use the lower advertised resolution
// rather than overclaiming.
func ReportedQuality(label string) *sports.SportsReportedQuality {
	height := 0
	for _, token := range resolutionLabel.FindAllString(strings.ToLower(label), -1) {
		value := 0
		switch token {
		case "8k":
			value = 4320
		case "4k", "uhd":
			value = 2160
		case "fhd":
			value = 1080
		default:
			value, _ = strconv.Atoi(strings.TrimRight(token, "pi"))
		}
		if height == 0 || value < height {
			height = value
		}
	}
	var bitrate int64
	for _, m := range bitrateLabel.FindAllStringSubmatch(label, -1) {
		value, _ := strconv.ParseFloat(m[1], 64)
		factor := float64(1000)
		if strings.EqualFold(m[2], "mbps") {
			factor = 1000000
		}
		b := int64(value * factor)
		if b >= 10000 && b <= 1000000000 && (bitrate == 0 || b < bitrate) {
			bitrate = b
		}
	}
	if height == 0 && bitrate == 0 {
		return nil
	}
	return &sports.SportsReportedQuality{ResolutionHeight: height, BitrateBps: bitrate, Origin: "label"}
}

// CompareQuality orders known values before unknown ones, then higher
// resolution, then higher bitrate, giving sorting a deterministic total order.
func CompareQuality(a, b *sports.SportsReportedQuality) int {
	var ah, bh int
	var ab, bb int64
	if a != nil {
		ah, ab = a.ResolutionHeight, a.BitrateBps
	}
	if b != nil {
		bh, bb = b.ResolutionHeight, b.BitrateBps
	}
	if ah > bh {
		return -1
	}
	if ah < bh {
		return 1
	}
	if ab > bb {
		return -1
	}
	if ab < bb {
		return 1
	}
	return 0
}

func sortStreamMatches(matches []sports.SportsStreamMatch) {
	for i := range matches {
		matches[i].ReportedQuality = ReportedQuality(matches[i].ChannelName)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Confidence != matches[j].Confidence {
			return matches[i].Confidence > matches[j].Confidence
		}
		if order := CompareQuality(matches[i].ReportedQuality, matches[j].ReportedQuality); order != 0 {
			return order < 0
		}
		if matches[i].ChannelName != matches[j].ChannelName {
			return strings.ToLower(matches[i].ChannelName) < strings.ToLower(matches[j].ChannelName)
		}
		return matches[i].SourceName < matches[j].SourceName
	})
}

func canonicalGroupKey(m sports.SportsStreamMatch) string {
	value := m.ChannelName
	if m.ProgramTitle != "" {
		value = m.ProgramTitle
	}
	segments := matchSegments(value)
	if len(segments) > 0 {
		value = strings.Join(segments, " ")
	}
	kept := make([]string, 0)
	for _, token := range tokens(value) {
		if _, noise := noiseTokens[token]; noise {
			continue
		}
		kept = append(kept, token)
	}
	key := strings.Join(kept, " ")
	if key == "" {
		key = normalize(m.ChannelName)
	}
	if key == "" {
		key = m.SourceID + ":" + m.ChannelID
	}
	return key
}

// BuildStreamGroups removes exact duplicate stream identities, then collapses
// equivalent display/program titles while retaining distinct feeds as
// alternatives. A possible feed never inherits a strong group's tier.
func BuildStreamGroups(matches []sports.SportsStreamMatch) ([]sports.SportsStreamMatch, []sports.SportsStreamGroup) {
	unique := make([]sports.SportsStreamMatch, 0, len(matches))
	seen := make(map[string]struct{})
	for _, m := range matches {
		identity := strings.TrimSpace(m.ChannelURL)
		if identity == "" {
			identity = m.SourceID + ":" + m.ChannelID
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		unique = append(unique, m)
	}

	groups := make([]sports.SportsStreamGroup, 0)
	byKey := make(map[string]int)
	for _, m := range unique {
		key := canonicalGroupKey(m)
		if m.ConfidenceTier != "strong" {
			key += ":possible"
		}
		if index, exists := byKey[key]; exists {
			groups[index].Alternatives = append(groups[index].Alternatives, m)
			groups[index].FeedCount++
			continue
		}
		byKey[key] = len(groups)
		display := m.ProgramTitle
		if display == "" {
			display = m.ChannelName
		}
		groups = append(groups, sports.SportsStreamGroup{
			GroupKey: key, DisplayName: display, Confidence: m.Confidence,
			ConfidenceTier: m.ConfidenceTier, MatchReason: m.MatchReason,
			FeedCount: 1, Primary: m, Alternatives: []sports.SportsStreamMatch{},
		})
	}
	return unique, groups
}

// Selectable keeps plausible candidates for explicit selection; a city or a
// generic league is not enough, and non-live/archived feeds are removed.
func Selectable(matches []sports.SportsStreamMatch) []sports.SportsStreamMatch {
	selected := make([]sports.SportsStreamMatch, 0, len(matches))
	for _, m := range matches {
		if m.Confidence >= 0.65 && strings.TrimSpace(m.ChannelURL) != "" &&
			!hasNonLiveLabel(m.ChannelName) && m.LifecycleState != lifecycleEnded {
			selected = append(selected, m)
		}
	}
	return selected
}

func clampMatch(score float64) float64 {
	return math.Min(score, 1)
}
