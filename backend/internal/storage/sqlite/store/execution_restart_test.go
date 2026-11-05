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
