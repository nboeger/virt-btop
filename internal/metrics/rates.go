// Package metrics turns cumulative libvirt counters into rates and keeps the
// history buffers used by the graphs.
package metrics

import (
	"time"

	"github.com/nboeger/virt-btop/internal/hypervisor"
)

// Counters is the cumulative state needed to compute one interval's rates.
type Counters struct {
	At          time.Time
	State       hypervisor.State
	CPUTimeNS   uint64
	VCPUTimesNS []uint64
	DiskRD      uint64
	DiskWR      uint64
	NetRX       uint64
	NetTX       uint64
}

func CountersOf(d *hypervisor.Domain, at time.Time) Counters {
	return Counters{
		At:          at,
		State:       d.State,
		CPUTimeNS:   d.CPUTimeNS,
		VCPUTimesNS: append([]uint64(nil), d.VCPUTimesNS...),
		DiskRD:      d.DiskReadBytes(),
		DiskWR:      d.DiskWriteBytes(),
		NetRX:       d.NetRXBytes(),
		NetTX:       d.NetTXBytes(),
	}
}

// Rates is one domain's derived values for one interval.
type Rates struct {
	CPUPct     float64 // percent of the domain's allocated vCPUs
	HostPct    float64 // percent of all host cores
	CPUDeltaNS uint64
	VCPUs      int
	VCPUPct    []float64

	// Memory. MemUsedKiB and MemPct are zero unless MemStatus is not
	// MemUnknown; callers must check MemStatus before showing them.
	MemStatus   hypervisor.MemStatus
	MemStatsAge time.Duration
	MemUsedKiB  uint64
	MemTotalKiB uint64
	MemPct      float64

	DiskRDBps float64
	DiskWRBps float64
	NetRXBps  float64
	NetTXBps  float64
}

// Compute derives rates from the previous and current cumulative counters.
// A state change resets the delta so a VM restart or resume does not produce a
// bogus spike.
func Compute(prev *Counters, cur Counters, d *hypervisor.Domain, hostCores int) Rates {
	r := Rates{
		VCPUs:       d.VCPUs,
		MemTotalKiB: d.MemCurrentKiB,
		MemStatus:   d.MemStatus(cur.At),
		MemStatsAge: d.MemStatsAge(cur.At),
	}
	if r.MemStatus != hypervisor.MemUnknown {
		r.MemUsedKiB = d.MemUsedKiB()
		if r.MemTotalKiB > 0 {
			r.MemPct = float64(r.MemUsedKiB) / float64(r.MemTotalKiB) * 100
		}
	}
	if r.VCPUs > 0 {
		r.VCPUPct = make([]float64, r.VCPUs)
	}
	if prev == nil || !cur.State.Active() || prev.State != cur.State {
		return r
	}
	dt := cur.At.Sub(prev.At).Seconds()
	if dt <= 0 {
		return r
	}
	dCPU := delta(cur.CPUTimeNS, prev.CPUTimeNS)
	r.CPUDeltaNS = dCPU
	vcpus := d.VCPUs
	if vcpus <= 0 {
		vcpus = 1
	}
	r.CPUPct = float64(dCPU) / 1e9 / dt / float64(vcpus) * 100
	if hostCores > 0 {
		r.HostPct = float64(dCPU) / 1e9 / dt / float64(hostCores) * 100
	}

	r.DiskRDBps = float64(delta(cur.DiskRD, prev.DiskRD)) / dt
	r.DiskWRBps = float64(delta(cur.DiskWR, prev.DiskWR)) / dt
	r.NetRXBps = float64(delta(cur.NetRX, prev.NetRX)) / dt
	r.NetTXBps = float64(delta(cur.NetTX, prev.NetTX)) / dt

	for i := 0; i < len(r.VCPUPct) && i < len(cur.VCPUTimesNS); i++ {
		var p uint64
		if i < len(prev.VCPUTimesNS) {
			p = prev.VCPUTimesNS[i]
		}
		r.VCPUPct[i] = float64(delta(cur.VCPUTimesNS[i], p)) / 1e9 / dt * 100
	}
	return r
}

// delta returns cur-prev, or zero if the counter went backwards (a reset).
func delta(cur, prev uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

// Combined is the aggregate shown in the CPU, memory, and network boxes.
type Combined struct {
	ActiveVMs    int
	VCPUs        int
	VCPUUtilPct  float64
	HostSharePct float64

	// Guest memory covers only VMs whose statistics are fresh
	// (MemReporting); stale or missing statistics would make the totals wrong,
	// so those VMs are counted in MemExcluded instead of being summed.
	MemUsedKiB   uint64
	MemTotalKiB  uint64 // configured memory of the reporting VMs
	MemPct       float64
	MemReporting int
	MemExcluded  int
	SwapInKiB    uint64
	SwapOutKiB   uint64
	MemRSSKiB    uint64 // host-side qemu RSS of all active VMs; always accurate

	NetRXBps   float64
	NetTXBps   float64
	NetRXTotal uint64
	NetTXTotal uint64

	DiskRDBps float64
	DiskWRBps float64
}

// Combine aggregates active domains for the memory and network boxes. Paused
// VMs are included because they still hold memory.
func Combine(domains []*hypervisor.Domain, rates map[string]Rates) Combined {
	var c Combined
	for _, d := range domains {
		if !d.State.Active() {
			continue
		}
		c.ActiveVMs++
		c.MemRSSKiB += d.MemRSSKiB
		c.NetRXTotal += d.NetRXBytes()
		c.NetTXTotal += d.NetTXBytes()

		r := rates[d.Name]
		if r.MemStatus == hypervisor.MemFresh {
			c.MemReporting++
			c.MemUsedKiB += r.MemUsedKiB
			c.MemTotalKiB += d.MemCurrentKiB
			c.SwapInKiB += d.SwapInKiB
			c.SwapOutKiB += d.SwapOutKiB
		} else {
			c.MemExcluded++
		}
		c.NetRXBps += r.NetRXBps
		c.NetTXBps += r.NetTXBps
		c.DiskRDBps += r.DiskRDBps
		c.DiskWRBps += r.DiskWRBps
	}
	if c.MemTotalKiB > 0 {
		c.MemPct = float64(c.MemUsedKiB) / float64(c.MemTotalKiB) * 100
	}
	return c
}

// CPUCombine aggregates only domains that can currently consume CPU: running
// and blocked. Paused, suspended, and stopped domains are excluded from the
// utilization denominator.
func CPUCombine(domains []*hypervisor.Domain, rates map[string]Rates, dt float64, hostCores int) (vcpus int, util, host float64) {
	var cpuDeltaNS uint64
	for _, d := range domains {
		if d.State != hypervisor.StateRunning && d.State != hypervisor.StateBlocked {
			continue
		}
		vcpus += d.VCPUs
		cpuDeltaNS += rates[d.Name].CPUDeltaNS
	}
	if dt > 0 {
		if vcpus > 0 {
			util = float64(cpuDeltaNS) / 1e9 / dt / float64(vcpus) * 100
		}
		if hostCores > 0 {
			host = float64(cpuDeltaNS) / 1e9 / dt / float64(hostCores) * 100
		}
	}
	return
}
