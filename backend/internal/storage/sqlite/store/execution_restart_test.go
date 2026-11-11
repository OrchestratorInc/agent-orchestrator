package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// restartFixture 在真实 SQLite 接受普通 Session；外部进程由调用方自行模拟，不自动发布新代际。
type restartFixture struct {
	s     *sqlite.Store
	f     *sqlitetest.AdmissionFixture
	now   time.Time
	rec   domain.SessionRecord
	ad    domain.SchedulerAdmission
	proof domain.AdmissionEvidence
}

func newRestartFixture(t *testing.T) *restartFixture {
	t.Helper()
	x := &restartFixture{s: newTestStore(t), now: workflowStoreTime()}
	x.f = sqlitetest.NewAdmissionFixture(t, x.s, func() time.Time { return x.now })
	x.proof, _ = x.f.PrepareSessionAdmission(context.Background(), ports.SpawnConfig{Harness: domain.HarnessCodex})
	var err error
	x.ad, _, err = x.s.ReserveSessionAdmission(context.Background(), domain.SessionAdmissionInput{Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, ClientRequestID: "restart-original", ClientRequestHash: domain.SessionAdmissionFingerprint("original-hash"), Evidence: x.proof})
	if err != nil {
		t.Fatal(err)
	}
	seed := domain.SessionRecord{Harness: domain.HarnessCodex, Kind: domain.KindWorker, Mode: domain.SessionModeChat, ClientRequestID: "restart-original", ClientRequestHash: "original-hash", CreatedAt: x.now, UpdatedAt: x.now, Metadata: domain.SessionMetadata{ControllerGeneration: "first"}}
	x.rec, _, err = x.s.CreateAdmittedSession(context.Background(), seed, x.ad, "", x.now)
	if err != nil {
		t.Fatal(err)
	}
	x.ad, _, _ = x.s.GetSchedulerAdmission(context.Background(), x.ad.ID)
	return x
}

// 删除接受代际与 Session 对齐检查会允许丢回执后的第二次启动，或用旧终止事实释放新槽。
func TestM3RestartReusesSlotAndFencesUnpublishedGeneration(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	if err := x.s.AdmitSessionRestart(ctx, x.rec, "second", x.proof); err != nil {
		t.Fatal(err)
	}
	if err := x.s.AdmitSessionRestart(ctx, x.rec, "third", x.proof); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("duplicate start: %v", err)
	}
	ads, _ := x.s.ListSchedulerAdmissions(ctx, false)
	if len(ads) != 1 || ads[0].ControllerGeneration != "second" {
		t.Fatalf("active slot not reused: %+v", ads)
	}
	x.rec.IsTerminated = true
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatal(err)
	}
	if deleted, err := x.s.DeleteTerminatedSession(ctx, x.rec.ID); deleted || !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("old generation deleted pending start: deleted=%v err=%v", deleted, err)
	}
	x.rec.IsTerminated = false
	x.rec.Metadata.ControllerGeneration = "second"
	x.rec.UpdatedAt = x.now.Add(time.Millisecond)
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatal(err)
	}
	ad, _, _ := x.s.GetSchedulerAdmission(ctx, x.ad.ID)
	if _, _, err := x.s.AdoptSchedulerAdmission(ctx, ad.ID, ad.Revision, ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	if err := x.s.AdmitSessionRestart(ctx, x.rec, "third", x.proof); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("unknown restarted: %v", err)
	}
}

// 删除 checkAdmission 会让已释放 Session 在容量已满时重新执行。
func TestM3ReleasedSessionRestartMustPassCapacity(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	x.rec.IsTerminated = true
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatal(err)
	}
	if err := x.s.ReleaseExecutionAdmission(ctx, x.ad, ports.SessionObservation{SessionID: x.rec.ID, Terminated: true, ControllerGeneration: "first", ObservedAt: x.now}, x.now); err != nil {
		t.Fatal(err)
	}
	m3Capacity(t, x.s, domain.HarnessCodex, 10, x.now)
	x.proof, _ = x.f.PrepareSessionAdmission(ctx, ports.SpawnConfig{Harness: domain.HarnessCodex})
	for _, key := range []string{"one", "two", "three"} {
		if _, _, err := x.s.ReserveSessionAdmission(ctx, domain.SessionAdmissionInput{Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, ClientRequestID: key, ClientRequestHash: strings.Repeat("a", 64), Evidence: x.proof}); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.s.AdmitSessionRestart(ctx, x.rec, "second", x.proof); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("full restart: %v", err)
	}
	ads, _ := x.s.ListSchedulerAdmissions(ctx, false)
	if len(ads) != 4 {
		t.Fatalf("failed restart left admission: %+v", ads)
	}
}

// 删除只排除本次替换旧 Session 的查询条件会把 Legacy Session 纳管误算成两个槽。
func TestM3LegacySessionRestartCountsOneSlot(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	m3Capacity(t, x.s, domain.HarnessCodex, 10, x.now)
	x.proof, _ = x.f.PrepareSessionAdmission(ctx, ports.SpawnConfig{Harness: domain.HarnessCodex})
	legacy := domain.SessionRecord{Harness: domain.HarnessCodex, Kind: domain.KindWorker, Mode: domain.SessionModeChat, CreatedAt: x.now, UpdatedAt: x.now, Metadata: domain.SessionMetadata{ControllerGeneration: "legacy"}}
	legacy, err := x.s.CreateSession(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.ReserveSessionAdmission(ctx, domain.SessionAdmissionInput{Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, ClientRequestID: "third-slot", ClientRequestHash: strings.Repeat("a", 64), Evidence: x.proof}); err != nil {
		t.Fatal(err)
	}
	if err := x.s.AdmitSessionRestart(ctx, legacy, "new", x.proof); err != nil {
		t.Fatalf("legacy double counted: %v", err)
	}
	occupied, err := x.s.ListSchedulerOccupancy(ctx)
	if err != nil || len(occupied) != 3 {
		t.Fatalf("occupied=%+v err=%v", occupied, err)
	}
}

// 守护普通 Chat 重启后的退出槽：恢复必须只把同代际 observed exit 重新占位，
// 否则下一次 Resume 会永久卡在 execution admission。
func TestExitedChatRestartsAfterRecovery(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	x.rec.Activity.State = domain.ActivityExited
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatal(err)
	}
	if err := x.s.RecoverExecutionAdmissionSession(ctx, x.ad, ports.SessionObservation{
		SessionID: x.rec.ID, ClientRequestID: x.rec.ClientRequestID,
		ControllerGeneration: x.rec.Metadata.ControllerGeneration,
		Readiness:            ports.SessionReadinessExited, ObservedAt: x.now,
	}, x.now); err != nil {
		t.Fatal(err)
	}
	adopted, _, err := x.s.GetSchedulerAdmission(ctx, x.ad.ID)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.State != domain.AdmissionActive {
		t.Fatalf("exited Chat admission = %s, want active", adopted.State)
	}
	if err := x.s.AdmitSessionRestart(ctx, x.rec, "resumed", x.proof); err != nil {
		t.Fatalf("resume exited Chat: %v", err)
	}
}

// 放行只针对普通 Chat：工作流退出仍由 Attempt 决策，旧代际仍可能是在途启动。
func TestExitedAdmissionRecoveryKeepsWorkflowAndDriftedGenerationUncertain(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation string
	}{
		{name: "controller generation drifted", generation: "replacement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newRestartFixture(t)
			ctx := context.Background()
			generation := x.rec.Metadata.ControllerGeneration
			if tc.generation != "" {
				generation = tc.generation
			}
			if err := x.s.RecoverExecutionAdmissionSession(ctx, x.ad, ports.SessionObservation{
				SessionID: x.rec.ID, ControllerGeneration: generation,
				Readiness: ports.SessionReadinessExited, ObservedAt: x.now,
			}, x.now); err != nil {
				t.Fatal(err)
			}
			kept, _, err := x.s.GetSchedulerAdmission(ctx, x.ad.ID)
			if err != nil || kept.State != domain.AdmissionUncertain {
				t.Fatalf("admission = %+v err=%v, want uncertain", kept, err)
			}
		})
	}
}

// A daemon restart adopts both live sessions and abandoned reservations. The
// latter never crossed the durable spawn boundary and otherwise hold capacity
// in uncertain forever.
func TestAbandonedSessionAdmissionReleasesDuringRecovery(t *testing.T) {
	dir := t.TempDir()
	x := &restartFixture{s: sqlitetest.MustOpenAt(t, dir), now: workflowStoreTime()}
	x.f = sqlitetest.NewAdmissionFixture(t, x.s, func() time.Time { return x.now })
	x.proof, _ = x.f.PrepareSessionAdmission(context.Background(), ports.SpawnConfig{Harness: domain.HarnessCodex})
	var err error
	x.ad, _, err = x.s.ReserveSessionAdmission(context.Background(), domain.SessionAdmissionInput{Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, ClientRequestID: "abandoned-source", ClientRequestHash: strings.Repeat("a", 64), Evidence: x.proof})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	empty, applied, err := x.s.AdoptSchedulerAdmission(ctx, x.ad.ID, x.ad.Revision, x.ad.Fence, x.now)
	if err != nil || !applied {
		t.Fatalf("adopt: applied=%v err=%v", applied, err)
	}
	empty.SessionID = ""
	empty.AttemptID = ""
	if err := x.s.ReleaseAbandonedExecutionAdmission(ctx, empty, x.now); err != nil {
		t.Fatal(err)
	}
	kept, _, err := x.s.GetSchedulerAdmission(ctx, empty.ID)
	if err != nil || kept.State != domain.AdmissionReleased {
		t.Fatalf("abandoned admission = %+v err=%v", kept, err)
	}
}

// A resumed Chat publishes its Session owner and execution slot in one commit;
// splitting these writes leaves the previous generation to strand every restart.
func TestChatSpawnCommitPublishesControllerGenerationToAdmission(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	conversation, err := x.s.CreateConversation(ctx, "conversation-resume-commit", domain.ConversationScopeSession, x.rec.ProjectID, x.rec.ID, x.now)
	if err != nil {
		t.Fatal(err)
	}
	boundary := domain.ConversationBranch{
		ID: "boundary-resume-commit", ConversationID: conversation.ID, SessionID: x.rec.ID,
		ProviderConversationID: "thread-resume-commit", ProviderScopeID: "boundary-resume-commit",
		ParentBranchID: conversation.ActiveBranchID, CreatedAt: x.now,
	}
	x.rec.Metadata.ProviderConversationID = boundary.ProviderConversationID
	x.rec.Metadata.ControllerGeneration = "resumed-commit"
	x.rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: x.now}
	x.rec.UpdatedAt = x.now.Add(time.Second)
	if err := x.s.CommitChatSpawn(ctx, x.rec, boundary); err != nil {
		t.Fatal(err)
	}
	ad, _, err := x.s.GetSchedulerAdmission(ctx, x.ad.ID)
	if err != nil || ad.ControllerGeneration != "resumed-commit" {
		t.Fatalf("admission generation=%q err=%v, want resumed-commit", ad.ControllerGeneration, err)
	}
}
