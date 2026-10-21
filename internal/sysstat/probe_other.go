//go:build !linux && !darwin

package sysstat

import "runtime"

// Elsewhere only the server's own memory is known.
type goProbe struct{}

func newProbe() probe { return goProbe{} }

func (goProbe) info() Info { return baseInfo() }

func (goProbe) sample() Sample {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return Sample{ProcMem: m.Sys}
}
