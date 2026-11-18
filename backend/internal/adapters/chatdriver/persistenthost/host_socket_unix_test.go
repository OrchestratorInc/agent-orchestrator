//go:build unix

package persistenthost

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHostRelaysThroughSocketProvider(t *testing.T) {
	// t.TempDir can exceed the unix socket path limit on macOS.
	dir, err := os.MkdirTemp("/tmp", "aohost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "adapter.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	closed := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			_, _ = fmt.Fprintf(conn, "%s\n", scanner.Bytes())
		}
		close(closed)
	}()

	dataDir := t.TempDir()
	cfg := Config{SessionID: "socket-1", DataDir: dataDir, Workdir: t.TempDir(), Argv: []string{SocketProvider, socket}}
	hostDone := make(chan error, 1)
	go func() { hostDone <- Run(context.Background(), cfg) }()
	transport, err := attach(context.Background(), awaitDescriptor(t, dataDir, cfg.SessionID), false)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := fmt.Fprintln(transport.Stdin, `{"id":1,"method":"ping"}`); err != nil {
		t.Fatalf("send: %v", err)
	}
	_ = transport.Stdin.(interface{ SetReadDeadline(time.Time) error }).SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(transport.Stdout).ReadString('\n')
	if err != nil || line != "{\"id\":1,\"method\":\"ping\"}\n" {
		t.Fatalf("echo = %q err=%v", line, err)
	}
	_ = transport.Stdin.Close()
	if err := Shutdown(context.Background(), dataDir, cfg.SessionID); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("host shutdown left the adapter connection open")
	}
	select {
	case <-hostDone:
	case <-time.After(5 * time.Second):
		t.Fatal("host did not exit after shutdown")
	}
}
