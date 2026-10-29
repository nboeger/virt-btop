package hypervisor

import (
	"testing"
	"time"
)

var epoch = time.Unix(1_700_000_000, 0)

func freshDomain() *Domain {
	return &Domain{
		State:         StateRunning,
		MemCurrentKiB: 1000,
		MemUsableKiB:  400, MemUsableSet: true,
		MemUnusedKiB: 300, MemUnusedSet: true,
		MemLastUpdate: epoch.Add(-5 * time.Second),
	}
}

func TestMemUsedPrefersUsable(t *testing.T) {
	d := freshDomain()
	if got := d.MemUsedKiB(); got != 600 { // current - usable
		t.Fatalf("used = %d, want 600 (current-usable)", got)
	}
	if got := d.MemStatus(epoch); got != MemFresh {
		t.Fatalf("status = %v, want fresh", got)
	}
}

func TestMemUsedFallsBackToUnused(t *testing.T) {
	d := freshDomain()
	d.MemUsableSet = false
	if got := d.MemUsedKiB(); got != 700 { // current - unused
		t.Fatalf("used = %d, want 700 (current-unused)", got)
	}
}

// Regression: libvirt's "available" is the total memory the guest sees and is
// constant. The old code computed current-available, which never moved with
// guest load. Changing usable must change the result.
func TestMemUsedTracksGuestLoadNotConstantAvailable(t *testing.T) {
	d := freshDomain()
	before := d.MemUsedKiB()
	d.MemUsableKiB = 100 // guest consumed 300 KiB more
	after := d.MemUsedKiB()
	if after != before+300 {
		t.Fatalf("used went %d -> %d, want +300", before, after)
	}
}

// Regression: a running VM that reports no balloon statistics used to show as
// 100% used (current - 0).
func TestMemUnknownWithoutStats(t *testing.T) {
	d := &Domain{State: StateRunning, MemCurrentKiB: 2097152}
	if got := d.MemStatus(epoch); got != MemUnknown {
		t.Fatalf("status = %v, want unknown", got)
	}
	if got := d.MemUsedKiB(); got != 0 {
		t.Fatalf("used = %d, want 0 when unknown", got)
	}
}

func TestMemSetFlagDistinguishesZeroFromAbsent(t *testing.T) {
	// A guest that really has 0 KiB usable is fully used, not "unknown".
	d := &Domain{State: StateRunning, MemCurrentKiB: 1000, MemUsableSet: true, MemUsableKiB: 0}
	if got := d.MemStatus(epoch); got != MemFresh {
		t.Fatalf("status = %v, want fresh", got)
	}
	if got := d.MemUsedKiB(); got != 1000 {
		t.Fatalf("used = %d, want 1000", got)
	}
}

func TestMemStale(t *testing.T) {
	d := freshDomain()
	d.MemLastUpdate = epoch.Add(-2 * time.Hour)
	if got := d.MemStatus(epoch); got != MemStale {
		t.Fatalf("status = %v, want stale", got)
	}
	if got := d.MemStatsAge(epoch); got != 2*time.Hour {
		t.Fatalf("age = %v, want 2h", got)
	}
	d.MemLastUpdate = epoch.Add(-MemStaleAfter)
	if got := d.MemStatus(epoch); got != MemFresh {
		t.Fatalf("exactly at the limit should still be fresh, got %v", got)
	}
	d.MemLastUpdate = epoch.Add(-MemStaleAfter - time.Second)
	if got := d.MemStatus(epoch); got != MemStale {
		t.Fatalf("just past the limit should be stale, got %v", got)
	}
}

func TestMemNeverUpdatedIsUnknown(t *testing.T) {
	d := freshDomain()
	d.MemLastUpdate = time.Time{}
	d.MemLastUpdateZero = true
	if got := d.MemStatus(epoch); got != MemUnknown {
		t.Fatalf("status = %v, want unknown for last-update=0", got)
	}
}

func TestMemMissingLastUpdateCannotBeProvenStale(t *testing.T) {
	d := freshDomain()
	d.MemLastUpdate = time.Time{} // driver did not report it at all
	if got := d.MemStatus(epoch); got != MemFresh {
		t.Fatalf("status = %v, want fresh when age is unknowable", got)
	}
	if got := d.MemStatsAge(epoch); got != 0 {
		t.Fatalf("age = %v, want 0 when unknown", got)
	}
}

func TestMemInactiveDomains(t *testing.T) {
	d := freshDomain()
	d.State = StateShutoff
	if d.MemStatus(epoch) != MemUnknown || d.MemUsedKiB() != 0 {
		t.Fatal("stopped domain must report unknown memory and zero used")
	}
}

func TestMemUsedClampsBogusValues(t *testing.T) {
	d := freshDomain()
	d.MemUsableKiB = 5000 // more free than configured: a guest/driver glitch
	if got := d.MemUsedKiB(); got != 0 {
		t.Fatalf("used = %d, want 0 (no underflow)", got)
	}
}

func TestDomainSums(t *testing.T) {
	d := &Domain{
		Disks: []Disk{{RDBytes: 10, WRBytes: 20}, {RDBytes: 30, WRBytes: 40}},
		Nets:  []Net{{RXBytes: 100, TXBytes: 200}, {RXBytes: 300, TXBytes: 400}},
	}
	if got := d.DiskReadBytes(); got != 40 {
		t.Fatalf("disk read = %d", got)
	}
	if got := d.DiskWriteBytes(); got != 60 {
		t.Fatalf("disk write = %d", got)
	}
	if got := d.NetRXBytes(); got != 400 {
		t.Fatalf("net rx = %d", got)
	}
	if got := d.NetTXBytes(); got != 600 {
		t.Fatalf("net tx = %d", got)
	}
}

func TestState(t *testing.T) {
	for _, s := range []State{StateRunning, StateBlocked, StatePaused, StatePMSuspended} {
		if !s.Active() {
			t.Fatalf("%s should be active", s)
		}
	}
	for _, s := range []State{StateShutoff, StateCrashed, StateShutdown} {
		if s.Active() {
			t.Fatalf("%s should not be active", s)
		}
	}
	if StateShutoff.String() != "shut off" {
		t.Fatalf("state string = %q", StateShutoff.String())
	}
}

func TestSnapshotCounts(t *testing.T) {
	snap := NewSnapshot(epoch, []*Domain{
		{Name: "a", State: StateRunning},
		{Name: "b", State: StatePaused},
		{Name: "c", State: StateShutoff},
	}, nil)
	if active, total := snap.Counts(); active != 2 || total != 3 {
		t.Fatalf("counts = %d/%d, want 2/3", active, total)
	}
	if snap.ByName["b"] == nil {
		t.Fatal("ByName missing b")
	}
}
