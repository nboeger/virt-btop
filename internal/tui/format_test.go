package tui

import (
	"testing"
	"time"
)

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.00 KiB"},
		{1536, "1.50 KiB"},
		{10 * 1024, "10.0 KiB"},
		{10 * 1024 * 1024, "10.0 MiB"},
		{150 * 1024 * 1024, "150 MiB"},
		{8.2 * 1024 * 1024 * 1024, "8.20 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanShort(t *testing.T) {
	if got := humanRateShort(8.2 * 1024 * 1024); got != "8.20M" {
		t.Fatalf("short rate = %q", got)
	}
	if got := humanKiBShort(1024 * 1024); got != "1.00G" {
		t.Fatalf("short KiB = %q", got)
	}
	if got := humanRateShort(0); got != "0" {
		t.Fatalf("zero rate = %q", got)
	}
}

func TestPct(t *testing.T) {
	if got := pct(42.4); got != "42%" {
		t.Fatalf("pct = %q", got)
	}
	if got := pct(0.4); got != "<1%" {
		t.Fatalf("small pct = %q", got)
	}
	if got := pct(0); got != "0%" {
		t.Fatalf("zero pct = %q", got)
	}
}

func TestDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{42 * time.Second, "42s"},
		{90 * time.Second, "1m"},
		{3*time.Hour + 2*time.Minute, "3h02m"},
		{25 * time.Hour, "1d1h"},
	}
	for _, c := range cases {
		if got := duration(c.in); got != c.want {
			t.Errorf("duration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNiceMax(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 1},
		{1, 1},
		{800, 1000},
		{1200, 2000},
		{8.2e6, 1e7},
	}
	for _, c := range cases {
		if got := niceMax(c.in); got != c.want {
			t.Errorf("niceMax(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTruncateAndClamp(t *testing.T) {
	if got := truncate("dalek-dev", 5); got != "dale…" {
		t.Fatalf("truncate = %q", got)
	}
	if got := clamp(1.5, 0, 1); got != 1 {
		t.Fatalf("clamp = %v", got)
	}
}
