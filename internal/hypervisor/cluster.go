package hypervisor

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Endpoint is one libvirt connection in a combined view. Label is empty for a
// single-host run; with several hosts it identifies the endpoint on screen and
// in Domain.Host.
type Endpoint struct {
	URI   string
	Label string
	// Local is true when the endpoint describes the machine running
	// virt-btop (a URI without a remote host part).
	Local bool
}

// BuildEndpoints resolves the primary URI and any --add URIs, removes exact
// duplicates, and assigns short display labels when more than one host is
// requested. The primary stays first.
func BuildEndpoints(primary string, add []string) ([]Endpoint, error) {
	uris := make([]string, 0, len(add)+1)
	seen := make(map[string]bool, len(add)+1)
	appendURI := func(uri string) {
		uri = ResolveURI(uri)
		if seen[uri] {
			return
		}
		seen[uri] = true
		uris = append(uris, uri)
	}

	appendURI(primary)
	for _, uri := range add {
		if strings.TrimSpace(uri) == "" {
			return nil, errors.New("--add needs a non-empty libvirt URI")
		}
		appendURI(uri)
	}

	endpoints := make([]Endpoint, 0, len(uris))
	used := make(map[string]int, len(uris))
	for _, uri := range uris {
		base := hostLabel(uri)
		ep := Endpoint{URI: uri, Local: isLocalURI(uri)}
		if len(uris) > 1 {
			ep.Label = uniqueLabel(base, used)
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, nil
}

// isLocalURI reports whether a libvirt URI has no remote host part
// (qemu:///system, qemu:///session, qemu+unix:///...).
func isLocalURI(uri string) bool {
	u, err := url.Parse(uri)
	return err != nil || u.Hostname() == ""
}

// hostLabel derives a compact label from a libvirt URI. Local URIs get the
// label "local"; remote ones use their hostname.
func hostLabel(uri string) string {
	if isLocalURI(uri) {
		return "local"
	}
	u, err := url.Parse(uri)
	if err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "local"
}

func uniqueLabel(base string, used map[string]int) string {
	if base == "" {
		base = "host"
	}
	used[base]++
	if used[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s%d", base, used[base])
}

// hostClient is the per-host subset Cluster needs. It is an interface so the
// merge logic can be tested without real libvirt connections.
type hostClient interface {
	Collect() (*Snapshot, error)
	Interfaces(host, name string) ([]IfaceInfo, error)
	InvalidateInfos()
	URI() string
	Label() string
	ReadWrite() bool
	Close() error
}

// Cluster combines several libvirt connections behind the same interface as a
// single Client. Collection runs concurrently; one unreachable host is
// reported as a warning instead of hiding the hosts that did respond.
type Cluster struct {
	clients  []hostClient
	warnings []string
}

// ConnectCluster opens every endpoint. If some connections fail but at least
// one succeeds, the cluster is usable and the failures are kept as warnings.
// If none succeed, the joined connection errors are returned.
func ConnectCluster(endpoints []Endpoint, readWrite bool) (*Cluster, error) {
	c := &Cluster{}
	var errs []error
	for _, ep := range endpoints {
		client, err := connectLabeled(ep.URI, ep.Label, readWrite)
		if err != nil {
			label := ep.Label
			if label == "" {
				label = ep.URI
			}
			c.warnings = append(c.warnings, fmt.Sprintf("%s: %v", label, err))
			errs = append(errs, err)
			continue
		}
		c.clients = append(c.clients, client)
	}
	if len(c.clients) == 0 {
		if len(errs) == 1 {
			return nil, errs[0]
		}
		return nil, fmt.Errorf("no configured host could be reached: %w", errors.Join(errs...))
	}
	return c, nil
}

// Warnings returns startup connection problems, if any.
func (c *Cluster) Warnings() []string {
	return append([]string(nil), c.warnings...)
}

// URI is shown in the footer. A single endpoint keeps its full URI; combined
// runs show their labels, which are far more compact.
func (c *Cluster) URI() string {
	if len(c.clients) == 1 {
		return c.clients[0].URI()
	}
	labels := make([]string, 0, len(c.clients))
	for _, client := range c.clients {
		labels = append(labels, client.Label())
	}
	return strings.Join(labels, ", ")
}

func (c *Cluster) ReadWrite() bool {
	return len(c.clients) > 0 && c.clients[0].ReadWrite()
}

func (c *Cluster) InvalidateInfos() {
	for _, client := range c.clients {
		client.InvalidateInfos()
	}
}

// Close closes every connection and joins any errors.
func (c *Cluster) Close() error {
	var errs []error
	for _, client := range c.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type collectResult struct {
	snap *Snapshot
	err  error
}

// Collect fetches all hosts concurrently and merges their domains. Domains are
// keyed by Host/Name so equal VM names on different hosts stay distinct.
func (c *Cluster) Collect() (*Snapshot, error) {
	if len(c.clients) == 0 {
		return nil, errDisconnected
	}
	results := make([]collectResult, len(c.clients))
	var wg sync.WaitGroup
	for i, client := range c.clients {
		wg.Add(1)
		go func(i int, client hostClient) {
			defer wg.Done()
			results[i].snap, results[i].err = client.Collect()
		}(i, client)
	}
	wg.Wait()

	domains := make([]*Domain, 0)
	hosts := make([]HostStats, 0, len(c.clients))
	warnings := append([]string(nil), c.warnings...)
	var errs []error
	var at time.Time
	ok := 0
	for i, res := range results {
		client := c.clients[i]
		if res.err != nil {
			err := withHost(client.Label(), res.err)
			errs = append(errs, err)
			warnings = append(warnings, err.Error())
			continue
		}
		if res.snap == nil {
			err := withHost(client.Label(), errors.New("returned no data"))
			errs = append(errs, err)
			warnings = append(warnings, err.Error())
			continue
		}
		ok++
		if res.snap.At.After(at) {
			at = res.snap.At
		}
		domains = append(domains, res.snap.Domains...)
		hosts = append(hosts, res.snap.Hosts...)
		for _, w := range res.snap.Warnings {
			warnings = append(warnings, withHost(client.Label(), errors.New(w)).Error())
		}
	}
	if ok == 0 {
		if len(errs) == 1 {
			return nil, errs[0]
		}
		return nil, fmt.Errorf("all hosts failed: %w", errors.Join(errs...))
	}
	if at.IsZero() {
		at = time.Now()
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Key() < domains[j].Key() })
	snap := NewSnapshot(at, domains, warnings)
	snap.Hosts = hosts
	return snap, nil
}

// Interfaces routes an address lookup to the connection that owns the domain.
func withHost(label string, err error) error {
	if label == "" {
		return err
	}
	return fmt.Errorf("%s: %w", label, err)
}

func (c *Cluster) Interfaces(host, name string) ([]IfaceInfo, error) {
	if len(c.clients) == 0 {
		return nil, errDisconnected
	}
	if len(c.clients) == 1 && host == "" {
		return c.clients[0].Interfaces("", name)
	}
	for _, client := range c.clients {
		if client.Label() == host {
			return client.Interfaces("", name)
		}
	}
	return nil, fmt.Errorf("unknown host %q", host)
}
