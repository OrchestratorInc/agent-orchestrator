//go:build darwin || linux

package browserchrome

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPeerVerifierPinsExecutable(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ao-peer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "p.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	server := <-accepted
	defer func() { _ = server.Close() }()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := PeerVerifier(self)(server); err != nil {
		t.Fatalf("own executable rejected: %v", err)
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := PeerVerifier(other)(server); err == nil {
		t.Fatal("peer accepted for a different executable")
	}
}
