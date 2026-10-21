// Package sysstat samples the machine's and the server's own CPU and memory
// use, for the stats dashboard. In a container the server's share is the
// container's (its cgroup); elsewhere it is Wren TV and the ffmpeg runs it
// started.
package sysstat

import (
	"runtime"
	"sync"
	"time"
)

// Sample is one reading. CPU figures are percentages of the whole machine
// (every core); memory is in bytes.
type Sample struct {
	T       int64   `json:"t"`
	CPU     float64 `json:"cpu"`
	Mem     uint64  `json:"mem"`
	ProcCPU float64 `json:"procCpu"`
	ProcMem uint64  `json:"procMem"`
}

// Info describes what the samples measure.
type Info struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CPUs     int    `json:"cpus"`
	MemTotal uint64 `json:"memTotal"`
	// Host is false where the machine's own use can't be read.
	Host bool `json:"host"`
	// ProcLabel names the second series: "Container" or "Wren TV".
	ProcLabel string `json:"procLabel"`
	ProcCPU   bool   `json:"procCpuAvailable"`
	// Limits a container runs under (0 when unlimited).
	MemLimit uint64  `json:"memLimit,omitempty"`
	CPULimit float64 `json:"cpuLimit,omitempty"` // cores
}

// probe reads the platform; implementations keep what they need for deltas.
type probe interface {
	info() Info
	sample() Sample
}

// Sampler keeps the last hour every 5 seconds and the last day by minute.
type Sampler struct {
	p       probe
	started time.Time

	mu      sync.Mutex
	fine    []Sample // ring, 5 s apart
	coarse  []Sample // ring, minute averages
	pending []Sample // this minute's fine samples
}

const (
	fineEvery  = 5 * time.Second
	fineKeep   = 720  // an hour
	coarseKeep = 1440 // a day
)

func Start() *Sampler {
	s := &Sampler{p: newProbe(), started: time.Now()}
	s.p.sample() // prime the deltas
	go s.run()
	return s
}

func (s *Sampler) run() {
	t := time.NewTicker(fineEvery)
	for range t.C {
		x := s.p.sample()
		x.T = time.Now().Unix()
		s.mu.Lock()
		s.fine = appendRing(s.fine, x, fineKeep)
		if len(s.pending) > 0 && s.pending[0].T/60 != x.T/60 {
			s.coarse = appendRing(s.coarse, average(s.pending), coarseKeep)
			s.pending = s.pending[:0]
		}
		s.pending = append(s.pending, x)
		s.mu.Unlock()
	}
}

func appendRing(r []Sample, x Sample, keep int) []Sample {
	if len(r) >= keep {
		r = append(r[:0:0], r[len(r)-keep+1:]...)
	}
	return append(r, x)
}

func average(xs []Sample) Sample {
	var a Sample
	for _, x := range xs {
		a.CPU += x.CPU
		a.ProcCPU += x.ProcCPU
		a.Mem += x.Mem
		a.ProcMem += x.ProcMem
	}
	n := float64(len(xs))
	a.T = xs[0].T / 60 * 60
	a.CPU, a.ProcCPU = a.CPU/n, a.ProcCPU/n
	a.Mem, a.ProcMem = a.Mem/uint64(len(xs)), a.ProcMem/uint64(len(xs))
	return a
}

func (s *Sampler) Info() Info { return s.p.info() }

// Uptime is how long the server has been running.
func (s *Sampler) Uptime() time.Duration { return time.Since(s.started) }

// Latest is the most recent sample, if any.
func (s *Sampler) Latest() (Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fine) == 0 {
		return Sample{}, false
	}
	return s.fine[len(s.fine)-1], true
}

// Since returns samples newer than d ago: 5-second ones for up to an hour,
// minute averages beyond that.
func (s *Sampler) Since(d time.Duration) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := time.Now().Add(-d).Unix()
	src := s.fine
	if d > time.Hour {
		src = s.coarse
	}
	out := []Sample{}
	for _, x := range src {
		if x.T >= from {
			out = append(out, x)
		}
	}
	return out
}

func baseInfo() Info {
	return Info{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), ProcLabel: "Wren TV"}
}

// Disk is the space on the volume that holds a path.
type Disk struct {
	Path  string `json:"path"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}

// DiskUsage reports the volume under path.
func DiskUsage(path string) (Disk, bool) {
	total, free, ok := statfs(path)
	return Disk{Path: path, Total: total, Free: free}, ok
}
