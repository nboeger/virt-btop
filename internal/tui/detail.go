package tui

import (
	"fmt"
	"time"

	"github.com/muesli/termenv"

	"github.com/nboeger/virt-btop/internal/hypervisor"
	"github.com/nboeger/virt-btop/internal/metrics"
)

func (m *Model) renderDetail(s *Screen, l Layout) {
	// View must not mutate the model; ensureSelection closes the overlay when
	// its VM disappears, so a missing domain here only means "draw nothing".
	d := m.selectedDomain()
	if d == nil {
		return
	}
	r := m.selectedRate()

	x, y, width, height := overlayRect(l, 112, 28)
	sub := "esc/enter close"
	s.Box(x, y, width, height, "vm — "+d.Name, sub, m.pal.DivLine, m.pal.Title, m.pal.GraphText)

	cx, cy := x+2, y+1
	cw, ch := width-4, height-2
	if cw <= 0 || ch <= 0 {
		return
	}

	leftW := cw
	rightW := 0
	if cw >= 84 {
		leftW = cw * 58 / 100
		rightW = cw - leftW - 2
	}
	rightX := cx + leftW + 2
	if rightW > 0 {
		for yy := cy; yy < cy+ch; yy++ {
			s.Set(cx+leftW, yy, '│', Style{FG: m.pal.DivLine})
		}
	}

	info := d.Info
	uuid := info.UUID
	if len(uuid) > 8 {
		uuid = uuid[:8]
	}
	autostart, persistent := "off", "no"
	if info.Autostart {
		autostart = "on"
	}
	if info.Persistent {
		persistent = "yes"
	}
	if info.Err != "" {
		// A failed lookup leaves zero values; don't present them as real.
		autostart, persistent = "?", "?"
	}
	id := "-"
	if info.ID >= 0 {
		id = fmt.Sprintf("%d", info.ID)
	}

	ly := cy
	putSpans(s, cx, ly, leftW, []span{
		{"id ", Style{FG: m.pal.GraphText}},
		{id, Style{FG: m.pal.MainFG}},
		{" · ", Style{FG: m.pal.DivLine}},
		{"uuid ", Style{FG: m.pal.GraphText}},
		{uuid, Style{FG: m.pal.MainFG}},
		{" · ", Style{FG: m.pal.DivLine}},
		{"autostart ", Style{FG: m.pal.GraphText}},
		{autostart, Style{FG: m.pal.MainFG}},
		{" · persistent ", Style{FG: m.pal.GraphText}},
		{persistent, Style{FG: m.pal.MainFG}},
	}, nil)
	ly++

	dot, dotColor := stateDot(d.State, m.pal)
	vcpuText := fmt.Sprintf("%d vCPU", d.VCPUs)
	if info.ConfiguredVCPUs > d.VCPUs && info.ConfiguredVCPUs > 0 {
		vcpuText = fmt.Sprintf("%d/%d vCPU", d.VCPUs, info.ConfiguredVCPUs)
	}
	putSpans(s, cx, ly, leftW, []span{
		{dot, Style{FG: dotColor, Bold: true}},
		{" " + d.State.String(), Style{FG: dotColor}},
		{" · ", Style{FG: m.pal.DivLine}},
		{vcpuText, Style{FG: m.pal.MainFG}},
		{" · cpu time ", Style{FG: m.pal.GraphText}},
		{duration(time.Duration(d.CPUTimeNS)), Style{FG: m.pal.MainFG}},
		{" · os ", Style{FG: m.pal.GraphText}},
		{info.OSType, Style{FG: m.pal.MainFG}},
	}, nil)
	ly++

	cores := 0.0
	if d.VCPUs > 0 {
		cores = r.CPUPct / 100 * float64(d.VCPUs)
	}
	putSpans(s, cx, ly, leftW, []span{
		{"cpu ", Style{FG: m.pal.GraphText}},
		{pct1(r.CPUPct), Style{FG: m.pal.CPU.At(clamp(r.CPUPct/100, 0, 1))}},
		{" of allocated vCPU", Style{FG: m.pal.GraphText}},
		{" · host ", Style{FG: m.pal.GraphText}},
		{pct1(r.HostPct), Style{FG: m.pal.CPU.At(clamp(r.HostPct/100, 0, 1))}},
		{" · ", Style{FG: m.pal.DivLine}},
		{fmt.Sprintf("%.1f/%.0f cores", cores, float64(d.VCPUs)), Style{FG: m.pal.MainFG}},
	}, nil)
	ly++

	graphH := 3
	if ch < 14 {
		graphH = 2
	}
	if ch < 10 {
		graphH = 1
	}
	vals := metrics.Window(m.hist.VMs[d.Name], leftW*2)
	DrawBraille(s, cx, ly, leftW, graphH, vals, 100,
		func(f float64) termenv.Color { return m.pal.CPU.At(f) })
	ly += graphH

	memLines := 2
	if ch < 10 {
		memLines = 1
	}
	reserved := 3 + graphH + memLines
	vcpuRows := ch - reserved
	if vcpuRows < 0 {
		vcpuRows = 0
	}
	if vcpuRows > 0 && len(r.VCPUPct) > 0 {
		cols := clampInt(leftW/9, 1, 16)
		rowsNeeded := (len(r.VCPUPct) + cols - 1) / cols
		if rowsNeeded > vcpuRows {
			rowsNeeded = vcpuRows
		}
		for row := 0; row < rowsNeeded; row++ {
			for col := 0; col < cols; col++ {
				i := row*cols + col
				if i >= len(r.VCPUPct) {
					break
				}
				state := 1
				if i < len(d.VCPUStates) {
					state = d.VCPUStates[i]
				}
				xcell := cx + col*(leftW/cols)
				if state == 0 {
					putSpans(s, xcell, ly+row, leftW/cols, []span{
						{fmt.Sprintf("c%d ", i), Style{FG: m.pal.GraphText}},
						{"off", Style{FG: m.pal.InactiveFG}},
					}, nil)
					continue
				}
				p := r.VCPUPct[i]
				putSpans(s, xcell, ly+row, leftW/cols, []span{
					{fmt.Sprintf("c%d ", i), Style{FG: m.pal.GraphText}},
					{pct(p), Style{FG: m.pal.Proc.At(clamp(p/100, 0, 1))}},
				}, nil)
			}
		}
		ly += rowsNeeded
	}

	now := m.now
	if m.snap != nil {
		now = m.snap.At
	}
	if ly < cy+ch && memLines >= 1 {
		muted := Style{FG: m.pal.GraphText}
		switch {
		case !d.State.Active():
			putSpans(s, cx, ly, leftW, []span{
				{"mem ", Style{FG: m.pal.GraphText}},
				{humanKiB(d.MemMaximumKiB), Style{FG: m.pal.InactiveFG}},
				{" allocated · not running", Style{FG: m.pal.InactiveFG}},
			}, nil)
		case r.MemStatus == hypervisor.MemUnknown:
			putSpans(s, cx, ly, leftW, []span{
				{"mem ", Style{FG: m.pal.GraphText}},
				{"—", Style{FG: m.pal.MainFG}},
				{" of ", Style{FG: m.pal.GraphText}},
				{humanKiB(d.MemCurrentKiB), Style{FG: m.pal.MainFG}},
				{" · no guest memory statistics reported", Style{FG: m.pal.StatePaused}},
			}, nil)
		case r.MemStatus == hypervisor.MemStale:
			putSpans(s, cx, ly, leftW, []span{
				{"mem ", muted},
				{humanKiB(r.MemUsedKiB) + "/" + humanKiB(d.MemCurrentKiB) + " (" + pct(r.MemPct) + ")", muted},
				{" · STALE " + duration(d.MemStatsAge(now)) + " old", Style{FG: m.pal.StatePaused, Bold: true}},
			}, nil)
		default:
			putSpans(s, cx, ly, leftW, []span{
				{"mem ", Style{FG: m.pal.GraphText}},
				{humanKiB(r.MemUsedKiB), Style{FG: m.pal.MainFG}},
				{"/", Style{FG: m.pal.GraphText}},
				{humanKiB(d.MemCurrentKiB), Style{FG: m.pal.MainFG}},
				{" (" + pct(r.MemPct) + ")", Style{FG: m.pal.MainFG}},
				{" · max ", Style{FG: m.pal.GraphText}},
				{humanKiB(info.MaxMemoryKiB), Style{FG: m.pal.MainFG}},
			}, nil)
		}
		ly++
	}
	if ly < cy+ch && memLines >= 2 {
		if r.MemStatus == hypervisor.MemFresh {
			avail := uint64(0)
			if d.MemCurrentKiB > r.MemUsedKiB {
				avail = d.MemCurrentKiB - r.MemUsedKiB
			}
			putSpans(s, cx, ly, leftW, []span{
				{"avail ", Style{FG: m.pal.GraphText}},
				{humanKiB(avail), Style{FG: m.pal.Avail.At(0.55)}},
				{" · rss ", Style{FG: m.pal.GraphText}},
				{humanKiB(d.MemRSSKiB), Style{FG: m.pal.MainFG}},
				{" · swap ", Style{FG: m.pal.GraphText}},
				{fmt.Sprintf("%s/%s", humanKiB(d.SwapInKiB), humanKiB(d.SwapOutKiB)), Style{FG: m.pal.MainFG}},
				{" · faults ", Style{FG: m.pal.GraphText}},
				{fmt.Sprintf("%d/%d", d.MajorFault, d.MinorFault), Style{FG: m.pal.MainFG}},
			}, nil)
		} else if d.State.Active() {
			// Only host-side RSS is trustworthy here; avail/swap/faults come
			// from the same stale or missing guest statistics.
			putSpans(s, cx, ly, leftW, []span{
				{"rss ", Style{FG: m.pal.GraphText}},
				{humanKiB(d.MemRSSKiB), Style{FG: m.pal.MainFG}},
				{" · set <stats period='5'/> on the memballoon", Style{FG: m.pal.GraphText}},
			}, nil)
		}
	}

	if rightW <= 0 {
		return
	}

	// Right column: disks and network.
	ry := cy
	if ry < cy+ch {
		s.Put(rightX, ry, rightW, "DISKS", Style{FG: m.pal.BorderCPU, Bold: true})
		ry++
	}
	shownDisks := 0
	for _, disk := range d.Disks {
		if disk.Capacity == 0 && disk.Allocation == 0 {
			continue
		}
		if ry >= cy+ch {
			break
		}
		shownDisks++
		alloc := 0.0
		if disk.Capacity > 0 {
			alloc = float64(disk.Allocation) / float64(disk.Capacity) * 100
		}
		alloc = clamp(alloc, 0, 100)
		putSpans(s, rightX, ry, rightW, []span{
			{disk.Name, Style{FG: m.pal.MainFG, Bold: true}},
			{"  " + humanBytes(float64(disk.Capacity)), Style{FG: m.pal.MainFG}},
			{"  alloc " + pct(alloc), Style{FG: m.pal.Mem.At(clamp(alloc/100, 0, 1))}},
		}, nil)
		ry++
		if ry < cy+ch && rightW >= 34 {
			putSpans(s, rightX, ry, rightW, []span{
				{"   read ", Style{FG: m.pal.GraphText}},
				{humanBytes(float64(disk.RDBytes)), Style{FG: m.pal.Download.At(0.6)}},
				{" · write ", Style{FG: m.pal.GraphText}},
				{humanBytes(float64(disk.WRBytes)), Style{FG: m.pal.Upload.At(0.6)}},
			}, nil)
			ry++
		}
	}
	if shownDisks == 0 && ry < cy+ch {
		s.Put(rightX, ry, rightW, "no block devices", Style{FG: m.pal.GraphText})
		ry++
	}

	if ry < cy+ch {
		ry++
	}
	if ry < cy+ch {
		s.Put(rightX, ry, rightW, "NETWORK", Style{FG: m.pal.BorderNet, Bold: true})
		ry++
	}
	if ry < cy+ch {
		putSpans(s, rightX, ry, rightW, []span{
			{"rx ", Style{FG: m.pal.GraphText}},
			{humanRate(r.NetRXBps), Style{FG: m.pal.Download.At(0.6)}},
			{" · tx ", Style{FG: m.pal.GraphText}},
			{humanRate(r.NetTXBps), Style{FG: m.pal.Upload.At(0.6)}},
		}, nil)
		ry++
	}
	if len(d.Nets) > 0 && ry < cy+ch {
		names := ""
		for i, n := range d.Nets {
			if i > 0 {
				names += ", "
			}
			names += n.Name
		}
		putSpans(s, rightX, ry, rightW, []span{
			{"host ", Style{FG: m.pal.GraphText}},
			{names, Style{FG: m.pal.MainFG}},
		}, nil)
		ry++
	}

	entry, cached := m.ifaceCache[d.Name]
	switch {
	case !d.State.Active():
		if ry < cy+ch {
			s.Put(rightX, ry, rightW, "addresses: VM not running", Style{FG: m.pal.GraphText})
			ry++
		}
	case !cached || (len(entry.ifaces) == 0 && entry.err == ""):
		if ry < cy+ch {
			msg := "addresses: none found"
			if !cached {
				msg = "addresses: not resolved"
				if m.ifacePending {
					msg = "resolving interface addresses…"
				}
			}
			s.Put(rightX, ry, rightW, truncate(msg, rightW), Style{FG: m.pal.GraphText})
			ry++
		}
	case entry.err != "":
		if ry < cy+ch {
			s.Put(rightX, ry, rightW, truncate("addresses: "+entry.err, rightW), Style{FG: m.pal.Error})
			ry++
		}
	default:
		for _, iface := range entry.ifaces {
			if ry >= cy+ch {
				break
			}
			spans := []span{{iface.Name, Style{FG: m.pal.MainFG, Bold: true}}}
			if len(iface.Addrs) > 0 {
				spans = append(spans, span{" " + iface.Addrs[0], Style{FG: m.pal.StateRunning}})
				for _, a := range iface.Addrs[1:] {
					spans = append(spans, span{", " + a, Style{FG: m.pal.StateRunning}})
				}
			} else {
				spans = append(spans, span{" " + iface.MAC, Style{FG: m.pal.GraphText}})
			}
			putSpans(s, rightX, ry, rightW, spans, nil)
			ry++
		}
	}

	if info.Err != "" && ry < cy+ch {
		ry++
		if ry < cy+ch {
			s.Put(rightX, ry, rightW, truncate("metadata: "+info.Err, rightW), Style{FG: m.pal.Error})
		}
	}
}
