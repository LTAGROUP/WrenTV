package api

import "testing"

func TestAgreedLength(t *testing.T) {
	ep := 42*60 + 17.0
	for _, c := range []struct {
		lengths []float64
		agreed  float64
		off     []bool
	}{
		{[]float64{ep, ep + 2, ep + 45}, ep + 2, []bool{false, false, true}}, // an ad in front
		{[]float64{ep, ep + 40}, ep, []bool{false, true}},                    // two: the shorter
		{[]float64{ep, ep + 8}, ep, []bool{false, false}},                    // within the slack
		{[]float64{ep, 0, ep - 300, ep + 1}, ep, []bool{false, false, true, false}},
		{[]float64{ep}, 0, []bool{false}}, // nothing to compare
	} {
		a := agreedLength(c.lengths)
		if a != c.agreed {
			t.Errorf("%v: agreed %v, want %v", c.lengths, a, c.agreed)
		}
		for i, l := range c.lengths {
			if offLength(l, a) != c.off[i] {
				t.Errorf("%v: %v off = %v", c.lengths, l, !c.off[i])
			}
		}
	}
}
