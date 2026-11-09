//go:build windows

package processalive_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty/ptyregistry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// TestObservationFailurePreservesRegistry 验证注册记录、运行时未知状态和竞争启动使用同一观察契约。
func TestObservationFailurePreservesRegistry(t *testing.T) {
	pid, stop := observationChild(t)
	dir := t.TempDir()
	spawnCalls := 0
	opts := conpty.Options{RunFilePath: filepath.Join(dir, "running.json"),
		Spawner: func(context.Context, string, string, []string, map[string]string, conpty.HostOptions) (string, int, error) {
			spawnCalls++
			return "", 0, errors.New("fixture: no host executable")
		}}
	rt := conpty.New(opts)
	t.Cleanup(func() { ptyregistry.SetRunFilePath("") })
	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	entry := ptyregistry.Entry{SessionID: "retained", PtyHostPID: pid,
		PipePath: address, LaunchID: "generation-1"}
	if err := ptyregistry.Register(ctx, entry); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "windows-pty-hosts.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restore := processalive.FailProbeForTest(t, pid, false)
	entries, complete, err := ptyregistry.Scan(ctx)
	if err == nil || complete || len(entries) != 1 || entries[0] != entry {
		t.Errorf("unknown scan lost owner: %v, complete=%v, err=%v", entries, complete, err)
	}
	handle := ports.RuntimeHandle{ID: entry.SessionID}
	if alive, err := rt.IsAlive(ctx, handle); alive || err == nil {
		t.Errorf("unknown runtime = %v, %v; want inconclusive", alive, err)
	}
	if alive, err := rt.IsChildAlive(ctx, handle); alive || err == nil {
		t.Errorf("unknown child = %v, %v; want inconclusive", alive, err)
	}
	ref := ports.FencedRuntimeRef{Handle: handle, SessionID: "retained", Generation: entry.LaunchID}
	if got := rt.ProbeFencedRuntime(ctx, ref); got.Liveness != ports.FencedUnknown {
		t.Errorf("unknown fenced runtime = %+v", got)
	}
	cfg := ports.RuntimeConfig{SessionID: "retained", WorkspacePath: dir, Argv: []string{"fixture"}}
	if _, err := rt.Create(ctx, cfg); err == nil || spawnCalls != 0 {
		t.Errorf("unknown ownership permitted competing spawn: %d, %v", spawnCalls, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("unknown observation rewrote registry: %q, %v", after, err)
	}
	stop()
	restore()
	entries, complete, err = ptyregistry.Scan(ctx)
	if err != nil || !complete || len(entries) != 0 {
		t.Errorf("confirmed dead registry not pruned: %v, %v, %v", entries, complete, err)
	}
	rt = conpty.New(opts)
	if got := rt.ProbeFencedRuntime(ctx, ref); got.Liveness != ports.FencedDead {
		t.Errorf("confirmed dead fenced runtime = %+v", got)
	}
	spawnCalls = 0
	_, _ = rt.Create(ctx, cfg)
	if spawnCalls != 1 {
		t.Errorf("confirmed death did not permit recovery: spawn calls=%d", spawnCalls)
	}
}
