// Package markers finds intros and credits in episodes that have no chapter
// markers, by comparing audio fingerprints of episodes in the same season:
// the stretch of audio two episodes share near the start is the intro, and
// near the end the credits.
package markers

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"math/cmplx"
	"os/exec"
	"strconv"
	"strings"
)

// Fingerprints are 32-bit sub-fingerprints in the style of Haitsma & Kalker:
// each bit is the sign of the change, over time, of the energy difference
// between two neighbouring frequency bands (300–2000 Hz, log-spaced). They
// survive re-encoding and volume changes well.
const (
	SampleRate = 11025
	frameSize  = 2048
	hopSize    = 512
	// HopSec is the time between consecutive sub-fingerprints.
	HopSec = float64(hopSize) / SampleRate
	nBands = 33
)

// Extract decodes [start, start+dur) of a file's default audio track with
// ffmpeg and returns its fingerprint.
func Extract(ctx context.Context, ffmpeg, path string, start, dur float64) ([]uint32, error) {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", path, "-t", strconv.FormatFloat(dur, 'f', 3, 64),
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-ac", "1", "-ar", strconv.Itoa(SampleRate), "-f", "s16le", "-")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	pcm := make([]int16, out.Len()/2)
	if err := binary.Read(&out, binary.LittleEndian, pcm); err != nil {
		return nil, err
	}
	return Fingerprint(pcm), nil
}

var (
	window    = hann(frameSize)
	bandEdges = func() [nBands + 1]int {
		var e [nBands + 1]int
		lo, hi := 300.0, 2000.0
		for i := range e {
			f := lo * math.Pow(hi/lo, float64(i)/nBands)
			e[i] = int(math.Round(f * frameSize / SampleRate))
		}
		return e
	}()
)

func hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

// quietRMS marks frames too quiet to fingerprint (silence, fades); they get
// the value 0, which never counts as a match.
const quietRMS = 60

// Fingerprint computes one sub-fingerprint per hop of mono 11025 Hz PCM.
func Fingerprint(pcm []int16) []uint32 {
	if len(pcm) < frameSize {
		return nil
	}
	n := (len(pcm)-frameSize)/hopSize + 1
	out := make([]uint32, n)
	buf := make([]complex128, frameSize)
	var prev, cur [nBands]float64
	for f := 0; f < n; f++ {
		off := f * hopSize
		var sq float64
		for i := 0; i < frameSize; i++ {
			s := float64(pcm[off+i])
			sq += s * s
			buf[i] = complex(s*window[i], 0)
		}
		fft(buf)
		for b := 0; b < nBands; b++ {
			var e float64
			for k := bandEdges[b]; k < bandEdges[b+1]; k++ {
				a := cmplx.Abs(buf[k])
				e += a * a
			}
			cur[b] = e
		}
		quiet := math.Sqrt(sq/frameSize) < quietRMS
		if f > 0 && !quiet {
			var h uint32
			for b := 0; b < nBands-1; b++ {
				if (cur[b]-cur[b+1])-(prev[b]-prev[b+1]) > 0 {
					h |= 1 << b
				}
			}
			if h == 0 {
				h = 1 // keep 0 reserved for "no fingerprint"
			}
			out[f] = h
		}
		prev = cur
	}
	return out
}

// fft is an in-place iterative radix-2 FFT; len(a) must be a power of two.
func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wn := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := a[start+k], a[start+k+size/2]*wn
				a[start+k], a[start+k+size/2] = u+v, u-v
				wn *= w
			}
		}
	}
}

// Match is a stretch of audio two fingerprints share, in seconds from the
// start of each.
type Match struct {
	AStart, AEnd float64
	BStart, BEnd float64
}

func (m Match) Len() float64 { return m.AEnd - m.AStart }

const (
	smoothFrames = 21 // ~1 s
	maxBitErrors = 10 // of 32, averaged over smoothFrames
	maxOffsets   = 6
)

// Common finds the longest stretch of audio that a and b share, if it lasts
// at least minLen seconds. Candidate alignments come from exact
// sub-fingerprint matches; each is then scored frame by frame.
func Common(a, b []uint32, minLen float64) (Match, bool) {
	if len(a) == 0 || len(b) == 0 {
		return Match{}, false
	}
	idx := map[uint32][]int32{}
	for j, h := range b {
		if h != 0 {
			idx[h] = append(idx[h], int32(j))
		}
	}
	votes := map[int]int{}
	for i, h := range a {
		js := idx[h]
		if h == 0 || len(js) > 30 { // repetitive (tones, music loops): no alignment info
			continue
		}
		for _, j := range js {
			votes[int(j)-i]++
		}
	}
	type cand struct{ off, votes int }
	var cands []cand
	for off, v := range votes {
		if v >= 4 {
			cands = append(cands, cand{off, v + votes[off-1] + votes[off+1]})
		}
	}
	// Strongest first; keep a handful.
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && cands[j].votes > cands[j-1].votes; j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}
	if len(cands) > maxOffsets {
		cands = cands[:maxOffsets]
	}
	var best Match
	found := false
	for _, c := range cands {
		if m, ok := alignedRun(a, b, c.off); ok && m.Len() >= minLen && (!found || m.Len() > best.Len()) {
			best, found = m, true
		}
	}
	return best, found
}

// alignedRun scores a against b shifted by off frames and returns the longest
// run where the smoothed bit error stays low.
func alignedRun(a, b []uint32, off int) (Match, bool) {
	i0 := max(0, -off)
	i1 := min(len(a), len(b)-off)
	if i1-i0 < smoothFrames {
		return Match{}, false
	}
	errs := make([]float64, i1-i0)
	for i := i0; i < i1; i++ {
		x, y := a[i], b[i+off]
		if x == 0 || y == 0 {
			errs[i-i0] = 16 // unknown counts as a coin flip
		} else {
			errs[i-i0] = float64(bits.OnesCount32(x ^ y))
		}
	}
	// Moving average, centred.
	half := smoothFrames / 2
	var sum float64
	sm := make([]float64, len(errs))
	for i := 0; i < len(errs)+half; i++ {
		if i < len(errs) {
			sum += errs[i]
		}
		if i-smoothFrames >= 0 {
			sum -= errs[i-smoothFrames]
		}
		if c := i - half; c >= 0 && c < len(errs) {
			n := min(i, len(errs)-1) - max(0, i-smoothFrames+1) + 1
			sm[c] = sum / float64(n)
		}
	}
	bestS, bestE, s := -1, -1, -1
	for i := 0; i <= len(sm); i++ {
		if i < len(sm) && sm[i] <= maxBitErrors {
			if s < 0 {
				s = i
			}
			continue
		}
		if s >= 0 && i-s > bestE-bestS {
			bestS, bestE = s, i
		}
		s = -1
	}
	if bestS < 0 {
		return Match{}, false
	}
	as, ae := i0+bestS, i0+bestE
	return Match{
		AStart: float64(as) * HopSec, AEnd: float64(ae) * HopSec,
		BStart: float64(as+off) * HopSec, BEnd: float64(ae+off) * HopSec,
	}, true
}
