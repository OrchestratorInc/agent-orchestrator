package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAutomationChatCompletionRequiresSuccessfulSettledWork(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		state                        domain.TurnState
		activity                     domain.ActivityState
		queued, openPR, orchestrator bool
		reportState                  domain.ReportState
		want                         bool
	}{
		{name: "reported needs input", state: domain.TurnStateCompleted, activity: domain.ActivityIdle, reportState: domain.ReportNeedsInput},
		{name: "reported stuck", state: domain.TurnStateCompleted, activity: domain.ActivityIdle, reportState: domain.ReportStuck},
		{name: "success", state: domain.TurnStateCompleted, activity: domain.ActivityIdle, want: true},
		{name: "running", state: domain.TurnStateRunning, activity: domain.ActivityIdle},
		{name: "failed", state: domain.TurnStateFailed, activity: domain.ActivityIdle},
		{name: "interrupted", state: domain.TurnStateInterrupted, activity: domain.ActivityIdle},
		{name: "recovered", state: domain.TurnStateRecovered, activity: domain.ActivityIdle},
		{name: "queued followup", state: domain.TurnStateCompleted, activity: domain.ActivityIdle, queued: true},
		{name: "open PR", state: domain.TurnStateCompleted, activity: domain.ActivityIdle, openPR: true},
		{name: "approval", state: domain.TurnStateCompleted, activity: domain.ActivityBlocked},
		{name: "waiting input", state: domain.TurnStateCompleted, activity: domain.ActivityWaitingInput},
		{name: "active", state: domain.TurnStateCompleted, activity: domain.ActivityActive},
		{name: "orchestrator", state: domain.TurnStateCompleted, activity: domain.ActivityIdle, orchestrator: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, session, conversation := seededChatConversation(t)
			session.Kind = domain.KindWorker
			if tc.orchestrator {
				session.Kind = domain.KindOrchestrator
			}
			session.Activity.State = tc.activity
			if err := s.UpdateSession(ctx, session); err != nil {
				t.Fatal(err)
			}
			ready, err := s.AutomationSessionReadyToComplete(ctx, session.ID, testNow)
			if err != nil || ready {
				t.Fatalf("empty conversation ready=%v err=%v", ready, err)
			}
			if tc.state != domain.TurnStateCompleted {
				appendBranchPrompt(t, s, session, conversation, "generation-root", "previous", "Earlier work")
				if err := s.SettleTurnByID(ctx, "turn-previous", domain.TurnStateCompleted, "", testNow); err != nil {
					t.Fatal(err)
				}
			}
			appendBranchPrompt(t, s, session, conversation, "generation-root", "a", "Do the scheduled work")
			if tc.state == domain.TurnStateRunning {
				if err := s.BindTurnToProvider(ctx, "turn-a", "provider-a", testNow); err != nil {
					t.Fatal(err)
				}
			} else if err := s.SettleTurnByID(ctx, "turn-a", tc.state, "", testNow); err != nil {
				t.Fatal(err)
			}
			if tc.reportState != "" {
				if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: "blocked-report", SessionID: session.ID, ProjectID: session.ProjectID, State: tc.reportState, Note: "Need help", CreatedAt: testNow, AvailableAt: testNow, DeliveryState: domain.ReportPending, RepeatCount: 1}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.queued {
				appendBranchPrompt(t, s, session, conversation, "generation-root", "b", "Followup")
			}
			if tc.openPR {
				if err := s.WriteSCMObservation(ctx, domain.PullRequest{URL: "https://github.com/o/r/pull/1", SessionID: session.ID, Number: 1, UpdatedAt: testNow}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
					t.Fatal(err)
				}
			}
			ready, err = s.AutomationSessionReadyToComplete(ctx, session.ID, testNow.Add(time.Minute))
			if err != nil || ready != tc.want {
				t.Fatalf("ready=%v want=%v err=%v", ready, tc.want, err)
			}
		})
	}
}

func TestAutomationTerminalCompletionRequiresCurrentSettledDoneReport(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "scheduled")
	rec := sampleRecord("scheduled")
	rec.Kind, rec.Mode, rec.Activity.State = domain.KindWorker, domain.SessionModeTUI, domain.ActivityIdle
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	now := session.CreatedAt.Add(time.Hour)
	check := func(at time.Time, want bool) {
		t.Helper()
		ready, err := s.AutomationSessionReadyToComplete(ctx, session.ID, at)
		if err != nil || ready != want {
			t.Fatalf("ready=%v want=%v err=%v", ready, want, err)
		}
	}
	check(now, false)
	report := domain.ReportRecord{ID: "done", DeliveryState: domain.ReportPending, SessionID: session.ID, ProjectID: session.ProjectID, State: domain.ReportDone, Note: "finished", CreatedAt: now, AvailableAt: now.Add(domain.ReportSettlementWindow), SettlementDeadline: now.Add(domain.ReportSettlementWindow), RepeatCount: 1}
	if _, err := s.CreateReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	check(now, false)
	check(now.Add(domain.ReportSettlementWindow), true)
	session.Metadata.LatestUserPromptAt = now.Add(time.Minute)
	if err := s.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	check(now.Add(time.Hour), false)
}

func TestAutomationPendingCoalescingPreservesOtherDefinitionsAndActiveRuns(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "scheduled")
	now := time.Date(2026, 10, 27, 3, 30, 0, 0, time.UTC)
	for _, id := range []domain.AutomationID{"a", "b"} {
		if _, err := s.CreateAutomation(ctx, domain.Automation{ID: id, ProjectID: "scheduled", DisplayName: "Daily", Prompt: "Review", Kind: domain.KindWorker, RRuleText: "FREQ=DAILY", Timezone: "UTC", Enabled: true, NextRunAt: now.Add(24 * time.Hour), CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []domain.AutomationRun{
		{ID: "active", AutomationID: "a", Status: domain.AutomationRunRunning, ScheduledFor: now.Add(-48 * time.Hour)},
		{ID: "old", AutomationID: "a", Status: domain.AutomationRunPending, ScheduledFor: now.Add(-24 * time.Hour)},
		{ID: "latest", AutomationID: "a", Status: domain.AutomationRunPending, ScheduledFor: now},
		{ID: "other", AutomationID: "b", Status: domain.AutomationRunPending, ScheduledFor: now.Add(-24 * time.Hour)},
	} {
		run.CreatedAt, run.UpdatedAt = now, now
		if _, _, err := s.CreateAutomationRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	claimed, ok, err := s.ClaimNextAutomationRun(ctx, now, now.Add(time.Minute))
	if err != nil || !ok || claimed.ID != "other" {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	for id, want := range map[domain.AutomationRunID]domain.AutomationRunStatus{"active": domain.AutomationRunRunning, "old": domain.AutomationRunFailed, "latest": domain.AutomationRunPending} {
		run, ok, err := s.GetAutomationRun(ctx, id)
		if err != nil || !ok || run.Status != want {
			t.Fatalf("%s=%+v err=%v", id, run, err)
		}
	}
}
