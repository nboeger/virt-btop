package tui

// btop-like minimum. Below this we show a resize message instead of a broken
// layout.
const (
	MinWidth  = 130
	MinHeight = 21
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

	l.LeftW = clampInt(int(float64(w)*0.32), 38, 70)
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
	showDisk, showNet                             bool
}

// computeListColumns lays out the VM list. showState is false when every
// visible VM has the same state (the default running-only filter), in which
// case the STATE column is dropped and its width goes to the name.
func computeListColumns(innerW int, showState bool) listColumns {
	lc := listColumns{
		innerW:  innerW,
		stateW:  11,
		cpuW:    9,
		vcpuW:   5,
		memW:    15,
		diskW:   14,
		netW:    13,
		cpuBarW: 4,
		memBarW: 4,
	}
	if !showState {
		// Spend most of the freed STATE width on wider (higher resolution)
		// meters; the rest goes to the name.
		lc.stateW = 0
		lc.cpuBarW, lc.memBarW = 8, 8
		lc.cpuW = lc.cpuBarW + 5
		lc.memW = lc.memBarW + 11
	}
	baseFixed := lc.stateW + lc.cpuW + lc.vcpuW + lc.memW

	fits := func(showDisk, showNet bool) int {
		fixed := baseFixed
		gaps := 4
		if !showState {
			gaps--
		}
		if showDisk {
			fixed += lc.diskW
			gaps++
		}
		if showNet {
			fixed += lc.netW
			gaps++
		}
		return innerW - fixed - gaps*colGap
	}

	if n := fits(true, true); n >= 14 {
		lc.showDisk, lc.showNet = true, true
		lc.nameW = n
	} else if n := fits(true, false); n >= 14 {
		lc.showDisk = true
		lc.nameW = n
	} else {
		lc.nameW = fits(false, false)
	}
	if lc.nameW < 8 {
		lc.nameW = 8
	}

	x := 0
	lc.nameX = x
	x += lc.nameW + colGap
	if showState {
		lc.stateX = x
		x += lc.stateW + colGap
	}
	lc.cpuX = x
	x += lc.cpuW + colGap
	lc.vcpuX = x
	x += lc.vcpuW + colGap
	lc.memX = x
	if lc.showDisk {
		x += lc.memW + colGap
		lc.diskX = x
		x += lc.diskW
	}
	if lc.showNet {
		x += colGap
		lc.netX = x
	}
	return lc
}
