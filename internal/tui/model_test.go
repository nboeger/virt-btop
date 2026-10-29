package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/nboeger/virt-btop/internal/hypervisor"
)

type fakeSource struct {
	ifaces []hypervisor.IfaceInfo
}

func (f *fakeSource) Collect() (*hypervisor.Snapshot, error)            { return nil, nil }
func (f *fakeSource) Interfaces(string) ([]hypervisor.IfaceInfo, error) { return f.ifaces, nil }
func (f *fakeSource) InvalidateInfos()                                  {}
func (f *fakeSource) URI() string                                       { return "qemu:///test" }
func (f *fakeSource) ReadWrite() bool                                   { return false }

func testDomain(name string, state hypervisor.State, vcpus int, cpuNS, memCur, memUsable uint64) *hypervisor.Domain {
	times := make([]uint64, vcpus)
	for i := range times {
		times[i] = cpuNS / uint64(vcpus)
	}
	return &hypervisor.Domain{
		Name:          name,
		State:         state,
		VCPUs:         vcpus,
		CPUTimeNS:     cpuNS,
		VCPUTimesNS:   times,
		MemCurrentKiB: memCur,
		MemMaximumKiB: memCur,
		MemUsableKiB:  memUsable,
		MemUsableSet:  state.Active(),
		MemRSSKiB:     memCur / 2,
		Disks: []hypervisor.Disk{{
			Name: "vda", Capacity: 20 << 30, Allocation: 10 << 30,
			RDBytes: 1 << 20, WRBytes: 2 << 20,
		}},
		Nets: []hypervisor.Net{{Name: "vnet0", RXBytes: 1 << 20, TXBytes: 2 << 20}},
		Info: hypervisor.Info{
			ID: 1, UUID: "66ffe9e3-a12b-4878-bd98-3c7f81bd987b",
			Autostart: true, Persistent: true, ConfiguredVCPUs: vcpus,
			MaxMemoryKiB: memCur, OSType: "hvm",
		},
	}
}

func testSnapshot(at time.Time, bump bool) *hypervisor.Snapshot {
	cpuDelta := uint64(0)
	if bump {
		cpuDelta = 1
	}
	domains := []*hypervisor.Domain{
		testDomain("vm-cpu", hypervisor.StateRunning, 16, 100_000_000_000+12_000_000_000*cpuDelta, 32<<20, 30<<20),
		testDomain("vm-mem", hypervisor.StateRunning, 4, 50_000_000_000+2_000_000_000*cpuDelta, 16<<20, 4<<20),
		testDomain("vm-pause", hypervisor.StatePaused, 2, 20_000_000_000, 4<<20, 3<<20),
		testDomain("vm-off", hypervisor.StateShutoff, 2, 0, 8<<20, 0),
	}
	if bump {
		// vm-cpu: 16 vCPU * 0.75s = 12 CPU-seconds over 1s = 75%.
		for i := range domains[0].VCPUTimesNS {
			domains[0].VCPUTimesNS[i] += 750_000_000
		}
		// vm-mem: 4 vCPU * 0.5s = 2 CPU-seconds over 1s = 50%.
		for i := range domains[1].VCPUTimesNS {
			domains[1].VCPUTimesNS[i] += 500_000_000
		}
		domains[0].Nets[0].RXBytes += 1 << 20
		domains[0].Disks[0].RDBytes += 2 << 20
		domains[1].Nets[0].RXBytes += 512 << 10
	}
	for _, d := range domains {
		d.MemLastUpdate = at.Add(-time.Second) // fresh guest statistics
	}
	return hypervisor.NewSnapshot(at, domains, nil)
}

func newTestModel(t *testing.T) *Model {
	t.Helper()
	t0 := time.Unix(1700000000, 0)
	initial := testSnapshot(t0, false)
	m := New(&fakeSource{}, initial, Options{Interval: time.Second, Sort: SortCPU, Filter: FilterAll})
	m.pal = newPalette(termenv.Ascii)
	m.ingest(testSnapshot(t0.Add(time.Second), true), 1.0)
	m.lastAt = t0.Add(time.Second)
	m.width, m.height = 130, 21
	return m
}

func assertViewFits(t *testing.T, m *Model) {
	t.Helper()
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("view has %d lines, want %d", len(lines), m.height)
	}
	widths := lineWidths(view)
	for i, w := range widths {
		if w != m.width {
			t.Fatalf("line %d width = %d, want %d: %q", i, w, m.width, stripANSI(lines[i]))
		}
	}
}

func TestModelViewFits(t *testing.T) {
	m := newTestModel(t)
	assertViewFits(t, m)

	m.width, m.height = 170, 40
	assertViewFits(t, m)

	m.width, m.height = 100, 20
	assertViewFits(t, m)

	m.width, m.height = 130, 21
	m.showHelp = true
	assertViewFits(t, m)

	m.showHelp = false
	m.showDetail = true
	m.selected = "vm-cpu"
	assertViewFits(t, m)
}

func TestVisibleDomains(t *testing.T) {
	m := newTestModel(t)
	names := func(list []*hypervisor.Domain) string {
		var out []string
		for _, d := range list {
			out = append(out, d.Name)
		}
		return strings.Join(out, ",")
	}

	if got := names(m.visibleDomains()); got != "vm-cpu,vm-mem,vm-pause,vm-off" {
		t.Fatalf("cpu sort = %s", got)
	}
	m.filter = FilterRunning
	if got := names(m.visibleDomains()); got != "vm-cpu,vm-mem" {
		t.Fatalf("running filter = %s", got)
	}
	m.filter = FilterPaused
	if got := names(m.visibleDomains()); got != "vm-pause" {
		t.Fatalf("paused filter = %s", got)
	}
	m.filter = FilterStopped
	if got := names(m.visibleDomains()); got != "vm-off" {
		t.Fatalf("stopped filter = %s", got)
	}

	m.filter = FilterAll
	m.search = "mem"
	if got := names(m.visibleDomains()); got != "vm-mem" {
		t.Fatalf("search = %s", got)
	}
	m.search = ""
	m.sortKey = SortName
	if got := names(m.visibleDomains()); got != "vm-cpu,vm-mem,vm-off,vm-pause" {
		t.Fatalf("name sort = %s", got)
	}
	m.reverse = true
	if got := names(m.visibleDomains()); got != "vm-pause,vm-off,vm-mem,vm-cpu" {
		t.Fatalf("reversed name sort = %s", got)
	}
	m.reverse = false
	m.sortKey = SortState
	if got := names(m.visibleDomains()); got != "vm-cpu,vm-mem,vm-pause,vm-off" {
		t.Fatalf("state sort = %s", got)
	}
}

func TestSearchKeys(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.searchMode {
		t.Fatal("search mode not entered")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("mem")})
	if m.search != "mem" {
		t.Fatalf("search = %q", m.search)
	}
	if m.selected != "vm-mem" {
		t.Fatalf("selected = %q, want vm-mem", m.selected)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.searchMode || m.search != "" {
		t.Fatalf("search not cleared: mode=%v search=%q", m.searchMode, m.search)
	}
}

func TestSortArrow(t *testing.T) {
	m := newTestModel(t)
	m.sortKey = SortCPU
	m.reverse = false
	if got := m.sortArrow(); got != "▾" {
		t.Fatalf("cpu arrow = %q", got)
	}
	m.reverse = true
	if got := m.sortArrow(); got != "▴" {
		t.Fatalf("cpu reverse arrow = %q", got)
	}
	m.sortKey = SortName
	m.reverse = false
	if got := m.sortArrow(); got != "▴" {
		t.Fatalf("name arrow = %q", got)
	}
	m.reverse = true
	if got := m.sortArrow(); got != "▾" {
		t.Fatalf("name reverse arrow = %q", got)
	}
}

func TestMouseClickSelectsRow(t *testing.T) {
	m := newTestModel(t)
	l := ComputeLayout(m.width, m.height)
	g := l.listGeometry()
	vis := m.visibleDomains()
	if len(vis) < 2 {
		t.Fatal("need at least two visible domains")
	}
	m.Update(tea.MouseMsg{
		X:      l.RightX + 5,
		Y:      g.RowsY + 1,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if m.selected != vis[1].Name {
		t.Fatalf("selected = %q, want %q", m.selected, vis[1].Name)
	}
}

func TestOverlayRect(t *testing.T) {
	for _, size := range [][2]int{{130, 21}, {170, 40}} {
		l := ComputeLayout(size[0], size[1])
		x, y, w, h := overlayRect(l, 112, 28)
		if x < 0 || y < l.HeaderH || x+w > l.W || y+h > l.FooterY {
			t.Fatalf("overlay %v out of body: x=%d y=%d w=%d h=%d", size, x, y, w, h)
		}
	}
}

func TestListColumnsFit(t *testing.T) {
	for _, inner := range []int{84, 102, 120} {
		lc := computeListColumns(inner, true)
		last := lc.memX + lc.memW
		if lc.showDisk {
			last = lc.diskX + lc.diskW
		}
		if lc.showNet {
			last = lc.netX + lc.netW
		}
		if last > inner {
			t.Fatalf("inner %d: columns overflow to %d", inner, last)
		}
		if lc.nameW < 8 {
			t.Fatalf("inner %d: name width %d", inner, lc.nameW)
		}
	}
}

func TestListColumnsHideState(t *testing.T) {
	withState := computeListColumns(120, true)
	without := computeListColumns(120, false)
	if without.stateW != 0 {
		t.Fatalf("state width = %d, want 0", without.stateW)
	}
	if without.cpuBarW <= withState.cpuBarW || without.memBarW <= withState.memBarW {
		t.Fatalf("bars not widened: cpu %d->%d mem %d->%d",
			withState.cpuBarW, without.cpuBarW, withState.memBarW, without.memBarW)
	}
	if without.nameW <= withState.nameW {
		t.Fatalf("name not widened: %d -> %d", withState.nameW, without.nameW)
	}
	if last := without.netX + without.netW; last > without.innerW {
		t.Fatalf("columns overflow to %d > %d", last, without.innerW)
	}
}
