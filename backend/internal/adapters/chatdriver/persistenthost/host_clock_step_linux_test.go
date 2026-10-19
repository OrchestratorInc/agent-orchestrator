//go:build linux

package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestConnectOrStartPreservesLegacyHostAfterLinuxClockStep(t *testing.T) {
	for _, descriptorPublished := range []bool{true, false} {
		name := "plain PID lock before descriptor publication"
		if descriptorPublished {
			name = "attached legacy descriptor"
		}
		t.Run(name, func(t *testing.T) {
			startLog := filepath.Join(t.TempDir(), "providers.log")
			cfg := Config{
				SessionID: "legacy-clock-step", DataDir: t.TempDir(), Workdir: t.TempDir(),
				Env: append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1", "AO_START_LOG="+startLog),
				Argv: []string{"/bin/sh", "-c", `echo $$ >> "$AO_START_LOG"; exec "$@"`,
					"clock-step-provider", os.Args[0], "-test.run=TestProviderHelper"},
			}
			legacy, descriptor := startLegacyHostWithoutGuard(t, cfg)
			providerPID := requestProviderPID(t, legacy, 1, "pid")
			if !descriptorPublished {
				path, _ := descriptorPath(cfg.DataDir, cfg.SessionID)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			dir, _ := hostDir(cfg.DataDir, cfg.SessionID)
			if _, err := os.Stat(filepath.Join(dir, "host.guard")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("legacy fixture unexpectedly has a host guard: %v", err)
			}

			before := processalive.Identify(context.Background(), descriptor.PID)
			if before.StartID == "" || before.StartedAt.IsZero() {
				t.Fatalf("live legacy host identity is unavailable: %+v", before)
			}
			ps, err := exec.LookPath("ps")
			if err != nil {
				t.Fatal(err)
			}
			shimDir := t.TempDir()
			shiftedStart := before.StartedAt.Add(time.Hour).UTC()
			shim := fmt.Sprintf("#!/bin/sh\nif [ \"$4\" = \"%d\" ]; then\n  printf '%%s\\n' %q\nelse\n  exec %q \"$@\"\nfi\n",
				descriptor.PID, shiftedStart.Format(time.ANSIC), ps)
			if err := os.WriteFile(filepath.Join(shimDir, "ps"), []byte(shim), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			cfg.Env = append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1", "AO_START_LOG="+startLog)
			after := processalive.Identify(context.Background(), descriptor.PID)
			if after.StartID != before.StartID || !after.StartedAt.Equal(shiftedStart) ||
				!after.StartedAt.After(descriptor.StartedAt.Add(2*time.Second)) {
				t.Fatalf("clock step changed process incarnation or was not observed: before=%+v after=%+v", before, after)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			replacement, connectErr := ConnectOrStart(ctx, cfg)
			var replacementPID int
			if replacement != nil {
				replacementPID = requestProviderPID(t, replacement, 1, "pid")
				t.Cleanup(func() {
					_ = replacement.Stdin.Close()
					_ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID)
				})
			}
			if got := requestProviderPID(t, legacy, 2, "pid"); got != providerPID {
				t.Fatalf("legacy provider changed: %d -> %d", providerPID, got)
			}
			starts, err := os.ReadFile(startLog)
			if err != nil {
				t.Fatal(err)
			}
			if count := len(strings.Fields(string(starts))); count != 1 {
				t.Fatalf("Linux wall-clock step launched a rival provider: starts=%d legacy_pid=%d replacement_pid=%d connect_error=%v",
					count, providerPID, replacementPID, connectErr)
			}
			if !errors.Is(connectErr, context.DeadlineExceeded) {
				t.Fatalf("connect to occupied legacy host = %v, want context deadline with one provider", connectErr)
			}
			lock, _ := lockPath(cfg.DataDir, cfg.SessionID)
			owner, err := os.ReadFile(lock)
			if err != nil || string(owner) != fmt.Sprintf("%d\n", descriptor.PID) {
				t.Fatalf("legacy launch lock changed: owner=%q error=%v", owner, err)
			}
		})
	}
}

// A pre-guard host uses the same relay and provider, but owns only a decimal PID
// lock; a modern guard would mask unsafe reclamation of its legacy timestamp.
func startLegacyHostWithoutGuard(t *testing.T, cfg Config) (*Transport, Descriptor) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	child := exec.Command(cfg.Argv[0], cfg.Argv[1:]...)
	child.Dir, child.Env = cfg.Workdir, cfg.Env
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	descriptor := Descriptor{
		Version: ProtocolVersion, SessionID: cfg.SessionID,
		Address: listener.Addr().String(), Token: "legacy-capability",
		PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	if err := writeDescriptor(cfg.DataDir, descriptor); err != nil {
		t.Fatal(err)
	}
	lock, _ := lockPath(cfg.DataDir, cfg.SessionID)
	if err := os.WriteFile(lock, []byte(fmt.Sprintf("%d\n", descriptor.PID)), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &host{
		ctx: context.Background(), listener: listener, child: child, stdin: stdin,
		token: descriptor.Token, pendingRequests: make(map[string]*pendingRequest),
		shutdown: make(chan struct{}),
	}
	h.cond = sync.NewCond(&h.mu)
	go func() { _ = h.forwardProvider(stdout) }()
	go func() { _ = h.accept() }()
	transport := awaitAttach(t, descriptor)
	t.Cleanup(func() { _ = transport.Stdin.Close() })
	return transport, descriptor
}
