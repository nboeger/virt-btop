package hypervisor

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	libvirt "libvirt.org/go/libvirt"
)

const statsTypes = libvirt.DOMAIN_STATS_STATE |
	libvirt.DOMAIN_STATS_CPU_TOTAL |
	libvirt.DOMAIN_STATS_BALLOON |
	libvirt.DOMAIN_STATS_VCPU |
	libvirt.DOMAIN_STATS_BLOCK |
	libvirt.DOMAIN_STATS_INTERFACE

// infoTTL bounds how stale cached domain metadata (ID, autostart, vCPU count)
// can get. The cache is also refreshed whenever a domain's state changes.
const infoTTL = 30 * time.Second

var (
	errClosed       = errors.New("libvirt client is closed")
	errDisconnected = errors.New("not connected to libvirt")
)

type cachedInfo struct {
	info  Info
	state State
	at    time.Time
}

// Client owns one libvirt connection.
//
// libvirt connections are safe for concurrent use, so Collect and Interfaces
// run concurrently under the read side of connMu: a slow guest-agent lookup
// cannot stall polling. The write side is taken only to replace or close the
// connection, which must not happen while a call is in flight (the binding
// uses the raw connection pointer without taking its own reference).
type Client struct {
	uri       string
	readWrite bool

	connMu sync.RWMutex
	conn   *libvirt.Connect // nil while disconnected
	closed bool

	infoMu sync.Mutex
	infos  map[string]cachedInfo

	// periodTried records domains (name/ID) whose balloon stats period we
	// already tried to enable, so a failure is not retried every poll.
	periodTried map[string]bool
}

// balloonStatsPeriod is the guest memory stats period (seconds) enabled in
// read-write mode for domains that do not report memory statistics.
const balloonStatsPeriod = 5

func openConn(uri string, readWrite bool) (*libvirt.Connect, error) {
	if readWrite {
		return libvirt.NewConnect(uri)
	}
	return libvirt.NewConnectReadOnly(uri)
}

// Connect creates a read-only connection by default. Read-only monitoring is
// allowed by libvirt's local polkit policy without adding the user to the
// libvirt group. Use readWrite for guest-agent interface addresses.
func Connect(uri string, readWrite bool) (*Client, error) {
	if uri == "" {
		uri = os.Getenv("LIBVIRT_DEFAULT_URI")
	}
	if uri == "" {
		uri = "qemu:///system"
	}
	conn, err := openConn(uri, readWrite)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", uri, err)
	}
	return &Client{
		uri:       uri,
		readWrite: readWrite,
		conn:      conn,
		infos:     make(map[string]cachedInfo),
	}, nil
}

func (c *Client) URI() string     { return c.uri }
func (c *Client) ReadWrite() bool { return c.readWrite }

// Close waits for in-flight calls to finish. If libvirtd is hung that can take
// a while; callers that must exit promptly should bound the wait themselves.
func (c *Client) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	c.closed = true
	if c.conn == nil {
		return nil
	}
	_, err := c.conn.Close()
	c.conn = nil
	return err
}

// InvalidateInfos forces domain metadata to be re-read on the next Collect.
func (c *Client) InvalidateInfos() {
	c.infoMu.Lock()
	defer c.infoMu.Unlock()
	c.infos = make(map[string]cachedInfo)
}

// Collect returns one snapshot of every domain. libvirt's
// virConnectGetAllDomainStats (nil domain list) includes inactive domains too.
//
// If the call fails and the connection turns out to be dead (for example
// libvirtd was restarted), Collect reconnects and retries once.
func (c *Client) Collect() (*Snapshot, error) {
	snap, err := c.collect()
	if err == nil {
		return snap, nil
	}
	reconnected, rerr := c.reconnectIfDead()
	if rerr != nil {
		return nil, fmt.Errorf("%w (reconnect failed: %v)", err, rerr)
	}
	if !reconnected {
		return nil, err
	}
	return c.collect()
}

// reconnectIfDead replaces the connection when libvirt says it is no longer
// alive. It returns (false, nil) when the connection is healthy, so errors
// unrelated to connectivity do not cause reconnect churn.
func (c *Client) reconnectIfDead() (bool, error) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.closed {
		return false, errClosed
	}
	if c.conn != nil {
		if alive, err := c.conn.IsAlive(); err == nil && alive {
			return false, nil
		}
		// The old connection is already dead, so a Close error is expected
		// and carries no information; the handle is released either way.
		_, _ = c.conn.Close()
		c.conn = nil
	}
	conn, err := openConn(c.uri, c.readWrite)
	if err != nil {
		return false, err
	}
	c.conn = conn
	c.InvalidateInfos()
	return true, nil
}

func (c *Client) collect() (*Snapshot, error) {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	if c.closed {
		return nil, errClosed
	}
	if c.conn == nil {
		return nil, errDisconnected
	}

	stats, err := c.conn.GetAllDomainStats(nil, statsTypes, 0)
	if err != nil {
		return nil, fmt.Errorf("domain stats: %w", err)
	}
	// The binding takes a reference on every returned domain; release them all.
	defer func() {
		for i := range stats {
			// Free fails only if the handle is already invalid; there is
			// nothing useful to do about it at this point.
			_ = stats[i].Domain.Free()
		}
	}()

	now := time.Now()
	domains := make([]*Domain, 0, len(stats))
	var warnings []string
	seen := make(map[string]bool, len(stats))
	for i := range stats {
		name, err := stats[i].Domain.GetName()
		if err != nil || name == "" {
			if err == nil {
				err = errors.New("empty name")
			}
			warnings = append(warnings, fmt.Sprintf("domain #%d skipped: name lookup failed: %v", i, err))
			continue
		}
		d := domainFromStats(name, &stats[i])
		d.Info = c.infoFor(name, stats[i].Domain, d.State, now)
		if d.VCPUs == 0 {
			d.VCPUs = d.Info.ConfiguredVCPUs
		}
		if d.VCPUs == 0 {
			d.VCPUs = 1
		}
		c.ensureStatsPeriod(name, stats[i].Domain, d, now)
		domains = append(domains, d)
		seen[name] = true
	}

	c.infoMu.Lock()
	for name := range c.infos {
		if !seen[name] {
			delete(c.infos, name)
		}
	}
	c.infoMu.Unlock()

	sort.Slice(domains, func(i, j int) bool { return domains[i].Name < domains[j].Name })
	return NewSnapshot(now, domains, warnings), nil
}

// ensureStatsPeriod turns on live balloon statistics for an active domain
// whose guest memory stats are missing or stale because no stats period is set.
// It needs a read-write connection and only changes the running domain (not
// its persistent definition); the change is lost when the domain restarts, at
// which point it is applied again. Failures are ignored: memory is then simply
// shown as unavailable.
func (c *Client) ensureStatsPeriod(name string, dom *libvirt.Domain, d *Domain, now time.Time) {
	if !c.readWrite || !d.State.Active() || d.MemCurrentKiB == 0 {
		return
	}
	if d.MemStatus(now) == MemFresh {
		return
	}
	key := fmt.Sprintf("%s/%d", name, d.Info.ID)
	c.infoMu.Lock()
	if c.periodTried == nil {
		c.periodTried = make(map[string]bool)
	}
	tried := c.periodTried[key]
	c.periodTried[key] = true
	c.infoMu.Unlock()
	if tried {
		return
	}
	_ = dom.SetMemoryStatsPeriod(balloonStatsPeriod, libvirt.DOMAIN_MEM_LIVE)
}

// infoFor returns cached metadata, re-reading it when it is old or the
// domain's state changed (a restart gives a domain a new ID).
func (c *Client) infoFor(name string, dom *libvirt.Domain, state State, now time.Time) Info {
	c.infoMu.Lock()
	ci, ok := c.infos[name]
	c.infoMu.Unlock()
	if ok && ci.state == state && now.Sub(ci.at) < infoTTL {
		return ci.info
	}
	info := infoFromDomain(dom, state)
	c.infoMu.Lock()
	c.infos[name] = cachedInfo{info: info, state: state, at: now}
	c.infoMu.Unlock()
	return info
}

func domainFromStats(name string, st *libvirt.DomainStats) *Domain {
	d := &Domain{Name: name}
	if st.State != nil {
		d.State = State(st.State.State)
	} else if state, _, err := st.Domain.GetState(); err == nil {
		d.State = State(state)
	}
	if st.Cpu != nil {
		d.CPUTimeNS = st.Cpu.Time
	}
	if b := st.Balloon; b != nil {
		d.MemCurrentKiB = b.Current
		d.MemMaximumKiB = b.Maximum
		d.MemUsableKiB, d.MemUsableSet = b.Usable, b.UsableSet
		d.MemUnusedKiB, d.MemUnusedSet = b.Unused, b.UnusedSet
		d.MemRSSKiB = b.Rss
		if b.DiskCachesSet {
			d.MemCachedKiB = b.DiskCaches
		}
		d.SwapInKiB = b.SwapIn
		d.SwapOutKiB = b.SwapOut
		d.MajorFault = b.MajorFault
		d.MinorFault = b.MinorFault
		if b.LastUpdateSet {
			if b.LastUpdate == 0 {
				d.MemLastUpdateZero = true
			} else {
				d.MemLastUpdate = time.Unix(int64(b.LastUpdate), 0)
			}
		}
	}
	if len(st.Vcpu) > 0 {
		d.VCPUs = len(st.Vcpu)
		d.VCPUStates = make([]int, len(st.Vcpu))
		d.VCPUTimesNS = make([]uint64, len(st.Vcpu))
		for i, v := range st.Vcpu {
			d.VCPUStates[i] = int(v.State)
			d.VCPUTimesNS[i] = v.Time
		}
	}
	for _, b := range st.Block {
		d.Disks = append(d.Disks, Disk{
			Name:       b.Name,
			RDBytes:    b.RdBytes,
			WRBytes:    b.WrBytes,
			Capacity:   b.Capacity,
			Allocation: b.Allocation,
		})
	}
	for _, n := range st.Net {
		d.Nets = append(d.Nets, Net{
			Name:    n.Name,
			RXBytes: n.RxBytes,
			TXBytes: n.TxBytes,
		})
	}
	return d
}

// infoFromDomain reads the slowly-changing metadata. Failures are recorded in
// Info.Err rather than dropped; fields that could not be read stay zero.
func infoFromDomain(dom *libvirt.Domain, state State) Info {
	info := Info{ID: -1}
	var errs []error
	note := func(what string, err error) {
		errs = append(errs, fmt.Errorf("%s: %w", what, err))
	}

	// libvirt reports no ID for inactive domains; asking is an error.
	if state.Active() {
		if id, err := dom.GetID(); err != nil {
			note("id", err)
		} else {
			info.ID = int(id)
		}
	}
	if uuid, err := dom.GetUUIDString(); err != nil {
		note("uuid", err)
	} else {
		info.UUID = uuid
	}
	if persistent, err := dom.IsPersistent(); err != nil {
		note("persistent", err)
	} else {
		info.Persistent = persistent
		// Autostart only exists for persistent domains; libvirt errors for
		// transient ones.
		if persistent {
			if autostart, err := dom.GetAutostart(); err != nil {
				note("autostart", err)
			} else {
				info.Autostart = autostart
			}
		}
	}
	if di, err := dom.GetInfo(); err != nil {
		note("info", err)
	} else {
		info.MaxMemoryKiB = di.MaxMem
		info.ConfiguredVCPUs = int(di.NrVirtCpu)
	}
	if osType, err := dom.GetOSType(); err != nil {
		note("ostype", err)
	} else {
		info.OSType = osType
	}
	if len(errs) > 0 {
		info.Err = errors.Join(errs...).Error()
	}
	return info
}

// Interfaces fetches interface addresses for the detail overlay. In read-only
// mode only ARP and DHCP-lease sources are available; guest-agent lookup needs
// a read-write connection. The domain must be running; libvirt rejects the
// call otherwise.
func (c *Client) Interfaces(name string) ([]IfaceInfo, error) {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	if c.closed {
		return nil, errClosed
	}
	if c.conn == nil {
		return nil, errDisconnected
	}
	dom, err := c.conn.LookupDomainByName(name)
	if err != nil {
		return nil, fmt.Errorf("lookup %s: %w", name, err)
	}
	defer func() { _ = dom.Free() }()

	type source struct {
		kind  libvirt.DomainInterfaceAddressesSource
		label string
	}
	sources := make([]source, 0, 3)
	if c.readWrite {
		sources = append(sources, source{libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_AGENT, "agent"})
	}
	sources = append(sources,
		source{libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_ARP, "arp"},
		source{libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_LEASE, "lease"},
	)

	var (
		out  []IfaceInfo
		seen = make(map[string]int)
		errs []error
	)
	for _, src := range sources {
		ifaces, err := dom.ListAllInterfaceAddresses(src.kind)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.label, err))
			continue
		}
		for _, iface := range ifaces {
			if isLoopback(iface.Hwaddr) {
				continue
			}
			addrs := make([]string, 0, len(iface.Addrs))
			for _, a := range iface.Addrs {
				if isNoisyAddr(a.Addr) {
					continue
				}
				if a.Prefix > 0 {
					addrs = append(addrs, fmt.Sprintf("%s/%d", a.Addr, a.Prefix))
				} else {
					addrs = append(addrs, a.Addr)
				}
			}
			if len(addrs) == 0 {
				continue
			}
			key := iface.Hwaddr + "|" + iface.Name
			if idx, ok := seen[key]; ok {
				out[idx].Addrs = appendUnique(out[idx].Addrs, addrs...)
				continue
			}
			seen[key] = len(out)
			out = append(out, IfaceInfo{Name: iface.Name, MAC: iface.Hwaddr, Addrs: addrs})
		}
		// The guest agent gives the guest's own interface names and normally
		// includes all addresses; don't duplicate them with ARP/lease results.
		if src.kind == libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_AGENT && len(out) > 0 {
			break
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// IsPermissionDenied reports whether err means libvirt refused the connection
// for lack of permission or authentication, as opposed to libvirtd being absent
// or broken.
func IsPermissionDenied(err error) bool {
	var lerr libvirt.Error
	if !errors.As(err, &lerr) {
		return false
	}
	switch lerr.Code {
	case libvirt.ERR_AUTH_FAILED, libvirt.ERR_AUTH_CANCELLED, libvirt.ERR_AUTH_UNAVAILABLE,
		libvirt.ERR_ACCESS_DENIED, libvirt.ERR_OPERATION_DENIED:
		return true
	case libvirt.ERR_SYSTEM_ERROR, libvirt.ERR_NO_CONNECT:
		// Socket-level EACCES surfaces as a generic system error whose message
		// carries the errno text.
		return strings.Contains(strings.ToLower(lerr.Message), "permission denied")
	default:
		return false
	}
}

func isLoopback(mac string) bool {
	switch strings.ToLower(mac) {
	case "", "00:00:00:00:00:00":
		return true
	default:
		return false
	}
}

func isNoisyAddr(addr string) bool {
	switch {
	case addr == "127.0.0.1", addr == "::1":
		return true
	case strings.HasPrefix(strings.ToLower(addr), "fe80:"):
		return true
	default:
		return false
	}
}

func appendUnique(dst []string, add ...string) []string {
	for _, s := range add {
		found := false
		for _, e := range dst {
			if e == s {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, s)
		}
	}
	return dst
}
