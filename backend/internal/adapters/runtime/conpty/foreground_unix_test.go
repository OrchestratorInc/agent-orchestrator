//go:build darwin || linux

package conpty

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestForegroundWatcherDetectsExecWithoutMoreOutput(t *testing.T) {
	terminal, err := newConPTY(t.TempDir(), "/bin/bash", []string{"--noprofile", "--norc", "-c",
		`printf 'ready\n'; IFS= read -r line; exec -a codex /bin/bash --noprofile --norc -c 'printf "agent\n"; IFS= read -r reply'`,
	}, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeConfig{SessionID: "foreground-exec", Listener: ln, PTY: terminal, Ring: NewRing()})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("host did not stop")
		}
	})
	states := make(chan ForegroundPayload, 8)
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- clientWatchForeground(ctx, ln.Addr().String(), func(state ForegroundPayload) {
			select {
			case states <- state:
			case <-ctx.Done():
			}
		})
	}()
	select {
	case <-states:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not report the initial shell")
	}
	viewer := newTestClient(t, ln.Addr().String())
	defer viewer.close()
	if err := viewer.send(MsgTerminalInput, []byte("start\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case state := <-states:
			if len(state.Argv) > 0 && state.Argv[0] == "codex" {
				return
			}
		case err := <-watchDone:
			t.Fatalf("watcher stopped: %v", err)
		case <-deadline:
			t.Fatal("exec'd agent was not reported after its first screen")
		}
	}
}
