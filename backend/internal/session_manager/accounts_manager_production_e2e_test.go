//go:build e2e && (linux || darwin)

package sessionmanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "pty-host" {
		if os.Getenv("AO_ACCOUNT_SWITCH_TEST_PTY_FAILURE") == "1" {
			os.Exit(73)
		}
		os.Exit(conpty.RunHost(os.Args[2:], os.Stdout))
	}
	os.Exit(m.Run())
}

func realSelectedAccountRuntimeFixture(t *testing.T, fallback bool) (*Manager, *sqlite.Store, runtimeselect.Runtime, *accountRetainedPaneAgent, domain.SessionRecord, AccountsManagerSwitchConfig) {
	t.Helper()
	tmuxBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "account-selected-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("AO_TMUX_BINARY", tmuxBinary)
	t.Setenv("AO_TMUX_SOCKET_NAME", "account-selected")
	setAccountHostFailure(t, fallback)
	binary := filepath.Join(t.TempDir(), "ao")
	build := exec.CommandContext(t.Context(), "go", "build", "-mod=readonly", "-p=1", "-o", binary, "../../cmd/ao")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build isolated supervisor: %v\n%s", err, out)
	}
	m, st, _, _, rec, cfg := accountSwitchFixture(t)
	m.runFilePath = filepath.Join(m.dataDir, "unused-running.json")
	rt := runtimeselect.New(nil, m.runFilePath)
	resolver, ok := rt.(ports.RuntimeLaunchHandleResolver)
	if !ok {
		t.Fatal("production factory lost the launch identity capability")
	}
	reserved, err := resolver.LaunchHandles(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current := runtimeselect.New(nil, m.runFilePath)
		for _, handle := range reserved {
			if err := current.Destroy(context.Background(), handle); err != nil {
				t.Error("cleanup isolated runtime", err)
			}
		}
	})
	handle, err := rt.Create(t.Context(), ports.RuntimeConfig{SessionID: rec.ID, WorkspacePath: rec.Metadata.WorkspacePath,
		Argv: []string{binary, "agent-process", "supervise", "--session", string(rec.ID), "--launch", rec.Metadata.RuntimeLaunchID, "--", "/bin/true"},
		Env:  map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: rec.Metadata.RuntimeLaunchID, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}})
	if err != nil || !slices.Contains(reserved, handle) || strings.HasPrefix(handle.ID, "ptyhost-v1:") == fallback {
		t.Fatal("production source did not use the intended reserved backend", err)
	}
	ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: handle, Generation: rec.Metadata.RuntimeLaunchID}
	waitSelectedAccountProbe(t, rt, ref, ports.FencedDead)
	rec.Metadata.RuntimeHandleID = handle.ID
	if err := st.UpdateSession(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	agent := &accountRetainedPaneAgent{}
	m.runtime, m.agents = rt, singleAgent{agent: agent}
	m.executable = func() (string, error) { return binary, nil }
	m.switchTargetStartWait = 3 * time.Second
	return m, st, rt, agent, rec, cfg
}

func setAccountHostFailure(t *testing.T, fail bool) {
	t.Helper()
	value := "0"
	if fail {
		value = "1"
	}
	t.Setenv("AO_ACCOUNT_SWITCH_TEST_PTY_FAILURE", value)
}

func waitSelectedAccountProbe(t *testing.T, rt runtimeselect.Runtime, ref ports.FencedRuntimeRef, want ports.FencedLiveness) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := rt.ProbeFencedRuntime(t.Context(), ref)
		if got.Liveness == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("production runtime probe=%+v want=%s", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAccountsManagerSwitchRealProductionRuntime(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "direct"
		if fallback {
			name = "fallback"
		}
		t.Run(name, func(t *testing.T) {
			m, st, rt, agent, rec, cfg := realSelectedAccountRuntimeFixture(t, fallback)
			if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
				t.Fatal(err)
			}
			ready := waitAccountSwitch(t, m, st, cfg.OperationID)
			if ready.Phase != domain.AccountsManagerSwitchReady {
				t.Fatalf("production switch phase=%s code=%s", ready.Phase, ready.ErrorCode)
			}
			target, _, err := st.GetSession(t.Context(), rec.ID)
			if err != nil || strings.HasPrefix(target.Metadata.RuntimeHandleID, "ptyhost-v1:") == fallback {
				t.Fatal("wrong production target backend", err)
			}
			waitSelectedAccountProbe(t, rt, ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(target.Metadata), Generation: ready.TargetGeneration}, ports.FencedAlive)
			if agent.lastLaunch.Prompt != "" {
				t.Fatal("production switch replayed the saved task")
			}
		})
	}
}

type accountSelectedCleanupFailure struct {
	runtimeselect.Runtime
	cleanupFail bool
	created     ports.RuntimeHandle
}

func (r *accountSelectedCleanupFailure) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	return r.Runtime.(ports.RuntimeLaunchHandleResolver).LaunchHandles(id)
}

func (r *accountSelectedCleanupFailure) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	handle, err := r.Runtime.Create(ctx, cfg)
	r.created = handle
	return handle, err
}

func (r *accountSelectedCleanupFailure) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	if r.cleanupFail {
		return errors.New("cleanup unavailable")
	}
	return r.Runtime.Destroy(ctx, handle)
}

type accountSelectedPublicationFailure struct {
	*lifecycle.Manager
	runtime *accountSelectedCleanupFailure
}

func (l *accountSelectedPublicationFailure) MarkSpawned(context.Context, domain.SessionID, domain.SessionMetadata) error {
	l.runtime.cleanupFail = true
	return errors.New("publication unavailable")
}

func TestAccountsManagerSwitchRealProductionColdRecovery(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "fallback-to-direct"
		if fallback {
			name = "direct-to-fallback"
		}
		for _, foreign := range []bool{false, true} {
			variant := name
			if foreign {
				variant += "/foreign-owner"
			}
			t.Run(variant, func(t *testing.T) {
				m, st, rt, agent, rec, cfg := realSelectedAccountRuntimeFixture(t, !fallback)
				m.switchTargetStartWait = time.Millisecond
				if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
					t.Fatal(err)
				}
				first := waitAccountSwitch(t, m, st, cfg.OperationID)
				if first.Phase != domain.AccountsManagerSwitchRecoveryRequired || first.TargetRevision == 0 {
					t.Fatalf("fixture did not commit then miss readiness: %+v", first)
				}
				setAccountHostFailure(t, fallback)
				faulted := &accountSelectedCleanupFailure{Runtime: rt}
				m.runtime = faulted
				m.lcm = &accountSelectedPublicationFailure{Manager: lifecycle.New(st, nil), runtime: faulted}
				if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
					t.Fatal(err)
				}
				failed := waitAccountSwitch(t, m, st, cfg.OperationID)
				if failed.TargetGeneration == first.TargetGeneration || strings.HasPrefix(faulted.created.ID, "ptyhost-v1:") == fallback {
					t.Fatalf("fixture did not preserve a reserved target on the other backend: phase=%s code=%s handle=%q", failed.Phase, failed.ErrorCode, faulted.created.ID)
				}
				ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: faulted.created, Generation: failed.TargetGeneration}
				waitSelectedAccountProbe(t, rt, ref, ports.FencedAlive)
				stored, _, err := st.GetSession(t.Context(), rec.ID)
				if err != nil || stored.Metadata.RuntimeLaunchID != first.TargetGeneration || stored.Metadata.RuntimeHandleID == ref.Handle.ID {
					t.Fatal("fixture published the reserved target", err)
				}
				if foreign {
					if err := rt.Destroy(t.Context(), ref.Handle); err != nil {
						t.Fatal(err)
					}
					ref.Generation = "foreign-generation"
					binary, err := m.executable()
					if err != nil {
						t.Fatal(err)
					}
					handle, err := rt.Create(t.Context(), ports.RuntimeConfig{SessionID: rec.ID, WorkspacePath: rec.Metadata.WorkspacePath,
						Argv: []string{binary, "agent-process", "supervise", "--session", string(rec.ID), "--launch", ref.Generation, "--", "/bin/sleep", "30"},
						Env:  map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: ref.Generation, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}})
					if err != nil || handle != ref.Handle {
						t.Fatal("foreign-owner control did not replace the reserved slot", err)
					}
					waitSelectedAccountProbe(t, rt, ref, ports.FencedAlive)
				}
				project, err := m.loadProject(t.Context(), rec.ProjectID)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := sqlite.OpenPreMigrated(project.Path)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				cold := runtimeselect.New(nil, m.runFilePath)
				reserved, err := cold.(ports.RuntimeLaunchHandleResolver).LaunchHandles(rec.ID)
				if err != nil || !slices.Contains(reserved, ref.Handle) {
					t.Fatal("restart forgot a possible launch slot", err)
				}
				restarted := New(Deps{Store: reopened, Runtime: cold, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: &accountSwitchRouter{store: reopened},
					Agents: singleAgent{agent: agent}, DataDir: m.dataDir, RunFilePath: m.runFilePath, LookPath: m.lookPath, BackgroundContext: t.Context()})
				restarted.executable, restarted.switchTargetStartWait = m.executable, 3*time.Second
				if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
					t.Fatal(err)
				}
				ready := waitAccountSwitch(t, restarted, reopened, cfg.OperationID)
				if foreign {
					if ready.Phase != domain.AccountsManagerSwitchRecoveryRequired || ready.TargetRevision != failed.TargetRevision || ready.TargetGeneration != failed.TargetGeneration {
						t.Fatal("foreign runtime allowed rotation or acknowledgement")
					}
					waitSelectedAccountProbe(t, cold, ref, ports.FencedAlive)
					if release, ok := restarted.AcquireSessionInput(rec.ID); ok {
						release()
						t.Fatal("foreign runtime reopened input")
					}
					return
				}
				if ready.Phase != domain.AccountsManagerSwitchReady || ready.TargetRevision <= failed.TargetRevision {
					t.Fatalf("production cold recovery stranded: phase=%s code=%s", ready.Phase, ready.ErrorCode)
				}
				ref.Generation = ready.TargetGeneration
				waitSelectedAccountProbe(t, cold, ref, ports.FencedAlive)
				if agent.lastLaunch.Prompt != "" {
					t.Fatal("production cold recovery replayed the task")
				}
			})
		}
	}
}
