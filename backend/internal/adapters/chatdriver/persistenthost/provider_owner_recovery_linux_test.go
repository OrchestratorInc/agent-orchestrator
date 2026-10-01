//go:build linux

package persistenthost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type removalCrashChat struct{ *chatsvc.Service }

func (removalCrashChat) QueueChatPrompt(context.Context, domain.SessionID, string) (string, error) {
	return "", errors.New("deletion must not queue a new prompt")
}

func (c removalCrashChat) DrainChatQueue(ctx context.Context, id domain.SessionID) error {
	return c.DrainQueued(ctx, id)
}

type removalCrashRouter struct {
	ports.AccountsManagerLaunchRouter
	store     *sqlite.Store
	finalized atomic.Int32
}

func (r *removalCrashRouter) PrepareAccountRemoval(ctx context.Context, id, account string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, bool, error) {
	return r.store.CreateAccountsManagerRemoval(ctx, id, account, revision, confirmed)
}

func (*removalCrashRouter) SynchronizeAgentBindings(context.Context) error { return nil }

func (r *removalCrashRouter) FinalizeAccountRemoval(ctx context.Context, id string) error {
	r.finalized.Add(1)
	if err := r.store.RecordAccountsManagerRemovalRevoked(ctx, id); err != nil {
		return err
	}
	return r.store.CompleteAccountsManagerRemoval(ctx, id)
}

func startRemovalLateForkHost(t *testing.T, dataDir, sessionID string) (*exec.Cmd, Descriptor, int, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{SessionID: sessionID, DataDir: dataDir, Workdir: dir,
		Env:  append(os.Environ(), "AO_OWNER_LATE_FORK_ROLE=parent", "AO_OWNER_LATE_FORK_DIR="+dir),
		Argv: []string{os.Args[0], "-test.run=^TestRemovalLateForkProviderHelper$"}}
	host := exec.Command(os.Args[0], hostArgs(cfg)...)
	host.Env, host.Dir = cfg.Env, cfg.Workdir
	host.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	var providerPID int
	t.Cleanup(func() {
		_ = host.Process.Kill()
		_ = host.Wait()
		if providerPID > 0 {
			_ = syscall.Kill(-providerPID, syscall.SIGKILL)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, err := readDescriptor(dataDir, sessionID)
		data, readErr := os.ReadFile(filepath.Join(dir, "ready"))
		if err == nil && d.PID == host.Process.Pid && readErr == nil {
			providerPID, err = strconv.Atoi(string(data))
			if err == nil && removalCrashProcessRunning(providerPID) {
				return host, d, providerPID, dir
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("late-fork host did not become ready")
	return nil, Descriptor{}, 0, ""
}

func TestRemovalHostCrashLateForkSQLiteRecovery(t *testing.T) {
	for _, removeReplacement := range []bool{false, true} {
		t.Run(strconv.FormatBool(removeReplacement), func(t *testing.T) {
			if !retirementAcceptance {
				t.Skip("deferred retirement acceptance; run with -tags=retirement_acceptance")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dataDir := t.TempDir()
			st := sqlitetest.MustOpenAt(t, dataDir)
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "crash-project", Path: dataDir, RegisteredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "crash-project", Harness: domain.HarnessClaudeCode,
				Mode: domain.SessionModeChat, Kind: domain.KindWorker, CreatedAt: time.Now(), Activity: domain.Activity{State: domain.ActivityActive},
				Metadata: domain.SessionMetadata{ControllerGeneration: "original-controller", WorkspacePath: dataDir}})
			if err != nil {
				t.Fatal(err)
			}
			provider := domain.AccountsManagerProviderClaude
			if _, _, err := st.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: rec.ID, Provider: provider,
				Mode: domain.AccountsManagerManaged, AccountID: "account-a"}); err != nil {
				t.Fatal(err)
			}
			host, descriptor, leader, dir := startRemovalLateForkHost(t, dataDir, string(rec.ID))
			identity := descriptorIdentity(descriptor)
			if err := st.RecordAccountsManagerChatHost(ctx, rec.ID, provider, rec.Metadata.ControllerGeneration, identity); err != nil {
				t.Fatal(err)
			}
			if err := host.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := host.Wait(); err == nil || !removalCrashProcessRunning(leader) {
				t.Fatal("parent crash did not preserve the original provider")
			}
			replacementHost, _, replacement := startRemovalCrashHost(t, dataDir, string(rec.ID))
			_, _, native := startRemovalCrashHost(t, dataDir, "native-unrelated")
			if removeReplacement {
				if err := Shutdown(ctx, dataDir, string(rec.ID)); err != nil {
					t.Fatal(err)
				}
				if err := replacementHost.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			router := &removalCrashRouter{store: st}
			newManager := func() *sessionmanager.Manager {
				chat := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, StopExactProviderHost: func(ctx context.Context, id domain.SessionID, owner string) error {
					return ShutdownExact(ctx, dataDir, string(id), owner)
				}})
				return sessionmanager.New(sessionmanager.Deps{Store: st, Chat: removalCrashChat{chat}, Lifecycle: lifecycle.New(st, nil),
					AccountsManager: router, DataDir: dataDir, BackgroundContext: ctx})
			}
			m := newManager()
			impact, err := st.AccountsManagerRemovalImpact(ctx, "account-a")
			if err != nil || len(impact.Sessions) != 1 || !impact.Sessions[0].OwnsController() {
				t.Fatal("missing managed controller in deletion impact", err)
			}
			observed, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			barrier := func(owner providerOwner) {
				if owner.Identity == identity {
					once.Do(func() { close(observed); <-resume })
				}
			}
			providerCensusBarrierForTest.Store(&barrier)
			t.Cleanup(func() { providerCensusBarrierForTest.Store(nil) })
			if _, err := m.StartAccountsManagerRemoval(ctx, "crash-delete", "account-a", impact.Revision, true); err != nil {
				t.Fatal(err)
			}
			select {
			case <-observed:
			case <-time.After(5 * time.Second):
				close(resume)
				t.Fatal("coordinator never reached the process census")
			}
			if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
				close(resume)
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for removalCrashProcessRunning(leader) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			close(resume)
			if removalCrashProcessRunning(leader) {
				t.Fatal("recorded leader did not exit at the barrier")
			}
			data, err := os.ReadFile(filepath.Join(dir, "child"))
			if err != nil {
				t.Fatal(err)
			}
			child, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			assertHeld := func() {
				t.Helper()
				waitCtx, stopWaiting := context.WithTimeout(ctx, 10*time.Second)
				defer stopWaiting()
				if err := m.WaitAgentSwitchWorkers(waitCtx); err != nil {
					t.Fatal(err)
				}
				op, found, err := st.GetAccountsManagerRemoval(ctx, "crash-delete")
				if err != nil || !found || op.Phase != domain.AccountsManagerRemovalRecovery || op.Impact.Sessions[0].Stopped || router.finalized.Load() != 0 {
					t.Fatal("incomplete census advanced stop or credential deletion", op.Phase, err)
				}
				if !removalCrashProcessRunning(child) || !removalCrashProcessRunning(native.Provider) || !removalCrashProcessRunning(native.Child) {
					t.Fatal("pending deletion stopped an unproven or unrelated process")
				}
				if !removeReplacement && (!removalCrashProcessRunning(replacement.Provider) || !removalCrashProcessRunning(replacement.Child)) {
					t.Fatal("pending deletion stopped the replacement")
				}
				if release, ok := m.AcquireSessionInput(rec.ID); ok {
					release()
					t.Error("pending deletion reopened intake")
				}
			}
			assertHeld()
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = sqlite.OpenPreMigrated(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			router.store = st
			for range 2 {
				m = newManager()
				if err := m.ReconcileStartupSafety(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := m.RetryAccountsManagerRemoval(ctx, "crash-delete"); err != nil {
					t.Fatal(err)
				}
				assertHeld()
			}
			process, err := readProviderProc(child)
			if err != nil || process.group != leader {
				t.Fatal("fixture child changed identity", err)
			}
			if err := signalProviderIdentity(ctx, process); err != nil {
				t.Fatal(err)
			}
			owner, err := readProviderOwner(dataDir, string(rec.ID), identity)
			if err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(5 * time.Second)
			for {
				absent, err := providerGroupAbsent(owner)
				if err != nil {
					t.Fatal(err)
				}
				if absent {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("fixture group was not reaped")
				}
				time.Sleep(time.Millisecond)
			}
			m = newManager()
			if err := m.ReconcileStartupSafety(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := m.RetryAccountsManagerRemoval(ctx, "crash-delete"); err != nil {
				t.Fatal(err)
			}
			if err := m.WaitAgentSwitchWorkers(ctx); err != nil {
				t.Fatal(err)
			}
			op, found, err := st.GetAccountsManagerRemoval(ctx, "crash-delete")
			if err != nil || !found || op.Phase != domain.AccountsManagerRemovalComplete || !op.Impact.Sessions[0].Stopped || router.finalized.Load() != 1 {
				t.Fatal("confirmed absence did not complete deletion", op.Phase, err)
			}
			if err := ShutdownExact(ctx, dataDir, string(rec.ID), identity); err != nil {
				t.Fatal("stopped replay failed", err)
			}
			binding, found, err := st.GetAccountsManagerSessionRoute(ctx, rec.ID, provider)
			if err != nil || !found || !binding.Blocked || binding.AccountID != "account-a" {
				t.Fatal("deletion lost the blocked selection or chose another account", err)
			}
			if !removalCrashProcessRunning(native.Provider) || !removalCrashProcessRunning(native.Child) ||
				(!removeReplacement && (!removalCrashProcessRunning(replacement.Provider) || !removalCrashProcessRunning(replacement.Child))) {
				t.Error("final deletion or stopped replay touched an unrelated process")
			}
		})
	}
}
