package metrics

import (
	"strings"
	"testing"
	"time"
)

// Regression: guest and guest_nice are already included in user and nice, so
// summing all ten fields double-counts VM time. These are real deltas from a
// busy hypervisor host (over ~3s): the old code reported 19.8% busy, the
// correct figure is 14.2%.
func TestParseCPUStatExcludesGuestTime(t *testing.T) {
	before := "cpu  1000 0 200 8000 0 0 0 0 600 0\ncpu0 1 1 1 1 1 1 1 1 1 1\n"
	after := "cpu  2093 0 411 16049 1 0 25 0 1259 0\n"

	t0, i0, err := parseCPUStat([]byte(before))
	if err != nil {
		t.Fatal(err)
	}
	t1, i1, err := parseCPUStat([]byte(after))
	if err != nil {
		t.Fatal(err)
	}
	dTotal, dIdle := float64(t1-t0), float64(i1-i0)
	busy := (dTotal - dIdle) / dTotal * 100
	if busy < 14.0 || busy > 14.4 {
		t.Fatalf("busy = %.2f%%, want about 14.2%% (guest time must not be double counted)", busy)
	}

	// And prove the guest columns are what made the difference.
	if total, _, _ := parseCPUStat([]byte("cpu 10 0 10 70 10 0 0 0 500 500\n")); total != 100 {
		t.Fatalf("total = %d, want 100: guest/guest_nice must be ignored", total)
	}
}

func TestParseCPUStatIdleIncludesIOWait(t *testing.T) {
	total, idle, err := parseCPUStat([]byte("cpu 10 0 10 70 10 0 0 0 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if total != 100 || idle != 80 {
		t.Fatalf("total=%d idle=%d, want 100/80", total, idle)
	}
}

func TestParseCPUStatOldKernelFewFields(t *testing.T) {
	// Kernels before 2.6.11 only had four fields.
	total, idle, err := parseCPUStat([]byte("cpu 10 0 10 80\n"))
	if err != nil || total != 100 || idle != 80 {
		t.Fatalf("total=%d idle=%d err=%v", total, idle, err)
	}
}

func TestParseCPUStatErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":        "",
		"no aggregate": "cpu0 1 2 3 4\n",
		"too short":    "cpu 1 2\n",
		"not a number": "cpu 1 x 3 4\n",
	} {
		if _, _, err := parseCPUStat([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseMemInfo(t *testing.T) {
	in := "MemTotal:       131072 kB\nMemFree:         1000 kB\nMemAvailable:   65536 kB\nBuffers: 10 kB\nCached: 20 kB\n"
	total, avail, err := parseMemInfo([]byte(in))
	if err != nil || total != 131072 || avail != 65536 {
		t.Fatalf("total=%d avail=%d err=%v", total, avail, err)
	}
}

func TestParseMemInfoFallbackWithoutMemAvailable(t *testing.T) {
	in := "MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 10 kB\nCached: 20 kB\n"
	_, avail, err := parseMemInfo([]byte(in))
	if err != nil || avail != 130 {
		t.Fatalf("avail=%d err=%v, want 130", avail, err)
	}
}

func TestParseMemInfoErrors(t *testing.T) {
	if _, _, err := parseMemInfo([]byte("MemFree: 1 kB\n")); err == nil {
		t.Error("missing MemTotal should error")
	}
	if _, _, err := parseMemInfo([]byte("MemTotal: lots kB\n")); err == nil {
		t.Error("bad number should error")
	}
}

func TestParseLoadAvgAndUptime(t *testing.T) {
	l1, l5, l15, err := parseLoadAvg([]byte("1.88 1.31 1.17 2/900 12345\n"))
	if err != nil || l1 != 1.88 || l5 != 1.31 || l15 != 1.17 {
		t.Fatalf("load = %v %v %v err=%v", l1, l5, l15, err)
	}
	if _, _, _, err := parseLoadAvg([]byte("1.0 2.0")); err == nil {
		t.Error("short loadavg should error")
	}
	if _, _, _, err := parseLoadAvg([]byte("a b c")); err == nil {
		t.Error("non-numeric loadavg should error")
	}

	up, err := parseUptime([]byte("5025.50 40000.00\n"))
	if err != nil || up != 5025500*time.Millisecond {
		t.Fatalf("uptime = %v err=%v", up, err)
	}
	if _, err := parseUptime([]byte("")); err == nil {
		t.Error("empty uptime should error")
	}
}

func TestSampleOnThisHost(t *testing.T) {
	h := NewHostSampler()
	first := h.Sample()
	if first.Err != nil && !strings.Contains(first.Err.Error(), "/proc") {
		t.Fatalf("unexpected error shape: %v", first.Err)
	}
	if first.Err != nil {
		t.Skipf("/proc unavailable: %v", first.Err)
	}
	if first.MemTotalKiB == 0 || first.Cores == 0 {
		t.Fatalf("empty sample: %+v", first)
	}
	second := h.Sample()
	if second.CPUPercent < 0 || second.CPUPercent > 100 {
		t.Fatalf("cpu%% out of range: %v", second.CPUPercent)
	}
}
