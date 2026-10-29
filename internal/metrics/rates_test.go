package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/nboeger/virt-btop/internal/hypervisor"
)

func approx(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s: got %.6f, want %.6f", label, got, want)
	}
}

func TestComputeRates(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	t1 := t0.Add(time.Second)

	d := &hypervisor.Domain{
		Name:          "vm",
		State:         hypervisor.StateRunning,
		VCPUs:         4,
		CPUTimeNS:     100_000_000_000,
		VCPUTimesNS:   []uint64{10_000_000_000, 10_000_000_000, 10_000_000_000, 10_000_000_000},
		MemCurrentKiB: 1024,
		MemUsableKiB:  512, MemUsableSet: true,
		MemLastUpdate: t1.Add(-time.Second),
		Disks:         []hypervisor.Disk{{RDBytes: 10_000}},
		Nets:          []hypervisor.Net{{RXBytes: 20_000, TXBytes: 5_000}},
	}
	prev := CountersOf(d, t0)

	d.CPUTimeNS += 2_000_000_000 // 2 CPU-seconds in one wall second
	for i := range d.VCPUTimesNS {
		d.VCPUTimesNS[i] += 500_000_000
	}
	d.Disks[0].RDBytes += 2_048
	d.Nets[0].RXBytes += 4_096
	d.Nets[0].TXBytes += 1_024
	cur := CountersOf(d, t1)

	r := Compute(&prev, cur, d, 8)
	approx(t, r.CPUPct, 50, "cpu pct")
	approx(t, r.HostPct, 25, "host pct")
	approx(t, r.MemPct, 50, "mem pct")
	approx(t, r.DiskRDBps, 2048, "disk rd")
	approx(t, r.NetRXBps, 4096, "net rx")
	approx(t, r.NetTXBps, 1024, "net tx")
	for _, p := range r.VCPUPct {
		approx(t, p, 50, "vcpu pct")
	}
	if r.MemStatus != hypervisor.MemFresh {
		t.Fatalf("mem status = %v, want fresh", r.MemStatus)
	}
}

func TestComputeMemoryStates(t *testing.T) {
	at := time.Unix(1700000000, 0)
	mk := func(mut func(*hypervisor.Domain)) Rates {
		d := &hypervisor.Domain{
			Name: "vm", State: hypervisor.StateRunning, VCPUs: 1, MemCurrentKiB: 1000,
			MemUsableKiB: 250, MemUsableSet: true, MemLastUpdate: at.Add(-time.Second),
		}
		mut(d)
		return Compute(nil, CountersOf(d, at), d, 4)
	}

	fresh := mk(func(*hypervisor.Domain) {})
	if fresh.MemStatus != hypervisor.MemFresh || fresh.MemUsedKiB != 750 {
		t.Fatalf("fresh: %+v", fresh)
	}
	approx(t, fresh.MemPct, 75, "fresh pct")

	stale := mk(func(d *hypervisor.Domain) { d.MemLastUpdate = at.Add(-2 * time.Hour) })
	if stale.MemStatus != hypervisor.MemStale || stale.MemStatsAge != 2*time.Hour {
		t.Fatalf("stale: %+v", stale)
	}

	// Regression: no guest stats must not read as 100% used.
	unknown := mk(func(d *hypervisor.Domain) { d.MemUsableSet = false })
	if unknown.MemStatus != hypervisor.MemUnknown || unknown.MemUsedKiB != 0 || unknown.MemPct != 0 {
		t.Fatalf("unknown: %+v", unknown)
	}
}

func TestComputeResetsOnStateChange(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	t1 := t0.Add(time.Second)
	d := &hypervisor.Domain{Name: "vm", State: hypervisor.StateRunning, VCPUs: 2, CPUTimeNS: 10}
	prev := CountersOf(d, t0)
	d.CPUTimeNS = 20
	d.State = hypervisor.StatePaused
	cur := CountersOf(d, t1)
	r := Compute(&prev, cur, d, 4)
	if r.CPUPct != 0 || r.CPUDeltaNS != 0 {
		t.Fatalf("state change should reset rates: %+v", r)
	}
}

func TestComputeUnderflow(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	t1 := t0.Add(time.Second)
	d := &hypervisor.Domain{Name: "vm", State: hypervisor.StateRunning, VCPUs: 1, CPUTimeNS: 100}
	prev := CountersOf(d, t0)
	d.CPUTimeNS = 50
	cur := CountersOf(d, t1)
	r := Compute(&prev, cur, d, 4)
	if r.CPUPct != 0 || r.CPUDeltaNS != 0 {
		t.Fatalf("underflow should produce zero delta: %+v", r)
	}
}

func TestCombineAggregatesOnlyFreshMemory(t *testing.T) {
	at := time.Unix(1700000000, 0)
	fresh := func(name string, cur, usable uint64, vcpus int) *hypervisor.Domain {
		return &hypervisor.Domain{
			Name: name, State: hypervisor.StateRunning, VCPUs: vcpus,
			MemCurrentKiB: cur, MemUsableKiB: usable, MemUsableSet: true,
			MemLastUpdate: at.Add(-time.Second), MemRSSKiB: 100,
			Nets:  []hypervisor.Net{{RXBytes: 1000, TXBytes: 2000}},
			Disks: []hypervisor.Disk{{RDBytes: 3000, WRBytes: 4000}},
		}
	}
	a := fresh("a", 8192, 4096, 4) // used 4096
	b := fresh("b", 4096, 1024, 2) // used 3072
	stale := fresh("stale", 9999, 0, 2)
	stale.MemLastUpdate = at.Add(-time.Hour)
	nostats := &hypervisor.Domain{Name: "nostats", State: hypervisor.StateRunning, VCPUs: 1, MemCurrentKiB: 7777, MemRSSKiB: 50}
	off := &hypervisor.Domain{Name: "off", State: hypervisor.StateShutoff, VCPUs: 8, MemCurrentKiB: 16384}

	domains := []*hypervisor.Domain{a, b, stale, nostats, off}
	rates := map[string]Rates{}
	for _, d := range domains {
		rates[d.Name] = Compute(nil, CountersOf(d, at), d, 8)
	}
	rates["a"] = withTraffic(rates["a"], 1000, 2000, 3000, 4000)
	rates["b"] = withTraffic(rates["b"], 500, 0, 0, 0)

	c := Combine(domains, rates)
	if c.ActiveVMs != 4 {
		t.Fatalf("active VMs = %d, want 4", c.ActiveVMs)
	}
	if c.MemReporting != 2 || c.MemExcluded != 2 {
		t.Fatalf("reporting/excluded = %d/%d, want 2/2", c.MemReporting, c.MemExcluded)
	}
	// Only the two fresh VMs contribute: stale and no-stats values would be wrong.
	if c.MemUsedKiB != 7168 || c.MemTotalKiB != 12288 {
		t.Fatalf("mem used/total = %d/%d, want 7168/12288", c.MemUsedKiB, c.MemTotalKiB)
	}
	approx(t, c.MemPct, 7168.0/12288.0*100, "mem pct")
	// RSS is host-side and accurate for every active VM.
	if c.MemRSSKiB != 100+100+100+50 {
		t.Fatalf("rss = %d, want 350", c.MemRSSKiB)
	}
	approx(t, c.NetRXBps, 1500, "net rx")
	approx(t, c.DiskRDBps, 3000, "disk rd")
	// a, b and stale each have a 1000/2000 NIC; nostats has none; off is excluded.
	if c.NetRXTotal != 3000 || c.NetTXTotal != 6000 {
		t.Fatalf("net totals: %d/%d", c.NetRXTotal, c.NetTXTotal)
	}
}

func TestCombineNoReportingVMs(t *testing.T) {
	d := &hypervisor.Domain{Name: "x", State: hypervisor.StateRunning, VCPUs: 1, MemCurrentKiB: 1000}
	rates := map[string]Rates{"x": Compute(nil, CountersOf(d, time.Unix(1, 0)), d, 4)}
	c := Combine([]*hypervisor.Domain{d}, rates)
	if c.MemReporting != 0 || c.MemExcluded != 1 || c.MemPct != 0 || c.MemUsedKiB != 0 {
		t.Fatalf("combined: %+v", c)
	}
}

func withTraffic(r Rates, rx, tx, rd, wr float64) Rates {
	r.NetRXBps, r.NetTXBps, r.DiskRDBps, r.DiskWRBps = rx, tx, rd, wr
	return r
}

func TestWindow(t *testing.T) {
	got := Window([]float64{1, 2, 3}, 5)
	want := []float64{0, 0, 1, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("window = %v, want %v", got, want)
		}
	}
	got = Window([]float64{1, 2, 3, 4, 5}, 3)
	want = []float64{3, 4, 5}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("window = %v, want %v", got, want)
		}
	}
}

func TestCPUCombineExcludesPaused(t *testing.T) {
	domains := []*hypervisor.Domain{
		{Name: "run", State: hypervisor.StateRunning, VCPUs: 4},
		{Name: "blocked", State: hypervisor.StateBlocked, VCPUs: 2},
		{Name: "paused", State: hypervisor.StatePaused, VCPUs: 8},
		{Name: "off", State: hypervisor.StateShutoff, VCPUs: 2},
	}
	rates := map[string]Rates{
		"run":     {CPUDeltaNS: 2_000_000_000},
		"blocked": {CPUDeltaNS: 1_000_000_000},
	}
	vcpus, util, host := CPUCombine(domains, rates, 1, 8)
	if vcpus != 6 {
		t.Fatalf("vcpus = %d, want 6", vcpus)
	}
	approx(t, util, 50, "cpu util")
	approx(t, host, 37.5, "host share")
}
