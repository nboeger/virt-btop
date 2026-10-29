package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nboeger/virt-btop/internal/hypervisor"
	"github.com/nboeger/virt-btop/internal/metrics"
	"github.com/nboeger/virt-btop/internal/tui"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "1.0"

const (
	minInterval = 250 * time.Millisecond
	maxInterval = 10 * time.Second
	// closeTimeout bounds how long exit waits for libvirt. Close blocks while a
	// call is in flight, and a hung libvirtd would otherwise make quit hang.
	closeTimeout = 2 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main without os.Exit so that deferred cleanup always executes.
func run(args []string, stdout, stderr io.Writer) int {
	var (
		uri       string
		interval  time.Duration
		readWrite bool
		once      bool
		showVer   bool
		sortName  string
		filterStr string
	)

	fs := flag.NewFlagSet("virt-btop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&uri, "c", "", "libvirt connection URI (default: $LIBVIRT_DEFAULT_URI or qemu:///system)")
	fs.StringVar(&uri, "connect", "", "libvirt connection URI (default: $LIBVIRT_DEFAULT_URI or qemu:///system)")
	fs.DurationVar(&interval, "i", time.Second, "poll interval, 250ms to 10s")
	fs.DurationVar(&interval, "interval", time.Second, "poll interval, 250ms to 10s")
	fs.BoolVar(&readWrite, "read-write", false, "use a read-write connection (needed for guest-agent IPs; libvirt group required)")
	fs.BoolVar(&once, "once", false, "collect two samples and print a text table, then exit")
	fs.StringVar(&sortName, "sort", "cpu", "initial sort: cpu, mem, name, disk, net, state")
	fs.StringVar(&filterStr, "filter", "running", "initial filter: all, running, paused, stopped")
	fs.BoolVar(&showVer, "version", false, "print version and exit")
	fs.BoolVar(&showVer, "v", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	if showVer {
		fmt.Fprintln(stdout, "virt-btop", version)
		return 0
	}
	if interval < minInterval || interval > maxInterval {
		fmt.Fprintf(stderr, "virt-btop: invalid interval %s (must be between %s and %s)\n", interval, minInterval, maxInterval)
		return 2
	}
	sortKey, ok := tui.ParseSort(sortName)
	if !ok {
		fmt.Fprintf(stderr, "virt-btop: invalid --sort %q (want cpu, mem, name, disk, net, state)\n", sortName)
		return 2
	}
	filter, ok := tui.ParseFilter(filterStr)
	if !ok {
		fmt.Fprintf(stderr, "virt-btop: invalid --filter %q (want all, running, paused, stopped)\n", filterStr)
		return 2
	}

	client, err := hypervisor.Connect(uri, readWrite)
	if err != nil {
		fmt.Fprintf(stderr, "virt-btop: %v%s\n", err, connectHint(err, readWrite))
		return 1
	}
	defer closeClient(client, stderr)

	initial, err := client.Collect()
	if err != nil {
		fmt.Fprintf(stderr, "virt-btop: %v%s\n", err, connectHint(err, readWrite))
		return 1
	}

	if once {
		if err := printOnce(stdout, stderr, client, initial, interval, sortKey, filter); err != nil {
			fmt.Fprintf(stderr, "virt-btop: %v\n", err)
			return 1
		}
		return 0
	}

	model := tui.New(client, initial, tui.Options{
		Interval: interval,
		Sort:     sortKey,
		Filter:   filter,
	})
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(stderr, "virt-btop: %v\n", err)
		return 1
	}
	return 0
}

// closeClient closes the libvirt connection but gives up after closeTimeout so
// a hung libvirtd cannot keep the process alive; the OS reclaims the socket.
func closeClient(client *hypervisor.Client, stderr io.Writer) {
	done := make(chan error, 1)
	go func() { done <- client.Close() }()
	select {
	case err := <-done:
		if err != nil {
			fmt.Fprintf(stderr, "virt-btop: closing libvirt connection: %v\n", err)
		}
	case <-time.After(closeTimeout):
		fmt.Fprintf(stderr, "virt-btop: libvirt did not respond to close within %s; exiting anyway\n", closeTimeout)
	}
}

func printOnce(stdout, stderr io.Writer, client *hypervisor.Client, initial *hypervisor.Snapshot, interval time.Duration, sortKey tui.SortKey, filter tui.Filter) error {
	prev := make(map[string]metrics.Counters, len(initial.Domains))
	for _, d := range initial.Domains {
		prev[d.Name] = metrics.CountersOf(d, initial.At)
	}

	time.Sleep(interval)

	snap, err := client.Collect()
	if err != nil {
		return err
	}
	for _, w := range snap.Warnings {
		fmt.Fprintf(stderr, "virt-btop: warning: %s\n", w)
	}
	dt := snap.At.Sub(initial.At).Seconds()
	rates := make(map[string]metrics.Rates, len(snap.Domains))
	for _, d := range snap.Domains {
		var p *metrics.Counters
		if c, ok := prev[d.Name]; ok {
			p = &c
		}
		rates[d.Name] = metrics.Compute(p, metrics.CountersOf(d, snap.At), d, runtime.NumCPU())
	}

	filtered := make([]*hypervisor.Domain, 0, len(snap.Domains))
	for _, d := range snap.Domains {
		if filter.Match(d) {
			filtered = append(filtered, d)
		}
	}
	tui.SortDomains(filtered, rates, sortKey, false)

	// Render into memory (which cannot fail) and write once, so the only error
	// to check is the final write.
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tSTATE\tvCPU\tCPU%%\tMEM\tDISK R/W\tNET RX/TX\n")
	for _, d := range filtered {
		r := rates[d.Name]
		mem, disk, net, cpu := "—", "—", "—", "—"
		switch {
		case !d.State.Active():
			if d.MemMaximumKiB > 0 {
				mem = tui.FormatKiBShort(d.MemMaximumKiB) + " alloc"
			}
		default:
			mem = memCell(d, r)
			disk = fmt.Sprintf("R%s W%s", tui.FormatRateShort(r.DiskRDBps), tui.FormatRateShort(r.DiskWRBps))
			net = fmt.Sprintf("↓%s ↑%s", tui.FormatRateShort(r.NetRXBps), tui.FormatRateShort(r.NetTXBps))
			cpu = tui.FormatPct(r.CPUPct)
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n", d.Name, d.State, d.VCPUs, cpu, mem, disk, net)
	}
	fmt.Fprintf(w, "\n%d/%d domains · %.0fms capture\n", len(filtered), len(snap.Domains), dt*1000)
	if err := w.Flush(); err != nil {
		return err
	}
	_, err = stdout.Write(buf.Bytes())
	return err
}

// memCell formats guest memory for --once, never presenting missing or stale
// statistics as if they were live.
func memCell(d *hypervisor.Domain, r metrics.Rates) string {
	total := tui.FormatKiBShort(d.MemCurrentKiB)
	switch r.MemStatus {
	case hypervisor.MemFresh:
		return tui.FormatKiBShort(r.MemUsedKiB) + "/" + total
	case hypervisor.MemStale:
		return tui.FormatKiBShort(r.MemUsedKiB) + "/" + total + " (stale)"
	default:
		return "n/a/" + total
	}
}

// connectHint adds actionable advice for a failed connection. It is based on
// the libvirt error code, not on matching message text, and only mentions the
// libvirt group when a read-write connection was actually requested.
func connectHint(err error, readWrite bool) string {
	if !hypervisor.IsPermissionDenied(err) {
		return ""
	}
	if readWrite {
		return "\n\nA read-write connection needs libvirt group membership:\n" +
			"  sudo usermod -aG libvirt $USER   (then log out and back in)\n" +
			"or drop --read-write for read-only monitoring."
	}
	return "\n\nRead-only access was denied. Check that libvirtd's read-only socket\n" +
		"(/run/libvirt/libvirt-sock-ro) is accessible, or try -c qemu:///session."
}
