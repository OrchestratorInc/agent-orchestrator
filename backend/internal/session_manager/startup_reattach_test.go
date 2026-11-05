package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// 删除 ErrSchedulerFence 的有界重试白名单或改回普通准入，此真实启动顺序用例无法连回现存 Host。
func TestM3StartupReattachWaitsForRecoveredInstance(t *testing.T) {
	f := newStartupReattachFixture(t, false, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.m.startupChatRetryDelays = []time.Duration{20 * time.Millisecond, 100 * time.Millisecond, 500 * time.Millisecond}
	if err := f.m.checkSessionHealth(ctx, f.rec); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("preboot: %v", err)
	}
	if err := f.m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	if f.driver.reconnects.Load() != 0 {
		t.Fatal("contacted provider before instance recovery")
	}
	f.boot(t)
	deadline := time.Now().Add(5 * time.Second)
	for !f.chat.HasLiveChatController(f.rec.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !f.chat.HasLiveChatController(f.rec.ID) {
		t.Fatal("background retry failed to reconnect after startup barrier")
	}
	current, found, err := f.s.GetSession(ctx, f.rec.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if current.Metadata.ControllerGeneration == f.rec.Metadata.ControllerGeneration || current.Metadata.ProviderConversationID != f.rec.Metadata.ProviderConversationID || current.Activity != f.rec.Activity {
		t.Fatalf("unexpected recovered identity/activity: %+v", current.ControllerOwner())
	}
	if f.driver.starts.Load() != 0 || f.driver.resumes.Load() != 0 || f.driver.reconnects.Load() != 1 {
		t.Fatal("startup did not exclusively reattach")
	}
	ads, err := f.s.ListSchedulerAdmissions(ctx, false)
	if err != nil || len(ads) != 0 {
		t.Fatalf("legacy observation created admission: %d %v", len(ads), err)
	}
}

// 删除观察准入分支会拒绝 UNKNOWN；删除 live 租约门禁会使重附着后的业务动作越权成功。
func TestM3StartupReattachUnknownWorkflowPreservesBusinessFences(t *testing.T) {
	f := newStartupReattachFixture(t, true, true)
	ctx := context.Background()
	before, _, err := f.s.GetWorkflowAttempt(ctx, f.attempt.ID)
	if err != nil || before.State != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("unknown fixture: %+v %v", before, err)
	}
	rs, err := f.s.ListBudgetReservations(ctx, f.ad.ID)
	if err != nil || len(rs) != 1 {
		t.Fatalf("reservation: %d %v", len(rs), err)
	}
	f.driver.before = func(ctx context.Context) error {
		if _, ok := ports.SessionReattachPermitFromContext(ctx); !ok {
			return errors.New("observation permit missing at driver")
		}
		if _, ok := ports.ExecutionPermitFromContext(ctx); ok {
			return errors.New("observation was upgraded to execution")
		}
		return nil
	}
	if err := f.m.checkSessionHealth(ctx, f.rec); err != nil {
		t.Fatal(err)
	}
	after, _, err := f.s.GetWorkflowAttempt(ctx, f.attempt.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("reattach changed attempt: %+v %v", after, err)
	}
	actualRS, err := f.s.ListBudgetReservations(ctx, f.ad.ID)
	if err != nil || !reflect.DeepEqual(rs, actualRS) {
		t.Fatalf("reattach changed budget: %+v %v", actualRS, err)
	}
	ads, err := f.s.ListSchedulerAdmissions(ctx, true)
	if err != nil || len(ads) != 1 || ads[0].ID != f.ad.ID || ads[0].Fence != f.ad.Fence || ads[0].Revision != f.ad.Revision+1 {
		t.Fatalf("slot changed: %+v %v", ads, err)
	}
	current, _, err := f.s.GetSession(ctx, f.rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := f.s.GetWorkflowAttemptBinding(ctx, f.attempt.ID)
	if err != nil || b.ControllerGeneration != current.Metadata.ControllerGeneration || ads[0].ControllerGeneration != current.Metadata.ControllerGeneration || b.ControllerGeneration == f.rec.Metadata.ControllerGeneration {
		t.Fatalf("split generation: %+v %v", b, err)
	}
	p, err := f.s.LoadExecutionPermit(ctx, f.attempt.ID, f.ad.Fence, f.now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.ValidateExecutionPermit(ctx, p, f.now()); !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("business execution accepted unknown: %v", err)
	}
	execCtx := ports.WithExecutionPermit(ctx, p)
	if _, _, err := f.s.StartWorkflowAttempt(execCtx, p.WorkflowID, p.AttemptID, p.LeaseToken, "start-after-observe", strings.Repeat("a", 64), f.now()); !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("start: %v", err)
	}
	if _, _, err := f.s.SettleWorkflowAttempt(execCtx, p.WorkflowID, p.AttemptID, p.LeaseToken, domain.WorkflowAttemptStateSucceeded, "", "settle-after-observe", strings.Repeat("b", 64), f.now()); !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("settle: %v", err)
	}
	obs := ports.SessionObservation{SessionID: p.SessionID, ControllerGeneration: p.Generation, Readiness: ports.SessionReadinessReady, ObservedAt: f.now()}
	if err := f.s.RenewExecutionLease(ctx, p, obs, f.now().Add(time.Minute), f.now()); !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("renew: %v", err)
	}
	if f.driver.starts.Load()+f.driver.resumes.Load() != 0 {
		t.Fatal("workflow observation spawned a provider")
	}
}

// 删除事务内实时 Fence 检查会接收过期租约或旧 epoch 的慢连接；cleanup 必须仅 Close 本次连接。
func TestM3StartupReattachLateHostCannotClaimNewEpoch(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		name := "expired-lease"
		if takeover {
			name = "new-owner"
		}
		t.Run(name, func(t *testing.T) {
			f := newStartupReattachFixture(t, true, true)
			f.driver.before = func(ctx context.Context) error {
				f.clock.Add(int64(31 * time.Second))
				if takeover {
					l, _, err := f.s.AcquireSchedulerLeader(ctx, "new-instance", f.now(), f.now().Add(time.Minute))
					if err != nil {
						return err
					}
					if _, _, err := f.s.CompleteSchedulerRecovery(ctx, l.Fence, l.Revision, f.now()); err != nil {
						return err
					}
				}
				return nil
			}
			if err := f.m.checkSessionHealth(context.Background(), f.rec); !errors.Is(err, domain.ErrSchedulerFence) {
				t.Fatalf("stale connection: %v", err)
			}
			current, _, err := f.s.GetSession(context.Background(), f.rec.ID)
			if err != nil || current.ControllerOwner() != f.rec.ControllerOwner() {
				t.Fatalf("late connection wrote owner: %+v %v", current.ControllerOwner(), err)
			}
			ad, _, err := f.s.GetSchedulerAdmission(context.Background(), f.ad.ID)
			if err != nil || ad != f.ad {
				t.Fatalf("late connection wrote slot: %+v %v", ad, err)
			}
			if f.driver.host.closed.Load() != 1 || f.driver.host.terminated.Load() != 0 || f.chat.HasLiveChatController(f.rec.ID) {
				t.Fatal("rejected connection was published or terminated host")
			}
		})
	}
}

// 删除纯重连返回值 guard，会在 callback 拒绝前写入 generation，并将非 live 连接错误 Terminate。
func TestM3StartupReattachRejectsNonLiveContractBeforeClaim(t *testing.T) {
	f := newStartupReattachFixture(t, false, true)
	f.driver.host.live = false
	if err := f.m.checkSessionHealth(context.Background(), f.rec); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("non-live result: %v", err)
	}
	current, _, err := f.s.GetSession(context.Background(), f.rec.ID)
	if err != nil || current.ControllerOwner() != f.rec.ControllerOwner() {
		t.Fatalf("non-live result changed owner: %+v %v", current.ControllerOwner(), err)
	}
	if f.driver.host.closed.Load() != 1 || f.driver.host.terminated.Load() != 0 || f.chat.HasLiveChatController(f.rec.ID) {
		t.Fatal("non-live result killed host or was published")
	}
}

// 删除 ReconnectOnly 驱动选择会进入 Resume；删除失败回执 Fence 复验会让迟到的 absent 把会话标退出。
func TestM3StartupReattachMissingHostIsNeverSpawned(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "current-owner"
		if expired {
			name = "late-absence"
		}
		t.Run(name, func(t *testing.T) {
			f := newStartupReattachFixture(t, false, true)
			f.driver.before = func(context.Context) error {
				if expired {
					f.clock.Add(int64(time.Minute))
				}
				return ports.ErrChatHostNotRunning
			}
			err := f.m.checkSessionHealth(context.Background(), f.rec)
			if expired && !errors.Is(err, domain.ErrSchedulerFence) || !expired && err != nil {
				t.Fatalf("missing host: %v", err)
			}
			current, _, err := f.s.GetSession(context.Background(), f.rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if expired && current.Activity != f.rec.Activity {
				t.Fatal("late absence marked current owner exited")
			}
			if !expired && current.Activity.State != domain.ActivityExited {
				t.Fatalf("confirmed absence not recorded: %s", current.Activity.State)
			}
			if f.driver.starts.Load()+f.driver.resumes.Load() != 0 || f.driver.reconnects.Load() != 1 {
				t.Fatal("missing host caused spawn")
			}
		})
	}
}
