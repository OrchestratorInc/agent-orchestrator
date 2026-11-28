package automation

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apierr"
)

func scratchpadInput() CreateInput {
	return CreateInput{
		DisplayName: "Notes digest", Prompt: "Summarize my notes",
		Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RRule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0", Timezone: "UTC",
	}
}

// A Scratchpad automation names no project, so it is created without a project
// row; a named project that does not exist is still refused.
func TestCreateScratchpadAutomationNeedsNoProject(t *testing.T) {
	store := newFakeStore()
	svc := New(Deps{Store: store, NewID: func() string { return "scratch" }})

	got, err := svc.Create(context.Background(), scratchpadInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ProjectID != "" || store.automations[got.ID].ProjectID != "" || got.Harness != domain.HarnessCodex {
		t.Fatalf("created = %#v, want a Scratchpad automation on codex", got)
	}

	missing := scratchpadInput()
	missing.ProjectID = "missing"
	if _, err := svc.Create(context.Background(), missing); !isAPIError(err, apierr.KindNotFound) {
		t.Fatalf("unknown project error = %v, want not found", err)
	}
}

// Each run starts a standalone session, which must be a worker with a harness,
// so a Scratchpad automation is held to the same rules when created or edited.
func TestScratchpadAutomationKeepsStandaloneSessionRules(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := New(Deps{Store: store, NewID: func() string { return "scratch" }})

	noHarness := scratchpadInput()
	noHarness.Harness = ""
	if _, err := svc.Create(ctx, noHarness); !isAPIError(err, apierr.KindInvalid) {
		t.Fatalf("create without harness error = %v, want invalid", err)
	}
	orchestrator := scratchpadInput()
	orchestrator.Kind = domain.KindOrchestrator
	if _, err := svc.Create(ctx, orchestrator); !isAPIError(err, apierr.KindInvalid) {
		t.Fatalf("create as orchestrator error = %v, want invalid", err)
	}

	created, err := svc.Create(ctx, scratchpadInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	empty := domain.AgentHarness("")
	if _, err := svc.Update(ctx, created.ID, UpdateInput{Harness: &empty}); !isAPIError(err, apierr.KindInvalid) {
		t.Fatalf("clearing harness error = %v, want invalid", err)
	}
	kind := domain.KindOrchestrator
	if _, err := svc.Update(ctx, created.ID, UpdateInput{Kind: &kind}); !isAPIError(err, apierr.KindInvalid) {
		t.Fatalf("orchestrator edit error = %v, want invalid", err)
	}
	name := "Renamed digest"
	renamed, err := svc.Update(ctx, created.ID, UpdateInput{DisplayName: &name})
	if err != nil || renamed.DisplayName != name || renamed.ProjectID != "" {
		t.Fatalf("rename = %#v, %v", renamed, err)
	}
}

// The scheduler hands a Scratchpad run to the spawner with no project, which
// starts it as a standalone worker session on the automation's harness.
func TestTickStartsScratchpadRunWithoutProject(t *testing.T) {
	base := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", DisplayName: "Notes digest", Prompt: "Summarize", Kind: domain.KindWorker, Harness: domain.HarnessCodex, RRuleText: "DTSTART:20260825T090000Z\nRRULE:FREQ=HOURLY", Timezone: "UTC", Enabled: true, NextRunAt: base}
	spawner := &recordingSpawner{store: store}
	sequence := 0
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return base }, NewID: func() string { sequence++; return string(rune('a' + sequence - 1)) }})

	if err := svc.Tick(context.Background(), base); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(spawner.calls) != 1 {
		t.Fatalf("spawns = %d, want 1", len(spawner.calls))
	}
	if got := spawner.calls[0]; got.ProjectID != "" || got.Kind != domain.KindWorker || got.Harness != domain.HarnessCodex {
		t.Fatalf("spawn config = %#v, want a standalone codex worker", got)
	}
}
