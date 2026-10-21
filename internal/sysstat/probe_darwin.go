package sysstat

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// darwinProbe reads ps for CPU and the process tree, and vm_stat for memory:
// the server builds without cgo, so the Mach host calls aren't available.
type darwinProbe struct{ inf Info }

func newProbe() probe {
	p := &darwinProbe{inf: baseInfo()}
	p.inf.Host, p.inf.ProcCPU = true, true
	p.inf.MemTotal, _ = unix.SysctlUint64("hw.memsize")
	return p
}

func (p *darwinProbe) info() Info { return p.inf }

func (p *darwinProbe) sample() Sample {
	var s Sample
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,%cpu=,rss=").Output()
	if err == nil {
		type proc struct {
			ppid int
			cpu  float64
			rss  uint64
		}
		procs := map[int]proc{}
		var total float64
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 4 {
				continue
			}
			pid, _ := strconv.Atoi(f[0])
			ppid, _ := strconv.Atoi(f[1])
			cpu, _ := strconv.ParseFloat(strings.ReplaceAll(f[2], ",", "."), 64)
			rss, _ := strconv.ParseUint(f[3], 10, 64)
			procs[pid] = proc{ppid, cpu, rss * 1024}
			total += cpu
		}
		n := float64(p.inf.CPUs)
		s.CPU = clamp(total / n)
		in := map[int]bool{os.Getpid(): true}
		for changed := true; changed; {
			changed = false
			for pid, x := range procs {
				if !in[pid] && in[x.ppid] {
					in[pid], changed = true, true
				}
			}
		}
		var cpu float64
		for pid := range in {
			cpu += procs[pid].cpu
			s.ProcMem += procs[pid].rss
		}
		s.ProcCPU = clamp(cpu / n)
	}
	s.Mem = vmUsed()
	return s
}

func clamp(v float64) float64 { return min(100, max(0, v)) }

// vmUsed is memory in use the way Activity Monitor counts it: app memory
// (anonymous pages), wired, and compressed.
func vmUsed() uint64 {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0
	}
	page := uint64(4096)
	if line, _, _ := bytes.Cut(out, []byte("\n")); bytes.Contains(line, []byte("page size of")) {
		f := strings.Fields(string(line))
		for i, w := range f {
			if w == "of" && i+1 < len(f) {
				if n, err := strconv.ParseUint(f[i+1], 10, 64); err == nil {
					page = n
				}
			}
		}
	}
	vals := map[string]uint64{}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
		if err == nil {
			vals[strings.TrimSpace(k)] = n
		}
	}
	return (vals["Anonymous pages"] + vals["Pages wired down"] + vals["Pages occupied by compressor"]) * page
}
