package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/nboeger/virt-btop/internal/hypervisor"
	"github.com/nboeger/virt-btop/internal/metrics"
)

type span struct {
	text  string
	style Style
}

func putSpans(s *Screen, x, y, maxW int, spans []span, bg termenv.Color) int {
	if maxW <= 0 {
		return x
	}
	used := 0
	for _, sp := range spans {
		if used >= maxW {
			break
		}
		st := sp.style
		if bg != nil {
			st.BG = bg
		}
		text := runewidth.Truncate(sp.text, maxW-used, "")
		if text == "" {
			continue
		}
		s.Put(x+used, y, maxW-used, text, st)
		used += runewidth.StringWidth(text)
	}
	return x + used
}

func styleOrDim(pal *Palette, dim bool) Style {
	if dim {
		return Style{FG: pal.InactiveFG}
	}
	return Style{FG: pal.MainFG}
}

func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "loading…"
	}
	s := NewScreen(m.width, m.height, m.pal)
	if m.width < MinWidth || m.height < MinHeight {
		m.renderTooSmall(s)
		return s.Render()
	}
	l := ComputeLayout(m.width, m.height)
	m.renderHeader(s, l)
	m.renderCPU(s, l)
	m.renderMem(s, l)
	m.renderNet(s, l)
	m.renderList(s, l)
	m.renderFooter(s, l)
	if m.showHelp || m.showDetail {
		s.Fill(0, l.HeaderH, l.W, l.H-l.HeaderH-1, Style{})
	}
	if m.showHelp {
		m.renderHelp(s, l)
	}
	if m.showDetail {
		m.renderDetail(s, l)
	}
	return s.Render()
}

func (m *Model) renderTooSmall(s *Screen) {
	if s.W <= 0 || s.H <= 0 {
		return
	}
	msg1 := fmt.Sprintf("virt-btop needs at least %dx%d", MinWidth, MinHeight)
	msg2 := fmt.Sprintf("terminal is %dx%d — please resize", m.width, m.height)
	y := s.H / 2
	if y >= s.H {
		y = s.H - 1
	}
	x1 := (s.W - runewidth.StringWidth(msg1)) / 2
	x2 := (s.W - runewidth.StringWidth(msg2)) / 2
	if x1 < 0 {
		x1 = 0
	}
	if x2 < 0 {
		x2 = 0
	}
	s.Put(x1, y, s.W-x1, msg1, Style{FG: m.pal.Title, Bold: true})
	if y+1 < s.H {
		s.Put(x2, y+1, s.W-x2, msg2, Style{FG: m.pal.GraphText})
	}
}

func (m *Model) renderHeader(s *Screen, l Layout) {
	now := m.now
	if now.IsZero() {
		now = time.Now()
	}
	mode := "RO"
	if m.readWrite {
		mode = "RW"
	}
	right := fmt.Sprintf("%s · %s · %s", now.Format("15:04:05"), m.interval, mode)
	if m.paused {
		right += " · PAUSED"
	}
	subColor := m.pal.GraphText
	if m.paused {
		subColor = m.pal.StatePaused
	}
	s.Box(0, 0, l.W, l.HeaderH, "virt-btop", right, m.pal.BorderHeader, m.pal.BorderHeader, subColor)

	x := 2
	y := 1
	maxW := l.W - 4

	hostCPU := m.host.CPUPercent
	hostMemPct := 0.0
	if m.host.MemTotalKiB > 0 {
		hostMemPct = float64(m.host.MemUsedKiB) / float64(m.host.MemTotalKiB) * 100
	}
	loadColor := m.pal.MainFG
	if m.host.Cores > 0 {
		switch {
		case m.host.Load1 >= float64(m.host.Cores)*0.9:
			loadColor = m.pal.StateCrashed
		case m.host.Load1 >= float64(m.host.Cores)*0.5:
			loadColor = m.pal.StatePaused
		}
	}
	hostname := m.host.Hostname
	if hostname == "" {
		hostname = "localhost"
	}
	putSpans(s, x, y, maxW, []span{
		{"host ", Style{FG: m.pal.GraphText}},
		{hostname, Style{FG: m.pal.MainFG, Bold: true}},
		{" · ", Style{FG: m.pal.DivLine}},
		{fmt.Sprintf("%d cores", m.host.Cores), Style{FG: m.pal.MainFG}},
		{" · load ", Style{FG: m.pal.GraphText}},
		{fmt.Sprintf("%.2f %.2f %.2f", m.host.Load1, m.host.Load5, m.host.Load15), Style{FG: loadColor}},
		{" · cpu ", Style{FG: m.pal.GraphText}},
		{pct1(hostCPU), Style{FG: m.pal.CPU.At(clamp(hostCPU/100, 0, 1))}},
		{" · mem ", Style{FG: m.pal.GraphText}},
		{fmt.Sprintf("%s/%s", humanKiB(m.host.MemUsedKiB), humanKiB(m.host.MemTotalKiB)), Style{FG: m.pal.Mem.At(clamp(hostMemPct/100, 0, 1))}},
		{" · up ", Style{FG: m.pal.GraphText}},
		{duration(m.host.Uptime), Style{FG: m.pal.MainFG}},
		{hostErrNote(m.host), Style{FG: m.pal.Error}},
	}, nil)

	active, total := 0, 0
	if m.snap != nil {
		active, total = m.snap.Counts()
	}
	c := m.combined
	putSpans(s, x, y+1, maxW, []span{
		{"VMs ", Style{FG: m.pal.GraphText}},
		{fmt.Sprintf("%d/%d", active, total), Style{FG: m.pal.MainFG, Bold: true}},
		{" · vCPU ", Style{FG: m.pal.GraphText}},
		{fmt.Sprintf("%d", c.VCPUs), Style{FG: m.pal.MainFG}},
		{" · util ", Style{FG: m.pal.GraphText}},
		{pct1(c.VCPUUtilPct), Style{FG: m.pal.CPU.At(clamp(c.VCPUUtilPct/100, 0, 1))}},
		{" · host ", Style{FG: m.pal.GraphText}},
		{pct1(c.HostSharePct), Style{FG: m.pal.CPU.At(clamp(c.HostSharePct/100, 0, 1))}},
		{" · mem ", Style{FG: m.pal.GraphText}},
		{vmMemSummary(c), Style{FG: m.pal.Mem.At(clamp(c.MemPct/100, 0, 1))}},
		{" · net ", Style{FG: m.pal.GraphText}},
		{"↓" + humanRateShort(c.NetRXBps) + " ↑" + humanRateShort(c.NetTXBps), Style{FG: m.pal.Download.At(0.55)}},
		{" · disk ", Style{FG: m.pal.GraphText}},
		{"R" + humanRateShort(c.DiskRDBps) + " W" + humanRateShort(c.DiskWRBps), Style{FG: m.pal.Upload.At(0.55)}},
	}, nil)
}

func (m *Model) runningByCPU() []*hypervisor.Domain {
	if m.snap == nil {
		return nil
	}
	active := make([]*hypervisor.Domain, 0, len(m.domains))
	for _, d := range m.domains {
		if d.State == hypervisor.StateRunning || d.State == hypervisor.StateBlocked {
			active = append(active, d)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		ra, rb := m.rates[active[i].Name], m.rates[active[j].Name]
		if ra.CPUPct == rb.CPUPct {
			return active[i].Name < active[j].Name
		}
		return ra.CPUPct > rb.CPUPct
	})
	return active
}

func (m *Model) renderCPU(s *Screen, l Layout) {
	subtitle := "no running VMs"
	if m.combined.VCPUs > 0 {
		subtitle = fmt.Sprintf("%s util · %d vCPU · %s host",
			pct1(m.combined.VCPUUtilPct), m.combined.VCPUs, pct1(m.combined.HostSharePct))
	}
	s.Box(0, l.CPUY, l.W, l.CPUH, "cpu", subtitle, m.pal.BorderCPU, m.pal.BorderCPU, m.pal.BorderCPU)

	active := m.runningByCPU()
	cols := clampInt(l.CPUGridW/26, 1, 4)
	const cellGap = 2
	cellW := (l.CPUGridW - cellGap*(cols-1)) / cols
	if cellW < 12 {
		cellW = 12
	}
	rows := l.CPUInnerH
	capacity := cols * rows

	show := len(active)
	more := 0
	if show > capacity {
		more = len(active) - (capacity - 1)
		show = capacity - 1
		if show < 0 {
			show = 0
		}
	}

	if len(active) == 0 {
		s.Put(l.CPUInnerX, l.CPUInnerY, l.CPUGridW, "no running VMs", Style{FG: m.pal.GraphText})
	}
	for i := 0; i < show; i++ {
		d := active[i]
		row, col := i/cols, i%cols
		if row >= rows {
			break
		}
		x := l.CPUInnerX + col*(cellW+cellGap)
		y := l.CPUInnerY + row
		selected := d.Name == m.selected
		var bg termenv.Color
		if selected {
			bg = m.pal.SelectedBG
			s.Fill(x, y, cellW, 1, Style{BG: bg})
		}
		nameW := cellW - 10
		if nameW < 4 {
			nameW = 4
		}
		nameStyle := Style{FG: m.pal.MainFG}
		if selected {
			nameStyle = Style{FG: m.pal.SelectedFG, BG: bg, Bold: true}
		}
		s.Put(x, y, nameW, truncate(d.Name, nameW), nameStyle)

		r := m.rates[d.Name]
		frac := clamp(r.CPUPct/100, 0, 1)
		drawBar(s, x+nameW+1, y, 4, frac, m.pal.Proc, bg, m.pal)
		pctStyle := Style{FG: m.pal.Proc.At(frac)}
		if selected {
			pctStyle = Style{FG: m.pal.SelectedFG, BG: bg}
		}
		s.Put(x+nameW+6, y, 4, fmt.Sprintf("%3.0f%%", r.CPUPct), pctStyle)
	}
	if more > 0 {
		i := show
		row, col := i/cols, i%cols
		if row < rows {
			x := l.CPUInnerX + col*(cellW+cellGap)
			y := l.CPUInnerY + row
			s.Put(x, y, cellW, fmt.Sprintf("+%d more", more), Style{FG: m.pal.GraphText})
		}
	}

	// Divider between the VM grid and the combined graph.
	for y := l.CPUInnerY; y < l.CPUInnerY+l.CPUInnerH; y++ {
		s.Set(l.CPUInnerX+l.CPUGridW, y, '│', Style{FG: m.pal.DivLine})
	}

	vals := metrics.Window(m.hist.CPU, l.CPUGraphW*2)
	DrawBraille(s, l.CPUGraphX, l.CPUInnerY, l.CPUGraphW, l.CPUInnerH, vals, 100,
		func(f float64) termenv.Color { return m.pal.CPU.At(f) })
}

func (m *Model) renderMem(s *Screen, l Layout) {
	c := m.combined
	sub := "no active VMs"
	switch {
	case c.MemReporting > 0:
		sub = fmt.Sprintf("%s · %s/%s", pct(c.MemPct), humanKiBShort(c.MemUsedKiB), humanKiBShort(c.MemTotalKiB))
		if c.MemExcluded > 0 {
			sub += fmt.Sprintf(" · %d/%d VMs", c.MemReporting, c.MemReporting+c.MemExcluded)
		}
	case c.ActiveVMs > 0:
		sub = "no guest stats"
	}
	s.Box(0, l.MemY, l.LeftW, l.MemH, "mem/disk", sub, m.pal.BorderMem, m.pal.BorderMem, m.pal.BorderMem)

	x, y, w, h := 2, l.MemY+1, l.LeftW-4, l.MemH-2
	if w <= 0 || h <= 0 {
		return
	}
	if c.MemReporting == 0 {
		if c.ActiveVMs > 0 && h >= 1 {
			s.Put(x, y, w, truncate(fmt.Sprintf("guest stats missing/stale on %d VMs", c.MemExcluded), w), Style{FG: m.pal.GraphText})
		}
		return
	}

	// btop-style rows: Total, then Used / Available / Cached / Free, each with
	// a value and a percent-of-total meter.
	total := c.MemTotalKiB
	row := 0
	if h > row {
		putSpans(s, x, y+row, w, []span{
			{"Total:", Style{FG: m.pal.GraphText}},
			{" " + humanKiB(total), Style{FG: m.pal.MainFG, Bold: true}},
		}, nil)
		row++
	}
	meters := []struct {
		label string
		kib   uint64
		grad  Gradient
	}{
		{"Used:", c.MemUsedKiB, m.pal.Mem},
		{"Available:", c.MemAvailKiB, m.pal.Avail},
		{"Cached:", c.MemCachedKiB, m.pal.Download},
		{"Free:", c.MemFreeKiB, m.pal.Free},
	}
	for _, mt := range meters {
		if h <= row {
			break
		}
		m.drawMemMeter(s, x, y+row, w, mt.label, mt.kib, total, mt.grad)
		row++
	}
	if h > row+3 {
		putSpans(s, x, y+row, w, []span{
			{"rss ", Style{FG: m.pal.GraphText}},
			{humanKiB(c.MemRSSKiB), Style{FG: m.pal.MainFG}},
			{" · swap ", Style{FG: m.pal.GraphText}},
			{fmt.Sprintf("%s/%s", humanKiB(c.SwapInKiB), humanKiB(c.SwapOutKiB)), Style{FG: m.pal.MainFG}},
		}, nil)
		row++
	}

	rem := h - row
	memGraphH := rem
	diskH := 0
	if rem >= 6 {
		memGraphH = rem / 3
		if memGraphH < 2 {
			memGraphH = 2
		}
		diskH = rem - memGraphH
	}
	if memGraphH > 0 {
		vals := metrics.Window(m.hist.Mem, w*2)
		DrawBraille(s, x, y+row, w, memGraphH, vals, 100,
			func(f float64) termenv.Color { return m.pal.Mem.At(f) })
	}
	if diskH >= 3 {
		m.renderDisk(s, x, y+row+memGraphH, w, diskH)
	}
}

// renderDisk draws the combined disk I/O: a label row, then one mirrored graph
// with reads growing up and writes growing down from a shared midline.
func (m *Model) renderDisk(s *Screen, x, y, w, h int) {
	c := m.combined
	putSpans(s, x, y, w, []span{
		{"disk ", Style{FG: m.pal.GraphText}},
		{"R ", Style{FG: m.pal.GraphText}},
		{humanRate(c.DiskRDBps), Style{FG: m.pal.Free.At(0.7)}},
		{" · W ", Style{FG: m.pal.GraphText}},
		{humanRate(c.DiskWRBps), Style{FG: m.pal.Avail.At(0.7)}},
	}, nil)
	gy, gh := y+1, h-1
	graphW := w - 2
	if gh < 2 || graphW < 1 {
		return
	}
	rdVals := metrics.Window(m.hist.DiskRD, graphW*2)
	wrVals := metrics.Window(m.hist.DiskWR, graphW*2)
	maxVal := niceMax(maxOf(rdVals, wrVals) * 1.05)
	rdH := gh / 2
	wrH := gh - rdH
	s.Put(x, gy, 1, "R", Style{FG: m.pal.Free.At(0.6)})
	DrawBraille(s, x+2, gy, graphW, rdH, rdVals, maxVal,
		func(f float64) termenv.Color { return m.pal.Free.At(f) })
	s.Put(x, gy+gh-1, 1, "W", Style{FG: m.pal.Avail.At(0.6)})
	DrawBrailleDown(s, x+2, gy+rdH, graphW, wrH, wrVals, maxVal,
		func(f float64) termenv.Color { return m.pal.Avail.At(f) })
}

// drawMemMeter draws "Label:  value  ⣿⣿⣿⣀⣀⣀  nn%" on one row: a braille
// bar so it reads like the graphs rather than a solid block.
func (m *Model) drawMemMeter(s *Screen, x, y, w int, label string, kib, total uint64, grad Gradient) {
	const labelW, valW, pctW = 11, 9, 4
	frac := 0.0
	if total > 0 {
		frac = clamp(float64(kib)/float64(total), 0, 1)
	}
	putSpans(s, x, y, labelW, []span{{label, Style{FG: m.pal.GraphText}}}, nil)
	val := humanKiB(kib)
	if pad := valW - len(val); pad > 0 {
		val = strings.Repeat(" ", pad) + val
	}
	putSpans(s, x+labelW, y, valW, []span{{val, Style{FG: m.pal.MainFG}}}, nil)
	barX := x + labelW + valW + 1
	barW := w - (labelW + valW + 1) - pctW - 1
	if barW >= 3 {
		drawBar(s, barX, y, barW, frac, grad, nil, m.pal)
		putSpans(s, barX+barW+1, y, pctW, []span{{fmt.Sprintf("%3.0f%%", frac*100), Style{FG: m.pal.MainFG}}}, nil)
	}
}

func (m *Model) renderNet(s *Screen, l Layout) {
	c := m.combined
	sub := fmt.Sprintf("↓ %s · ↑ %s", humanRate(c.NetRXBps), humanRate(c.NetTXBps))
	s.Box(0, l.NetY, l.LeftW, l.NetH, "net", sub, m.pal.BorderNet, m.pal.BorderNet, m.pal.BorderNet)

	x, y, w, h := 2, l.NetY+1, l.LeftW-4, l.NetH-2
	if w <= 0 || h <= 0 {
		return
	}
	totalsH := 0
	if h >= 3 {
		totalsH = 1
	}
	graphArea := h - totalsH
	// One mirrored graph sharing a midline: upload grows up, download grows down.
	txH := graphArea / 2
	if txH < 1 {
		txH = 1
	}
	rxH := graphArea - txH
	graphW := w - 2
	if graphW < 1 {
		return
	}
	rxVals := metrics.Window(m.hist.NetRX, graphW*2)
	txVals := metrics.Window(m.hist.NetTX, graphW*2)
	maxVal := niceMax(maxOf(rxVals, txVals) * 1.05)

	s.Put(x, y, 1, "↑", Style{FG: m.pal.Upload.At(0.6)})
	DrawBraille(s, x+2, y, graphW, txH, txVals, maxVal,
		func(f float64) termenv.Color { return m.pal.Upload.At(f) })
	if rxH > 0 {
		s.Put(x, y+graphArea-1, 1, "↓", Style{FG: m.pal.Download.At(0.6)})
		DrawBrailleDown(s, x+2, y+txH, graphW, rxH, rxVals, maxVal,
			func(f float64) termenv.Color { return m.pal.Download.At(f) })
	}
	if totalsH > 0 {
		putSpans(s, x, y+graphArea, w, []span{
			{"rx ", Style{FG: m.pal.GraphText}},
			{humanBytes(float64(c.NetRXTotal)), Style{FG: m.pal.Download.At(0.6)}},
			{" · tx ", Style{FG: m.pal.GraphText}},
			{humanBytes(float64(c.NetTXTotal)), Style{FG: m.pal.Upload.At(0.6)}},
		}, nil)
	}
}

// drawBar draws a horizontal bar out of braille dots (2 dots per cell, full
// cells plus a partial end cell) so meters match the style of the graphs.
func drawBar(s *Screen, x, y, w int, frac float64, grad Gradient, bg termenv.Color, pal *Palette) {
	if w <= 0 {
		return
	}
	frac = clamp(frac, 0, 1)
	dots := int(math.Round(frac * float64(w*2)))
	if dots == 0 && frac > 0 {
		dots = 1
	}
	color := grad.At(frac)
	const leftDots, rightDots uint8 = 0x01 | 0x02 | 0x04 | 0x40, 0x08 | 0x10 | 0x20 | 0x80
	for i := 0; i < w; i++ {
		st := Style{FG: color, BG: bg}
		var mask uint8
		switch {
		case dots >= (i+1)*2:
			mask = leftDots | rightDots
		case dots == i*2+1:
			mask = leftDots
		}
		if mask == 0 {
			st.FG = pal.MeterBG
		}
		s.Set(x+i, y, 0x2800+rune(mask), st)
	}
}

func (m *Model) renderList(s *Screen, l Layout) {
	g := l.listGeometry()
	lc := computeListColumns(g.W)

	total := 0
	if m.snap != nil {
		_, total = m.snap.Counts()
	}
	vis := m.visibleDomains()
	sub := fmt.Sprintf("%d/%d · %s%s · %s", len(vis), total, m.sortKey, m.sortArrow(), m.filter)
	if m.search != "" {
		sub += " · /" + m.search
	}
	s.Box(l.RightX, l.ListY, l.RightW, l.ListH, "vms", sub, m.pal.BorderVMs, m.pal.BorderVMs, m.pal.GraphText)

	headerStyle := Style{FG: m.pal.GraphText, Bold: true}
	sortStyle := Style{FG: m.pal.BorderVMs, Bold: true}
	header := func(x, w int, label string, key SortKey) {
		st := headerStyle
		if m.sortKey == key {
			label += m.sortArrow()
			st = sortStyle
		}
		s.Put(x, g.HeaderY, w, truncate(label, w), st)
	}
	header(g.X+2, lc.nameW-2, "NAME", SortName)
	header(g.X+lc.stateX, lc.stateW, "STATE", SortState)
	header(g.X+lc.cpuX, lc.cpuW, "CPU%", SortCPU)
	s.Put(g.X+lc.vcpuX, g.HeaderY, lc.vcpuW, "vCPU", headerStyle)
	header(g.X+lc.memX, lc.memW, "MEM", SortMem)
	if lc.showDisk {
		header(g.X+lc.diskX, lc.diskW, "DISK R/W", SortDisk)
	}
	if lc.showNet {
		header(g.X+lc.netX, lc.netW, "NET R/W", SortNet)
	}

	rows := g.Rows
	if len(vis) == 0 {
		msg := "no VMs match"
		s.Put(g.X+(g.W-runewidth.StringWidth(msg))/2, g.RowsY+rows/2, g.W, msg, Style{FG: m.pal.GraphText})
	} else {
		start := m.listStart(vis, rows)
		end := start + rows
		if end > len(vis) {
			end = len(vis)
		}
		for i := start; i < end; i++ {
			d := vis[i]
			y := g.RowsY + (i - start)
			selected := d.Name == m.selected
			var bg termenv.Color
			if selected {
				bg = m.pal.SelectedBG
				s.Fill(l.RightX+1, y, l.RightW-2, 1, Style{BG: bg})
			}
			m.drawListRow(s, g, lc, d, y, selected, bg)
		}
		if start > 0 && rows > 0 {
			s.Set(l.RightX+l.RightW-1, g.RowsY, '▲', Style{FG: m.pal.GraphText})
		}
		if end < len(vis) && rows > 0 {
			s.Set(l.RightX+l.RightW-1, g.RowsY+rows-1, '▼', Style{FG: m.pal.GraphText})
		}
	}

	if g.ShowTotals {
		c := m.combined
		putSpans(s, g.X, g.TotalY, g.W, []span{
			{"total ", Style{FG: m.pal.GraphText}},
			{fmt.Sprintf("%d active", c.ActiveVMs), Style{FG: m.pal.MainFG}},
			{" · ", Style{FG: m.pal.DivLine}},
			{pct1(c.VCPUUtilPct) + " util", Style{FG: m.pal.CPU.At(clamp(c.VCPUUtilPct/100, 0, 1))}},
			{" · ", Style{FG: m.pal.DivLine}},
			{vmMemTotal(c) + " mem", Style{FG: m.pal.Mem.At(clamp(c.MemPct/100, 0, 1))}},
			{" · ", Style{FG: m.pal.DivLine}},
			{"R" + humanRateShort(c.DiskRDBps) + " W" + humanRateShort(c.DiskWRBps), Style{FG: m.pal.Upload.At(0.55)}},
		}, nil)
	}
}

func (m *Model) drawListRow(s *Screen, g listGeometry, lc listColumns, d *hypervisor.Domain, y int, selected bool, bg termenv.Color) {
	r := m.rates[d.Name]
	dim := !d.State.Active()

	// name + selection marker
	mark := " "
	if selected {
		mark = "▸"
	}
	markStyle := Style{FG: m.pal.BorderVMs, BG: bg}
	if selected {
		markStyle.FG = m.pal.SelectedFG
	}
	s.Put(g.X, y, 1, mark, markStyle)
	nameStyle := Style{FG: m.pal.MainFG, BG: bg}
	if selected {
		nameStyle = Style{FG: m.pal.SelectedFG, BG: bg, Bold: true}
	} else if dim {
		nameStyle.FG = m.pal.InactiveFG
	}
	s.Put(g.X+2, y, lc.nameW-2, truncate(d.Name, lc.nameW-2), nameStyle)

	// state
	dot, stateColor := stateDot(d.State, m.pal)
	s.Put(g.X+lc.stateX, y, 1, dot, Style{FG: stateColor, BG: bg})
	stateStyle := Style{FG: m.pal.MainFG, BG: bg}
	if selected {
		stateStyle.FG = m.pal.SelectedFG
	} else if dim {
		stateStyle.FG = m.pal.InactiveFG
	}
	s.Put(g.X+lc.stateX+2, y, lc.stateW-2, d.State.String(), stateStyle)

	// CPU
	if d.State.Active() {
		frac := clamp(r.CPUPct/100, 0, 1)
		drawBar(s, g.X+lc.cpuX, y, 4, frac, m.pal.Proc, bg, m.pal)
		pctStyle := Style{FG: m.pal.Proc.At(frac), BG: bg}
		if selected {
			pctStyle.FG = m.pal.SelectedFG
		}
		s.Put(g.X+lc.cpuX+5, y, 4, fmt.Sprintf("%3.0f%%", r.CPUPct), pctStyle)
	} else {
		s.Put(g.X+lc.cpuX, y, lc.cpuW, "     —", Style{FG: m.pal.InactiveFG, BG: bg})
	}

	// vCPU
	vcpu := fmt.Sprintf("%d", d.VCPUs)
	if d.Info.ConfiguredVCPUs > d.VCPUs && d.Info.ConfiguredVCPUs > 0 {
		vcpu = fmt.Sprintf("%d/%d", d.VCPUs, d.Info.ConfiguredVCPUs)
	}
	if runewidth.StringWidth(vcpu) > lc.vcpuW {
		vcpu = fmt.Sprintf("%d", d.VCPUs)
	}
	vcpuStyle := styleOrDim(m.pal, dim)
	vcpuStyle.BG = bg
	if selected {
		vcpuStyle.FG = m.pal.SelectedFG
	}
	s.PutRight(g.X+lc.vcpuX, y, lc.vcpuW, vcpu, vcpuStyle)

	// memory
	switch {
	case !d.State.Active():
		txt := humanKiBShort(d.MemMaximumKiB) + " alloc"
		s.Put(g.X+lc.memX, y, lc.memW, truncate(txt, lc.memW), Style{FG: m.pal.InactiveFG, BG: bg})
	case r.MemStatus == hypervisor.MemUnknown:
		s.Put(g.X+lc.memX, y, lc.memW, "— no stats", Style{FG: m.pal.InactiveFG, BG: bg})
	default:
		frac := clamp(r.MemPct/100, 0, 1)
		grad := m.pal.Mem
		pctStyle := Style{FG: m.pal.MainFG, BG: bg}
		usedStyle := Style{FG: m.pal.MainFG, BG: bg}
		if selected {
			pctStyle.FG = m.pal.SelectedFG
			usedStyle.FG = m.pal.SelectedFG
		}
		if r.MemStatus == hypervisor.MemStale {
			// Stale numbers are shown muted so they are not mistaken for live
			// ones.
			grad = m.pal.Stale
			pctStyle.FG = m.pal.GraphText
			usedStyle.FG = m.pal.GraphText
		}
		drawBar(s, g.X+lc.memX, y, 4, frac, grad, bg, m.pal)
		s.Put(g.X+lc.memX+5, y, 4, fmt.Sprintf("%4s", pct(r.MemPct)), pctStyle)
		s.Put(g.X+lc.memX+10, y, lc.memW-10, humanKiBShort(r.MemUsedKiB), usedStyle)
	}

	// disk I/O
	if lc.showDisk {
		if dim {
			s.Put(g.X+lc.diskX, y, lc.diskW, "—", Style{FG: m.pal.InactiveFG, BG: bg})
		} else {
			putSpans(s, g.X+lc.diskX, y, lc.diskW, []span{
				{"R", Style{FG: m.pal.Download.At(0.6), BG: bg}},
				{humanRateShort(r.DiskRDBps), Style{FG: m.pal.MainFG, BG: bg}},
				{" W", Style{FG: m.pal.Upload.At(0.6), BG: bg}},
				{humanRateShort(r.DiskWRBps), Style{FG: m.pal.MainFG, BG: bg}},
			}, bg)
		}
	}

	// network I/O
	if lc.showNet {
		if dim {
			s.Put(g.X+lc.netX, y, lc.netW, "—", Style{FG: m.pal.InactiveFG, BG: bg})
		} else {
			putSpans(s, g.X+lc.netX, y, lc.netW, []span{
				{"↓" + humanRateShort(r.NetRXBps), Style{FG: m.pal.Download.At(0.6), BG: bg}},
				{" ↑" + humanRateShort(r.NetTXBps), Style{FG: m.pal.Upload.At(0.6), BG: bg}},
			}, bg)
		}
	}
}

func stateDot(st hypervisor.State, pal *Palette) (string, termenv.Color) {
	switch st {
	case hypervisor.StateRunning:
		return "●", pal.StateRunning
	case hypervisor.StateBlocked:
		return "●", pal.StateBlocked
	case hypervisor.StatePaused:
		return "‖", pal.StatePaused
	case hypervisor.StatePMSuspended:
		return "◌", pal.StateSuspended
	case hypervisor.StateCrashed:
		return "◆", pal.StateCrashed
	case hypervisor.StateShutdown:
		return "◌", pal.StateShutdown
	default:
		return "○", pal.StateShutoff
	}
}

func (m *Model) renderFooter(s *Screen, l Layout) {
	y := l.FooterY
	x := 2
	w := l.W - 4
	if w <= 0 {
		return
	}

	if m.searchMode {
		left := []span{
			{"search: ", Style{FG: m.pal.GraphText}},
			{m.search, Style{FG: m.pal.MainFG}},
			{"█", Style{FG: m.pal.BorderHeader}},
		}
		right := "enter apply · esc clear"
		rw := runewidth.StringWidth(right)
		putSpans(s, x, y, w-rw-2, left, nil)
		s.PutRight(x+w-rw, y, rw, right, Style{FG: m.pal.GraphText})
		return
	}

	left := []span{
		{"↑↓/jk", Style{FG: m.pal.MainFG, Bold: true}},
		{" select · ", Style{FG: m.pal.GraphText}},
		{"s", Style{FG: m.pal.MainFG, Bold: true}},
		{" sort · ", Style{FG: m.pal.GraphText}},
		{"f", Style{FG: m.pal.MainFG, Bold: true}},
		{" filter · ", Style{FG: m.pal.GraphText}},
		{"/", Style{FG: m.pal.MainFG, Bold: true}},
		{" search · ", Style{FG: m.pal.GraphText}},
		{"p", Style{FG: m.pal.MainFG, Bold: true}},
		{" pause · ", Style{FG: m.pal.GraphText}},
		{"+/-", Style{FG: m.pal.MainFG, Bold: true}},
		{" interval · ", Style{FG: m.pal.GraphText}},
		{"?", Style{FG: m.pal.MainFG, Bold: true}},
		{" help · ", Style{FG: m.pal.GraphText}},
		{"q", Style{FG: m.pal.MainFG, Bold: true}},
		{" quit", Style{FG: m.pal.GraphText}},
	}

	right := ""
	rightStyle := Style{FG: m.pal.GraphText}
	if m.status != "" && (m.statusUntil.IsZero() || time.Now().Before(m.statusUntil)) {
		right = m.status
		if m.statusErr {
			rightStyle = Style{FG: m.pal.Error}
		}
	} else {
		mode := "RO"
		if m.readWrite {
			mode = "RW"
		}
		right = mode + " · " + m.uri
		if m.paused {
			right = "PAUSED · " + right
			rightStyle = Style{FG: m.pal.StatePaused}
		}
	}
	right = truncate(right, w/2)
	rw := runewidth.StringWidth(right)
	if rw > w {
		rw = w
	}
	putSpans(s, x, y, w-rw-2, left, nil)
	if right != "" {
		s.PutRight(x+w-rw, y, rw, right, rightStyle)
	}
}

func (m *Model) renderHelp(s *Screen, l Layout) {
	x, y, width, height := overlayRect(l, 104, 28)
	s.Box(x, y, width, height, "help", "any key to close", m.pal.DivLine, m.pal.Title, m.pal.GraphText)

	left := []string{
		"NAVIGATION",
		"  ↑/↓, k/j        select",
		"  PgUp/PgDn       page",
		"  Home/End        first/last",
		"  click/wheel     select/scroll",
		"",
		"SORT & FILTER",
		"  s               cycle sort",
		"  1..6            cpu mem name disk net state",
		"  r               reverse sort",
		"  f               all/running/paused/stopped",
		"  /               search by name",
		"",
		"CPU BOX",
		"  running VMs, top N by CPU; braille graphs",
	}
	right := []string{
		"DISPLAY",
		"  p / space       pause sampling",
		"  + / -           poll interval",
		"  R               refresh metadata",
		"  Enter           VM detail",
		"  ?               this help",
		"  q / Ctrl-C      quit",
		"",
		"METRICS",
		"  CPU%        guest CPU time / allocated vCPUs",
		"  util        weighted vCPU utilization",
		"  host        share of all host cores",
		"  mem         balloon current - available",
		"  net         host-side vnet RX/TX",
		"",
		"CONNECTION",
		"  RO          read-only, no group/sudo required",
		"  RW          --read-write, libvirt group needed",
	}

	contentH := height - 2
	colW := (width - 4) / 2
	if colW >= 30 && contentH >= 10 {
		for yy := y + 1; yy < y+height-1; yy++ {
			s.Set(x+2+colW, yy, '│', Style{FG: m.pal.DivLine})
		}
	}
	if colW < 30 || contentH < 10 {
		// Single column, truncate to what fits.
		all := append(left, "", "")
		all = append(all, right...)
		for i := 0; i < contentH && i < len(all); i++ {
			style := Style{FG: m.pal.MainFG}
			if all[i] == "NAVIGATION" || all[i] == "SORT & FILTER" || all[i] == "DISPLAY" || all[i] == "METRICS" || all[i] == "CONNECTION" {
				style = Style{FG: m.pal.BorderHeader, Bold: true}
			}
			s.Put(x+2, y+1+i, width-4, all[i], style)
		}
		return
	}
	for i := 0; i < contentH && i < len(left); i++ {
		style := Style{FG: m.pal.MainFG}
		if left[i] == "NAVIGATION" || left[i] == "SORT & FILTER" || left[i] == "CPU BOX" {
			style = Style{FG: m.pal.BorderHeader, Bold: true}
		}
		s.Put(x+2, y+1+i, colW, left[i], style)
	}
	for i := 0; i < contentH && i < len(right); i++ {
		style := Style{FG: m.pal.MainFG}
		if right[i] == "DISPLAY" || right[i] == "METRICS" || right[i] == "CONNECTION" {
			style = Style{FG: m.pal.BorderHeader, Bold: true}
		}
		s.Put(x+4+colW, y+1+i, width-5-colW, right[i], style)
	}
}

func overlayRect(l Layout, maxW, maxH int) (x, y, w, h int) {
	bodyTop := l.HeaderH
	bodyH := l.H - l.HeaderH - 1
	w = minInt(l.W-8, maxW)
	if w < 40 {
		w = l.W - 2
	}
	if w < 1 {
		w = 1
	}
	h = minInt(bodyH, maxH)
	if h < 10 {
		h = bodyH
	}
	if h < 1 {
		h = 1
	}
	x = (l.W - w) / 2
	y = bodyTop + (bodyH-h)/2
	if x < 0 {
		x = 0
	}
	if y < bodyTop {
		y = bodyTop
	}
	return x, y, w, h
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// vmMemSummary formats aggregate guest memory for the header, noting when some
// VMs are excluded for lack of fresh statistics.
func vmMemSummary(c metrics.Combined) string {
	if c.MemReporting == 0 {
		if c.ActiveVMs > 0 {
			return "n/a (no guest stats)"
		}
		return "—"
	}
	out := fmt.Sprintf("%s/%s", humanKiB(c.MemUsedKiB), humanKiB(c.MemTotalKiB))
	if c.MemExcluded > 0 {
		out += fmt.Sprintf(" (%d/%d VMs)", c.MemReporting, c.MemReporting+c.MemExcluded)
	}
	return out
}

func vmMemTotal(c metrics.Combined) string {
	if c.MemReporting == 0 {
		return "n/a"
	}
	return humanKiB(c.MemUsedKiB)
}

func hostErrNote(h metrics.Host) string {
	if h.Err == nil {
		return ""
	}
	return " · host stats unavailable"
}
