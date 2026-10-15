package media

import "testing"

func TestMarkersFromChapters(t *testing.T) {
	ch := func(s, e float64, title string) Chapter { return Chapter{Start: s, End: e, Title: title} }
	cases := []struct {
		name          string
		chapters      []Chapter
		duration      float64
		intro, credit *Range
	}{
		{"anime OP/ED", []Chapter{ch(0, 90, "Prologue"), ch(90, 180, "OP"), ch(180, 1300, "Part A"), ch(1300, 1390, "ED"), ch(1390, 1420, "Preview")}, 1420,
			&Range{90, 180}, &Range{1300, 1390}},
		{"named intro and credits", []Chapter{ch(0, 60, "Intro"), ch(60, 2500, "Chapter 2"), ch(2500, 2600, "End Credits")}, 2600,
			&Range{0, 60}, &Range{2500, 2600}},
		{"opening credits", []Chapter{ch(0, 120, "Cold Open"), ch(120, 170, "Opening Credits"), ch(170, 3000, "Main")}, 3000,
			&Range{120, 170}, nil},
		{"numbered chapters only", []Chapter{ch(0, 600, "Chapter 1"), ch(600, 1200, "Chapter 2")}, 1200, nil, nil},
		{"intro too long", []Chapter{ch(0, 900, "Intro"), ch(900, 3000, "Main")}, 3000, nil, nil},
		{"credits early in file ignored", []Chapter{ch(0, 100, "Credits"), ch(100, 3000, "Main")}, 3000, nil, nil},
		{"missing end times", []Chapter{ch(0, 0, "Recap"), ch(30, 0, "Opening"), ch(85, 0, "Episode"), ch(1250, 0, "Ending")}, 1320,
			&Range{30, 85}, &Range{1250, 1320}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := MarkersFromChapters(c.chapters, c.duration)
			if !eqRange(m.Intro, c.intro) || !eqRange(m.Credits, c.credit) {
				t.Fatalf("got intro=%v credits=%v, want %v %v", m.Intro, m.Credits, c.intro, c.credit)
			}
			if !m.Empty() && m.Source != "chapters" {
				t.Fatalf("source = %q", m.Source)
			}
		})
	}
}

func eqRange(a, b *Range) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
