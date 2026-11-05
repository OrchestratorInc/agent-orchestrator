package store_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const interventionSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func interventionFixture(
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
	clientMessageID string,
	mode domain.WorkflowInterventionMode,
) domain.WorkflowIntervention {
	attempt := domain.WorkflowAttemptID("attempt-1")
	state := domain.WorkflowInterventionStateRequested
	// No session by default: session_id is nullable in every state, and most of these
	// tests are about receipts rather than session hosting. Tests that need a real session
	// seed one and set it explicitly.
	intervention := domain.WorkflowIntervention{
		ID: "intervention-" + clientMessageID, WorkflowID: workflowID, TaskID: taskID,
		AttemptID: &attempt, Mode: mode, State: state,
		ControllerGeneration: "gen-1", ClientMessageID: clientMessageID,
		OperationSHA: interventionSHA,
	}
	if mode == domain.WorkflowInterventionModeSteer {
		intervention.TargetProviderTurnID = "prov-turn-1"
	}
	return intervention
}

func TestWorkflowInterventionCreateIsIdempotentByClientMessage(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-idem")
	taskID := domain.WorkflowTaskID("task-intervention")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"intervention-create", strings.Repeat("a", 64), now)
	seedProject(t, s, "intervention-project")

	intervention := interventionFixture(workflowID, taskID, "msg-1", domain.WorkflowInterventionModeSteer)
	first, created, err := s.CreateWorkflowIntervention(ctx, intervention, now.Add(time.Second))
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	if first.State != domain.WorkflowInterventionStateRequested || first.Revision != 1 {
		t.Fatalf("first = %+v, want requested at revision 1", first)
	}

	// Replay with the same fingerprint: the original comes back, nothing is created.
	second, created, err := s.CreateWorkflowIntervention(ctx, intervention, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("replay err = %v", err)
	}
	if created {
		t.Fatal("a replay created a second intervention")
	}
	if second.ID != first.ID || second.Revision != first.Revision {
		t.Fatalf("replay = %+v, want the original %+v", second, first)
	}

	// Same key, different fingerprint: a typed conflict.
	conflicting := intervention
	conflicting.OperationSHA = strings.Repeat("f", 64)
	if _, _, err := s.CreateWorkflowIntervention(ctx, conflicting, now.Add(3*time.Second)); !errors.Is(err, domain.ErrInterventionOperationConflict) {
		t.Fatalf("conflicting sha err = %v, want ErrInterventionOperationConflict", err)
	}
}

// TestWorkflowInterventionRefusesFabricatedApplied is the store-level "no fabricated applied"
// proof: the receipt path refuses the state when the chat fact does not license it.
func TestWorkflowInterventionRefusesFabricatedApplied(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-fabricate")
	taskID := domain.WorkflowTaskID("task-fabricate")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"fabricate-create", strings.Repeat("a", 64), now)

	intervention := interventionFixture(workflowID, taskID, "msg-fab", domain.WorkflowInterventionModeSteer)
	created, _, err := s.CreateWorkflowIntervention(ctx, intervention, now.Add(time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, state := range []domain.WorkflowInterventionState{
		domain.WorkflowInterventionStateApplied,
		domain.WorkflowInterventionStateDelivered,
		domain.WorkflowInterventionStateQueued,
	} {
		_, applied, err := s.ApplyWorkflowInterventionReceipt(
			ctx, created.ID, state, domain.InterventionChatFact{}, "forced", false,
			"gen-1", created.Revision, now.Add(2*time.Second),
		)
		if !errors.Is(err, domain.ErrInterventionEvidenceMissing) {
			t.Fatalf("%q err = %v, want ErrInterventionEvidenceMissing", state, err)
		}
		if applied {
			t.Fatalf("%q was written without chat evidence", state)
		}
	}

	// With evidence, applied is allowed.
	applied, ok, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateApplied,
		domain.InterventionChatFact{ChatTurnID: "turn-1", ProviderTurnID: "prov-1"},
		"applied", false, "gen-1", created.Revision, now.Add(3*time.Second),
	)
	if err != nil || !ok {
		t.Fatalf("applied with evidence: ok=%v err=%v", ok, err)
	}
	if applied.State != domain.WorkflowInterventionStateApplied {
		t.Fatalf("state = %q, want applied", applied.State)
	}
	if applied.SettledAt == nil {
		t.Fatal("a terminal receipt must record settled_at")
	}
	if applied.ProviderTurnID != "prov-1" || applied.ChatTurnID != "turn-1" {
		t.Fatalf("stored turns = %q/%q", applied.ChatTurnID, applied.ProviderTurnID)
	}
}

// TestWorkflowInterventionModeStateCheck proves the migration's CHECK rejects a steer
// recorded as a stop state and a stop recorded as applied.
func TestWorkflowInterventionModeStateCheck(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-mode")
	taskID := domain.WorkflowTaskID("task-mode")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"mode-create", strings.Repeat("a", 64), now)

	// A steer cannot transition into a stop state.
	steer := interventionFixture(workflowID, taskID, "msg-mode-steer", domain.WorkflowInterventionModeSteer)
	createdSteer, _, err := s.CreateWorkflowIntervention(ctx, steer, now.Add(time.Second))
	if err != nil {
		t.Fatalf("create steer: %v", err)
	}
	if _, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, createdSteer.ID, domain.WorkflowInterventionStateStopConfirmed,
		domain.InterventionChatFact{}, "forced", false, "gen-1", createdSteer.Revision,
		now.Add(2*time.Second),
	); err == nil {
		t.Fatal("a steer was recorded as stop_confirmed")
	}

	// A stop cannot transition into applied, even with perfect evidence.
	stop := interventionFixture(workflowID, taskID, "msg-mode-stop", domain.WorkflowInterventionModeStop)
	stop.TargetProviderTurnID = ""
	createdStop, _, err := s.CreateWorkflowIntervention(ctx, stop, now.Add(time.Second))
	if err != nil {
		t.Fatalf("create stop: %v", err)
	}
	if _, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, createdStop.ID, domain.WorkflowInterventionStateApplied,
		domain.InterventionChatFact{ChatTurnID: "turn-1", ProviderTurnID: "prov-1"},
		"forced", false, "gen-1", createdStop.Revision, now.Add(2*time.Second),
	); err == nil {
		t.Fatal("a stop was recorded as applied")
	}
}

// TestWorkflowInterventionGenerationFence proves a receipt from a replaced controller cannot
// settle a newer intervention.
func TestWorkflowInterventionGenerationFence(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-gen")
	taskID := domain.WorkflowTaskID("task-gen")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"gen-create", strings.Repeat("a", 64), now)

	stop := interventionFixture(workflowID, taskID, "msg-gen", domain.WorkflowInterventionModeStop)
	stop.TargetProviderTurnID = ""
	created, _, err := s.CreateWorkflowIntervention(ctx, stop, now.Add(time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateStopConfirmed,
		domain.InterventionChatFact{}, "late", false, "gen-0", created.Revision,
		now.Add(2*time.Second),
	); !errors.Is(err, domain.ErrInterventionStaleGeneration) {
		t.Fatalf("err = %v, want ErrInterventionStaleGeneration", err)
	}
	// The current generation still works, walking the legal stop path: requested →
	// stop_requested → stop_confirmed. The rejected attempt above consumed no revision,
	// which is itself the point: a stale-generation receipt changes nothing.
	inFlight, applied, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateStopRequested,
		domain.InterventionChatFact{}, "", false, "gen-1", created.Revision,
		now.Add(3*time.Second),
	)
	if err != nil || !applied {
		t.Fatalf("stop_requested: applied=%v err=%v", applied, err)
	}
	if _, applied, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateStopConfirmed,
		domain.InterventionChatFact{}, "stop_confirmed", false, "gen-1", inFlight.Revision,
		now.Add(4*time.Second),
	); err != nil || !applied {
		t.Fatalf("current generation: applied=%v err=%v", applied, err)
	}
}

// TestWorkflowInterventionCountOpenStops proves the guard a retried stop relies on.
func TestWorkflowInterventionCountOpenStops(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-open")
	taskID := domain.WorkflowTaskID("task-open")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"open-create", strings.Repeat("a", 64), now)

	count, err := s.CountOpenStopInterventions(ctx, workflowID, taskID)
	if err != nil || count != 0 {
		t.Fatalf("initial count = %d err=%v, want 0", count, err)
	}

	stop := interventionFixture(workflowID, taskID, "msg-open", domain.WorkflowInterventionModeStop)
	stop.TargetProviderTurnID = ""
	created, _, err := s.CreateWorkflowIntervention(ctx, stop, now.Add(time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// requested is not yet an open stop: the coordinator marks stop_requested explicitly.
	if count, _ := s.CountOpenStopInterventions(ctx, workflowID, taskID); count != 0 {
		t.Fatalf("count while requested = %d, want 0", count)
	}
	inFlight, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateStopRequested,
		domain.InterventionChatFact{}, "", false, "gen-1", created.Revision, now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("mark stop_requested: %v", err)
	}
	if count, _ := s.CountOpenStopInterventions(ctx, workflowID, taskID); count != 1 {
		t.Fatalf("count after stop_requested = %d, want 1", count)
	}
	// Once settled, the guard releases.
	if _, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateStopUnknown,
		domain.InterventionChatFact{}, "stop_receipt_uncertain", false, "gen-1",
		inFlight.Revision, now.Add(3*time.Second),
	); err != nil {
		t.Fatalf("mark stop_unknown: %v", err)
	}
	if count, _ := s.CountOpenStopInterventions(ctx, workflowID, taskID); count != 1 {
		t.Fatalf("an unknown stop must still count as open: count = %d", count)
	}
}

// TestWorkflowInterventionSurvivesSessionDeletion is the M2-03 lesson applied: a session
// delete must succeed and the intervention must not keep naming a dead session.
func TestWorkflowInterventionSurvivesSessionDeletion(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "intervention-delete-project"
	workflowID := domain.WorkflowID("workflow-intervention-delete")
	taskID := domain.WorkflowTaskID("task-intervention-delete")
	seedProject(t, s, projectID)
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"delete-create", strings.Repeat("a", 64), now)

	session := seedSession(t, s, projectID)
	rec, found, err := s.GetSession(ctx, session)
	if err != nil || !found {
		t.Fatalf("get session: found=%v err=%v", found, err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	intervention := interventionFixture(workflowID, taskID, "msg-delete", domain.WorkflowInterventionModeSteer)
	intervention.SessionID = &session
	created, _, err := s.CreateWorkflowIntervention(ctx, intervention, now.Add(time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateQueued,
		domain.InterventionChatFact{ChatTurnID: "turn-1", Queued: true}, "queued", false,
		"gen-1", created.Revision, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("queue receipt: %v", err)
	}

	if _, err := s.DeleteTerminatedSession(ctx, session); err != nil {
		t.Fatalf("DeleteTerminatedSession was blocked by an intervention row: %v", err)
	}

	after, found, err := s.GetWorkflowIntervention(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("get intervention after delete: found=%v err=%v", found, err)
	}
	if after.SessionID != nil {
		t.Fatalf("the intervention still names session %v", after.SessionID)
	}
	// An unsettled intervention becomes unknown: its outcome can no longer be correlated.
	if after.State != domain.WorkflowInterventionStateUnknown {
		t.Fatalf("state = %q, want unknown after the session was deleted", after.State)
	}
	if after.ReasonCode != "session_deleted" {
		t.Fatalf("reason = %q, want the stable session_deleted code", after.ReasonCode)
	}
}

// TestWorkflowInterventionSettledReceiptKeepsStateAcrossDeletion proves history is not
// rewritten: a settled receipt keeps its state, only losing the session reference.
func TestWorkflowInterventionSettledReceiptKeepsStateAcrossDeletion(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "intervention-settled-project"
	workflowID := domain.WorkflowID("workflow-intervention-settled")
	taskID := domain.WorkflowTaskID("task-intervention-settled")
	seedProject(t, s, projectID)
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"settled-create", strings.Repeat("a", 64), now)

	session := seedSession(t, s, projectID)
	rec, _, err := s.GetSession(ctx, session)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	intervention := interventionFixture(workflowID, taskID, "msg-settled", domain.WorkflowInterventionModeSteer)
	intervention.SessionID = &session
	created, _, err := s.CreateWorkflowIntervention(ctx, intervention, now.Add(time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateApplied,
		domain.InterventionChatFact{ChatTurnID: "turn-1", ProviderTurnID: "prov-1"},
		"applied", false, "gen-1", created.Revision, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("applied receipt: %v", err)
	}

	if _, err := s.DeleteTerminatedSession(ctx, session); err != nil {
		t.Fatalf("DeleteTerminatedSession: %v", err)
	}
	after, _, err := s.GetWorkflowIntervention(ctx, created.ID)
	if err != nil {
		t.Fatalf("get intervention: %v", err)
	}
	if after.State != domain.WorkflowInterventionStateApplied {
		t.Fatalf("state = %q, want the settled applied preserved as history", after.State)
	}
	if after.SessionID != nil {
		t.Fatal("the settled intervention still names a deleted session")
	}
}

// TestWorkflowInterventionListAndOrder proves the per-attempt read is ordered and complete.
func TestWorkflowInterventionListAndOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-list")
	taskID := domain.WorkflowTaskID("task-list")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"list-create", strings.Repeat("a", 64), now)

	for i, clientMessageID := range []string{"msg-a", "msg-b", "msg-c"} {
		intervention := interventionFixture(workflowID, taskID, clientMessageID, domain.WorkflowInterventionModeQueue)
		intervention.ID = "intervention-" + clientMessageID
		if _, _, err := s.CreateWorkflowIntervention(
			ctx, intervention, now.Add(time.Duration(i+1)*time.Second),
		); err != nil {
			t.Fatalf("create %s: %v", clientMessageID, err)
		}
	}
	rows, err := s.ListWorkflowInterventionsForAttempt(ctx, workflowID, taskID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	for i, want := range []string{"msg-a", "msg-b", "msg-c"} {
		if rows[i].ClientMessageID != want {
			t.Fatalf("row %d = %q, want %q (creation order)", i, rows[i].ClientMessageID, want)
		}
	}
}

// TestWorkflowInterventionRevisionCAS proves a stale revision loses.
func TestWorkflowInterventionRevisionCAS(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-cas")
	taskID := domain.WorkflowTaskID("task-cas")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"cas-create", strings.Repeat("a", 64), now)

	intervention := interventionFixture(workflowID, taskID, "msg-cas", domain.WorkflowInterventionModeQueue)
	created, _, err := s.CreateWorkflowIntervention(ctx, intervention, now.Add(time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateQueued,
		domain.InterventionChatFact{ChatTurnID: "turn-1", Queued: true}, "queued", false,
		"gen-1", created.Revision, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("first receipt: %v", err)
	}
	// The same base revision is now stale.
	if _, _, err := s.ApplyWorkflowInterventionReceipt(
		ctx, created.ID, domain.WorkflowInterventionStateDelivered,
		domain.InterventionChatFact{ChatTurnID: "turn-1", ChatTurnState: domain.TurnStateRunning},
		"delivered", false, "gen-1", created.Revision, now.Add(3*time.Second),
	); !errors.Is(err, domain.ErrInterventionRevisionConflict) {
		t.Fatalf("err = %v, want ErrInterventionRevisionConflict", err)
	}
}

// TestWorkflowInterventionSessionDeletionIsLegalForEveryModeAndState is the exhaustive form of
// the P0 fix.
//
// The loss trigger rewrites an unsettled intervention to an uncertain state, and that rewrite
// must satisfy the table's own mode↔state CHECK. A mode-blind rewrite breaks it for stop-mode
// rows: 'unknown' is not a legal stop state, so the DELETE itself failed with a raw CHECK
// constraint error and the session became undeletable.
//
// Three spot checks would not have caught it, because the failure depends on which (mode,state)
// pair the trigger happens to touch. This walks every pair the trigger can see.
func TestWorkflowInterventionSessionDeletionIsLegalForEveryModeAndState(t *testing.T) {
	// Every (mode, state) pair the mode↔state CHECK permits.
	pairs := []struct {
		mode  domain.WorkflowInterventionMode
		state domain.WorkflowInterventionState
	}{
		{domain.WorkflowInterventionModeSteer, domain.WorkflowInterventionStateRequested},
		{domain.WorkflowInterventionModeSteer, domain.WorkflowInterventionStateQueued},
		{domain.WorkflowInterventionModeSteer, domain.WorkflowInterventionStateDelivered},
		{domain.WorkflowInterventionModeSteer, domain.WorkflowInterventionStateApplied},
		{domain.WorkflowInterventionModeSteer, domain.WorkflowInterventionStateRejected},
		{domain.WorkflowInterventionModeSteer, domain.WorkflowInterventionStateCancelled},
		{domain.WorkflowInterventionModeSteer, domain.WorkflowInterventionStateUnknown},
		{domain.WorkflowInterventionModeQueue, domain.WorkflowInterventionStateRequested},
		{domain.WorkflowInterventionModeQueue, domain.WorkflowInterventionStateQueued},
		{domain.WorkflowInterventionModeQueue, domain.WorkflowInterventionStateDelivered},
		{domain.WorkflowInterventionModeQueue, domain.WorkflowInterventionStateApplied},
		{domain.WorkflowInterventionModeQueue, domain.WorkflowInterventionStateRejected},
		{domain.WorkflowInterventionModeQueue, domain.WorkflowInterventionStateCancelled},
		{domain.WorkflowInterventionModeQueue, domain.WorkflowInterventionStateUnknown},
		{domain.WorkflowInterventionModeStop, domain.WorkflowInterventionStateRequested},
		{domain.WorkflowInterventionModeStop, domain.WorkflowInterventionStateStopRequested},
		{domain.WorkflowInterventionModeStop, domain.WorkflowInterventionStateStopConfirmed},
		{domain.WorkflowInterventionModeStop, domain.WorkflowInterventionStateStopUnknown},
		// Rejected is shared by all three modes.
		{domain.WorkflowInterventionModeStop, domain.WorkflowInterventionStateRejected},
	}

	// settled states keep their value; everything else becomes the mode's uncertain state.
	settled := map[domain.WorkflowInterventionState]bool{
		domain.WorkflowInterventionStateApplied:       true,
		domain.WorkflowInterventionStateRejected:      true,
		domain.WorkflowInterventionStateCancelled:     true,
		domain.WorkflowInterventionStateStopConfirmed: true,
	}

	for _, pair := range pairs {
		t.Run(string(pair.mode)+"/"+string(pair.state), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			now := workflowStoreTime()
			projectID := "loss-matrix-project"
			workflowID := domain.WorkflowID("workflow-loss-" + string(pair.mode) + "-" + string(pair.state))
			taskID := domain.WorkflowTaskID("task-loss")
			seedProject(t, s, projectID)
			createWorkflowForStoreTest(t, s, workflowID,
				[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
				"loss-create", strings.Repeat("a", 64), now)

			session := seedSession(t, s, projectID)
			rec, found, err := s.GetSession(ctx, session)
			if err != nil || !found {
				t.Fatalf("get session: found=%v err=%v", found, err)
			}
			rec.IsTerminated = true
			if err := s.UpdateSession(ctx, rec); err != nil {
				t.Fatalf("terminate: %v", err)
			}

			intervention := interventionFixture(workflowID, taskID, "msg-loss",
				pair.mode)
			if pair.mode == domain.WorkflowInterventionModeStop {
				intervention.TargetProviderTurnID = ""
			}
			intervention.SessionID = &session
			created, _, err := s.CreateWorkflowIntervention(ctx, intervention, now.Add(time.Second))
			if err != nil {
				t.Fatalf("create intervention: %v", err)
			}
			// Reach the state under test through the legal graph, so the fixture itself proves
			// the pair is reachable rather than only insertable.
			current := created
			for _, step := range pathTo(pair.mode, pair.state) {
				updated, applied, err := s.ApplyWorkflowInterventionReceipt(
					ctx, current.ID, step, licensingFact(step), "test", false,
					"gen-1", current.Revision, now.Add(2*time.Second),
				)
				if err != nil || !applied {
					t.Fatalf("transition to %q: applied=%v err=%v", step, applied, err)
				}
				current = updated
			}
			if current.State != pair.state {
				t.Fatalf("fixture state = %q, want %q", current.State, pair.state)
			}

			// THE ASSERTION: the session must be deletable.
			if _, err := s.DeleteTerminatedSession(ctx, session); err != nil {
				t.Fatalf("DeleteTerminatedSession failed for %s/%s, so the session is pinned: %v",
					pair.mode, pair.state, err)
			}

			// The row survives, satisfies every CHECK (the store read validates), and is no
			// longer live-looking.
			after, found, err := s.GetWorkflowIntervention(ctx, current.ID)
			if err != nil || !found {
				t.Fatalf("get intervention after delete: found=%v err=%v", found, err)
			}
			if after.SessionID != nil {
				t.Fatalf("the intervention still names session %v", after.SessionID)
			}
			if after.ReasonCode != "session_deleted" {
				t.Fatalf("reason = %q, want session_deleted", after.ReasonCode)
			}
			if !after.Mode.AllowsState(after.State) {
				t.Fatalf("post-delete state %q is illegal for mode %q — the trigger produced a state its own CHECK forbids",
					after.State, after.Mode)
			}
			if settled[pair.state] {
				if after.State != pair.state {
					t.Fatalf("settled state %q was rewritten to %q; history must be preserved", pair.state, after.State)
				}
			} else {
				want := domain.WorkflowInterventionStateUnknown
				if pair.mode == domain.WorkflowInterventionModeStop {
					want = domain.WorkflowInterventionStateStopUnknown
				}
				if after.State != want {
					t.Fatalf("unsettled %s/%s became %q, want %q", pair.mode, pair.state, after.State, want)
				}
			}
		})
	}
}

// pathTo returns the sequence of states needed to reach target from requested through the legal
// graph, so the fixture exercises real transitions.
func pathTo(mode domain.WorkflowInterventionMode, target domain.WorkflowInterventionState) []domain.WorkflowInterventionState {
	if mode == domain.WorkflowInterventionModeStop {
		switch target {
		case domain.WorkflowInterventionStateRequested:
			return nil
		case domain.WorkflowInterventionStateRejected:
			return []domain.WorkflowInterventionState{domain.WorkflowInterventionStateRejected}
		case domain.WorkflowInterventionStateStopRequested:
			return []domain.WorkflowInterventionState{domain.WorkflowInterventionStateStopRequested}
		case domain.WorkflowInterventionStateStopUnknown:
			return []domain.WorkflowInterventionState{
				domain.WorkflowInterventionStateStopRequested,
				domain.WorkflowInterventionStateStopUnknown,
			}
		case domain.WorkflowInterventionStateStopConfirmed:
			return []domain.WorkflowInterventionState{
				domain.WorkflowInterventionStateStopRequested,
				domain.WorkflowInterventionStateStopConfirmed,
			}
		}
		return nil
	}
	switch target {
	case domain.WorkflowInterventionStateRequested:
		return nil
	case domain.WorkflowInterventionStateQueued, domain.WorkflowInterventionStateRejected,
		domain.WorkflowInterventionStateCancelled, domain.WorkflowInterventionStateUnknown:
		return []domain.WorkflowInterventionState{target}
	case domain.WorkflowInterventionStateApplied:
		return []domain.WorkflowInterventionState{domain.WorkflowInterventionStateApplied}
	default:
		return []domain.WorkflowInterventionState{target}
	}
}

// licensingFact supplies the chat evidence a receipt state requires, so the fixture can reach
// states that must not be fabricable.
func licensingFact(state domain.WorkflowInterventionState) domain.InterventionChatFact {
	switch state {
	case domain.WorkflowInterventionStateQueued:
		return domain.InterventionChatFact{ChatTurnID: "turn-q", Queued: true}
	case domain.WorkflowInterventionStateDelivered:
		return domain.InterventionChatFact{ChatTurnID: "turn-d", ChatTurnState: domain.TurnStateRunning}
	case domain.WorkflowInterventionStateApplied:
		return domain.InterventionChatFact{ChatTurnID: "turn-a", ProviderTurnID: "prov-a"}
	default:
		return domain.InterventionChatFact{}
	}
}

// TestCreateWorkflowInterventionIfNoOpenStopIsAtomic is the P1 concurrency proof for the
// stop path.
//
// CountOpenStopInterventions followed by CreateWorkflowIntervention is a check-then-act
// sequence: two stops with different client message ids both observe "no open stop" and both
// issue an interrupt. The atomic method must make exactly one of them win even when eight
// callers race, with no migration and no schema change.
func TestCreateWorkflowInterventionIfNoOpenStopIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-atomic")
	taskID := domain.WorkflowTaskID("task-atomic")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"atomic-create", strings.Repeat("a", 64), now)

	const callers = 8
	type result struct {
		index   int
		created bool
		blocked bool
		stored  domain.WorkflowIntervention
		err     error
	}
	results := make([]result, callers)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			intervention := interventionFixture(
				workflowID, taskID, "msg-atomic-"+strconv.Itoa(i), domain.WorkflowInterventionModeStop,
			)
			intervention.TargetProviderTurnID = ""
			stored, created, blocked, err := s.CreateWorkflowInterventionIfNoOpenStop(
				ctx, intervention, now.Add(time.Second),
			)
			results[i] = result{index: i, created: created, blocked: blocked, stored: stored, err: err}
		}()
	}
	close(start)
	wg.Wait()

	var created, blocked int
	var creatorID string
	var blockedID string
	for _, r := range results {
		if r.err != nil {
			t.Fatalf("caller %d: %v", r.index, r.err)
		}
		switch {
		case r.created:
			created++
			creatorID = r.stored.ID
		case r.blocked:
			blocked++
			if blockedID == "" {
				blockedID = r.stored.ID
			}
			if r.stored.ID != blockedID {
				t.Fatalf("caller %d was blocked by %q, others by %q: the blocked row must be one row",
					r.index, r.stored.ID, blockedID)
			}
		default:
			t.Fatalf("caller %d reported neither created nor blocked", r.index)
		}
	}
	if created != 1 {
		t.Fatalf("created = %d, want exactly 1 across %d concurrent stops", created, callers)
	}
	if blocked != callers-1 {
		t.Fatalf("blocked = %d, want %d", blocked, callers-1)
	}
	if blockedID != creatorID {
		t.Fatalf("blocked row %q is not the created row %q", blockedID, creatorID)
	}
	// Exactly one row exists: the losers wrote nothing.
	rows, err := s.ListWorkflowInterventionsForAttempt(ctx, workflowID, taskID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("interventions = %d, want 1", len(rows))
	}
}

// TestCreateWorkflowInterventionIfNoOpenStopReplayAndSettled proves the other two branches:
// a same-key replay is neither created nor blocked, and a settled stop does not block a new
// one.
func TestCreateWorkflowInterventionIfNoOpenStopReplayAndSettled(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-atomic-replay")
	taskID := domain.WorkflowTaskID("task-atomic-replay")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"atomic-replay-create", strings.Repeat("a", 64), now)

	first := interventionFixture(workflowID, taskID, "msg-replay", domain.WorkflowInterventionModeStop)
	first.TargetProviderTurnID = ""
	stored, created, blocked, err := s.CreateWorkflowInterventionIfNoOpenStop(ctx, first, now.Add(time.Second))
	if err != nil || !created || blocked {
		t.Fatalf("first: created=%v blocked=%v err=%v", created, blocked, err)
	}

	// Same key, same fingerprint: a replay, not a block.
	replay, created, blocked, err := s.CreateWorkflowInterventionIfNoOpenStop(ctx, first, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("replay err = %v", err)
	}
	if created || blocked {
		t.Fatalf("replay: created=%v blocked=%v, want neither", created, blocked)
	}
	if replay.ID != stored.ID {
		t.Fatalf("replay returned %q, want %q", replay.ID, stored.ID)
	}

	// Settle the stop, then a differently-keyed stop is allowed.
	if _, applied, err := s.ApplyWorkflowInterventionReceipt(
		ctx, stored.ID, domain.WorkflowInterventionStateStopRequested,
		domain.InterventionChatFact{}, "", false, "gen-1", stored.Revision, now.Add(3*time.Second),
	); err != nil || !applied {
		t.Fatalf("mark in flight: applied=%v err=%v", applied, err)
	}
	settled, applied, err := s.ApplyWorkflowInterventionReceipt(
		ctx, stored.ID, domain.WorkflowInterventionStateStopConfirmed,
		domain.InterventionChatFact{}, "stop_confirmed", false, "gen-1", 2, now.Add(4*time.Second),
	)
	if err != nil || !applied {
		t.Fatalf("settle stop: applied=%v err=%v", applied, err)
	}
	if settled.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("state = %q, want stop_confirmed", settled.State)
	}

	second := interventionFixture(workflowID, taskID, "msg-replay-2", domain.WorkflowInterventionModeStop)
	second.TargetProviderTurnID = ""
	next, created, blocked, err := s.CreateWorkflowInterventionIfNoOpenStop(ctx, second, now.Add(5*time.Second))
	if err != nil {
		t.Fatalf("second stop err = %v", err)
	}
	if !created || blocked {
		t.Fatalf("second stop: created=%v blocked=%v, want created", created, blocked)
	}
	if next.ID == stored.ID {
		t.Fatal("the second stop reused the settled row")
	}

	// Existing data is unaffected: the settled receipt and its state survive untouched.
	unchanged, found, err := s.GetWorkflowIntervention(ctx, stored.ID)
	if err != nil || !found {
		t.Fatalf("get settled: found=%v err=%v", found, err)
	}
	if unchanged.State != domain.WorkflowInterventionStateStopConfirmed ||
		unchanged.OperationSHA != stored.OperationSHA {
		t.Fatalf("the settled row changed: %+v", unchanged)
	}
}

// TestCreateWorkflowInterventionIfNoOpenStopRejectsNonStop proves the guard cannot be
// borrowed to weaken the message paths' own idempotency rules.
func TestCreateWorkflowInterventionIfNoOpenStopRejectsNonStop(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-intervention-atomic-mode")
	taskID := domain.WorkflowTaskID("task-atomic-mode")
	createWorkflowForStoreTest(t, s, workflowID,
		[]domain.WorkflowTask{workflowStoreTask(taskID, workflowID, 1, now)}, nil,
		"atomic-mode-create", strings.Repeat("a", 64), now)

	steer := interventionFixture(workflowID, taskID, "msg-atomic-steer", domain.WorkflowInterventionModeSteer)
	if _, _, _, err := s.CreateWorkflowInterventionIfNoOpenStop(ctx, steer, now.Add(time.Second)); !errors.Is(err, domain.ErrInvalidInterventionMode) {
		t.Fatalf("err = %v, want ErrInvalidInterventionMode", err)
	}
}
