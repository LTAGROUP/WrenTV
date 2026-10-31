package media

import (
	"regexp"
	"strings"
)

// Range is a span of a file in seconds.
type Range struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Markers are the skippable parts of a file. Source says where they came
// from: "chapters", "fingerprint" or "community". Recap ("previously on")
// is separate from the intro, which can come before or after it.
type Markers struct {
	Recap   *Range `json:"recap,omitempty"`
	Intro   *Range `json:"intro,omitempty"`
	Credits *Range `json:"credits,omitempty"`
	Source  string `json:"source,omitempty"`
}

func (m Markers) Empty() bool { return m.Recap == nil && m.Intro == nil && m.Credits == nil }

var (
	introRe   = regexp.MustCompile(`^(intro(duction)?|opening( credits| theme| titles?| song)?|op\s*\d*|title sequence|main titles?|theme( song)?)$`)
	recapRe   = regexp.MustCompile(`^(recap|previously( on.*)?|last time( on.*)?|(the )?story so far)$`)
	creditsRe = regexp.MustCompile(`^(credits|end(ing)? credits|closing( credits| theme)?|ending( theme| song)?|ed\s*\d*|outro|end titles?)$`)
	// "Chapter 3", "03", "Kapitel 3": unnamed chapters carry no meaning.
	trimRe = regexp.MustCompile(`^[\s\-–—:#.\d]+|[\s\-–—:#.]+$`)
)

func normTitle(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	t = strings.NewReplacer("_", " ", "'", "", "’", "").Replace(t)
	return trimRe.ReplaceAllString(t, "")
}

// MarkersFromChapters finds the recap, intro and credits from chapter names
// ("Recap", "Previously On", "Intro", "Opening", "OP", "Credits", "ED", ...).
// A recap or intro must start in the first half of the file and last
// 5 s–5 min; credits must start in the second half.
func MarkersFromChapters(chapters []Chapter, duration float64) Markers {
	var m Markers
	if duration <= 0 && len(chapters) > 0 {
		duration = chapters[len(chapters)-1].End
	}
	for i, c := range chapters {
		end := c.End
		if end <= c.Start {
			if i+1 < len(chapters) {
				end = chapters[i+1].Start
			} else {
				end = duration
			}
		}
		if end <= c.Start {
			continue
		}
		t := normTitle(c.Title)
		switch {
		case m.Recap == nil && recapRe.MatchString(t):
			if l := end - c.Start; l >= 5 && l <= 300 && c.Start < duration/2 {
				m.Recap = &Range{c.Start, end}
			}
		case m.Intro == nil && introRe.MatchString(t):
			if l := end - c.Start; l >= 5 && l <= 300 && c.Start < duration/2 {
				m.Intro = &Range{c.Start, end}
			}
		case m.Credits == nil && creditsRe.MatchString(t):
			if c.Start >= duration/2 {
				m.Credits = &Range{c.Start, end}
			}
		}
	}
	if !m.Empty() {
		m.Source = "chapters"
	}
	return m
}
