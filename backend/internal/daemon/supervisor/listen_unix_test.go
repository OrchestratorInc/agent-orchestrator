//go:build !windows

package supervisor

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListen_basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")

	ln, addr, err := Listen(runFile)
	if err != nil {
		t.Fatalf("Listen: unexpected error: %v", err)
	}
	defer ln.Close()

	wantSock := filepath.Join(dir, "supervise.sock")
	if addr != wantSock {
		t.Errorf("addr = %q, want %q", addr, wantSock)
	}

	if _, err := os.Stat(wantSock); err != nil {
		t.Errorf("socket file missing after Listen: %v", err)
	}

	conn, err := net.Dial("unix", addr)
	if err != nil {
		t.Fatalf("Dial(%q): %v", addr, err)
	}
	conn.Close()
}

func TestListen_staleSocket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")
	sockPath := filepath.Join(dir, "supervise.sock")

	if err := os.WriteFile(sockPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("pre-create stale file: %v", err)
	}

	ln, _, err := Listen(runFile)
	if err != nil {
		t.Fatalf("Listen with stale socket: unexpected error: %v", err)
	}
	ln.Close()
}

func TestListen_unlinkOnClose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")
	sockPath := filepath.Join(dir, "supervise.sock")

	ln, _, err := Listen(runFile)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln.Close()

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket file still present after Close (err=%v); expected not-exist", err)
	}
}
