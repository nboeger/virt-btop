package tui

import (
	"fmt"
	"math"
	"time"

	"github.com/mattn/go-runewidth"
)

func humanBytes(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		v = 0
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f B", v)
	}
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f %s", v, units[i])
	case v >= 10:
		return fmt.Sprintf("%.1f %s", v, units[i])
	default:
		return fmt.Sprintf("%.2f %s", v, units[i])
	}
}

func humanRate(v float64) string {
	return humanBytes(v) + "/s"
}

// humanBytesShort is for dense table cells: 8.2M, 512K, 0.
func humanBytesShort(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return "0"
	}
	units := []string{"B", "K", "M", "G", "T", "P"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f", v)
	}
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f%s", v, units[i])
	case v >= 10:
		return fmt.Sprintf("%.1f%s", v, units[i])
	default:
		return fmt.Sprintf("%.2f%s", v, units[i])
	}
}

func humanRateShort(v float64) string { return humanBytesShort(v) }
func humanKiB(k uint64) string        { return humanBytes(float64(k) * 1024) }
func humanKiBShort(k uint64) string   { return humanBytesShort(float64(k) * 1024) }

func pct(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0%"
	}
	if v > 0 && v < 1 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", v)
}

func pct1(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", v)
}

func duration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	s := int64(d.Seconds())
	days := s / 86400
	hours := (s % 86400) / 3600
	mins := (s % 3600) / 60
	secs := s % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm", mins)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// truncate shortens s to w cells. It sanitizes first so the width it measures
// is the width Screen.Put actually draws.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.Truncate(sanitize(s), w, "…")
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func niceMax(v float64) float64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 1
	}
	exp := math.Floor(math.Log10(v))
	base := math.Pow(10, exp)
	frac := v / base
	nice := 10.0
	switch {
	case frac <= 1:
		nice = 1
	case frac <= 2:
		nice = 2
	case frac <= 5:
		nice = 5
	}
	return nice * base
}

// Exported formatting helpers for the --once text output.
func FormatRateShort(v float64) string { return humanRateShort(v) }
func FormatKiBShort(k uint64) string   { return humanKiBShort(k) }
func FormatPct(v float64) string       { return pct(v) }
