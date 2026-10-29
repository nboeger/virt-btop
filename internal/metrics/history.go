package metrics

import "github.com/nboeger/virt-btop/internal/hypervisor"

// MaxHistory is roughly 15 minutes at the default 1s interval.
const MaxHistory = 900

// History holds the combined series for the main boxes and a CPU series per VM
// for the detail overlay.
type History struct {
	CPU    []float64
	Mem    []float64
	NetRX  []float64
	NetTX  []float64
	DiskRD []float64
	DiskWR []float64
	VMs    map[string][]float64
}

func NewHistory() *History {
	return &History{VMs: make(map[string][]float64)}
}

func (h *History) Push(c Combined, domains []*hypervisor.Domain, rates map[string]Rates) {
	h.CPU = push(h.CPU, c.VCPUUtilPct)
	h.Mem = push(h.Mem, c.MemPct)
	h.NetRX = push(h.NetRX, c.NetRXBps)
	h.NetTX = push(h.NetTX, c.NetTXBps)
	h.DiskRD = push(h.DiskRD, c.DiskRDBps)
	h.DiskWR = push(h.DiskWR, c.DiskWRBps)

	live := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		live[d.Name] = struct{}{}
		h.VMs[d.Name] = push(h.VMs[d.Name], rates[d.Name].CPUPct)
	}
	for name := range h.VMs {
		if _, ok := live[name]; !ok {
			delete(h.VMs, name)
		}
	}
}

func push(s []float64, v float64) []float64 {
	s = append(s, v)
	if len(s) > MaxHistory {
		s = s[len(s)-MaxHistory:]
	}
	return s
}

// Window returns the last n values, left-padded with zeros when history is
// still accumulating.
func Window(values []float64, n int) []float64 {
	if n <= 0 {
		return nil
	}
	out := make([]float64, n)
	if len(values) >= n {
		copy(out, values[len(values)-n:])
		return out
	}
	copy(out[n-len(values):], values)
	return out
}
