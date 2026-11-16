package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nboeger/virt-btop/internal/hypervisor"
)

func TestSummarizeHostsWeightsCPUByCores(t *testing.T) {
	hosts := []hypervisor.HostStats{
		{Cores: 4, CPUValid: true, CPUPercent: 50,
			MemValid: true, MemTotalKiB: 1000, MemUsedKiB: 400},
		{Cores: 36, CPUValid: true, CPUPercent: 100,
			MemValid: true, MemTotalKiB: 9000, MemUsedKiB: 3000},
	}
	got := summarizeHosts(hosts, 3)
	if got.Reporting != 2 || got.Configured != 3 || got.Cores != 40 {
		t.Fatalf("summary = %+v", got)
	}
	approxHostFloat(t, got.CPUPercent, 95, "weighted cpu")
	if !got.CPUValid || !got.MemValid {
		t.Fatalf("validity flags = %+v", got)
	}
	if got.MemTotalKiB != 10000 || got.MemUsedKiB != 3400 {
		t.Fatalf("memory = %d/%d", got.MemUsedKiB, got.MemTotalKiB)
	}
}

func TestSummarizeHostsSkipsMissingSamples(t *testing.T) {
	got := summarizeHosts([]hypervisor.HostStats{
		{Cores: 8, CPUValid: false},
		{Cores: 16, CPUValid: true, CPUPercent: 25, MemTotalKiB: 1000, MemUsedKiB: 250, MemValid: true},
	}, 2)
	approxHostFloat(t, got.CPUPercent, 25, "cpu from the one valid host")
	if !got.CPUValid || !got.MemValid {
		t.Fatalf("summary = %+v", got)
	}
}

func TestHostHeaderAndOverlay(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 170, 40
	m.hostCount = 2
	m.localHost = true
	m.snap.Hosts = []hypervisor.HostStats{
		{Label: "local", Hostname: "here.example", Cores: 8,
			CPUValid: true, CPUPercent: 20,
			MemValid: true, MemTotalKiB: 8 << 20, MemUsedKiB: 2 << 20},
		{Label: "alpha", Cores: 32,
			CPUValid: true, CPUPercent: 80,
			MemValid: true, MemTotalKiB: 64 << 20, MemUsedKiB: 48 << 20},
	}
	assertViewFits(t, m)
	view := stripANSI(m.View())
	for _, want := range []string{"hosts 2", "40 cores", "68.0%", "local load"} {
		if !strings.Contains(view, want) {
			t.Fatalf("header missing %q:\n%s", want, view)
		}
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	if !m.showHosts {
		t.Fatal("H did not open the host overlay")
	}
	assertViewFits(t, m)
	view = stripANSI(m.View())
	for _, want := range []string{"here.example", "alpha", "80.0%"} {
		if !strings.Contains(view, want) {
			t.Fatalf("host overlay missing %q:\n%s", want, view)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.showHosts {
		t.Fatal("esc did not close the host overlay")
	}
}

func approxHostFloat(t *testing.T, got, want float64, label string) {
	t.Helper()
	if d := got - want; d < -1e-6 || d > 1e-6 {
		t.Fatalf("%s: got %.6f, want %.6f", label, got, want)
	}
}
