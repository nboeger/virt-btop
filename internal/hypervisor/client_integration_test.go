package hypervisor

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const roSocket = "/run/libvirt/libvirt-sock-ro"

// proxy forwards a unix socket to libvirt's read-only socket. Stopping it
// severs every live connection, which is what libvirtd restarting looks like
// to a client.
type proxy struct {
	path  string
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func startProxy(t *testing.T, path string) *proxy {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &proxy{path: path, ln: ln}
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			upstream, err := net.Dial("unix", roSocket)
			if err != nil {
				client.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, client, upstream)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(upstream, client); upstream.Close() }()
			go func() { _, _ = io.Copy(client, upstream); client.Close() }()
		}
	}()
	return p
}

func (p *proxy) stop() {
	_ = p.ln.Close()
	p.mu.Lock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
	p.mu.Unlock()
	_ = os.Remove(p.path)
}

// TestReconnectAfterConnectionLoss exercises the real libvirt client against a
// connection that dies and later comes back. It is skipped when no local
// libvirt is available.
func TestReconnectAfterConnectionLoss(t *testing.T) {
	if _, err := os.Stat(roSocket); err != nil {
		t.Skipf("no libvirt read-only socket: %v", err)
	}
	dir, err := os.MkdirTemp("", "vb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")

	p := startProxy(t, sock)
	uri := "qemu+unix:///system?socket=" + sock
	c, err := Connect(uri, false)
	if err != nil {
		p.stop()
		t.Skipf("cannot connect through proxy (libvirt client unavailable?): %v", err)
	}
	defer c.Close()

	if _, err := c.Collect(); err != nil {
		p.stop()
		t.Fatalf("initial collect: %v", err)
	}

	// libvirtd "goes away": the connection is severed and nothing listens.
	p.stop()
	_, err = c.Collect()
	if err == nil {
		t.Fatal("collect succeeded with no daemon; expected an error")
	}
	if !strings.Contains(err.Error(), "reconnect failed") {
		t.Fatalf("error should say the reconnect failed, got: %v", err)
	}

	// While the daemon is down every call keeps failing cleanly.
	if _, err := c.Collect(); err == nil {
		t.Fatal("second collect succeeded with no daemon")
	}
	if _, err := c.Interfaces("whatever"); err == nil {
		t.Fatal("interfaces succeeded with no daemon")
	}

	// libvirtd "comes back": the next Collect must recover on its own.
	p2 := startProxy(t, sock)
	defer p2.stop()
	var snap *Snapshot
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err = c.Collect()
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("collect did not recover after the daemon returned: %v", err)
	}
	if snap == nil {
		t.Fatal("nil snapshot after recovery")
	}
}

// TestCloseIsIdempotentAndBlocksFurtherUse checks the closed state.
func TestCloseIsIdempotentAndBlocksFurtherUse(t *testing.T) {
	if _, err := os.Stat(roSocket); err != nil {
		t.Skipf("no libvirt read-only socket: %v", err)
	}
	c, err := Connect("qemu:///system", false)
	if err != nil {
		t.Skipf("cannot connect: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := c.Collect(); err == nil {
		t.Fatal("collect after close should fail")
	}
}
