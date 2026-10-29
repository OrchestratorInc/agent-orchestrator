package daemon

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

type usageTestMilestones map[string]bool

func (m usageTestMilestones) Claim(name string) (bool, bool) {
	if m[name] {
		return false, true
	}
	m[name] = true
	return true, true
}

func usageEventsNamed(sink *usageTestSink, name string) []ports.TelemetryEvent {
	var out []ports.TelemetryEvent
	for _, e := range sink.events {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func TestUsageFlusherMessageBatchCarriesSurfacesSteersSendsAndSession(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/private/repo", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "c", domain.ConversationScopeSession, "p", session.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	identity := &usageTestIdentity{}
	store.SetUsageTelemetryAllowed(func() bool { return true })
	if created, err := store.AppendUserMessage(telemetrymeta.WithSurface(ctx, domain.RequestSurfaceMobile), conversation.ID, session.ID, "g",
		domain.ConversationMessage{ID: "m", Text: "private", Origin: domain.MessageOriginHuman, ClientMessageID: "m"}, "t", now); err != nil || !created {
		t.Fatalf("append: %v %v", created, err)
	}
	if err := store.RecordUsageHumanSend(telemetrymeta.WithSurface(ctx, domain.RequestSurfaceDesktop), session.ID, true); err != nil {
		t.Fatal(err)
	}
	sink := &usageTestSink{}
	f := &usageTelemetryFlusher{store: store, sink: sink, identity: identity, log: slog.Default()}
	f.flush(ctx, false)

	batches := usageEventsNamed(sink, "ao.usage.messages_sent")
	if len(batches) != 1 {
		t.Fatalf("batches = %+v", sink.events)
	}
	p := batches[0].Payload
	want := map[string]any{"count": int64(1), "steer_count": int64(1), "send_count": int64(0),
		"mobile_count": int64(1), "desktop_count": int64(1), "cli_count": int64(0), "kind": "orchestrator", "harness": "codex"}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("payload[%s] = %#v, want %#v", k, p[k], v)
		}
	}
	projectTelemetryID, _ := p["project_telemetry_id"].(string)
	if p["session_telemetry_id"] != deliverytelemetry.SessionTelemetryID(projectTelemetryID, string(session.ID)) {
		t.Fatalf("session_telemetry_id = %#v, want the shared derivation", p["session_telemetry_id"])
	}
}

func TestUsageFlusherReportsNewWorkerPROutcomesWithDetail(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/private/repo", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	spawned := now.Add(-2 * time.Hour)
	worker, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, CreatedAt: spawned, UpdatedAt: spawned})
	if err != nil {
		t.Fatal(err)
	}
	store.SetUsageTelemetryAllowed(func() bool { return true })
	identity := &usageTestIdentity{}
	milestones := usageTestMilestones{}
	sink := &usageTestSink{}
	f := &usageTelemetryFlusher{store: store, sink: sink, identity: identity, milestones: milestones, log: slog.Default()}
	opened := now.Add(-time.Hour)
	pr := func(n int, title string) domain.PullRequest {
		return domain.PullRequest{URL: "https://github.com/org/private-repo/pull/" + string(rune('0'+n)), SessionID: worker.ID,
			Provider: "github", Host: "github.com", Repo: "org/private-repo", Number: n, Title: title,
			ProviderID: "PR_" + string(rune('0'+n)), UpdatedAt: now, CreatedAtProvider: opened}
	}
	write := func(p domain.PullRequest) {
		t.Helper()
		if err := store.WriteSCMObservation(ctx, p, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatal(err)
		}
	}

	first := pr(1, "secret title")
	write(first)
	f.flush(ctx, false)
	second := pr(2, "secret title")
	write(second)
	f.flush(ctx, false)

	assoc := usageEventsNamed(sink, "ao.usage.worker_pr_associated")
	if len(assoc) != 2 {
		t.Fatalf("associations = %+v", assoc)
	}
	a := assoc[0].Payload
	if a["count"] != 1 || a["historical"] != false || a["harness"] != "claude-code" || a["scm_provider"] != "github" ||
		a["ms_since_spawn"] != int64(time.Hour/time.Millisecond) || a["first_for_install"] != true {
		t.Fatalf("first association = %#v", a)
	}
	if assoc[1].Payload["first_for_install"] != false {
		t.Fatalf("second association first_for_install = %#v", assoc[1].Payload["first_for_install"])
	}

	// AO merges the first from a phone; the second merges on GitHub, then a
	// third PR is closed unmerged.
	if err := store.RecordWorkerPRMergedVia(ctx, first.URL, deliverytelemetry.MergedViaMobileSwipe); err != nil {
		t.Fatal(err)
	}
	first.Merged, first.MergedAtProvider = true, now
	write(first)
	second.Merged, second.MergedAtProvider = true, now
	write(second)
	third := pr(3, "secret title")
	third.Closed, third.ClosedAtProvider = true, now
	write(third)
	f.flush(ctx, false)

	merges := usageEventsNamed(sink, "ao.usage.worker_pr_merged")
	if len(merges) != 2 {
		t.Fatalf("merges = %+v", merges)
	}
	via := map[any]map[string]any{}
	for _, m := range merges {
		via[m.Payload["merged_via"]] = m.Payload
	}
	if via["mobile_swipe"] == nil || via["github"] == nil {
		t.Fatalf("merged_via = %#v", via)
	}
	m := via["mobile_swipe"]
	if m["ms_since_open"] != int64(time.Hour/time.Millisecond) || m["ms_since_spawn"] != int64(2*time.Hour/time.Millisecond) ||
		m["review_rounds"] != int64(0) || m["ao_review_used"] != false || m["first_for_install"] != true {
		t.Fatalf("merge detail = %#v", m)
	}
	if via["github"]["first_for_install"] != false {
		t.Fatalf("second merge first_for_install = %#v", via["github"]["first_for_install"])
	}
	closed := usageEventsNamed(sink, "ao.usage.worker_pr_closed")
	if len(closed) != 1 || closed[0].Payload["historical"] != false {
		t.Fatalf("closed = %+v", closed)
	}
	if _, ok := closed[0].Payload["merged_via"]; ok {
		t.Fatalf("closed carries merged_via: %#v", closed[0].Payload)
	}
	for _, e := range sink.events {
		for k, v := range e.Payload {
			if s, ok := v.(string); ok && (s == "secret title" || s == first.URL || s == "org/private-repo") {
				t.Fatalf("%s payload leaks %s=%q", e.Name, k, s)
			}
		}
	}

	before := len(sink.events)
	f.flush(ctx, false)
	if len(sink.events) != before {
		t.Fatalf("outcomes reported twice: %+v", sink.events[before:])
	}
}
