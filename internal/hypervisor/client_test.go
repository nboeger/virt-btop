package hypervisor

import (
	"errors"
	"fmt"
	"testing"
	"time"

	libvirt "libvirt.org/go/libvirt"
)

func TestDomainFromStatsMemory(t *testing.T) {
	st := &libvirt.DomainStats{
		State: &libvirt.DomainStatsState{StateSet: true, State: libvirt.DOMAIN_RUNNING},
		Cpu:   &libvirt.DomainStatsCPU{TimeSet: true, Time: 42},
		Balloon: &libvirt.DomainStatsBalloon{
			CurrentSet: true, Current: 1000,
			MaximumSet: true, Maximum: 2000,
			UsableSet: true, Usable: 400,
			UnusedSet: true, Unused: 300,
			AvailableSet: true, Available: 950, // must be ignored for "used"
			RssSet: true, Rss: 555,
			LastUpdateSet: true, LastUpdate: uint64(epoch.Add(-3 * time.Second).Unix()),
		},
		Vcpu: []libvirt.DomainStatsVcpu{
			{StateSet: true, State: libvirt.VCPU_RUNNING, TimeSet: true, Time: 10},
			{StateSet: true, State: libvirt.VCPU_RUNNING, TimeSet: true, Time: 20},
		},
		Block: []libvirt.DomainStatsBlock{{Name: "vda", RdBytes: 1, WrBytes: 2, Capacity: 3, Allocation: 4}},
		Net:   []libvirt.DomainStatsNet{{Name: "vnet0", RxBytes: 5, TxBytes: 6}},
	}
	d := domainFromStats("vm", st)

	if d.State != StateRunning || d.CPUTimeNS != 42 {
		t.Fatalf("state/cpu = %v/%d", d.State, d.CPUTimeNS)
	}
	if d.MemCurrentKiB != 1000 || d.MemMaximumKiB != 2000 || d.MemRSSKiB != 555 {
		t.Fatalf("memory fields: %+v", d)
	}
	if !d.MemUsableSet || !d.MemUnusedSet {
		t.Fatal("Set flags must be carried through")
	}
	if got := d.MemUsedKiB(); got != 600 {
		t.Fatalf("used = %d, want 600", got)
	}
	if got := d.MemStatus(epoch); got != MemFresh {
		t.Fatalf("status = %v, want fresh", got)
	}
	if d.VCPUs != 2 || len(d.VCPUTimesNS) != 2 || d.VCPUTimesNS[1] != 20 {
		t.Fatalf("vcpus: %+v", d)
	}
	if len(d.Disks) != 1 || d.Disks[0].Capacity != 3 || len(d.Nets) != 1 || d.Nets[0].TXBytes != 6 {
		t.Fatalf("disks/nets: %+v %+v", d.Disks, d.Nets)
	}
}

// A guest without balloon statistics reports only current/maximum.
func TestDomainFromStatsNoGuestMemoryStats(t *testing.T) {
	st := &libvirt.DomainStats{
		State: &libvirt.DomainStatsState{StateSet: true, State: libvirt.DOMAIN_RUNNING},
		Balloon: &libvirt.DomainStatsBalloon{
			CurrentSet: true, Current: 2097152,
			MaximumSet: true, Maximum: 2097152,
			RssSet: true, Rss: 1234,
		},
	}
	d := domainFromStats("bsd", st)
	if d.MemUsableSet || d.MemUnusedSet {
		t.Fatal("absent stats must not be marked set")
	}
	if got := d.MemStatus(epoch); got != MemUnknown {
		t.Fatalf("status = %v, want unknown", got)
	}
	if got := d.MemUsedKiB(); got != 0 {
		t.Fatalf("used = %d, want 0", got)
	}
}

func TestDomainFromStatsLastUpdateZero(t *testing.T) {
	st := &libvirt.DomainStats{
		State: &libvirt.DomainStatsState{StateSet: true, State: libvirt.DOMAIN_RUNNING},
		Balloon: &libvirt.DomainStatsBalloon{
			CurrentSet: true, Current: 1000,
			UnusedSet: true, Unused: 100,
			LastUpdateSet: true, LastUpdate: 0,
		},
	}
	d := domainFromStats("vm", st)
	if got := d.MemStatus(epoch); got != MemUnknown {
		t.Fatalf("status = %v, want unknown when last-update is 0", got)
	}
}

func TestIsPermissionDenied(t *testing.T) {
	denied := []error{
		libvirt.Error{Code: libvirt.ERR_AUTH_FAILED},
		libvirt.Error{Code: libvirt.ERR_ACCESS_DENIED},
		libvirt.Error{Code: libvirt.ERR_OPERATION_DENIED},
		libvirt.Error{Code: libvirt.ERR_AUTH_UNAVAILABLE},
		libvirt.Error{Code: libvirt.ERR_SYSTEM_ERROR, Message: "Failed to connect socket to '/run/libvirt/libvirt-sock': Permission denied"},
		fmt.Errorf("connect qemu:///system: %w", libvirt.Error{Code: libvirt.ERR_AUTH_FAILED}),
	}
	for i, err := range denied {
		if !IsPermissionDenied(err) {
			t.Errorf("case %d (%v) should be permission denied", i, err)
		}
	}
	other := []error{
		nil,
		errors.New("permission denied"), // plain text is not a libvirt error
		libvirt.Error{Code: libvirt.ERR_NO_DOMAIN},
		libvirt.Error{Code: libvirt.ERR_SYSTEM_ERROR, Message: "Connection refused"},
		libvirt.Error{Code: libvirt.ERR_NO_CONNECT, Message: "No such file or directory"},
	}
	for i, err := range other {
		if IsPermissionDenied(err) {
			t.Errorf("case %d (%v) should not be permission denied", i, err)
		}
	}
}

func TestAppendUniqueAndFilters(t *testing.T) {
	got := appendUnique([]string{"a", "b"}, "b", "c", "a", "d")
	want := []string{"a", "b", "c", "d"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("appendUnique = %v, want %v", got, want)
	}
	if !isLoopback("") || !isLoopback("00:00:00:00:00:00") || isLoopback("52:54:00:aa:bb:cc") {
		t.Fatal("isLoopback wrong")
	}
	if !isNoisyAddr("127.0.0.1") || !isNoisyAddr("FE80::1") || isNoisyAddr("192.168.1.2") {
		t.Fatal("isNoisyAddr wrong")
	}
}
