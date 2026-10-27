package onlinesubs

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Cue is one subtitle, in seconds.
type Cue struct {
	Start, End float64
	Text       string
}

// ParseVTT reads a WebVTT file's cues. A file still being written may end
// mid-cue, so its last block is left for the next read unless complete.
func ParseVTT(text string, complete bool) []Cue {
	blocks := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n\n")
	if !complete && len(blocks) > 0 {
		blocks = blocks[:len(blocks)-1]
	}
	var out []Cue
	for _, blk := range blocks {
		lines := strings.Split(strings.Trim(blk, "\n"), "\n")
		for i, l := range lines {
			a, z, ok := strings.Cut(l, "-->")
			if !ok {
				continue
			}
			start, ok1 := parseTime(a)
			end, ok2 := parseTime(strings.Fields(z + " x")[0])
			body := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			if ok1 && ok2 && body != "" && end > start {
				out = append(out, Cue{start, end, body})
			}
			break
		}
	}
	return out
}

func parseTime(s string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	t := 0.0
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.Replace(p, ",", ".", 1), 64)
		if err != nil {
			return 0, false
		}
		t = t*60 + v
	}
	return t, true
}

// WriteCue is one cue as WebVTT, blank line included.
func WriteCue(c Cue) string {
	return vttTime(c.Start) + " --> " + vttTime(c.End) + "\n" + c.Text + "\n\n"
}

func vttTime(t float64) string {
	ms := max(int64(math.Round(t*1000)), 0)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// A subtitle made for another release of the same title is usually off by a
// steady amount within each stretch of it, with jumps where the two cuts
// differ (a recap trimmed, a scene longer). Aligning finds those stretches
// against a reference timed for the file (its own subtitle track): by
// matching words when the two share a language, else by where speech is.

// Reach is how far either side of a cue its counterpart is looked for, and
// how far past a cue the reference has to have been read before the cue's
// offset is settled.
const Reach = 120.0

// settle is how far past a line's match the reference has to have been read
// for the match to count.
const settle = 30.0

type anchor struct{ at, off float64 }

// stretch is a run of the subtitle that one offset fits.
type stretch struct{ from, to, off float64 }

// Alignment says how to move a subtitle's cues onto the file's timing.
type Alignment struct {
	stretches []stretch
}

// OK reports whether enough of the subtitle matched the reference to trust.
func (a Alignment) OK() bool { return len(a.stretches) > 0 }

// String lists the stretches, for logs: "0s+0.07 375s+7.20".
func (a Alignment) String() string {
	parts := make([]string, len(a.stretches))
	for i, s := range a.stretches {
		parts[i] = fmt.Sprintf("%.0fs%+.2f", s.from, s.off)
	}
	return strings.Join(parts, " ")
}

// Offset is the shift for a cue that starts at t.
func (a Alignment) Offset(t float64) float64 {
	ss := a.stretches
	if len(ss) == 0 {
		return 0
	}
	for i, s := range ss {
		if t < s.from {
			if i == 0 {
				return s.off
			}
			// Between two stretches: where the cut changes is unknown, so
			// the nearer one.
			if t-ss[i-1].to < s.from-t {
				return ss[i-1].off
			}
			return s.off
		}
		if t <= s.to {
			return s.off
		}
	}
	return ss[len(ss)-1].off
}

// Apply moves cues by the alignment.
func (a Alignment) Apply(cues []Cue) []Cue {
	out := make([]Cue, 0, len(cues))
	for _, c := range cues {
		off := a.Offset(c.Start)
		c.Start, c.End = c.Start+off, c.End+off
		if c.End > 0 {
			c.Start = max(c.Start, 0)
			out = append(out, c)
		}
	}
	return out
}

// Align fits sub onto ref. refEnd is how far into the file ref has been read
// (math.Inf(1) once it's whole); only cues it covers are matched. sameLang
// says the two can be matched by their words.
func Align(sub, ref []Cue, sameLang bool, refEnd float64) Alignment {
	// Lines that say the same thing are the surer guide, and need little of
	// the reference: four in a row that agree are a stretch.
	if sameLang {
		anchors := textAnchors(sub, ref, refEnd)
		if ss := stretches(anchors, 4); len(ss) > 0 || len(anchors) >= 12 {
			return Alignment{stretches: ss}
		}
	}
	return Alignment{stretches: stretches(timingAnchors(sub, ref, refEnd), 1)}
}

// textAnchors pairs subtitle cues with reference cues that say the same
// thing. Releases split lines differently, so a cue matches when most of
// its words are in one reference cue.
func textAnchors(sub, ref []Cue, refEnd float64) []anchor {
	refWords := make([]map[string]bool, len(ref))
	for i, r := range ref {
		refWords[i] = wordSet(r.Text)
	}
	var out []anchor
	lo := 0
	for _, s := range sub {
		if s.Start-Reach > refEnd {
			break
		}
		words := wordSet(s.Text)
		if len(words) < 3 {
			continue
		}
		for lo < len(ref) && ref[lo].Start < s.Start-Reach {
			lo++
		}
		best, second, bestAt := 0.0, 0.0, 0.0
		for j := lo; j < len(ref) && ref[j].Start <= s.Start+Reach; j++ {
			n := 0
			for w := range words {
				if refWords[j][w] {
					n++
				}
			}
			score := float64(n) / float64(len(words))
			switch {
			case score > best:
				// A repeat of the same line nearby isn't a rival.
				if math.Abs(ref[j].Start-bestAt) > 2 {
					second = best
				}
				best, bestAt = score, ref[j].Start
			case score > second && math.Abs(ref[j].Start-bestAt) > 2:
				second = score
			}
		}
		// With the reference read only partway, a match counts once it's
		// read a little past it: a rival later on is unlikely, and a stray
		// has to agree with its neighbours to count.
		if best >= 0.6 && best-second >= 0.25 && (s.Start+Reach <= refEnd || bestAt+settle <= refEnd) {
			out = append(out, anchor{s.Start, bestAt - s.Start})
		}
	}
	return out
}

func wordSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(stripTags(text)), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 2 {
			out[w] = true
		}
	}
	return out
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// timingAnchors works without words: for each few minutes of the subtitle,
// the shift that lands the most of its cue starts on the reference's. Starts
// are sharp where coverage isn't (dialogue fills most of any minute).
func timingAnchors(sub, ref []Cue, refEnd float64) []anchor {
	const bin, tol, window, step = 0.1, 2, 300.0, 100.0 // tol in bins either side
	if len(sub) == 0 || len(ref) == 0 {
		return nil
	}
	last := math.Min(ref[len(ref)-1].End, refEnd)
	refStarts := make([]bool, int(last/bin)+tol+1)
	for _, r := range ref {
		if r.Start >= last {
			break
		}
		for d := -tol; d <= tol; d++ {
			if j := int(r.Start/bin) + d; j >= 0 {
				refStarts[j] = true
			}
		}
	}
	maxShift := int(Reach / bin)
	var out []anchor
	for from := 0.0; from+window/2 < sub[len(sub)-1].End; from += step {
		to := from + window
		if to+Reach > refEnd {
			break
		}
		var starts []int
		for _, s := range sub {
			if s.Start >= from && s.Start < to {
				starts = append(starts, int(s.Start/bin))
			}
		}
		if len(starts) < 20 {
			continue
		}
		scores := make([]int, 2*maxShift+1)
		for k := range scores {
			shift := k - maxShift
			for _, b := range starts {
				if j := b + shift; j >= 0 && j < len(refStarts) && refStarts[j] {
					scores[k]++
				}
			}
		}
		bestK := 0
		for k, v := range scores {
			if v > scores[bestK] {
				bestK = k
			}
		}
		// It has to stand out from every shift more than a second away (in
		// dense dialogue a third of starts land on something at any shift).
		rival := 0
		for k, v := range scores {
			if abs(k-bestK) > int(1/bin) && v > rival {
				rival = v
			}
		}
		best := scores[bestK]

		if float64(best) >= 0.35*float64(len(starts)) && float64(best) >= 1.35*float64(rival) {
			// The middle of the run of shifts that tie for best.
			lo, hi := bestK, bestK
			for lo > 0 && scores[lo-1] == best {
				lo--
			}
			for hi < len(scores)-1 && scores[hi+1] == best {
				hi++
			}
			out = append(out, anchor{from + window/2, float64((lo+hi)/2-maxShift) * bin})
		}
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// stretches groups anchors into runs that share an offset, dropping strays
// and runs too short to trust.
func stretches(anchors []anchor, minRun int) []stretch {
	if len(anchors) == 0 {
		return nil
	}
	// A stray (a line matched to the wrong one) agrees with none of the
	// anchors either side of it; real ones come in runs.
	var kept []anchor
	for i, a := range anchors {
		for j := max(0, i-2); j < min(len(anchors), i+3); j++ {
			if j != i && math.Abs(anchors[j].off-a.off) <= 0.5 {
				kept = append(kept, a)
				break
			}
		}
	}
	var out []stretch
	var run []anchor
	flush := func() {
		if len(run) >= minRun {
			offs := make([]float64, len(run))
			for i, a := range run {
				offs[i] = a.off
			}
			out = append(out, stretch{run[0].at, run[len(run)-1].at, median(offs)})
		}
		run = nil
	}
	for _, a := range kept {
		if len(run) > 0 {
			offs := make([]float64, len(run))
			for i, r := range run {
				offs[i] = r.off
			}
			if math.Abs(a.off-median(offs)) > 0.5 {
				flush()
			}
		}
		run = append(run, a)
	}
	flush()
	// Neighbouring runs within half a second are one stretch: the
	// difference is how each release times its lines, not a cut.
	var merged []stretch
	for _, s := range out {
		if n := len(merged); n > 0 && math.Abs(merged[n-1].off-s.off) <= 0.5 {
			m := &merged[n-1]
			a, b := math.Max(m.to-m.from, 1), math.Max(s.to-s.from, 1)
			m.off = (m.off*a + s.off*b) / (a + b)
			m.to = s.to
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
