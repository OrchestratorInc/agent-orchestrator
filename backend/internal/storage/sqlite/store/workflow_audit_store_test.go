package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// auditFixture is one valid operator decision.
func auditFixture(at time.Time) principal.AuditRecord {
	return principal.AuditRecord{
		ActorKind:        principal.KindOperator,
		ActorRef:         "operator:operator",
		WorkflowID:       domain.WorkflowID("workflow-audit"),
		AttemptID:        domain.WorkflowAttemptID("attempt-1"),
		Decision:         "resolve_attempt",
		Outcome:          "succeeded",
		TokenFingerprint: "0a1b2c3d",
		At:               at,
	}
}

// TestWorkflowAuditAppendsOnce proves a decision is stored exactly once and reads back with
// every field the handler supplied — including the credential FINGERPRINT and never the
// credential.
func TestWorkflowAuditAppendsOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, time.November, 4, 9, 47, 6, 356_000_000, time.UTC)

	record := auditFixture(at)
	if err := st.RecordSensitiveDecision(ctx, record); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := st.ListWorkflowAudits(ctx, string(record.WorkflowID), 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want exactly 1", len(rows))
	}
	got := rows[0]
	if got.ActorKind != string(principal.KindOperator) || got.ActorRef != record.ActorRef {
		t.Fatalf("actor = %s/%s, want the supplied operator", got.ActorKind, got.ActorRef)
	}
	if got.WorkflowID != string(record.WorkflowID) || got.AttemptID != string(record.AttemptID) {
		t.Fatalf("subject = %s/%s, want the supplied attempt", got.WorkflowID, got.AttemptID)
	}
	if got.Decision != record.Decision || got.Outcome != record.Outcome {
		t.Fatalf("decision = %s/%s, want %s/%s",
			got.Decision, got.Outcome, record.Decision, record.Outcome)
	}
	if got.TokenFingerprint != record.TokenFingerprint {
		t.Fatalf("fingerprint = %q, want %q", got.TokenFingerprint, record.TokenFingerprint)
	}
	// The stored timestamp is the UTC instant that was recorded, at full nine-digit precision.
	if got.CreatedAt != "2026-11-04T09:47:06.356000000Z" {
		t.Fatalf("createdAt = %q, want the fixed-width UTC instant", got.CreatedAt)
	}
}

// TestWorkflowAuditRefusesAnUnreadableRecord proves Validate is the guard: a record that could
// not be read back is refused rather than stored as an unreadable fact.
func TestWorkflowAuditRefusesAnUnreadableRecord(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	for name, mutate := range map[string]func(*principal.AuditRecord){
		"unknown_actor_kind": func(r *principal.AuditRecord) { r.ActorKind = "robot" },
		"empty_actor_ref":    func(r *principal.AuditRecord) { r.ActorRef = "  " },
		"empty_decision":     func(r *principal.AuditRecord) { r.Decision = "" },
		"empty_workflow":     func(r *principal.AuditRecord) { r.WorkflowID = "" },
		"no_subject": func(r *principal.AuditRecord) {
			r.AttemptID = ""
			r.RequestID = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			record := auditFixture(time.Now().UTC())
			mutate(&record)
			if err := st.RecordSensitiveDecision(ctx, record); err == nil {
				t.Fatal("an unreadable record was accepted")
			}
		})
	}
	rows, err := st.ListWorkflowAudits(ctx, "workflow-audit", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0: nothing may be stored for a refused record", len(rows))
	}
}

// TestWorkflowAuditIsScopedAndOrdered proves the list is per-workflow and oldest first, which is
// the order an operator reads a trail in.
func TestWorkflowAuditIsScopedAndOrdered(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, time.November, 4, 10, 0, 0, 0, time.UTC)

	// Deliberately written out of order: the list must sort, not preserve insert order.
	for _, offset := range []time.Duration{3 * time.Second, 1 * time.Second, 2 * time.Second} {
		record := auditFixture(base.Add(offset))
		record.AttemptID = domain.WorkflowAttemptID("attempt-" + offset.String())
		if err := st.RecordSensitiveDecision(ctx, record); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	other := auditFixture(base)
	other.WorkflowID = domain.WorkflowID("workflow-other")
	if err := st.RecordSensitiveDecision(ctx, other); err != nil {
		t.Fatalf("record other: %v", err)
	}

	rows, err := st.ListWorkflowAudits(ctx, "workflow-audit", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (another workflow's row must not appear)", len(rows))
	}
	want := []string{
		"2026-11-04T10:00:01.000000000Z",
		"2026-11-04T10:00:02.000000000Z",
		"2026-11-04T10:00:03.000000000Z",
	}
	for i, createdAt := range want {
		if rows[i].CreatedAt != createdAt {
			t.Fatalf("row %d createdAt = %q, want %q", i, rows[i].CreatedAt, createdAt)
		}
	}

	// Paging reads the same trail in the same order.
	page, err := st.ListWorkflowAudits(ctx, "workflow-audit", 1, 1)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if len(page) != 1 || page[0].CreatedAt != want[1] {
		t.Fatalf("page = %+v, want the second row only", page)
	}
}

// TestWorkflowAuditStoresNoCredential is the disclosure guard: a 64-character credential must
// never appear anywhere in the stored trail, however the recorder is called.
func TestWorkflowAuditStoresNoCredential(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	const token = "3f9a0b1c2d3e4f5061728394a5b6c7d8e9f0a1b2c3d4e5f60718293a4b5c6d7e"
	record := auditFixture(time.Now().UTC())
	// Exactly what the handler passes: the fingerprint, never the token.
	record.TokenFingerprint = principal.OperatorTokenFingerprint(token)

	if err := st.RecordSensitiveDecision(ctx, record); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := st.ListWorkflowAudits(ctx, "workflow-audit", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	// Every field of the stored row, flattened, must not contain the credential. A field
	// added later is covered too because the assertion walks the struct's values.
	stored := strings.Join([]string{
		rows[0].ID, rows[0].ActorKind, rows[0].ActorRef, rows[0].WorkflowID,
		rows[0].AttemptID, rows[0].RequestID, rows[0].Decision, rows[0].Outcome,
		rows[0].TokenFingerprint, rows[0].CreatedAt,
	}, "\x00")
	if strings.Contains(stored, token) {
		t.Fatalf("the stored audit row contains the credential: %s", stored)
	}
	if strings.Contains(stored, token[:32]) {
		t.Fatalf("the stored audit row contains half the credential: %s", stored)
	}
	if rows[0].TokenFingerprint != principal.OperatorTokenFingerprint(token) {
		t.Fatalf("fingerprint = %q, want the 8-hex fingerprint", rows[0].TokenFingerprint)
	}
}
