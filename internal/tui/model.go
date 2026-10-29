package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nboeger/virt-btop/internal/hypervisor"
	"github.com/nboeger/virt-btop/internal/metrics"
)

// DataSource is the subset of hypervisor.Client the UI needs. It is an
// interface so the view/model can be tested without a libvirt connection.
type DataSource interface {
	Collect() (*hypervisor.Snapshot, error)
	Interfaces(name string) ([]hypervisor.IfaceInfo, error)
	InvalidateInfos()
	URI() string
	ReadWrite() bool
}

type Options struct {
	Interval time.Duration
	Sort     SortKey
	Filter   Filter
}

type SortKey int

const (
	SortCPU SortKey = iota
	SortMem
	SortName
	SortDisk
	SortNet
	SortState
)

var sortKeys = []SortKey{SortCPU, SortMem, SortName, SortDisk, SortNet, SortState}

func (k SortKey) String() string {
	switch k {
	case SortCPU:
		return "cpu"
	case SortMem:
		return "mem"
	case SortName:
		return "name"
	case SortDisk:
		return "disk"
	case SortNet:
		return "net"
	case SortState:
		return "state"
	default:
		return "cpu"
	}
}

func sortKeyFromString(s string) (SortKey, bool) {
	for _, k := range sortKeys {
		if k.String() == strings.ToLower(s) {
			return k, true
		}
	}
	return SortCPU, false
}

type Filter int

const (
	FilterAll Filter = iota
	FilterRunning
	FilterPaused
	FilterStopped
)

func (f Filter) String() string {
	switch f {
	case FilterRunning:
		return "running"
	case FilterPaused:
		return "paused"
	case FilterStopped:
		return "stopped"
	default:
		return "all"
	}
}

func (f Filter) Match(d *hypervisor.Domain) bool {
	switch f {
	case FilterRunning:
		return d.State == hypervisor.StateRunning || d.State == hypervisor.StateBlocked
	case FilterPaused:
		return d.State == hypervisor.StatePaused || d.State == hypervisor.StatePMSuspended
	case FilterStopped:
		return !d.State.Active()
	default:
		return true
	}
}

func filterFromString(s string) (Filter, bool) {
	switch strings.ToLower(s) {
	case "all":
		return FilterAll, true
	case "running":
		return FilterRunning, true
	case "paused":
		return FilterPaused, true
	case "stopped":
		return FilterStopped, true
	default:
		return FilterAll, false
	}
}

type tickMsg time.Time

type pollMsg struct {
	snap *hypervisor.Snapshot
	err  error
}

// ifaceEntry caches an interface-address lookup, including a failed one, so a
// persistent error (for example "domain is not running") is not retried on
// every poll.
type ifaceEntry struct {
	ifaces []hypervisor.IfaceInfo
	err    string
	at     time.Time
}

const (
	// ifaceRefresh is how long a lookup result, success or failure, is reused.
	ifaceRefresh = 30 * time.Second
	// pollSlowAfter is how long a libvirt call may run before the footer warns
	// that libvirt is not responding.
	pollSlowAfter = 5 * time.Second
)

type ifacesMsg struct {
	name   string
	ifaces []hypervisor.IfaceInfo
	err    error
}

type Model struct {
	client DataSource
	pal    *Palette

	width, height int
	now           time.Time

	snap     *hypervisor.Snapshot
	domains  []*hypervisor.Domain
	rates    map[string]metrics.Rates
	prev     map[string]metrics.Counters
	combined metrics.Combined
	hist     *metrics.History

	host        metrics.Host
	hostSampler *metrics.HostSampler
	lastAt      time.Time

	uri       string
	readWrite bool

	sortKey SortKey
	reverse bool
	filter  Filter

	search     string
	searchMode bool
	selected   string

	paused      bool
	polling     bool
	pollStarted time.Time
	nextPoll    time.Time
	interval    time.Duration

	showHelp   bool
	showDetail bool

	ifaceCache   map[string]ifaceEntry
	ifacePending bool

	status      string
	statusUntil time.Time
	statusErr   bool
}

func New(client DataSource, initial *hypervisor.Snapshot, opts Options) *Model {
	m := &Model{
		client:      client,
		pal:         NewPalette(),
		now:         time.Now(),
		rates:       make(map[string]metrics.Rates),
		prev:        make(map[string]metrics.Counters),
		hist:        metrics.NewHistory(),
		hostSampler: metrics.NewHostSampler(),
		sortKey:     opts.Sort,
		filter:      opts.Filter,
		interval:    opts.Interval,
		ifaceCache:  make(map[string]ifaceEntry),
	}
	if m.interval <= 0 {
		m.interval = time.Second
	}
	if client != nil {
		m.uri = client.URI()
		m.readWrite = client.ReadWrite()
	}
	m.host = m.hostSampler.Sample()
	m.ingest(initial, 0)
	if initial != nil {
		m.lastAt = initial.At
	}
	m.nextPoll = m.now.Add(m.interval)
	return m
}

func (m *Model) Init() tea.Cmd { return tickCmd() }

func tickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.now = time.Now()
		if m.status != "" && m.now.After(m.statusUntil) {
			m.status = ""
			m.statusErr = false
		}
		cmds := []tea.Cmd{tickCmd()}
		if m.polling && m.now.Sub(m.pollStarted) > pollSlowAfter {
			// The libvirt call cannot be cancelled; say so rather than
			// leaving stale numbers on screen looking healthy.
			m.setStatus(fmt.Sprintf("libvirt not responding (%s)", m.now.Sub(m.pollStarted).Round(time.Second)), time.Second, true)
		}
		if !m.paused && !m.polling && !m.now.Before(m.nextPoll) {
			if cmd := m.startPoll(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
	case pollMsg:
		return m, m.handlePoll(msg)
	case ifacesMsg:
		m.ifacePending = false
		entry := ifaceEntry{ifaces: msg.ifaces, at: time.Now()}
		if msg.err != nil {
			entry.err = msg.err.Error()
		}
		m.ifaceCache[msg.name] = entry
		if m.showDetail && m.selected != msg.name {
			return m, m.ensureIface(m.selected)
		}
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	case tea.MouseMsg:
		return m, m.handleMouse(msg)
	default:
		return m, nil
	}
}

func (m *Model) startPoll() tea.Cmd {
	if m.client == nil || m.polling || m.paused {
		return nil
	}
	m.polling = true
	m.pollStarted = time.Now()
	return func() tea.Msg {
		snap, err := m.client.Collect()
		return pollMsg{snap: snap, err: err}
	}
}

func (m *Model) handlePoll(msg pollMsg) tea.Cmd {
	m.polling = false
	m.now = time.Now()
	if msg.err != nil {
		m.setStatus("libvirt: "+msg.err.Error(), 8*time.Second, true)
		m.nextPoll = m.now.Add(m.interval)
		// Drop the old counters so recovery does not average over the outage.
		m.prev = make(map[string]metrics.Counters)
		m.lastAt = time.Time{}
		return nil
	}
	if msg.snap == nil {
		m.setStatus("libvirt returned no data", 8*time.Second, true)
		m.nextPoll = m.now.Add(m.interval)
		return nil
	}
	dt := 0.0
	if !m.lastAt.IsZero() {
		dt = msg.snap.At.Sub(m.lastAt).Seconds()
	}
	m.host = m.hostSampler.Sample()
	m.ingest(msg.snap, dt)
	m.lastAt = msg.snap.At
	m.nextPoll = m.now.Add(m.interval)
	if m.statusErr {
		m.status = ""
		m.statusErr = false
	}
	if len(msg.snap.Warnings) > 0 {
		m.setStatus("libvirt: "+msg.snap.Warnings[0], 5*time.Second, true)
	}
	if m.showDetail {
		return m.ensureIface(m.selected)
	}
	return nil
}

func (m *Model) ingest(snap *hypervisor.Snapshot, dt float64) {
	if snap == nil {
		return
	}
	for _, d := range snap.Domains {
		cur := metrics.CountersOf(d, snap.At)
		var prev *metrics.Counters
		if old, ok := m.prev[d.Name]; ok {
			prev = &old
		}
		m.rates[d.Name] = metrics.Compute(prev, cur, d, m.host.Cores)
		m.prev[d.Name] = cur
	}
	for name := range m.prev {
		if _, ok := snap.ByName[name]; !ok {
			delete(m.prev, name)
			delete(m.rates, name)
		}
	}
	m.domains = snap.Domains
	m.snap = snap
	m.combined = metrics.Combine(snap.Domains, m.rates)
	vcpus, util, host := metrics.CPUCombine(snap.Domains, m.rates, dt, m.host.Cores)
	m.combined.VCPUs = vcpus
	m.combined.VCPUUtilPct = util
	m.combined.HostSharePct = host
	m.hist.Push(m.combined, snap.Domains, m.rates)
	m.ensureSelection()
}

func (m *Model) ensureSelection() {
	before := m.selected
	defer func() {
		// State changes belong here, not in View(). If the VM the detail
		// overlay was showing is gone or filtered out, close the overlay
		// rather than silently switching it to a different VM.
		if m.showDetail && (m.selected != before || m.selectedDomain() == nil) {
			m.showDetail = false
		}
	}()
	vis := m.visibleDomains()
	if len(vis) == 0 {
		m.selected = ""
		return
	}
	for _, d := range vis {
		if d.Name == m.selected {
			return
		}
	}
	m.selected = vis[0].Name
}

func stateOrder(s hypervisor.State) int {
	switch s {
	case hypervisor.StateRunning:
		return 0
	case hypervisor.StateBlocked:
		return 1
	case hypervisor.StatePaused:
		return 2
	case hypervisor.StatePMSuspended:
		return 3
	case hypervisor.StateShutdown:
		return 4
	case hypervisor.StateShutoff:
		return 5
	case hypervisor.StateCrashed:
		return 6
	default:
		return 7
	}
}

func (m *Model) visibleDomains() []*hypervisor.Domain {
	q := strings.ToLower(strings.TrimSpace(m.search))
	out := make([]*hypervisor.Domain, 0, len(m.domains))
	for _, d := range m.domains {
		if !m.filter.Match(d) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(d.Name), q) {
			continue
		}
		out = append(out, d)
	}

	SortDomains(out, m.rates, m.sortKey, m.reverse)
	return out
}

// SortDomains applies the TUI's ordering in place. It is exported so the
// --once text mode uses exactly the same ordering as the interactive view.
func SortDomains(domains []*hypervisor.Domain, rates map[string]metrics.Rates, key SortKey, reverse bool) {
	defaultAsc := key == SortName || key == SortState
	desc := defaultAsc == reverse // reverse flips the default direction

	sort.SliceStable(domains, func(i, j int) bool {
		a, b := domains[i], domains[j]
		ra, rb := rates[a.Name], rates[b.Name]
		cmp := 0
		switch key {
		case SortCPU:
			cmp = cmpFloat(ra.CPUPct, rb.CPUPct)
		case SortMem:
			cmp = cmpFloat(ra.MemPct, rb.MemPct)
		case SortDisk:
			cmp = cmpFloat(ra.DiskRDBps+ra.DiskWRBps, rb.DiskRDBps+rb.DiskWRBps)
		case SortNet:
			cmp = cmpFloat(ra.NetRXBps+ra.NetTXBps, rb.NetRXBps+rb.NetTXBps)
		case SortName:
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case SortState:
			cmp = cmpInt(stateOrder(a.State), stateOrder(b.State))
		}
		if key == SortName {
			if desc {
				return cmp > 0
			}
			return cmp < 0
		}
		if cmp == 0 {
			// Ties favour active VMs, then name. This also keeps the initial
			// selection (all rates still zero) off the stopped VMs.
			if sa, sb := stateOrder(a.State), stateOrder(b.State); sa != sb {
				return sa < sb
			}
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) < 0
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func indexOfDomain(list []*hypervisor.Domain, name string) int {
	for i, d := range list {
		if d.Name == name {
			return i
		}
	}
	return -1
}

func (m *Model) listStart(vis []*hypervisor.Domain, rows int) int {
	if len(vis) == 0 || rows <= 0 {
		return 0
	}
	idx := indexOfDomain(vis, m.selected)
	if idx < 0 {
		idx = 0
	}
	start := 0
	if idx >= rows {
		start = idx - rows + 1
	}
	if start+rows > len(vis) {
		start = len(vis) - rows
	}
	if start < 0 {
		start = 0
	}
	return start
}

func (m *Model) pageSize() int {
	if m.width < MinWidth || m.height < MinHeight {
		return 10
	}
	g := ComputeLayout(m.width, m.height).listGeometry()
	return clampInt(g.Rows, 1, 100)
}

func (m *Model) moveSelection(delta int) tea.Cmd {
	vis := m.visibleDomains()
	if len(vis) == 0 {
		m.selected = ""
		return nil
	}
	idx := indexOfDomain(vis, m.selected)
	if idx < 0 {
		idx = 0
	} else {
		idx = clampInt(idx+delta, 0, len(vis)-1)
	}
	m.selected = vis[idx].Name
	if m.showDetail {
		return m.ensureIface(m.selected)
	}
	return nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		return tea.Quit
	}
	if m.searchMode {
		return m.handleSearchKey(msg)
	}
	if key == "q" {
		return tea.Quit
	}
	if m.showHelp {
		m.showHelp = false
		return nil
	}
	if m.showDetail {
		switch key {
		case "esc", "enter":
			m.showDetail = false
			return nil
		case "up", "k":
			return m.moveSelection(-1)
		case "down", "j":
			return m.moveSelection(1)
		case "pgup":
			return m.moveSelection(-m.pageSize())
		case "pgdown":
			return m.moveSelection(m.pageSize())
		case "home":
			return m.moveSelection(-1 << 20)
		case "end":
			return m.moveSelection(1 << 20)
		case "?":
			m.showDetail = false
			m.showHelp = true
			return nil
		}
		return nil
	}

	switch key {
	case "?":
		m.showHelp = true
	case "up", "k":
		return m.moveSelection(-1)
	case "down", "j":
		return m.moveSelection(1)
	case "pgup":
		return m.moveSelection(-m.pageSize())
	case "pgdown":
		return m.moveSelection(m.pageSize())
	case "home":
		return m.moveSelection(-1 << 20)
	case "end":
		return m.moveSelection(1 << 20)
	case "s":
		m.cycleSort()
	case "r":
		m.reverse = !m.reverse
		m.setStatus("reverse: "+map[bool]string{true: "on", false: "off"}[m.reverse], 1500*time.Millisecond, false)
	case "f":
		m.cycleFilter()
	case "/":
		m.searchMode = true
		m.search = ""
		m.ensureSelection()
	case "p", " ":
		m.togglePause()
	case "+", "=":
		m.changeInterval(250 * time.Millisecond)
	case "-", "_":
		m.changeInterval(-250 * time.Millisecond)
	case "R":
		m.refresh()
	case "enter":
		return m.openDetail()
	case "1":
		m.setSort(SortCPU)
	case "2":
		m.setSort(SortMem)
	case "3":
		m.setSort(SortName)
	case "4":
		m.setSort(SortDisk)
	case "5":
		m.setSort(SortNet)
	case "6":
		m.setSort(SortState)
	}
	return nil
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.search = ""
		m.searchMode = false
		m.ensureSelection()
		return nil
	case tea.KeyEnter:
		m.searchMode = false
		return nil
	case tea.KeyBackspace:
		r := []rune(m.search)
		if len(r) > 0 {
			m.search = string(r[:len(r)-1])
		}
		m.ensureSelection()
		return nil
	case tea.KeySpace:
		m.search += " "
		m.ensureSelection()
		return nil
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				m.search += string(r)
			}
		}
		m.ensureSelection()
		return nil
	}
	if msg.String() == "ctrl+u" {
		m.search = ""
		m.ensureSelection()
	}
	return nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.showHelp {
		m.showHelp = false
		return nil
	}
	if m.searchMode || m.showDetail {
		return nil
	}
	if m.width < MinWidth || m.height < MinHeight {
		return nil
	}
	if msg.Action != tea.MouseActionPress {
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.moveSelection(-3)
	case tea.MouseButtonWheelDown:
		return m.moveSelection(3)
	case tea.MouseButtonLeft:
		l := ComputeLayout(m.width, m.height)
		g := l.listGeometry()
		if msg.X < l.RightX+1 || msg.X > l.RightX+l.RightW-2 {
			return nil
		}
		row := msg.Y - g.RowsY
		if row < 0 || row >= g.Rows {
			return nil
		}
		vis := m.visibleDomains()
		if len(vis) == 0 {
			return nil
		}
		start := m.listStart(vis, g.Rows)
		idx := start + row
		if idx < 0 || idx >= len(vis) {
			return nil
		}
		m.selected = vis[idx].Name
	}
	return nil
}

func (m *Model) cycleSort() {
	idx := 0
	for i, k := range sortKeys {
		if k == m.sortKey {
			idx = i
			break
		}
	}
	m.sortKey = sortKeys[(idx+1)%len(sortKeys)]
	m.ensureSelection()
	m.setStatus("sort: "+m.sortKey.String(), 1500*time.Millisecond, false)
}

func (m *Model) setSort(k SortKey) {
	m.sortKey = k
	m.ensureSelection()
	m.setStatus("sort: "+k.String(), 1500*time.Millisecond, false)
}

func (m *Model) cycleFilter() {
	m.filter = Filter((int(m.filter) + 1) % 4)
	m.ensureSelection()
	m.setStatus("filter: "+m.filter.String(), 1500*time.Millisecond, false)
}

func (m *Model) togglePause() {
	m.paused = !m.paused
	if m.paused {
		m.status = ""
		m.statusErr = false
		return
	}
	// Avoid a rate spike over the paused interval.
	m.prev = make(map[string]metrics.Counters)
	m.lastAt = time.Time{}
	m.nextPoll = time.Now()
	m.setStatus("resumed", 1500*time.Millisecond, false)
}

func (m *Model) changeInterval(d time.Duration) {
	m.interval += d
	if m.interval < 250*time.Millisecond {
		m.interval = 250 * time.Millisecond
	}
	if m.interval > 10*time.Second {
		m.interval = 10 * time.Second
	}
	m.nextPoll = time.Now().Add(m.interval)
	m.setStatus("interval "+m.interval.String(), 1500*time.Millisecond, false)
}

func (m *Model) refresh() {
	if m.client != nil {
		m.client.InvalidateInfos()
	}
	m.ifaceCache = make(map[string]ifaceEntry)
	m.nextPoll = time.Now()
	m.setStatus("refreshing metadata…", 3*time.Second, false)
}

func (m *Model) openDetail() tea.Cmd {
	if m.selected == "" {
		m.setStatus("no VM selected", 1500*time.Millisecond, false)
		return nil
	}
	m.showDetail = true
	m.showHelp = false
	return m.ensureIface(m.selected)
}

func (m *Model) ensureIface(name string) tea.Cmd {
	if name == "" || m.client == nil || !m.showDetail {
		return nil
	}
	// libvirt rejects address lookups for inactive domains, so don't ask.
	if m.snap == nil {
		return nil
	}
	if d := m.snap.ByName[name]; d == nil || !d.State.Active() {
		return nil
	}
	if e, ok := m.ifaceCache[name]; ok && time.Since(e.at) < ifaceRefresh {
		return nil
	}
	if m.ifacePending {
		return nil
	}
	m.ifacePending = true
	return func() tea.Msg {
		ifaces, err := m.client.Interfaces(name)
		return ifacesMsg{name: name, ifaces: ifaces, err: err}
	}
}

func (m *Model) setStatus(msg string, d time.Duration, isErr bool) {
	m.status = msg
	m.statusErr = isErr
	if d > 0 {
		m.statusUntil = time.Now().Add(d)
	} else {
		m.statusUntil = time.Now().Add(10 * time.Second)
	}
}

func (m *Model) selectedDomain() *hypervisor.Domain {
	if m.snap == nil || m.selected == "" {
		return nil
	}
	return m.snap.ByName[m.selected]
}

func (m *Model) selectedRate() metrics.Rates {
	if m.selected == "" {
		return metrics.Rates{}
	}
	return m.rates[m.selected]
}

func (m *Model) sortArrow() string {
	defaultAsc := m.sortKey == SortName || m.sortKey == SortState
	asc := defaultAsc != m.reverse
	if asc {
		return "▴"
	}
	return "▾"
}

func ParseSort(s string) (SortKey, bool)  { return sortKeyFromString(s) }
func ParseFilter(s string) (Filter, bool) { return filterFromString(s) }
