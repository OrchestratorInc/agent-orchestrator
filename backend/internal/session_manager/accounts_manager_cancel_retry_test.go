package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type accountRetryReadKey struct{}

type accountRetryBarrier struct {
	arrived, resume chan struct{}
	once, released  sync.Once
}

func newAccountRetryBarrier(t *testing.T) *accountRetryBarrier {
	b := &accountRetryBarrier{arrived: make(chan struct{}), resume: make(chan struct{})}
	t.Cleanup(b.release)
	return b
}

func (b *accountRetryBarrier) release() { b.released.Do(func() { close(b.resume) }) }

func (b *accountRetryBarrier) pause() {
	b.once.Do(func() {
		close(b.arrived)
		<-b.resume
	})
}

func (b *accountRetryBarrier) wait(t *testing.T) {
	t.Helper()
	select {
	case <-b.arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation race did not reach its barrier")
	}
}

type accountCancelledRetryStore struct {
	*sqlite.Store
	read, finishing *accountRetryBarrier
}

func (s *accountCancelledRetryStore) GetAccountsManagerSwitch(ctx context.Context, id string) (domain.AccountsManagerSwitch, bool, error) {
	op, found, err := s.Store.GetAccountsManagerSwitch(ctx, id)
	if ctx.Value(accountRetryReadKey{}) != nil {
		s.read.pause()
	}
	return op, found, err
}

func (s *accountCancelledRetryStore) AdvanceAccountsManagerSwitch(ctx context.Context, id string, expected, next domain.AccountsManagerSwitchPhase, code string) (domain.AccountsManagerSwitch, error) {
	if expected == domain.AccountsManagerSwitchWaiting && next == expected && code == "SOURCE_OWNERSHIP_UNCONFIRMED" {
		s.finishing.pause()
	}
	return s.Store.AdvanceAccountsManagerSwitch(ctx, id, expected, next, code)
}

type accountRetryCleanupGate struct {
	cleanup            *accountRetryBarrier
	acquired, released atomic.Int64
}

func (g *accountRetryCleanupGate) BeginInputDrain(string) (time.Time, func()) {
	g.acquired.Add(1)
	return time.Time{}, func() {
		g.cleanup.pause()
		g.released.Add(1)
	}
}

func exerciseAccountCancelledRetry(t *testing.T, m *Manager, st *sqlite.Store, rec domain.SessionRecord, cfg AccountsManagerSwitchConfig, cold bool, makeOwnerLive func(), interrupted <-chan struct{}) {
	t.Helper()
	store := &accountCancelledRetryStore{Store: st, read: newAccountRetryBarrier(t), finishing: newAccountRetryBarrier(t)}
	gate := &accountRetryCleanupGate{cleanup: newAccountRetryBarrier(t)}
	m.store = store
	m.SetTerminalInputGate(gate)
	var before domain.AccountsManagerSwitch
	var err error
	if cold {
		before = accountHandoffJournal(t, st, rec, cfg, domain.AccountsManagerSwitchWaiting)
		if err := m.ReconcileStartupSafety(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, err = m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID)
	} else {
		before, err = m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	store.finishing.wait(t)
	m.accountSwitchMu.Lock()
	run := m.accountSwitches[rec.ID]
	originalDone := run.done
	m.accountSwitchMu.Unlock()
	retried := make(chan error, 1)
	go func() {
		_, err := m.RetryAccountsManagerSwitch(context.WithValue(t.Context(), accountRetryReadKey{}, true), rec.ID, cfg.OperationID)
		retried <- err
	}()
	store.read.wait(t)
	if cancelled, err := m.CancelAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil || cancelled.Phase != domain.AccountsManagerSwitchCancelled {
		t.Fatal("cancellation did not win", err)
	}
	makeOwnerLive()
	store.finishing.release()
	gate.cleanup.wait(t)
	m.accountSwitchMu.Lock()
	t.Logf("cleanup window: running=%v cancelled=%v", run.running, run.cancelled)
	m.accountSwitchMu.Unlock()
	if release, ok := m.AcquireSessionInput(rec.ID); ok {
		release()
		t.Error("cancellation released session intake before cleanup")
	}
	store.read.release()
	select {
	case err = <-retried:
	case <-time.After(5 * time.Second):
		t.Fatal("stale retry did not return")
	}
	if !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Error("stale retry admitted a cancelled run", err)
		select {
		case <-interrupted:
		case <-time.After(5 * time.Second):
			t.Error("admitted retry did not reach the observed input boundary")
		}
	}
	gate.cleanup.release()
	settled := make(chan struct{})
	go func() { m.agentSwitchWorkers.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled workers did not settle")
	}
	select {
	case <-originalDone:
	default:
		t.Error("original worker completion was lost")
	}
	if run.done != originalDone {
		t.Error("cancelled worker completion channel was replaced")
	}
	m.accountSwitchMu.Lock()
	retained := m.accountSwitches[rec.ID] != nil
	m.accountSwitchMu.Unlock()
	if retained {
		t.Error("cancelled run remained eligible for lookup after cleanup")
	}
	if gate.acquired.Load() != 1 || gate.released.Load() != 1 {
		t.Fatal("input fence was not acquired and released exactly once")
	}
	if release, ok := m.AcquireSessionInput(rec.ID); !ok {
		t.Fatal("cancelled operation retained session intake")
	} else {
		release()
	}
	verifyCancelled := func(current *sqlite.Store) {
		t.Helper()
		after, found, err := current.GetAccountsManagerSwitch(t.Context(), cfg.OperationID)
		binding, _, bindingErr := current.GetAccountsManagerSessionRoute(t.Context(), rec.ID, before.Provider)
		if err != nil || !found || bindingErr != nil || after.Phase != domain.AccountsManagerSwitchCancelled ||
			after.TargetGeneration != before.TargetGeneration || after.TargetRevision != 0 || binding.Revision != before.SourceRevision || binding.AccountID != before.SourceAccountID || binding.Blocked {
			t.Fatal("cancelled operation mutated durable intent", err, bindingErr)
		}
	}
	verifyCancelled(st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := New(Deps{Store: reopened, Runtime: m.runtime, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: &accountSwitchRouter{store: reopened},
		Agents: m.agents, DataDir: m.dataDir, RunFilePath: m.runFilePath, LookPath: m.lookPath, BackgroundContext: t.Context()})
	restartedGate := &transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1)}
	restarted.SetTerminalInputGate(restartedGate)
	if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
			t.Fatal("restart admitted a cancelled operation", err)
		}
	}
	if len(restartedGate.acquired) != 0 {
		t.Fatal("restart fenced a cancelled operation")
	}
	if release, ok := restarted.AcquireSessionInput(rec.ID); !ok {
		t.Fatal("restart retained cancelled session intake")
	} else {
		release()
	}
	verifyCancelled(reopened)
}

func TestAccountsManagerSwitchCancelledRetryCannotRestart(t *testing.T) {
	for _, prefix := range []string{"ptyhost-v1:", ""} {
		for _, cold := range []bool{false, true} {
			name := prefix + "/initial"
			if cold {
				name = prefix + "/startup"
			}
			t.Run(name, func(t *testing.T) {
				m, st, fake, _, rec, cfg := accountSwitchFixture(t)
				handle := ports.RuntimeHandle{ID: prefix + string(rec.ID)}
				rec.Metadata.RuntimeHandleID, rec.Activity.State = handle.ID, domain.ActivityActive
				if err := st.UpdateSession(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
				rt := &accountHandoffOwnershipRuntime{accountTeardownSlots: &accountTeardownSlots{accountSlotsRuntime: accountSlotsRuntime{
					accountSwitchRuntime: accountSwitchRuntime{fake}, generations: map[string]string{handle.ID: rec.Metadata.RuntimeLaunchID}},
					handles: []ports.RuntimeHandle{handle}, active: handle}, unknown: ports.FencedReasonProbeFailed}
				m.runtime = rt
				fake.aliveByHandle = map[string]bool{handle.ID: true}
				interrupted := make(chan struct{}, 1)
				fake.onInterrupt = func(ports.RuntimeHandle) { interrupted <- struct{}{} }
				fake.interruptErr = errors.New("unexpected post-cancellation interrupt")
				probes := 0
				exerciseAccountCancelledRetry(t, m, st, rec, cfg, cold, func() {
					probes = len(rt.fencedRefs)
					rt.unknown = ""
				}, interrupted)
				if len(rt.fencedRefs) != probes || len(fake.interrupts) != 0 || rt.inputs != 0 || fake.destroyed != 0 || fake.created != 0 {
					t.Errorf("post-cancellation effects: probes=%d interrupts=%d input=%d destroy=%d launch=%d", len(rt.fencedRefs)-probes, len(fake.interrupts), rt.inputs, fake.destroyed, fake.created)
				}
			})
		}
	}
}
