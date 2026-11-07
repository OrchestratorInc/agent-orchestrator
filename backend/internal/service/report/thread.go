package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ThreadStore is the persistence boundary that lets an ingested report appear
// in the active orchestrator's chat thread at report time.
type ThreadStore interface {
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	UpsertActivity(context.Context, string, string, domain.ConversationActivity, time.Time) error
}

// threadAction is the ao_action action name a report thread row carries. The
// renderer routes it to the same quiet worker row a delivered batch draws.
const threadAction = "worker.report"

// ThreadActivityID is the provider item id of one report's thread row.
func ThreadActivityID(reportID string) string {
	return "ao-action:" + threadAction + ":" + reportID
}

// ConversationThread writes one ao_action timeline row per ingested report into the
// project's active Chat orchestrator conversation, so an open thread renders
// the report live through the ordinary CDC pipeline instead of waiting for the
// next delivery or piggyback flush. Best effort by contract: it skips what it
// cannot show and never fails the report.
type ConversationThread struct {
	store  ThreadStore
	now    func() time.Time
	logger *slog.Logger
}

// NewConversationThread constructs a recorder over the shared store.
func NewConversationThread(store ThreadStore) *ConversationThread {
	return &ConversationThread{store: store, now: time.Now}
}

// RecordReportThreadEntry shows one ingested report in the orchestrator thread.
func (r *ConversationThread) RecordReportThreadEntry(ctx context.Context, rec domain.ReportRecord) {
	if r == nil || r.store == nil || rec.ID == "" || rec.SessionID == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	orchestrator, ok := r.activeChatOrchestrator(ctx, rec.ProjectID)
	if !ok {
		return
	}
	displayName := ""
	if worker, found, err := r.store.GetSession(ctx, rec.SessionID); err != nil {
		r.warn("report thread row: worker lookup failed", err, "worker", rec.SessionID)
	} else if found {
		displayName = strings.TrimSpace(worker.DisplayName)
	}
	conversation, err := r.store.ConversationForSession(ctx, orchestrator.ID)
	if err != nil {
		if !errors.Is(err, domain.ErrNoConversation) {
			r.warn("report thread row: conversation lookup failed", err, "orchestrator", orchestrator.ID)
		}
		return
	}
	detail, err := json.Marshal(threadDetail{
		Action:          threadAction,
		OperationID:     rec.ID,
		TargetSessionID: string(rec.SessionID),
		ProjectID:       string(rec.ProjectID),
		DisplayName:     displayName,
		Href:            threadSessionHref(rec.ProjectID, rec.SessionID),
		State:           threadStateLabel(rec.State),
		Text:            reportText(rec),
		Outputs:         threadOutputs(rec.Outputs),
	})
	if err != nil {
		r.warn("report thread row: detail encode failed", err, "report", rec.ID)
		return
	}
	activity := domain.ConversationActivity{
		ID:             ThreadActivityID(rec.ID),
		Kind:           domain.ActivityKindAOAction,
		Status:         domain.ActivityStatusCompleted,
		Summary:        "Worker report",
		Detail:         detail,
		ProviderItemID: ThreadActivityID(rec.ID),
	}
	if err := r.store.UpsertActivity(ctx, conversation.ID, "", activity, r.now().UTC()); err != nil {
		r.warn("report thread row: write failed", err, "report", rec.ID, "conversation", conversation.ID)
	}
}

// activeChatOrchestrator mirrors the delivery coordinator's orchestrator
// selection, narrowed to Chat: only a Chat orchestrator owns a conversation
// projection the thread row can land in.
func (r *ConversationThread) activeChatOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionRecord, bool) {
	if projectID == "" {
		return domain.SessionRecord{}, false
	}
	recs, err := r.store.ListSessions(ctx, projectID)
	if err != nil {
		r.warn("report thread row: session list failed", err, "project", projectID)
		return domain.SessionRecord{}, false
	}
	var selected domain.SessionRecord
	for _, rec := range recs {
		if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
			continue
		}
		if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
			continue
		}
		if selected.ID == "" || rec.CreatedAt.After(selected.CreatedAt) {
			selected = rec
		}
	}
	return selected, selected.ID != ""
}

func (r *ConversationThread) warn(message string, err error, args ...any) {
	if r.logger == nil {
		return
	}
	r.logger.Warn(message, append(args, "err", err)...)
}

// threadDetail mirrors the fields the renderer needs to draw the same quiet
// worker row a delivered batch draws, and to recognize its own later copy of
// the same report in a batch or piggyback envelope.
type threadDetail struct {
	Action          string              `json:"action"`
	OperationID     string              `json:"operationId"`
	TargetSessionID string              `json:"targetSessionId,omitempty"`
	ProjectID       string              `json:"projectId,omitempty"`
	DisplayName     string              `json:"displayName,omitempty"`
	Href            string              `json:"href,omitempty"`
	State           string              `json:"state,omitempty"`
	Text            string              `json:"text,omitempty"`
	Outputs         []threadOutputValue `json:"outputs,omitempty"`
}

type threadOutputValue struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
}

// threadStateLabel is the state label PreparedBatch.Render writes for an
// entry, which the renderer's report parser also reads.
func threadStateLabel(state domain.ReportState) string {
	if state == "" {
		return "information"
	}
	return string(state)
}

// reportText is the body PreparedBatch.Render writes for an entry: the note,
// or the free-form message when there is no note.
func reportText(rec domain.ReportRecord) string {
	if rec.Note != "" {
		return rec.Note
	}
	return rec.Message
}

func threadOutputs(outputs []domain.ReportOutput) []threadOutputValue {
	if len(outputs) == 0 {
		return nil
	}
	values := make([]threadOutputValue, 0, len(outputs))
	for _, output := range outputs {
		values = append(values, threadOutputValue{Kind: string(output.Kind), Reference: output.Reference})
	}
	return values
}

// threadSessionHref is the markdown attribution link a report entry shows.
func threadSessionHref(project domain.ProjectID, id domain.SessionID) string {
	if project == "" || id == "" {
		return ""
	}
	return fmt.Sprintf("ao://sessions/%s/%s", url.PathEscape(string(project)), url.PathEscape(string(id)))
}
