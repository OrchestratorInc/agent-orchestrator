package conpty

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty/ptyregistry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestDestroyRecoveredHostRequiresProcessOwnership(t *testing.T) {
	for _, tc := range []struct {
		name       string
		startID    string
		unknown    bool
		unreadable bool
	}{
		{name: "reused PID", startID: "different-process-incarnation"},
		{name: "legacy missing identity", unknown: true},
		{name: "modern unreadable identity", startID: testProcessStartID(t), unknown: true, unreadable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rt := New(Options{RunFilePath: filepath.Join(dir, "running.json")})
			t.Cleanup(func() { ptyregistry.SetRunFilePath("") })
			data, err := json.Marshal([]map[string]any{{
				"sessionId": "recovered", "ptyHostPid": os.Getpid(), "pipePath": "127.0.0.1:1",
				"launchId": "old-launch", "processStartId": tc.startID,
			}})
			if err != nil {
				t.Fatal(err)
			}
			registryPath := filepath.Join(dir, "windows-pty-hosts.json")
			if err := os.WriteFile(registryPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			killCalls := 0
			rt.killHost = func(string) error { killCalls++; return nil }
			rt.processFinder = func(int) (processKiller, error) {
				killCalls++
				return processKillerFunc(func() error { killCalls++; return errors.New("must not signal test process") }), nil
			}
			rt.destroyWait = 0
			if tc.unreadable {
				rt.checkOwnership = func(context.Context, int, string) processalive.Ownership { return processalive.OwnershipUnknown }
			}

			err = rt.Destroy(context.Background(), ports.RuntimeHandle{ID: "recovered"})
			if killCalls != 0 {
				t.Fatalf("Destroy issued %d kill operations without matching process ownership", killCalls)
			}
			if tc.unknown {
				if !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
					t.Fatalf("Destroy unknown owner = %v, want inconclusive", err)
				}
				if _, err := os.Stat(registryPath); err != nil {
					t.Fatalf("unknown ownership lost durable registry: %v", err)
				}
			} else if err != nil {
				t.Fatalf("Destroy old reused owner = %v, want safe stale cleanup", err)
			} else if _, statErr := os.Stat(registryPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("old reused registry still exists: %v", statErr)
			}
		})
	}
}

func TestDestroyCachedOwnerPreservesSuccessorGeneration(t *testing.T) {
	isolateRegistry(t)
	rt := New(Options{})
	old := &hostSession{addr: "127.0.0.1:1", pid: livePID(), processStartID: "old-process", launchID: "old-launch"}
	rt.sessions["successor"] = old
	next := ptyregistry.Entry{
		SessionID: "successor", PtyHostPID: livePID(), ProcessStartID: testProcessStartID(t),
		PipePath: "127.0.0.1:2", LaunchID: "next-launch",
	}
	if err := ptyregistry.Register(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	rt.killHost = func(string) error { t.Error("stale cache sent a TCP kill"); return nil }
	rt.processFinder = func(int) (processKiller, error) {
		t.Error("stale cache attempted an OS kill")
		return nil, errors.New("must not kill successor")
	}
	if err := rt.Destroy(context.Background(), ports.RuntimeHandle{ID: "successor"}); !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("Destroy replaced generation = %v, want inconclusive", err)
	}
	entries, _, err := ptyregistry.Scan(context.Background())
	if err != nil || len(entries) != 1 || entries[0] != next {
		t.Fatalf("successor registry after stale cleanup = %+v, %v", entries, err)
	}
}

func TestCreateRetainsUnknownRecoveredHost(t *testing.T) {
	for _, startID := range []string{"", testProcessStartID(t)} {
		t.Run(startID, func(t *testing.T) {
			isolateRegistry(t)
			if err := ptyregistry.Register(context.Background(), ptyregistry.Entry{
				SessionID: "unknown", PtyHostPID: livePID(), ProcessStartID: startID,
				PipePath: "127.0.0.1:1", LaunchID: "launch",
			}); err != nil {
				t.Fatal(err)
			}
			rt := New(Options{Spawner: func(context.Context, string, string, []string, map[string]string, HostOptions) (string, int, error) {
				t.Error("unknown recovered owner was replaced")
				return "", 0, errors.New("must not spawn")
			}})
			rt.checkOwnership = func(context.Context, int, string) processalive.Ownership { return processalive.OwnershipUnknown }
			_, err := rt.Create(context.Background(), ports.RuntimeConfig{SessionID: "unknown", WorkspacePath: t.TempDir(), Argv: []string{"sh"}})
			if err == nil {
				t.Fatal("Create replaced unknown recovered owner")
			}
			entries, _, err := ptyregistry.Scan(context.Background())
			if err != nil || len(entries) != 1 {
				t.Fatalf("unknown registry lost after Create: %+v, %v", entries, err)
			}
		})
	}
}

func testProcessStartID(t *testing.T) string {
	t.Helper()
	startID := processalive.Identify(context.Background(), os.Getpid()).StartID
	if startID == "" {
		t.Fatal("test process identity unavailable")
	}
	return startID
}

// The listener runs in the test process, whose OS identity is real. Model its
// separate lifetime only after Serve exits, and make force-killing the test
// process impossible even if teardown regresses.
func modelInProcHostExit(t *testing.T, rt *Runtime, h *inProcHost) {
	t.Helper()
	rt.checkOwnership = func(ctx context.Context, pid int, startID string) processalive.Ownership {
		select {
		case <-h.finished:
			return processalive.OwnershipGone
		default:
			return processalive.CheckOwnership(ctx, pid, startID)
		}
	}
	rt.processFinder = func(int) (processKiller, error) {
		t.Error("in-process host teardown attempted an OS force kill")
		return nil, errors.New("cannot kill test process")
	}
}

func TestDestroyRechecksOwnershipBeforeForceKill(t *testing.T) {
	for _, changed := range []processalive.Ownership{processalive.OwnershipReused, processalive.OwnershipUnknown} {
		t.Run(string(changed), func(t *testing.T) {
			isolateRegistry(t)
			rt := New(Options{})
			sess := &hostSession{addr: "127.0.0.1:1", pid: livePID(), processStartID: testProcessStartID(t), launchID: "launch"}
			rt.sessions["owner-change"] = sess
			if err := ptyregistry.Register(context.Background(), registryEntry("owner-change", sess)); err != nil {
				t.Fatal(err)
			}
			rt.destroyWait = time.Nanosecond
			gracefulCalls, forceCalls, checksAfterGraceful := 0, 0, 0
			rt.killHost = func(string) error { gracefulCalls++; return nil }
			rt.checkOwnership = func(context.Context, int, string) processalive.Ownership {
				if gracefulCalls > 0 {
					checksAfterGraceful++
					if checksAfterGraceful >= 3 {
						return changed
					}
				}
				return processalive.OwnershipMatching
			}
			rt.processFinder = func(int) (processKiller, error) {
				forceCalls++
				return nil, errors.New("must not resolve a replacement PID")
			}
			err := rt.Destroy(context.Background(), ports.RuntimeHandle{ID: "owner-change"})
			if gracefulCalls != 1 || forceCalls != 0 {
				t.Fatalf("Destroy kill calls = graceful %d force %d, want 1/0", gracefulCalls, forceCalls)
			}
			if changed == processalive.OwnershipUnknown {
				if !errors.Is(err, ports.ErrRuntimeProbeInconclusive) || rt.sessions["owner-change"] != sess {
					t.Fatalf("unknown owner after grace lost evidence: err=%v session=%+v", err, rt.sessions["owner-change"])
				}
			} else if err != nil || rt.sessions["owner-change"] != nil {
				t.Fatalf("reused owner after grace = err %v session %+v, want stale cleanup", err, rt.sessions["owner-change"])
			}
		})
	}
}

func TestDestroyWaitsForDefinitiveExitAfterTransientUnknown(t *testing.T) {
	isolateRegistry(t)
	rt := New(Options{})
	sess := &hostSession{addr: "127.0.0.1:1", pid: livePID(), processStartID: testProcessStartID(t)}
	rt.sessions["exiting"] = sess
	if err := ptyregistry.Register(context.Background(), registryEntry("exiting", sess)); err != nil {
		t.Fatal(err)
	}
	stopping, exitChecks := false, 0
	rt.killHost = func(string) error { stopping = true; return nil }
	rt.checkOwnership = func(context.Context, int, string) processalive.Ownership {
		if !stopping {
			return processalive.OwnershipMatching
		}
		exitChecks++
		if exitChecks == 1 {
			return processalive.OwnershipUnknown // process exited between liveness and identity reads
		}
		return processalive.OwnershipGone
	}
	rt.destroyPoll = time.Nanosecond
	rt.processFinder = func(int) (processKiller, error) {
		t.Error("inconclusive exit observation authorized force kill")
		return nil, errors.New("must not force kill")
	}
	if err := rt.Destroy(context.Background(), ports.RuntimeHandle{ID: "exiting"}); err != nil {
		t.Fatalf("Destroy transiently unknown exiting owner = %v, want observed exit", err)
	}
}
