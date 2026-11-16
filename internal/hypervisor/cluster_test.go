package hypervisor

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildEndpointsSingleHostKeepsLegacyFormat(t *testing.T) {
	eps, err := BuildEndpoints("qemu:///system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].URI != "qemu:///system" || eps[0].Label != "" {
		t.Fatalf("single endpoint = %+v", eps)
	}
}

func TestBuildEndpointsLabelsAndDeduplicates(t *testing.T) {
	eps, err := BuildEndpoints("qemu:///system", []string{
		"qemu+ssh://user@alpha/system",
		"qemu+tcp://beta:16509/system",
		"qemu:///system", // duplicate primary
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 3 {
		t.Fatalf("endpoints = %+v, want 3 after dedupe", eps)
	}
	got := []string{eps[0].Label, eps[1].Label, eps[2].Label}
	want := []string{"local", "alpha", "beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if !eps[0].Local || eps[1].Local || eps[2].Local {
		t.Fatalf("local flags = %v/%v/%v", eps[0].Local, eps[1].Local, eps[2].Local)
	}
}

func TestBuildEndpointsDistinguishesSameHostLabels(t *testing.T) {
	eps, err := BuildEndpoints("qemu:///system", []string{
		"qemu:///session",
		"qemu+ssh://user@alpha/system",
		"qemu+tcp://alpha/system",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{eps[0].Label, eps[1].Label, eps[2].Label, eps[3].Label}
	want := []string{"local", "local2", "alpha", "alpha2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("labels = %v, want %v", got, want)
	}
}

func TestBuildEndpointsRejectsEmptyAdd(t *testing.T) {
	if _, err := BuildEndpoints("", []string{"  "}); err == nil {
		t.Fatal("empty --add should be rejected")
	}
}

func TestDomainKeyAndDisplayName(t *testing.T) {
	local := &Domain{Host: "local", Name: "web"}
	remote := &Domain{Host: "alpha", Name: "web"}
	if local.Key() == remote.Key() {
		t.Fatalf("same VM name on two hosts shared key %q", local.Key())
	}
	if local.DisplayName() != "local/web" || remote.DisplayName() != "alpha/web" {
		t.Fatalf("display names = %q, %q", local.DisplayName(), remote.DisplayName())
	}
	host, name := SplitKey(remote.Key())
	if host != "alpha" || name != "web" {
		t.Fatalf("SplitKey = %q, %q", host, name)
	}
	if (&Domain{Name: "web"}).Key() != "web" {
		t.Fatal("single-host key must stay the bare VM name")
	}
}

func TestSnapshotIndexesByDomainKey(t *testing.T) {
	snap := NewSnapshot(epoch, []*Domain{
		{Host: "local", Name: "web", State: StateRunning},
		{Host: "alpha", Name: "web", State: StateRunning},
	}, nil)
	if len(snap.ByKey) != 2 {
		t.Fatalf("ByKey has %d entries, want 2", len(snap.ByKey))
	}
	if snap.ByKey["local\x00web"] == nil || snap.ByKey["alpha\x00web"] == nil {
		t.Fatalf("ByKey keys = %v", snap.ByKey)
	}
	if snap.ByKey["web"] != nil {
		t.Fatal("ByKey must not be indexed by bare name")
	}
}

type fakeHost struct {
	label   string
	snap    *Snapshot
	err     error
	ifaces  []IfaceInfo
	closed  bool
	queried string
}

func (f *fakeHost) Collect() (*Snapshot, error) { return f.snap, f.err }
func (f *fakeHost) Interfaces(_, name string) ([]IfaceInfo, error) {
	f.queried = name
	return f.ifaces, nil
}
func (f *fakeHost) InvalidateInfos() {}
func (f *fakeHost) URI() string      { return "fake:///" + f.label }
func (f *fakeHost) Label() string    { return f.label }
func (f *fakeHost) ReadWrite() bool  { return false }
func (f *fakeHost) Close() error     { f.closed = true; return nil }

func TestClusterCollectMergesAndKeepsPartialResults(t *testing.T) {
	at := epoch.Add(time.Second)
	localSnap := NewSnapshot(at, []*Domain{
		{Host: "local", Name: "web", State: StateRunning},
	}, nil)
	localSnap.Hosts = []HostStats{{Label: "local", Cores: 8, CPUValid: true, CPUPercent: 25, MemValid: true, MemTotalKiB: 1000, MemUsedKiB: 500}}
	local := &fakeHost{label: "local", snap: localSnap}
	remote := &fakeHost{label: "alpha", err: errors.New("connection refused")}
	c := &Cluster{clients: []hostClient{local, remote}}

	snap, err := c.Collect()
	if err != nil {
		t.Fatalf("partial failure should not fail collection: %v", err)
	}
	if len(snap.Domains) != 1 || snap.Domains[0].Host != "local" {
		t.Fatalf("domains = %+v", snap.Domains)
	}
	if len(snap.Warnings) != 1 || !strings.Contains(snap.Warnings[0], "alpha") {
		t.Fatalf("warnings = %v, want the failed host", snap.Warnings)
	}
	if len(snap.Hosts) != 1 || snap.Hosts[0].Label != "local" || snap.Hosts[0].Cores != 8 {
		t.Fatalf("hosts = %+v, want the local node stats", snap.Hosts)
	}
}

func TestClusterCollectFailsWhenEveryHostFails(t *testing.T) {
	c := &Cluster{clients: []hostClient{
		&fakeHost{label: "local", err: errors.New("a")},
		&fakeHost{label: "remote", err: errors.New("b")},
	}}
	if _, err := c.Collect(); err == nil {
		t.Fatal("all hosts failed but Collect returned no error")
	}
}

func TestClusterKeepsIdenticalNamesSeparate(t *testing.T) {
	at := epoch.Add(time.Second)
	c := &Cluster{clients: []hostClient{
		&fakeHost{label: "local", snap: NewSnapshot(at, []*Domain{{Host: "local", Name: "web"}}, nil)},
		&fakeHost{label: "alpha", snap: NewSnapshot(at, []*Domain{{Host: "alpha", Name: "web"}}, nil)},
	}}
	snap, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Domains) != 2 || len(snap.ByKey) != 2 {
		t.Fatalf("domains=%d ByKey=%d, want 2 distinct entries", len(snap.Domains), len(snap.ByKey))
	}
}

func TestClusterRoutesInterfaceLookupByHost(t *testing.T) {
	local := &fakeHost{label: "local", ifaces: []IfaceInfo{{Name: "eth0"}}}
	alpha := &fakeHost{label: "alpha", ifaces: []IfaceInfo{{Name: "ens3"}}}
	c := &Cluster{clients: []hostClient{local, alpha}}

	ifaces, err := c.Interfaces("alpha", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(ifaces) != 1 || ifaces[0].Name != "ens3" || alpha.queried != "web" {
		t.Fatalf("lookup routed to the wrong host: %+v (queried %q)", ifaces, alpha.queried)
	}
	if local.queried != "" {
		t.Fatal("local host should not have been queried")
	}
	if _, err := c.Interfaces("missing", "web"); err == nil {
		t.Fatal("unknown host should return an error")
	}
}
