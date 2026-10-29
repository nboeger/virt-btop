package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	libvirt "libvirt.org/go/libvirt"

	"github.com/nboeger/virt-btop/internal/hypervisor"
	"github.com/nboeger/virt-btop/internal/metrics"
)

func runArgs(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := runArgs("--version")
	if code != 0 || !strings.Contains(out, version) {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// Regression: out-of-range intervals used to be silently clamped.
func TestIntervalOutOfRangeIsRejectedNotClamped(t *testing.T) {
	for _, iv := range []string{"50ms", "249ms", "11s", "1h", "-1s", "0s"} {
		code, _, errOut := runArgs("-i", iv, "--once")
		if code != 2 {
			t.Errorf("-i %s: exit code %d, want 2", iv, code)
		}
		if !strings.Contains(errOut, "invalid interval") {
			t.Errorf("-i %s: stderr %q should explain the problem", iv, errOut)
		}
	}
}

func TestBadSortAndFilterAreRejected(t *testing.T) {
	if code, _, e := runArgs("--sort", "bogus"); code != 2 || !strings.Contains(e, "--sort") {
		t.Errorf("sort: code=%d stderr=%q", code, e)
	}
	if code, _, e := runArgs("--filter", "bogus"); code != 2 || !strings.Contains(e, "--filter") {
		t.Errorf("filter: code=%d stderr=%q", code, e)
	}
	if code, _, _ := runArgs("--no-such-flag"); code != 2 {
		t.Errorf("unknown flag should exit 2, got %d", code)
	}
}

func TestConnectFailureExitsNonZeroWithMessage(t *testing.T) {
	code, _, errOut := runArgs("-c", "qemu+unix:///system?socket=/nonexistent/libvirt-sock", "--once")
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut, "virt-btop:") {
		t.Fatalf("stderr = %q", errOut)
	}
	// Not a permission problem, so no group advice.
	if strings.Contains(errOut, "usermod") {
		t.Fatalf("irrelevant group hint shown: %q", errOut)
	}
}

func TestConnectHint(t *testing.T) {
	denied := libvirt.Error{Code: libvirt.ERR_AUTH_FAILED}
	if h := connectHint(denied, true); !strings.Contains(h, "usermod") || !strings.Contains(h, "libvirt group") {
		t.Errorf("read-write denial should explain the group: %q", h)
	}
	// Regression: the group advice used to be shown even for read-only runs.
	h := connectHint(denied, false)
	if strings.Contains(h, "usermod") {
		t.Errorf("read-only denial must not tell the user to join a group: %q", h)
	}
	if h == "" {
		t.Error("read-only denial should still give a hint")
	}
	// Regression: matching on message text misfired; only real libvirt codes count.
	if h := connectHint(fmt.Errorf("something about permission"), true); h != "" {
		t.Errorf("plain text must not trigger a hint: %q", h)
	}
	if h := connectHint(libvirt.Error{Code: libvirt.ERR_NO_DOMAIN}, true); h != "" {
		t.Errorf("unrelated libvirt error got a hint: %q", h)
	}
}

func TestMemCell(t *testing.T) {
	at := time.Unix(1700000000, 0)
	mk := func(mut func(*hypervisor.Domain)) (*hypervisor.Domain, metrics.Rates) {
		d := &hypervisor.Domain{
			Name: "vm", State: hypervisor.StateRunning, VCPUs: 1,
			MemCurrentKiB: 1 << 20, MemUsableKiB: 1 << 19, MemUsableSet: true,
			MemLastUpdate: at.Add(-time.Second),
		}
		mut(d)
		return d, metrics.Compute(nil, metrics.CountersOf(d, at), d, 4)
	}
	d, r := mk(func(*hypervisor.Domain) {})
	if got := memCell(d, r); got != "512M/1.00G" {
		t.Errorf("fresh = %q", got)
	}
	d, r = mk(func(d *hypervisor.Domain) { d.MemLastUpdate = at.Add(-time.Hour) })
	if got := memCell(d, r); !strings.HasSuffix(got, "(stale)") {
		t.Errorf("stale = %q", got)
	}
	d, r = mk(func(d *hypervisor.Domain) { d.MemUsableSet = false })
	if got := memCell(d, r); got != "n/a/1.00G" {
		t.Errorf("unknown = %q (must not claim usage)", got)
	}
}
