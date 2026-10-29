package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/nboeger/virt-btop/internal/hypervisor"
	"github.com/nboeger/virt-btop/internal/metrics"
)

// countingSource records interface lookups.
type countingSource struct {
	calls int
	err   error
	out   []hypervisor.IfaceInfo
}

func (c *countingSource) Collect() (*hypervisor.Snapshot, error) { return nil, nil }
func (c *countingSource) Interfaces(string) ([]hypervisor.IfaceInfo, error) {
	c.calls++
	return c.out, c.err
}
func (c *countingSource) InvalidateInfos() {}
func (c *countingSource) URI() string      { return "test" }
func (c *countingSource) ReadWrite() bool  { return false }

// runLookup executes the command ensureIface returned, like Bubble Tea would.
func runLookup(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	m.Update(cmd())
}

// Regression: a failing lookup used to be retried on every poll (10 calls in
// 10 polls), each holding the client lock.
func TestFailedInterfaceLookupIsNotRetriedEveryPoll(t *testing.T) {
	src := &countingSource{err: errors.New("boom")}
	m := newTestModel(t)
	m.client = src
	m.selected = "vm-cpu"
	m.showDetail = true

	t0 := time.Unix(1700000010, 0)
	for i := 0; i < 10; i++ {
		cmd := m.handlePoll(pollMsg{snap: testSnapshot(t0.Add(time.Duration(i)*time.Second), true)})
		runLookup(m, cmd)
	}
	if src.calls != 1 {
		t.Fatalf("Interfaces called %d times in 10 polls, want exactly 1", src.calls)
	}
	if e := m.ifaceCache["vm-cpu"]; e.err == "" {
		t.Fatal("failure should be cached with its error message")
	}

	// After the refresh interval it is allowed to try again.
	e := m.ifaceCache["vm-cpu"]
	e.at = time.Now().Add(-ifaceRefresh - time.Second)
	m.ifaceCache["vm-cpu"] = e
	runLookup(m, m.ensureIface("vm-cpu"))
	if src.calls != 2 {
		t.Fatalf("calls = %d, want 2 after the cache expired", src.calls)
	}
}

func TestSuccessfulInterfaceLookupIsCached(t *testing.T) {
	src := &countingSource{out: []hypervisor.IfaceInfo{{Name: "eth0", Addrs: []string{"10.0.0.2/24"}}}}
	m := newTestModel(t)
	m.client = src
	m.selected = "vm-cpu"
	m.showDetail = true
	for i := 0; i < 5; i++ {
		runLookup(m, m.ensureIface("vm-cpu"))
	}
	if src.calls != 1 {
		t.Fatalf("calls = %d, want 1", src.calls)
	}
	if got := m.ifaceCache["vm-cpu"].ifaces; len(got) != 1 || got[0].Name != "eth0" {
		t.Fatalf("cached ifaces = %+v", got)
	}
}

// libvirt always rejects address lookups for stopped domains, so don't ask.
func TestNoInterfaceLookupForStoppedOrUnknownVM(t *testing.T) {
	src := &countingSource{}
	m := newTestModel(t)
	m.client = src
	m.filter = FilterAll
	m.showDetail = true
	for _, name := range []string{"vm-off", "does-not-exist", ""} {
		if cmd := m.ensureIface(name); cmd != nil {
			t.Fatalf("lookup scheduled for %q", name)
		}
	}
	if src.calls != 0 {
		t.Fatalf("calls = %d, want 0", src.calls)
	}
	m.snap = nil
	if cmd := m.ensureIface("vm-cpu"); cmd != nil {
		t.Fatal("lookup scheduled without a snapshot")
	}
}

// Regression: View() used to set m.showDetail = false when the selected VM was
// missing. Rendering must be free of side effects.
func TestViewDoesNotMutateModel(t *testing.T) {
	m := newTestModel(t)
	m.showDetail = true
	m.selected = "ghost" // not in the snapshot; ensureSelection was not run
	before := *m
	for i := 0; i < 3; i++ {
		_ = m.View()
	}
	if !m.showDetail || m.selected != "ghost" {
		t.Fatalf("View mutated the model: showDetail=%v selected=%q", m.showDetail, m.selected)
	}
	if m.sortKey != before.sortKey || m.filter != before.filter || m.search != before.search {
		t.Fatal("View changed view settings")
	}
	assertViewFits(t, m) // and it still draws a full frame
}

func TestDetailClosesWhenItsVMDisappears(t *testing.T) {
	m := newTestModel(t)
	m.selected = "vm-mem"
	m.showDetail = true

	t0 := time.Unix(1700000020, 0)
	snap := testSnapshot(t0, true)
	var kept []*hypervisor.Domain
	for _, d := range snap.Domains {
		if d.Name != "vm-mem" {
			kept = append(kept, d)
		}
	}
	m.handlePoll(pollMsg{snap: hypervisor.NewSnapshot(t0, kept, nil)})
	if m.showDetail {
		t.Fatal("detail overlay should close when its VM is gone")
	}
	if m.selected == "vm-mem" {
		t.Fatal("selection should move off the vanished VM")
	}
}

func TestPollWarningsAreSurfaced(t *testing.T) {
	m := newTestModel(t)
	snap := testSnapshot(time.Unix(1700000030, 0), true)
	snap.Warnings = []string{"domain #3 skipped: name lookup failed: boom"}
	m.handlePoll(pollMsg{snap: snap})
	if !m.statusErr || !strings.Contains(m.status, "name lookup failed") {
		t.Fatalf("status = %q (err=%v), want the warning shown", m.status, m.statusErr)
	}
}

func TestNilSnapshotIsReportedNotIgnored(t *testing.T) {
	m := newTestModel(t)
	m.handlePoll(pollMsg{})
	if !m.statusErr || m.status == "" {
		t.Fatalf("nil snapshot with no error should be reported; status=%q", m.status)
	}
}

func TestSlowPollIsReported(t *testing.T) {
	m := newTestModel(t)
	m.polling = true
	m.pollStarted = time.Now().Add(-pollSlowAfter - 2*time.Second)
	m.Update(tickMsg(time.Now()))
	if !m.statusErr || !strings.Contains(m.status, "not responding") {
		t.Fatalf("status = %q, want a not-responding warning", m.status)
	}
	// And the poll is not restarted while one is still in flight.
	if cmd := m.startPoll(); cmd != nil {
		t.Fatal("a second poll must not start while one is in flight")
	}
}

// Wide characters and control characters in VM names must not break the grid
// or inject escape sequences.
func TestHostileVMNamesKeepGridExactAndEscapeFree(t *testing.T) {
	m := newTestModel(t)
	t0 := time.Unix(1700000040, 0)
	snap := testSnapshot(t0, true)
	snap.Domains[0].Name = "虚拟机-🙂-ünï"
	snap.Domains[1].Name = "evil\x1b[31mred\x1b]0;title\x07\nnewline"
	m.handlePoll(pollMsg{snap: hypervisor.NewSnapshot(t0, snap.Domains, nil)})

	for _, size := range [][2]int{{130, 21}, {170, 40}} {
		m.width, m.height = size[0], size[1]
		assertViewFits(t, m)
	}
	m.selected = snap.Domains[1].Name
	m.showDetail = true
	assertViewFits(t, m)

	view := m.View()
	for _, bad := range []string{"\x1b[31mred", "\x1b]0;title", "\x07"} {
		if strings.Contains(view, bad) {
			t.Fatalf("VM name leaked a terminal sequence: %q", bad)
		}
	}
}

func TestTruncateMatchesWhatIsDrawn(t *testing.T) {
	for _, in := range []string{"虚拟机虚拟机虚拟机", "ab🙂cd", "plain-name"} {
		got := truncate(in, 6)
		if w := len([]rune(got)); w > 6 {
			t.Errorf("truncate(%q, 6) = %q (%d cells)", in, got, w)
		}
		if sanitize(got) != got {
			t.Errorf("truncate(%q) = %q still contains unsanitized runes", in, got)
		}
	}
}

func TestMemoryRenderingStates(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 170, 40
	t0 := time.Unix(1700000050, 0)
	snap := testSnapshot(t0, true)
	// vm-cpu: fresh (default). vm-mem: stale. vm-pause: no guest statistics.
	snap.ByName["vm-mem"].MemLastUpdate = t0.Add(-2 * time.Hour)
	snap.ByName["vm-pause"].MemUsableSet = false
	m.handlePoll(pollMsg{snap: snap})

	view := strings.Join(visibleLines(m.View()), "\n")
	if !strings.Contains(view, "— no stats") {
		t.Errorf("VM without guest stats should show \"— no stats\":\n%s", view)
	}
	if strings.Contains(view, "100%") {
		t.Errorf("a VM without stats must not render as 100%% used:\n%s", view)
	}
	// The box title covers only fresh VMs and says how many report.
	if !strings.Contains(view, "1/3 VMs") {
		t.Errorf("mem box should note only 1 of 3 VMs reports:\n%s", view)
	}

	// Detail overlay explains each state.
	m.filter = FilterAll
	m.selected = "vm-mem"
	m.showDetail = true
	detail := strings.Join(visibleLines(m.View()), "\n")
	if !strings.Contains(detail, "STALE 2h00m old") {
		t.Errorf("stale VM detail should state the age:\n%s", detail)
	}
	m.selected = "vm-pause"
	detail = strings.Join(visibleLines(m.View()), "\n")
	if !strings.Contains(detail, "no guest memory statistics") {
		t.Errorf("no-stats VM detail should say so:\n%s", detail)
	}
}

func TestMemBoxWhenNoVMReportsStats(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 170, 40
	t0 := time.Unix(1700000060, 0)
	snap := testSnapshot(t0, true)
	for _, d := range snap.Domains {
		d.MemUsableSet = false
	}
	m.handlePoll(pollMsg{snap: snap})
	view := strings.Join(visibleLines(m.View()), "\n")
	if !strings.Contains(view, "no guest stats") {
		t.Errorf("mem box should say no guest stats:\n%s", view)
	}
	if !strings.Contains(view, "n/a (no guest stats)") {
		t.Errorf("header should say memory is n/a:\n%s", view)
	}
}

func TestMetadataErrorsShownAsUnknownNotOff(t *testing.T) {
	m := newTestModel(t)
	m.selected = "vm-cpu"
	m.showDetail = true
	m.snap.ByName["vm-cpu"].Info.Err = "autostart: boom"
	m.snap.ByName["vm-cpu"].Info.Autostart = false
	detail := strings.Join(visibleLines(m.View()), "\n")
	if !strings.Contains(detail, "autostart ?") || !strings.Contains(detail, "persistent ?") {
		t.Errorf("failed metadata lookup must render as ?:\n%s", detail)
	}
	if !strings.Contains(detail, "metadata: autostart: boom") {
		t.Errorf("metadata error should be shown:\n%s", detail)
	}
}

func TestInactiveDomainShowsDashForID(t *testing.T) {
	m := newTestModel(t)
	m.filter = FilterAll
	m.selected = "vm-off"
	m.snap.ByName["vm-off"].Info.ID = -1
	m.showDetail = true
	detail := strings.Join(visibleLines(m.View()), "\n")
	if strings.Contains(detail, "id -1") {
		t.Errorf("inactive domain should not show id -1:\n%s", detail)
	}
	if !strings.Contains(detail, "addresses: VM not running") {
		t.Errorf("stopped VM should explain why there are no addresses:\n%s", detail)
	}
}

func TestHostErrNote(t *testing.T) {
	if hostErrNote(metrics.Host{}) != "" {
		t.Fatal("no error, no note")
	}
	if hostErrNote(metrics.Host{Err: errors.New("x")}) == "" {
		t.Fatal("host error should be noted")
	}
}

// Graphs for memory and network are 30% brighter than btop's defaults.
func TestMemAndNetGradientsAreBrightened(t *testing.T) {
	if got := scaleHex("#808080", 1.3); got != "#a6a6a6" {
		t.Fatalf("scaleHex(#808080, 1.3) = %s, want #a6a6a6", got)
	}
	if got := scaleHex("#ffffff", 1.3); got != "#ffffff" {
		t.Fatalf("white must stay white, got %s", got)
	}
	// A channel that would overflow must not lose the brightness gain: the
	// mean of #d9626d (140.7) should still rise by ~30% to ~183.
	r, g, b := hexRGB(scaleHex("#d9626d", 1.3))
	if r > 255 || g > 255 || b > 255 {
		t.Fatalf("channel overflow: %d %d %d", r, g, b)
	}
	if mean := float64(r+g+b) / 3; mean < 181 || mean > 185 {
		t.Fatalf("clipped color mean = %.1f, want about 183 (+30%%)", mean)
	}
	if got := scaleHex("#123456", 1); got != "#123456" {
		t.Fatalf("scale 1 must be identity, got %s", got)
	}

	p := termenv.TrueColor
	pal := newPalette(p)
	base := map[string]Gradient{
		"mem":      newGradient(p, "#592b26", "#d9626d", "#ff4769"),
		"download": newGradient(p, "#291f75", "#4f43a3", "#b0a9de"),
		"upload":   newGradient(p, "#620665", "#7d4180", "#dcafde"),
	}
	got := map[string]Gradient{"mem": pal.Mem, "download": pal.Download, "upload": pal.Upload}
	for name, g := range got {
		for _, f := range []float64{0, 0.5} {
			b := luminance(t, base[name].At(f))
			n := luminance(t, g.At(f))
			if n < b*1.25 || n > b*1.35 {
				t.Errorf("%s gradient at %.1f: brightness %.1f -> %.1f, want about +30%%", name, f, b, n)
			}
		}
	}
	// The CPU gradient is deliberately left alone.
	if luminance(t, pal.CPU.At(0.5)) != luminance(t, newGradient(p, "#77ca9b", "#cbc06c", "#dc4c4c").At(0.5)) {
		t.Error("CPU gradient should not have been brightened")
	}
}

func luminance(t *testing.T, c termenv.Color) float64 {
	t.Helper()
	rgb, ok := c.(termenv.RGBColor)
	if !ok {
		t.Fatalf("expected truecolor, got %T", c)
	}
	r, g, b := hexRGB(string(rgb))
	return float64(r+g+b) / 3
}
