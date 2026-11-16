package tui

import (
	"fmt"

	"github.com/nboeger/virt-btop/internal/hypervisor"
)

// hostSummary is the combined node-wide view shown in the header.
type hostSummary struct {
	Configured int
	Reporting  int
	Cores      int

	CPUPercent float64
	CPUValid   bool

	MemTotalKiB uint64
	MemUsedKiB  uint64
	MemValid    bool

	Errors int
}

// summarizeHosts combines per-host node stats. CPU is weighted by each host's
// core count, so a 64-core server does not have the same influence as a
// 2-core machine. Memory is summed in KiB.
func summarizeHosts(hosts []hypervisor.HostStats, configured int) hostSummary {
	s := hostSummary{Reporting: len(hosts), Configured: configured}
	if s.Configured <= 0 {
		s.Configured = len(hosts)
	}

	var weightedCPU, weight float64
	for _, h := range hosts {
		if h.Cores > 0 {
			s.Cores += h.Cores
		}
		if h.CPUValid {
			w := float64(h.Cores)
			if w <= 0 {
				w = 1
			}
			weightedCPU += h.CPUPercent * w
			weight += w
		}
		if h.MemValid {
			s.MemTotalKiB += h.MemTotalKiB
			s.MemUsedKiB += h.MemUsedKiB
		}
		if h.Err != "" {
			s.Errors++
		}
	}
	if weight > 0 {
		s.CPUPercent = weightedCPU / weight
		s.CPUValid = true
	}
	s.MemValid = s.MemTotalKiB > 0
	return s
}

func (s hostSummary) memPct() float64 {
	if s.MemTotalKiB == 0 {
		return 0
	}
	return float64(s.MemUsedKiB) / float64(s.MemTotalKiB) * 100
}

// hostTotals returns the aggregate header values. A missing snapshot (for
// example before the first poll) simply reports no remote hosts.
func (m *Model) hostTotals() hostSummary {
	if m.snap == nil {
		return hostSummary{Configured: m.hostCount}
	}
	return summarizeHosts(m.snap.Hosts, m.hostCount)
}

func hostDisplayName(h hypervisor.HostStats) (name, detail string) {
	name = h.Label
	if name == "" {
		name = h.Hostname
	}
	if name == "" {
		name = "local"
	}
	if h.Hostname != "" && h.Hostname != name {
		detail = h.Hostname
	}
	return name, detail
}

func (m *Model) renderHosts(s *Screen, l Layout) {
	hosts := []hypervisor.HostStats(nil)
	if m.snap != nil {
		hosts = m.snap.Hosts
	}
	hs := summarizeHosts(hosts, m.hostCount)

	height := clampInt(len(hosts)+5, 9, 24)
	x, y, width, height := overlayRect(l, 104, height)
	sub := fmt.Sprintf("%d/%d reporting · H/esc close", hs.Reporting, hs.Configured)
	s.Box(x, y, width, height, "hosts", sub, m.pal.DivLine, m.pal.Title, m.pal.GraphText)

	cx, cy := x+2, y+1
	cw, ch := width-4, height-2
	if cw <= 0 || ch <= 0 {
		return
	}

	row := 0
	if ch > 0 {
		putSpans(s, cx, cy, cw, []span{
			{hostCores(hs.Cores), Style{FG: m.pal.MainFG}},
			{" · cpu ", Style{FG: m.pal.GraphText}},
			{hostPct(hs.CPUValid, hs.CPUPercent), Style{FG: m.pal.CPU.At(clamp(hs.CPUPercent/100, 0, 1))}},
			{" · mem ", Style{FG: m.pal.GraphText}},
			{hostMem(hs.MemValid, hs.MemUsedKiB, hs.MemTotalKiB), Style{FG: m.pal.Mem.At(clamp(hs.memPct()/100, 0, 1))}},
		}, nil)
		row++
	}
	if ch > 1 {
		row++
	}

	if len(hosts) == 0 && row < ch {
		s.Put(cx, cy+row, cw, "no host statistics available", Style{FG: m.pal.GraphText})
		return
	}

	for _, h := range hosts {
		if row >= ch {
			break
		}
		name, detail := hostDisplayName(h)
		nameStyle := Style{FG: m.pal.MainFG, Bold: true}
		if h.Err != "" {
			nameStyle.FG = m.pal.Error
		}
		spans := []span{{name, nameStyle}}
		if detail != "" {
			spans = append(spans, span{" (" + detail + ")", Style{FG: m.pal.GraphText}})
		}
		spans = append(spans,
			span{" · ", Style{FG: m.pal.DivLine}},
			span{hostCores(h.Cores), Style{FG: m.pal.MainFG}},
			span{" · cpu ", Style{FG: m.pal.GraphText}},
			span{hostPct(h.CPUValid, h.CPUPercent), Style{FG: m.pal.CPU.At(clamp(h.CPUPercent/100, 0, 1))}},
			span{" · mem ", Style{FG: m.pal.GraphText}},
			span{hostMem(h.MemValid, h.MemUsedKiB, h.MemTotalKiB), Style{FG: m.pal.Mem.At(clamp(hostMemPct(h)/100, 0, 1))}},
		)
		if h.Err != "" {
			spans = append(spans, span{" · " + h.Err, Style{FG: m.pal.Error}})
		}
		putSpans(s, cx, cy+row, cw, spans, nil)
		row++
	}

	if hs.Reporting < hs.Configured && row < ch {
		s.Put(cx, cy+row, cw, fmt.Sprintf("%d configured host(s) not reporting", hs.Configured-hs.Reporting), Style{FG: m.pal.Error})
		row++
	}
	if warnings := m.snapWarnings(); row < ch && len(warnings) > 0 {
		s.Put(cx, cy+row, cw, truncate(warnings[0], cw), Style{FG: m.pal.Error})
	}
}

func (m *Model) snapWarnings() []string {
	if m.snap == nil {
		return nil
	}
	return m.snap.Warnings
}

func hostPct(valid bool, pct float64) string {
	if !valid {
		return "—"
	}
	return pct1(pct)
}

func hostStatErrLabel(n int) string {
	if n == 1 {
		return "1 host stat error"
	}
	return fmt.Sprintf("%d host stat errors", n)
}

func hostCores(n int) string {
	if n <= 0 {
		return "cores —"
	}
	return fmt.Sprintf("%d cores", n)
}

func hostMem(valid bool, used, total uint64) string {
	if !valid || total == 0 {
		return "—"
	}
	return fmt.Sprintf("%s/%s", humanKiB(used), humanKiB(total))
}

func hostMemPct(h hypervisor.HostStats) float64 {
	if h.MemTotalKiB == 0 {
		return 0
	}
	return float64(h.MemUsedKiB) / float64(h.MemTotalKiB) * 100
}
