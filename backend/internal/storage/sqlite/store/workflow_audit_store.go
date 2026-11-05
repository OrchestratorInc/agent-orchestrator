package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ principal.AuditRecorder = (*Store)(nil)

// Workflow audit store (durable operator-decision trail).
//
// Import direction: this package imports service/principal, never the other way round —
// principal depends only on internal/domain, so the recorder interface can live beside the
// identity proof that produces its fields without a cycle. That is why the store implements
// AuditRecorder directly instead of taking plain arguments plus an adapter.
//
// Two invariants:
//
//  1. APPEND-ONLY. There is no update and no delete path here, and the queries file has no
//     such statement. A correction is a new row.
//  2. NO SECRETS. Only the credential FINGERPRINT is written; the token itself never reaches
//     this layer (see principal.OperatorTokenFingerprint).

// auditTimeLayout is a FIXED-WIDTH UTC RFC3339 layout.
//
// created_at is TEXT, and the trail is read with ORDER BY created_at. time.RFC3339Nano
// trims trailing zeros from the fraction, which makes "…:00.5Z" sort before "…:00Z" as
// text while being chronologically later. Always writing nine fractional digits keeps
// lexicographic order equal to chronological order.
const auditTimeLayout = "2006-01-02T15:04:05.000000000Z"

// RecordSensitiveDecision appends one audit row.
//
// It validates first: a row that could not be read back (unknown actor kind, missing actor
// ref, no workflow, neither an attempt nor a request id) is refused rather than written as
// an unreadable fact. A primary-key replay inserts nothing and is NOT an error — the
// handler only records applied decisions, and a retry that races the first request must
// converge rather than fail.
func (s *Store) RecordSensitiveDecision(ctx context.Context, record principal.AuditRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	at := record.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.qw.InsertWorkflowAudit(ctx, gen.InsertWorkflowAuditParams{
		ID:               uuid.NewString(),
		ActorKind:        string(record.ActorKind),
		ActorRef:         strings.TrimSpace(record.ActorRef),
		WorkflowID:       string(record.WorkflowID),
		AttemptID:        string(record.AttemptID),
		RequestID:        record.RequestID,
		Decision:         strings.TrimSpace(record.Decision),
		Outcome:          record.Outcome,
		TokenFingerprint: record.TokenFingerprint,
		CreatedAt:        at.UTC().Format(auditTimeLayout),
	})
	if err != nil {
		return fmt.Errorf("record workflow audit: %w", err)
	}
	return nil
}

// AuditEntry is one stored decision, oldest-first when listed.
type AuditEntry struct {
	ID               string
	ActorKind        string
	ActorRef         string
	WorkflowID       string
	AttemptID        string
	RequestID        string
	Decision         string
	Outcome          string
	TokenFingerprint string
	CreatedAt        string
}

// ListWorkflowAudits reads one workflow's trail, oldest first.
//
// It exists for tests and for an operator reading the trail; HTTP exposure is deliberately
// absent — the audit table is not a UI surface in M2.
func (s *Store) ListWorkflowAudits(
	ctx context.Context,
	workflowID string,
	limit, offset int64,
) ([]AuditEntry, error) {
	if strings.TrimSpace(workflowID) == "" {
		return nil, fmt.Errorf("list workflow audits: empty workflow id")
	}
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.qr.ListWorkflowAudits(ctx, gen.ListWorkflowAuditsParams{
		WorkflowID: workflowID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow audits: %w", err)
	}
	out := make([]AuditEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, AuditEntry{
			ID: row.ID, ActorKind: row.ActorKind, ActorRef: row.ActorRef,
			WorkflowID: row.WorkflowID, AttemptID: row.AttemptID, RequestID: row.RequestID,
			Decision: row.Decision, Outcome: row.Outcome,
			TokenFingerprint: row.TokenFingerprint, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}
