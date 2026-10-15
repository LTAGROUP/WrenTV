package markers

import (
	"math"
	"math/rand"
	"testing"
)

// music makes sec seconds of plucked sawtooth chords (harmonics and decaying
// notes, like real music), a stand-in for a theme song.
func music(r *rand.Rand, sec float64) []float64 {
	out := make([]float64, int(sec*SampleRate))
	for i := 0; i < len(out); {
		n := int((0.15 + r.Float64()*0.35) * SampleRate)
		freqs := []float64{110 + r.Float64()*440, 110 + r.Float64()*440, 110 + r.Float64()*440}
		for k := 0; k < n && i < len(out); k, i = k+1, i+1 {
			t := float64(k) / SampleRate
			var v float64
			for _, f := range freqs {
				v += 2*math.Mod(f*t, 1) - 1 // sawtooth: all harmonics
			}
			out[i] = v * 4000 * math.Exp(-3*t)
		}
	}
	return out
}

// render mixes the parts with a gain and some noise (a different "encode").
func render(r *rand.Rand, gain, noise float64, parts ...[]float64) []int16 {
	var out []int16
	for _, p := range parts {
		for _, s := range p {
			v := s*gain + r.NormFloat64()*noise
			out = append(out, int16(max(-32768, min(32767, v))))
		}
	}
	return out
}

func TestCommonFindsSharedIntro(t *testing.T) {
	// Episode B has a different cold open length, is quieter and noisier
	// (another encode). The second case is the worst alignment: half a hop.
	for _, coldOpen := range []float64{47.31, 20 + 588.5*HopSec} {
		r := rand.New(rand.NewSource(1))
		intro := music(r, 40)
		a := Fingerprint(render(r, 1, 200, music(r, 20), intro, music(r, 60)))
		b := Fingerprint(render(r, 0.7, 400, music(r, coldOpen), intro, music(r, 60)))

		m, ok := Common(a, b, 15)
		if !ok {
			t.Fatalf("cold open %.3f: no common segment found", coldOpen)
		}
		near := func(got, want float64) bool { return math.Abs(got-want) < 1.2 }
		if !near(m.AStart, 20) || !near(m.AEnd, 60) || !near(m.BStart, coldOpen) || !near(m.BEnd, coldOpen+40) {
			t.Fatalf("cold open %.3f: match = %+v, want A 20–60", coldOpen, m)
		}
	}
}

func TestCommonRejectsUnrelatedAudio(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	a := Fingerprint(render(r, 1, 200, music(r, 90)))
	b := Fingerprint(render(r, 1, 200, music(r, 90)))
	if m, ok := Common(a, b, 15); ok {
		t.Fatalf("unrelated audio matched: %+v", m)
	}
}

func TestCommonIgnoresSharedSilence(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	silence := make([]float64, 30*SampleRate)
	a := Fingerprint(render(r, 1, 5, silence, music(r, 60)))
	b := Fingerprint(render(r, 1, 5, silence, music(r, 60)))
	if m, ok := Common(a, b, 15); ok {
		t.Fatalf("silence matched: %+v", m)
	}
}
