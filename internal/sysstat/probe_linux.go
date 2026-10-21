package sysstat

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// linuxProbe reads /proc for the machine. Inside a container /proc/stat and
// /proc/meminfo still describe the host, and the container's own use comes
// from its cgroup; outside one it is the server's process tree.
type linuxProbe struct {
	container bool
	v2        bool
	inf       Info

	prevBusy, prevTotal float64
	prevProc            float64 // CPU seconds
	prevAt              time.Time
}

const clkTck = 100 // USER_HZ, fixed at 100 on Linux

func newProbe() probe {
	p := &linuxProbe{inf: baseInfo()}
	_, err1 := os.Stat("/.dockerenv")
	_, err2 := os.Stat("/run/.containerenv")
	p.container = err1 == nil || err2 == nil || os.Getenv("container") != ""
	_, err := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	p.v2 = err == nil
	p.inf.Host = true
	p.inf.ProcCPU = true
	p.inf.MemTotal = meminfo()["MemTotal"]
	if p.container {
		p.inf.ProcLabel = "Container"
		p.inf.MemLimit, p.inf.CPULimit = p.limits()
		if p.inf.MemLimit >= p.inf.MemTotal {
			p.inf.MemLimit = 0
		}
	}
	return p
}

func (p *linuxProbe) info() Info { return p.inf }

func (p *linuxProbe) sample() Sample {
	var s Sample
	now := time.Now()
	if busy, total, ok := procStat(); ok {
		if dt := total - p.prevTotal; p.prevTotal > 0 && dt > 0 {
			s.CPU = clamp((busy - p.prevBusy) / dt * 100)
		}
		p.prevBusy, p.prevTotal = busy, total
	}
	m := meminfo()
	if avail, ok := m["MemAvailable"]; ok && m["MemTotal"] >= avail {
		s.Mem = m["MemTotal"] - avail
	}
	var procSecs float64
	if p.container {
		procSecs, s.ProcMem = p.cgroup()
	} else {
		procSecs, s.ProcMem = processTree(os.Getpid())
	}
	if !p.prevAt.IsZero() {
		if wall := now.Sub(p.prevAt).Seconds(); wall > 0 && procSecs >= p.prevProc {
			s.ProcCPU = clamp((procSecs - p.prevProc) / wall / float64(p.inf.CPUs) * 100)
		}
	}
	p.prevProc, p.prevAt = procSecs, now
	return s
}

func clamp(v float64) float64 { return min(100, max(0, v)) }

// procStat returns busy and total jiffies across every core.
func procStat() (busy, total float64, ok bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := bytes.Cut(b, []byte("\n"))
	f := strings.Fields(string(line))
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	var v []float64
	for _, x := range f[1:min(len(f), 9)] { // user..steal; guest is already in user
		n, _ := strconv.ParseFloat(x, 64)
		v = append(v, n)
	}
	for _, n := range v {
		total += n
	}
	idle := v[3]
	if len(v) > 4 {
		idle += v[4] // iowait
	}
	return total - idle, total, true
}

// meminfo returns /proc/meminfo in bytes.
func meminfo() map[string]uint64 {
	out := map[string]uint64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fs := strings.Fields(v)
		if len(fs) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(fs[0], 10, 64)
		if len(fs) > 1 && fs[1] == "kB" {
			n *= 1024
		}
		out[k] = n
	}
	return out
}

func readUint(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n, err == nil
}

// statField reads "key value" lines such as cpu.stat and memory.stat.
func statField(path, key string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == key {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// cgroup returns the container's CPU seconds and its memory in use, counted
// the way `docker stats` does (page cache that can be dropped doesn't count).
func (p *linuxProbe) cgroup() (secs float64, mem uint64) {
	if p.v2 {
		if us, ok := statField("/sys/fs/cgroup/cpu.stat", "usage_usec"); ok {
			secs = float64(us) / 1e6
		}
		mem, _ = readUint("/sys/fs/cgroup/memory.current")
		if inactive, ok := statField("/sys/fs/cgroup/memory.stat", "inactive_file"); ok && inactive < mem {
			mem -= inactive
		}
		return
	}
	if ns, ok := readUint("/sys/fs/cgroup/cpuacct/cpuacct.usage"); ok {
		secs = float64(ns) / 1e9
	}
	mem, _ = readUint("/sys/fs/cgroup/memory/memory.usage_in_bytes")
	if inactive, ok := statField("/sys/fs/cgroup/memory/memory.stat", "total_inactive_file"); ok && inactive < mem {
		mem -= inactive
	}
	return
}

// limits returns the container's memory limit and CPU quota in cores.
func (p *linuxProbe) limits() (mem uint64, cores float64) {
	if p.v2 {
		mem, _ = readUint("/sys/fs/cgroup/memory.max") // "max" fails to parse: no limit
		if b, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
			f := strings.Fields(string(b))
			if len(f) == 2 && f[0] != "max" {
				q, _ := strconv.ParseFloat(f[0], 64)
				per, _ := strconv.ParseFloat(f[1], 64)
				if per > 0 {
					cores = q / per
				}
			}
		}
		return
	}
	mem, _ = readUint("/sys/fs/cgroup/memory/memory.limit_in_bytes")
	q, ok1 := readUint("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	per, ok2 := readUint("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if ok1 && ok2 && per > 0 {
		cores = float64(q) / float64(per)
	}
	return
}

// processTree sums CPU seconds and resident memory for pid and everything it
// started (the ffmpeg runs behind transcodes and remuxes).
func processTree(root int) (secs float64, mem uint64) {
	type proc struct {
		ppid int
		secs float64
		rss  uint64
	}
	procs := map[int]proc{}
	dirs, _ := filepath.Glob("/proc/[0-9]*/stat")
	page := uint64(os.Getpagesize())
	for _, path := range dirs {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		i := bytes.LastIndexByte(b, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(b[i+1:]))
		if len(f) < 22 {
			continue
		}
		pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(path)))
		ppid, _ := strconv.Atoi(f[1])
		ut, _ := strconv.ParseFloat(f[11], 64)
		st, _ := strconv.ParseFloat(f[12], 64)
		rss, _ := strconv.ParseUint(f[21], 10, 64)
		procs[pid] = proc{ppid, (ut + st) / clkTck, rss * page}
	}
	in := map[int]bool{root: true}
	for changed := true; changed; {
		changed = false
		for pid, p := range procs {
			if !in[pid] && in[p.ppid] {
				in[pid], changed = true, true
			}
		}
	}
	for pid := range in {
		if p, ok := procs[pid]; ok {
			secs += p.secs
			mem += p.rss
		}
	}
	return
}
