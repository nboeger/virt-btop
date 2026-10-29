package metrics

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Host is a snapshot of host-wide resource use, used only in the top bar.
type Host struct {
	Hostname    string
	Cores       int
	CPUPercent  float64
	MemTotalKiB uint64
	MemUsedKiB  uint64
	Load1       float64
	Load5       float64
	Load15      float64
	Uptime      time.Duration

	// Err is non-nil when some of the values above could not be read (for
	// example /proc is unavailable). Unreadable values are left at zero.
	Err error
}

// HostSampler reads /proc on Linux. It keeps the previous CPU counters so it
// can report a rate instead of a since-boot average.
type HostSampler struct {
	hostname string
	cores    int

	prevTotal uint64
	prevIdle  uint64
	havePrev  bool
}

func NewHostSampler() *HostSampler {
	// An unreadable hostname is cosmetic; the UI falls back to "localhost".
	hostname, _ := os.Hostname()
	return &HostSampler{hostname: hostname, cores: runtime.NumCPU()}
}

// Sample reads the host's current state. Individual failures do not abort the
// sample; they are joined into Host.Err.
func (h *HostSampler) Sample() Host {
	host := Host{Hostname: h.hostname, Cores: h.cores}
	var errs []error

	if data, err := os.ReadFile("/proc/stat"); err != nil {
		errs = append(errs, err)
	} else if total, idle, err := parseCPUStat(data); err != nil {
		errs = append(errs, fmt.Errorf("/proc/stat: %w", err))
	} else {
		if h.havePrev && total > h.prevTotal && idle >= h.prevIdle {
			dTotal := total - h.prevTotal
			dIdle := idle - h.prevIdle
			host.CPUPercent = float64(dTotal-dIdle) / float64(dTotal) * 100
		}
		h.prevTotal, h.prevIdle, h.havePrev = total, idle, true
	}

	if data, err := os.ReadFile("/proc/meminfo"); err != nil {
		errs = append(errs, err)
	} else if total, avail, err := parseMemInfo(data); err != nil {
		errs = append(errs, fmt.Errorf("/proc/meminfo: %w", err))
	} else {
		host.MemTotalKiB = total
		if total > avail {
			host.MemUsedKiB = total - avail
		}
	}

	if data, err := os.ReadFile("/proc/loadavg"); err != nil {
		errs = append(errs, err)
	} else if l1, l5, l15, err := parseLoadAvg(data); err != nil {
		errs = append(errs, fmt.Errorf("/proc/loadavg: %w", err))
	} else {
		host.Load1, host.Load5, host.Load15 = l1, l5, l15
	}

	if data, err := os.ReadFile("/proc/uptime"); err != nil {
		errs = append(errs, err)
	} else if up, err := parseUptime(data); err != nil {
		errs = append(errs, fmt.Errorf("/proc/uptime: %w", err))
	} else {
		host.Uptime = up
	}

	host.Err = errors.Join(errs...)
	return host
}

// parseCPUStat returns total and idle jiffies from the aggregate "cpu" line.
//
// Only the first eight fields are summed. guest and guest_nice (fields 9 and
// 10) are already counted inside user and nice, so adding them would count
// virtual-machine time twice, which badly inflates host CPU% on a hypervisor.
func parseCPUStat(data []byte) (total, idle uint64, err error) {
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 4 {
			return 0, 0, fmt.Errorf("short cpu line: %q", line)
		}
		if len(fields) > 8 {
			fields = fields[:8]
		}
		for i, f := range fields {
			v, perr := strconv.ParseUint(f, 10, 64)
			if perr != nil {
				return 0, 0, fmt.Errorf("field %d: %w", i, perr)
			}
			total += v
			if i == 3 || i == 4 { // idle + iowait
				idle += v
			}
		}
		return total, idle, nil
	}
	return 0, 0, errors.New("no aggregate cpu line")
}

func parseMemInfo(data []byte) (total, available uint64, err error) {
	var free, buffers, cached uint64
	var haveTotal, haveAvail bool
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var dst *uint64
		switch fields[0] {
		case "MemTotal:":
			dst, haveTotal = &total, true
		case "MemAvailable:":
			dst, haveAvail = &available, true
		case "MemFree:":
			dst = &free
		case "Buffers:":
			dst = &buffers
		case "Cached:":
			dst = &cached
		default:
			continue
		}
		v, perr := strconv.ParseUint(fields[1], 10, 64)
		if perr != nil {
			return 0, 0, fmt.Errorf("%s %w", fields[0], perr)
		}
		*dst = v
	}
	if !haveTotal {
		return 0, 0, errors.New("MemTotal missing")
	}
	if !haveAvail {
		available = free + buffers + cached
	}
	return total, available, nil
}

func parseLoadAvg(data []byte) (l1, l5, l15 float64, err error) {
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("expected 3 load fields, got %d", len(fields))
	}
	vals := [3]float64{}
	for i := range vals {
		vals[i], err = strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return 0, 0, 0, err
		}
	}
	return vals[0], vals[1], vals[2], nil
}

func parseUptime(data []byte) (time.Duration, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, errors.New("empty")
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(secs * float64(time.Second)), nil
}
