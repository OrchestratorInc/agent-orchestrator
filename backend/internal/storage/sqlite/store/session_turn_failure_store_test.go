package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestListTurnFailureCandidatesFiltersInSQL(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	lastAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	var want []domain.SessionID
	for i, change := range []func(*domain.SessionRecord){
		func(*domain.SessionRecord) {},
		func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityWaitingInput },
		// Esc at a permission prompt ends the turn without a hook.
		func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityBlocked },
		func(r *domain.SessionRecord) { r.Harness = domain.HarnessClaudeCode },
		func(r *domain.SessionRecord) { r.Mode = domain.SessionModeChat },
		func(r *domain.SessionRecord) { r.IsTerminated = true },
		func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityIdle },
		func(r *domain.SessionRecord) { r.Metadata.NativeTranscriptPath = "" },
	} {
		rec := sampleRecord("mer")
		rec.Harness, rec.Mode = domain.HarnessCodex, domain.SessionModeTUI
		rec.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: lastAt}
		rec.Metadata.RuntimeLaunchID = "launch-1"
		rec.Metadata.NativeTranscriptPath = "/rollouts/rollout-1.jsonl"
		rec.Metadata.LatestUserPromptAt = lastAt.Add(time.Second)
		change(&rec)
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			want = append(want, created.ID)
		}
	}
	slices.Sort(want)
	got, err := s.ListTurnFailureCandidates(ctx, domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]domain.SessionID, 0, len(got))
	for _, candidate := range got {
		ids = append(ids, candidate.ID)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("candidates = %v, want %v", ids, want)
	}
	current, ok, err := s.GetSession(ctx, got[0].ID)
	if err != nil || !ok {
		t.Fatalf("get session = %v, %v", ok, err)
	}
	if first := got[0]; first.Harness != domain.HarnessCodex || first.Activity.State != current.Activity.State ||
		!first.Activity.LastActivityAt.Equal(lastAt) || !first.LatestUserPromptAt.Equal(lastAt.Add(time.Second)) ||
		first.Revision != current.Revision ||
		first.RuntimeLaunchID != "launch-1" || first.NativeTranscriptPath != "/rollouts/rollout-1.jsonl" {
		t.Fatalf("candidate = %+v, session = %+v", first, current)
	}
	if got, err := s.ListTurnFailureCandidates(ctx, domain.HarnessClaudeCode); err != nil || len(got) != 1 {
		t.Fatalf("claude candidates = %v, %v; want the one claude session", got, err)
	}
}
