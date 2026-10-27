package onlinesubs

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
)

// cut is how a second release differs from the file: from at on, its
// lines come off seconds earlier.
type cut struct{ at, off float64 }

var cuts = []cut{{0, 0}, {400, 7.2}, {900, 14.1}, {1350, 20.9}, {1800, 28}, {2100, 34.9}}

func offAt(t float64) float64 {
	off := 0.0
	for _, c := range cuts {
		if t >= c.at {
			off = c.off
		}
	}
	return off
}

var words = strings.Fields("the a devil detective case night blade angel mother father brother city club piano drink truth lie gun body " +
	"murder knife trust deal favor heaven hell sister doctor police station car house party dance song wine money " +
	"secret plan fire love hate fear hope dream sleep wake run hide find lose win wait call tell ask")

// episode is a reference track, and the same lines as another release has
// them: shifted per cut, a little jitter, some lines split or missing.
func episode(seed int64) (ref, sub []Cue) {
	r := rand.New(rand.NewSource(seed))
	for t := 2.0; t < 2600; t += 1.5 + r.Float64()*4 {
		n := 4 + r.Intn(6)
		line := make([]string, n)
		for i := range line {
			line[i] = words[r.Intn(len(words))]
		}
		d := 1 + r.Float64()*2.5
		ref = append(ref, Cue{t, t + d, strings.Join(line, " ")})
		if r.Float64() < 0.1 {
			continue // not in the other release
		}
		st := t - offAt(t) + (r.Float64()-0.5)*0.3
		if r.Float64() < 0.15 { // split in two
			sub = append(sub, Cue{st, st + d/2, strings.Join(line[:n/2], " ")}, Cue{st + d/2, st + d, strings.Join(line[n/2:], " ")})
			t += d
			continue
		}
		sub = append(sub, Cue{st, st + d, strings.Join(line, " ")})
		t += d
	}
	return ref, sub
}

func TestAlignFindsCuts(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprint("sameLang=", same), func(t *testing.T) {
			ref, sub := episode(1)
			a := Align(sub, ref, same, math.Inf(1))
			if !a.OK() {
				t.Fatal("no alignment")
			}
			// Well inside each stretch (timing works in five-minute windows,
			// so it can't place a cut closely).
			for i, c := range cuts {
				at := c.at - c.off + 60
				if i+1 < len(cuts) && !same {
					at = (c.at + cuts[i+1].at) / 2
				}
				if got := a.Offset(at); math.Abs(got-c.off) > 0.4 {
					t.Errorf("at %.0f: offset %.2f, want %.2f (%s)", at, got, c.off, a)
				}
			}
			// Applied, most cues land on their reference line.
			out := a.Apply(sub)
			good := 0
			for _, c := range out {
				for _, r := range ref {
					if math.Abs(r.Start-c.Start) < 0.5 || math.Abs(r.Start+(r.End-r.Start)/2-c.Start) < 0.5 {
						good++
						break
					}
				}
			}
			if frac := float64(good) / float64(len(out)); frac < 0.85 {
				t.Errorf("%.0f%% of cues on their line", frac*100)
			}
		})
	}
}

func TestAlignPartialReference(t *testing.T) {
	ref, sub := episode(2)
	var head []Cue
	for _, r := range ref {
		if r.Start < 1000 {
			head = append(head, r)
		}
	}
	a := Align(sub, head, true, 1000)
	if !a.OK() {
		t.Fatal("no alignment from the first part")
	}
	if last := a.stretches[len(a.stretches)-1]; last.to > 1000-Reach {
		t.Errorf("matched past what was read: %s", a)
	}
	if got := a.Offset(600); math.Abs(got-7.2) > 0.4 {
		t.Errorf("offset %.2f at 600", got)
	}
}

func TestAlignUnrelated(t *testing.T) {
	ref, _ := episode(3)
	_, other := episode(4) // another episode's lines and timing
	for _, same := range []bool{true, false} {
		if a := Align(other, ref, same, math.Inf(1)); a.OK() {
			t.Errorf("sameLang=%v: aligned to an unrelated track: %s", same, a)
		}
	}
}

func TestParseVTTAndWriteCue(t *testing.T) {
	text := "WEBVTT\n\n00:00:01.500 --> 00:00:03.000 align:start\nHello\nthere\n\n1:02:03.004 --> 1:02:04.000\nLater\n\n00:00:09.000 --> 00:00:1"
	cs := ParseVTT(text, false)
	if len(cs) != 2 || cs[0].Text != "Hello\nthere" || cs[1].Start != 3723.004 {
		t.Fatalf("%+v", cs)
	}
	if got := WriteCue(Cue{3723.004, 3724, "Later"}); got != "01:02:03.004 --> 01:02:04.000\nLater\n\n" {
		t.Fatalf("%q", got)
	}
}
