// Package hypervisor talks to libvirt directly (no virsh subprocess) and
// converts libvirt's stats into small, UI-friendly values.
package hypervisor

import "time"

// State mirrors libvirt's virDomainState values.
type State int

const (
	StateNoState State = iota
	StateRunning
	StateBlocked
	StatePaused
	StateShutdown
	StateShutoff
	StateCrashed
	StatePMSuspended
)

func (s State) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateBlocked:
		return "blocked"
	case StatePaused:
		return "paused"
	case StateShutdown:
		return "shutdown"
	case StateShutoff:
		return "shut off"
	case StateCrashed:
		return "crashed"
	case StatePMSuspended:
		return "pmsuspend"
	default:
		return "unknown"
	}
}

// Active reports whether the domain is allocated and consuming resources.
// Paused domains are included: they still hold memory and vCPUs.
func (s State) Active() bool {
	return s == StateRunning || s == StateBlocked || s == StatePaused || s == StatePMSuspended
}

// Disk holds per-block-device cumulative counters and capacity information.
type Disk struct {
	Name       string
	RDBytes    uint64
	WRBytes    uint64
	Capacity   uint64
	Allocation uint64
}

// Net holds per-interface cumulative counters.
type Net struct {
	Name    string
	RXBytes uint64
	TXBytes uint64
}

// MemStatus says how far the guest-reported memory numbers can be trusted.
//
// libvirt only refreshes guest memory statistics when the domain's memballoon
// has <stats period='N'/> configured. Without it the numbers are whatever the
// balloon driver last pushed, possibly hours ago, and libvirt still returns
// them without any error.
type MemStatus int

const (
	// MemUnknown: the guest reports no usable memory statistics (no balloon
	// driver, no stats period, or the domain is not running).
	MemUnknown MemStatus = iota
	// MemStale: statistics exist but were last updated more than
	// MemStaleAfter ago.
	MemStale
	// MemFresh: statistics were updated recently.
	MemFresh
)

// MemStaleAfter is how old balloon statistics may be before they are shown as
// stale. A typical stats period is 5s, so a minute means the period is unset
// or the guest driver stopped reporting.
const MemStaleAfter = 60 * time.Second

// Domain is a single snapshot of one libvirt domain.
type Domain struct {
	Name string
	Info Info

	State State

	CPUTimeNS uint64

	// Memory. Current/Maximum are configuration (always valid). Usable and
	// Unused are guest-reported; the Set flags record whether libvirt supplied
	// them at all, so "absent" is never confused with "zero".
	MemCurrentKiB     uint64
	MemMaximumKiB     uint64
	MemUsableKiB      uint64
	MemUsableSet      bool
	MemUnusedKiB      uint64
	MemUnusedSet      bool
	MemLastUpdate     time.Time // zero when libvirt did not report it
	MemLastUpdateZero bool      // libvirt reported last-update == 0 (never updated)
	MemRSSKiB         uint64    // host-side qemu RSS; always accurate
	MemCachedKiB      uint64    // guest page cache (balloon disk_caches)
	SwapInKiB         uint64
	SwapOutKiB        uint64
	MajorFault        uint64
	MinorFault        uint64

	VCPUs       int
	VCPUStates  []int
	VCPUTimesNS []uint64

	Disks []Disk
	Nets  []Net
}

// MemStatus classifies the guest memory statistics as of now.
func (d *Domain) MemStatus(now time.Time) MemStatus {
	if !d.State.Active() || d.MemCurrentKiB == 0 {
		return MemUnknown
	}
	if !d.MemUsableSet && !d.MemUnusedSet {
		return MemUnknown
	}
	if d.MemLastUpdateZero {
		return MemUnknown
	}
	if !d.MemLastUpdate.IsZero() && now.Sub(d.MemLastUpdate) > MemStaleAfter {
		return MemStale
	}
	return MemFresh
}

// MemStatsAge returns how old the guest memory statistics are, or zero when
// libvirt did not say.
func (d *Domain) MemStatsAge(now time.Time) time.Duration {
	if d.MemLastUpdate.IsZero() || now.Before(d.MemLastUpdate) {
		return 0
	}
	return now.Sub(d.MemLastUpdate)
}

// MemUsedKiB is the guest's used memory: configured memory minus the memory
// the guest could still hand out without swapping (balloon "usable", i.e. the
// guest kernel's MemAvailable), falling back to balloon "unused" (MemFree).
//
// It is only meaningful when MemStatus is not MemUnknown; otherwise it returns
// zero. Note it is NOT current-minus-"available": libvirt's "available" is the
// total memory the guest sees and is constant, so that difference only measures
// kernel/firmware reservations and never moves with guest load.
func (d *Domain) MemUsedKiB() uint64 {
	if !d.State.Active() || d.MemCurrentKiB == 0 {
		return 0
	}
	var free uint64
	switch {
	case d.MemUsableSet:
		free = d.MemUsableKiB
	case d.MemUnusedSet:
		free = d.MemUnusedKiB
	default:
		return 0
	}
	if free > d.MemCurrentKiB {
		return 0
	}
	return d.MemCurrentKiB - free
}

// MemAvailKiB is the memory the guest could still use without swapping:
// balloon "usable", falling back to "unused". Zero without statistics.
func (d *Domain) MemAvailKiB() uint64 {
	switch {
	case d.MemUsableSet:
		return d.MemUsableKiB
	case d.MemUnusedSet:
		return d.MemUnusedKiB
	}
	return 0
}

func (d *Domain) DiskReadBytes() (v uint64) {
	for _, disk := range d.Disks {
		v += disk.RDBytes
	}
	return v
}

func (d *Domain) DiskWriteBytes() (v uint64) {
	for _, disk := range d.Disks {
		v += disk.WRBytes
	}
	return v
}

func (d *Domain) NetRXBytes() (v uint64) {
	for _, n := range d.Nets {
		v += n.RXBytes
	}
	return v
}

func (d *Domain) NetTXBytes() (v uint64) {
	for _, n := range d.Nets {
		v += n.TXBytes
	}
	return v
}

// Info is domain metadata that changes rarely; the client caches it and
// refreshes it periodically and whenever the domain's state changes.
type Info struct {
	ID              int // -1 for inactive domains
	UUID            string
	Autostart       bool
	Persistent      bool
	MaxMemoryKiB    uint64
	ConfiguredVCPUs int
	OSType          string

	// Err is non-empty when some metadata could not be read. The affected
	// fields hold zero values and must be shown as unknown, not as "off".
	Err string
}

// IfaceInfo is a guest or host-side interface address record.
type IfaceInfo struct {
	Name  string
	MAC   string
	Addrs []string
}

// Snapshot is one complete collection cycle.
type Snapshot struct {
	At      time.Time
	Domains []*Domain
	ByName  map[string]*Domain
	// Warnings lists per-domain problems that did not fail the whole
	// collection (for example a domain whose name could not be read and was
	// therefore skipped).
	Warnings []string
}

func NewSnapshot(at time.Time, domains []*Domain, warnings []string) *Snapshot {
	byName := make(map[string]*Domain, len(domains))
	for _, d := range domains {
		byName[d.Name] = d
	}
	return &Snapshot{At: at, Domains: domains, ByName: byName, Warnings: warnings}
}

func (s *Snapshot) Counts() (active, total int) {
	if s == nil {
		return 0, 0
	}
	for _, d := range s.Domains {
		if d.State.Active() {
			active++
		}
	}
	return active, len(s.Domains)
}
