package tui

// Minimum size the layout stays usable at. Below this we show a resize message
// instead of a broken layout. 80x24 is the comfortable target; 80x18 is the
// smallest terminal where every panel still has at least one content row.
const (
	MinWidth  = 80
	MinHeight = 18
)

type Layout struct {
	W, H int

	HeaderY, HeaderH int
	BodyY, BodyH     int

	CPUY, CPUH int
	LeftW      int
	RightX     int
	RightW     int

	MemY, MemH   int
	NetY, NetH   int
	ListY, ListH int

	FooterY int

	CPUInnerX, CPUInnerY int
	CPUInnerW, CPUInnerH int
	CPUGridW             int
	CPUGraphX, CPUGraphW int
}

func ComputeLayout(w, h int) Layout {
	l := Layout{W: w, H: h}
	l.HeaderY, l.HeaderH = 0, 4
	l.FooterY = h - 1
	l.BodyY = l.HeaderH
	l.BodyH = l.FooterY - l.BodyY

	l.CPUY = l.BodyY
	l.CPUH = clampInt(int(float64(l.BodyH)*0.34), 6, 14)
	if l.BodyH-l.CPUH < 10 && l.BodyH >= 10 {
		l.CPUH = l.BodyH - 10
	}

	// Narrow terminals need the right-hand list to keep enough room for at
	// least NAME, CPU%, and MEM, so let the left column shrink below its
	// usual 38-cell floor. The cap keeps a few columns for the list.
	l.LeftW = clampInt(int(float64(w)*0.32), 30, 70)
	if maxLeft := w - 48; l.LeftW > maxLeft {
		l.LeftW = maxLeft
	}
	l.RightX = l.LeftW + 1
	l.RightW = w - l.RightX

	below := l.BodyH - l.CPUH
	if below < 10 {
		below = 10
	}
	l.MemY = l.CPUY + l.CPUH
	l.ListY = l.MemY
	l.ListH = below
	l.MemH = clampInt(int(float64(below)*0.45), 5, below-5)
	l.NetY = l.MemY + l.MemH
	l.NetH = below - l.MemH

	l.CPUInnerX, l.CPUInnerY = 2, l.CPUY+1
	l.CPUInnerW, l.CPUInnerH = w-4, l.CPUH-2
	l.CPUGridW = clampInt(int(float64(l.CPUInnerW)*0.42), 20, l.CPUInnerW-24)
	l.CPUGraphX = l.CPUInnerX + l.CPUGridW + 1
	l.CPUGraphW = l.CPUInnerW - l.CPUGridW - 1
	return l
}

type listGeometry struct {
	X, Y, W    int // content origin and width
	HeaderY    int
	RowsY      int
	Rows       int
	ShowTotals bool
	TotalY     int
}

func (l Layout) listGeometry() listGeometry {
	g := listGeometry{
		X:       l.RightX + 2,
		Y:       l.ListY + 1,
		W:       l.RightW - 4,
		HeaderY: l.ListY + 1,
		RowsY:   l.ListY + 2,
		Rows:    l.ListH - 3,
	}
	if l.ListH >= 12 {
		g.ShowTotals = true
		g.TotalY = l.ListY + l.ListH - 2
		g.Rows--
	}
	if g.Rows < 0 {
		g.Rows = 0
	}
	return g
}

const colGap = 2

type listColumns struct {
	innerW int

	nameW, stateW, cpuW, vcpuW, memW, diskW, netW int
	cpuBarW, memBarW                              int

	nameX, stateX, cpuX, vcpuX, memX, diskX, netX int
	showDisk, showNet, showVCPU                   bool
}

// computeListColumns lays out the VM list. showState is false when every
// visible VM has the same state (the default running-only filter), in which
// case the STATE column is dropped and its width goes to wider meters.
//
// Optional columns are dropped in priority order on narrow terminals (disk,
// net, vCPU, the memory usage figure) so NAME, CPU%, and MEM always fit. With
// an inner width of 45 (an 80-column terminal) the list still shows a name
// plus CPU and memory meters, even when STATE is present.
func computeListColumns(innerW int, showState bool) listColumns {
	lc := listColumns{innerW: innerW}

	// renderCPU asks for the CPU meter width with innerW == 0; return the
	// default geometry instead of letting the narrow-width fallback pick a
	// thinner bar.
	if innerW <= 0 {
		lc.stateW = 0
		lc.cpuBarW, lc.memBarW = 4, 4
		if !showState {
			lc.cpuBarW, lc.memBarW = 8, 8
		}
		lc.cpuW = lc.cpuBarW + 5
		lc.memW = lc.memBarW + 11
		lc.vcpuW = 5
		lc.showVCPU = true
		if showState {
			lc.stateW = 11
		}
		lc.nameW = 8
		return lc
	}

	// Wide terminals get the richer columns. STATE costs 11 cells, so when it
	// is shown the meters use the compact 4-cell bars; without it the freed
	// width goes to 8-cell bars.
	baseBars := 4
	if !showState {
		baseBars = 8
	}
	type config struct {
		disk, net, vcpu bool
		bars            int
		memUsage        bool
	}
	configs := []config{
		{true, true, true, baseBars, true},
		{true, false, true, baseBars, true},
		{false, false, true, baseBars, true},
	}
	if baseBars > 4 {
		configs = append(configs, config{false, false, true, 4, true})
	}
	configs = append(configs,
		config{false, false, true, 4, false},  // drop the MEM usage figure
		config{false, false, false, 4, true},  // drop vCPU
		config{false, false, false, 4, false}, // name + CPU + MEM% only
	)

	apply := func(c config) {
		lc.showDisk, lc.showNet, lc.showVCPU = c.disk, c.net, c.vcpu
		lc.cpuBarW, lc.memBarW = c.bars, c.bars
		lc.cpuW = c.bars + 5
		lc.memW = c.bars + 5 // bar + 1 + "nnn%"
		if c.memUsage {
			lc.memW += 6
		}
		lc.stateW = 0
		if showState {
			lc.stateW = 11
		}
		lc.vcpuW = 0
		if c.vcpu {
			lc.vcpuW = 5
		}
		lc.diskW, lc.netW = 0, 0
		if c.disk {
			lc.diskW = 14
		}
		if c.net {
			lc.netW = 13
		}
	}

	nameW := 0
	for _, c := range configs {
		apply(c)
		fixed := lc.stateW + lc.cpuW + lc.vcpuW + lc.memW + lc.diskW + lc.netW
		cols := 3 // name, cpu, mem
		if showState {
			cols++
		}
		if c.vcpu {
			cols++
		}
		if c.disk {
			cols++
		}
		if c.net {
			cols++
		}
		nameW = innerW - fixed - (cols-1)*colGap
		// Optional I/O columns are only worth showing if they leave a
		// reasonably readable name.
		minName := 8
		if c.disk || c.net {
			minName = 14
		}
		if nameW >= minName {
			break
		}
	}
	if nameW < 8 {
		nameW = 8
	}
	lc.nameW = nameW

	x := 0
	lc.nameX = x
	x += lc.nameW + colGap
	if showState {
		lc.stateX = x
		x += lc.stateW + colGap
	}
	lc.cpuX = x
	x += lc.cpuW + colGap
	if lc.showVCPU {
		lc.vcpuX = x
		x += lc.vcpuW + colGap
	}
	lc.memX = x
	x += lc.memW
	if lc.showDisk {
		x += colGap
		lc.diskX = x
		x += lc.diskW
	}
	if lc.showNet {
		x += colGap
		lc.netX = x
	}
	return lc
}
