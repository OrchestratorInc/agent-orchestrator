package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// An in-chat provider switch publishes a new Chat controller generation. The
// session's admission slot must follow it, otherwise the next relaunch is
// refused with ErrSchedulerAdmissionConflict ("step: execution admission") and
// the session can never be restored.
func TestProviderSwitchKeepsExecutionAdmissionGenerationInSync(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	conversation, err := x.s.CreateConversation(ctx, "conversation-switch",
		domain.ConversationScopeSession, x.rec.ProjectID, x.rec.ID, x.now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	// The caller publishes the target controller's identity on the same record,
	// as the provider switch does before committing.
	x.rec.Metadata.ControllerGeneration = "switched"
	x.rec.Metadata.ProviderConversationID = "thread-omp"
	if err := x.s.CommitProviderSwitch(ctx, x.rec, domain.ConversationBranch{
		ID:                     "branch-omp",
		ConversationID:         conversation.ID,
		SessionID:              x.rec.ID,
		ProviderConversationID: "thread-omp",
		ParentBranchID:         conversation.ActiveBranchID,
	}, "switched", domain.ConversationSettings{}, domain.ConversationActivity{
		ID:     "branch-omp:provider-switch",
		Kind:   domain.ActivityKindSystem,
		Status: domain.ActivityStatusCompleted,
	}); err != nil {
		t.Fatalf("CommitProviderSwitch: %v", err)
	}

	admissions, err := x.s.ListSchedulerAdmissions(ctx, false)
	if err != nil || len(admissions) != 1 {
		t.Fatalf("admissions = %+v err=%v", admissions, err)
	}
	if got := admissions[0].ControllerGeneration; got != "switched" {
		t.Errorf("admission generation after provider switch = %q, want the session's %q", got, "switched")
	}

	// The relaunch path a terminated/resumed session takes: it must be admitted,
	// not refused because the slot still names the pre-switch controller.
	rec, found, err := x.s.GetSession(ctx, x.rec.ID)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	if err := x.s.AdmitSessionRestart(ctx, rec, "relaunched", x.proof); err != nil {
		t.Fatalf("AdmitSessionRestart after provider switch: %v", err)
	}
}

// A store written before the switch/activation paths synced the slot keeps an
// active admission naming a generation the session no longer runs. The session
// was terminated (no controller can still be unpublished), so the relaunch must
// repair the slot instead of refusing it forever.
func TestTerminatedSessionRepairsDriftedAdmissionGeneration(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	x.rec.IsTerminated = true
	x.rec.Metadata.ControllerGeneration = "switched"
	x.rec.UpdatedAt = x.now.Add(time.Second)
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if err := x.s.AdmitSessionRestart(ctx, x.rec, "relaunched", x.proof); err != nil {
		t.Fatalf("AdmitSessionRestart with drifted slot: %v", err)
	}
	admissions, err := x.s.ListSchedulerAdmissions(ctx, false)
	if err != nil || len(admissions) != 1 {
		t.Fatalf("admissions = %+v err=%v", admissions, err)
	}
	if got := admissions[0].ControllerGeneration; got != "relaunched" {
		t.Errorf("repaired admission generation = %q, want %q", got, "relaunched")
	}
}

// The repair above must not weaken the fence for a live session: an active slot
// naming another generation there can still be an unpublished start.
func TestLiveSessionStillRefusesDriftedAdmissionGeneration(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	x.rec.Metadata.ControllerGeneration = "switched"
	x.rec.UpdatedAt = x.now.Add(time.Second)
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if err := x.s.AdmitSessionRestart(ctx, x.rec, "relaunched", x.proof); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("live drifted restart = %v, want conflict", err)
	}
}

// Boot recovery adopts an old-term slot and marks it uncertain. A terminated
// session must still be restartable from that state, or a daemon restart would
// strand it forever.
func TestTerminatedSessionRestartsFromUncertainAdmission(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	adopted, applied, err := x.s.AdoptSchedulerAdmission(ctx, x.ad.ID, x.ad.Revision, x.ad.Fence, x.now)
	if err != nil || !applied || adopted.State != domain.AdmissionUncertain {
		t.Fatalf("adopt: applied=%v state=%v err=%v", applied, adopted.State, err)
	}
	x.rec.IsTerminated = true
	x.rec.Metadata.ControllerGeneration = "switched"
	x.rec.UpdatedAt = x.now.Add(time.Second)
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if err := x.s.AdmitSessionRestart(ctx, x.rec, "relaunched", x.proof); err != nil {
		t.Fatalf("AdmitSessionRestart from uncertain slot: %v", err)
	}
	admissions, _ := x.s.ListSchedulerAdmissions(ctx, false)
	if len(admissions) != 1 || admissions[0].State != domain.AdmissionActive || admissions[0].ControllerGeneration != "relaunched" {
		t.Fatalf("revived admission = %+v", admissions)
	}
}
