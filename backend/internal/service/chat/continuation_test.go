package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPrepareContinuationReadFailureDoesNotSendWithoutContext(t *testing.T) {
	readError := errors.New("conversation unavailable")
	controller := Controller{continuationReader: SnapshotReaderFunc(func(context.Context, string) (ConversationRows, error) {
		return ConversationRows{}, readError
	})}
	message := ports.ChatUserMessage{Text: "Continue from where you stopped.", Continuation: true}
	got, err := controller.prepareContinuation(context.Background(), message)
	if !errors.Is(err, readError) || got.Text != message.Text {
		t.Fatalf("unavailable context should return the read error without changing the request: %#v, %v", got, err)
	}
}

func continuationRows() ConversationRows {
	return ConversationRows{
		Turns: []domain.ConversationTurn{
			{ID: "old", State: domain.TurnStateCompleted, ProviderTurnID: "p-old"},
			{ID: "task", State: domain.TurnStateInterrupted, ProviderTurnID: "p-task"},
		},
		Messages: []domain.ConversationMessage{
			{ID: "old-user", TurnID: "old", Sequence: 1, Role: domain.MessageRoleUser, Text: "An unrelated old task"},
			{ID: "old-answer", TurnID: "old", Sequence: 2, Role: domain.MessageRoleAssistant, Text: "Unrelated old answer"},
			{ID: "task-user", TurnID: "task", Sequence: 3, Role: domain.MessageRoleUser, Text: "Write 30 AO tips without tools; end with TASK_DONE"},
			{ID: "task-answer", TurnID: "task", Sequence: 4, Role: domain.MessageRoleAssistant, Text: "1. Use separate worktrees.\n2. Keep the"},
		},
	}
}

func TestStoppedContinuationContextUsesCurrentTask(t *testing.T) {
	rows := continuationRows()
	reminder := stoppedContinuationContext(rows)
	for _, want := range []string{rows.Messages[2].Text, "Keep the", "avoid repeating completed work", "check current state"} {
		if !strings.Contains(reminder, want) {
			t.Fatalf("context omits %q: %s", want, reminder)
		}
	}
	if strings.Contains(reminder, "unrelated") || strings.Contains(reminder, "Unrelated") {
		t.Fatalf("context includes an older task: %s", reminder)
	}
}

func TestStoppedContinuationContextRepeatedStopsKeepOriginalRequest(t *testing.T) {
	rows := continuationRows()
	rows.Turns = append(rows.Turns, domain.ConversationTurn{ID: "continued", State: domain.TurnStateInterrupted, ProviderTurnID: "p-continued"})
	rows.Messages = append(rows.Messages,
		domain.ConversationMessage{ID: "continued-user", TurnID: "continued", Sequence: 5, Role: domain.MessageRoleUser, Text: "PREVIOUS_ENRICHED_CONTINUE_CONTEXT", Continuation: true},
		domain.ConversationMessage{ID: "continued-answer", TurnID: "continued", Sequence: 6, Role: domain.MessageRoleAssistant, Text: "3. Keep each branch"},
	)
	reminder := stoppedContinuationContext(rows)
	if !strings.Contains(reminder, rows.Messages[2].Text) || !strings.Contains(reminder, "Keep each branch") ||
		strings.Contains(reminder, "PREVIOUS_ENRICHED_CONTINUE_CONTEXT") {
		t.Fatalf("repeated continue lost its original task or nested old context: %s", reminder)
	}
}

func TestStoppedContinuationContextIgnoresWithdrawnWork(t *testing.T) {
	rows := continuationRows()
	rolledBack := time.Now()
	rows.Turns = append(rows.Turns,
		domain.ConversationTurn{ID: "queued", State: domain.TurnStateInterrupted},
		domain.ConversationTurn{ID: "cancelled", State: domain.TurnStateCancelled},
		domain.ConversationTurn{ID: "undone", State: domain.TurnStateCompleted, ProviderTurnID: "p-undone", RolledBackAt: &rolledBack},
	)
	rows.Messages = append(rows.Messages,
		domain.ConversationMessage{ID: "undone-message", TurnID: "undone", Sequence: 7, Role: domain.MessageRoleAssistant, Text: "UNDONE_RESPONSE"},
		domain.ConversationMessage{ID: "queued-message", TurnID: "queued", Sequence: 8, Role: domain.MessageRoleUser, Text: "UNDISPATCHED_TASK"},
		domain.ConversationMessage{ID: "late-old-answer", TurnID: "old", Sequence: 9, Role: domain.MessageRoleAssistant, Text: "LATE_OLD_RESPONSE"},
	)
	reminder := stoppedContinuationContext(rows)
	if reminder == "" || strings.Contains(reminder, "UNDONE_RESPONSE") || strings.Contains(reminder, "LATE_OLD_RESPONSE") || strings.Contains(reminder, "UNDISPATCHED_TASK") {
		t.Fatalf("withdrawn work changed continuation context: %s", reminder)
	}
}

func TestStoppedContinuationContextDoesNotResurrectOlderStop(t *testing.T) {
	for _, state := range []domain.TurnState{domain.TurnStateCompleted, domain.TurnStateFailed, domain.TurnStateRunning, domain.TurnStateQueued} {
		t.Run(string(state), func(t *testing.T) {
			rows := continuationRows()
			rows.Turns = append(rows.Turns, domain.ConversationTurn{ID: "new", State: state, ProviderTurnID: "p-new"})
			if got := stoppedContinuationContext(rows); got != "" {
				t.Fatalf("new %s task resurrected older stop: %s", state, got)
			}
		})
	}
}

func TestStoppedContinuationContextBeforeOutput(t *testing.T) {
	rows := continuationRows()
	rows.Messages = rows.Messages[:3]
	reminder := stoppedContinuationContext(rows)
	if !strings.Contains(reminder, "TASK_DONE") || strings.Contains(reminder, "partialResponseTail") {
		t.Fatalf("stop before output should retain the request without inventing a response: %s", reminder)
	}
}

func TestStoppedContinuationContextBoundsUnicodeAndOmitsPrivateActivity(t *testing.T) {
	rows := continuationRows()
	rows.Messages[2].Text = "REQUEST_START" + strings.Repeat("界", 20000) + "REQUEST_END"
	rows.Messages[3].Text = "RESPONSE_START" + strings.Repeat("界", 20000) + "RESPONSE_END"
	rows.Activities = []domain.ConversationActivity{
		{TurnID: "task", Kind: domain.ActivityKindReasoning, Summary: "PRIVATE_REASONING"},
		{TurnID: "task", Kind: domain.ActivityKindCommand, Summary: "COMMAND_TO_REPLAY"},
	}
	reminder := stoppedContinuationContext(rows)
	for _, want := range []string{"REQUEST_START", "REQUEST_END", "RESPONSE_END", "requestTruncated", "responseTailTruncated"} {
		if !strings.Contains(reminder, want) {
			t.Fatalf("bounded context omits %q", want)
		}
	}
	if !utf8.ValidString(reminder) || len(reminder) > 20*1024 {
		t.Fatalf("context is invalid UTF-8 or oversized: %d bytes", len(reminder))
	}
	for _, unwanted := range []string{"RESPONSE_START", "PRIVATE_REASONING", "COMMAND_TO_REPLAY"} {
		if strings.Contains(reminder, unwanted) {
			t.Fatalf("context retained %q", unwanted)
		}
	}
}

func TestStoppedContinuationContextIncludesOnlyAttachmentReferences(t *testing.T) {
	rows := continuationRows()
	rows.Messages[2].DeliveryContentJSON = `[{"type":"image","mimeType":"image/png","name":"diagram.png","data":"IMAGE_BYTES"},{"type":"resource","uri":"file:///spec.md","name":"spec.md","text":"RESOURCE_BODY"},{"type":"resource","internal":true,"uri":"ao://conversation/edit-replay","text":"OLD_REPLAY_SEED"}]`
	reminder := stoppedContinuationContext(rows)
	for _, want := range []string{"diagram.png", "spec.md", "file:///spec.md"} {
		if !strings.Contains(reminder, want) {
			t.Fatalf("context omits attachment reference %q", want)
		}
	}
	for _, unwanted := range []string{"IMAGE_BYTES", "RESOURCE_BODY", "OLD_REPLAY_SEED", "edit-replay"} {
		if strings.Contains(reminder, unwanted) {
			t.Fatalf("context copied %q", unwanted)
		}
	}
}

func TestStoppedContinuationContextBoundsAttachmentMetadata(t *testing.T) {
	rows := continuationRows()
	metadata := strings.Repeat("界", 20000)
	references := make([]map[string]string, 20)
	for i := range references {
		references[i] = map[string]string{"type": metadata, "name": metadata, "uri": metadata, "mimeType": metadata}
	}
	encoded, err := json.Marshal(references)
	if err != nil {
		t.Fatal(err)
	}
	rows.Messages[2].DeliveryContentJSON = string(encoded)
	reminder := stoppedContinuationContext(rows)
	if !utf8.ValidString(reminder) || len(reminder) > 20*1024 {
		t.Fatalf("attachment metadata made the reminder invalid or oversized: %d bytes", len(reminder))
	}
}

func TestContinuableTurnIDSurvivesProviderScopeChange(t *testing.T) {
	rows := continuationRows()
	started := time.Now()
	rows.Turns[1].StartedAt, rows.Turns[1].ProviderTurnID = &started, ""
	if ContinuableTurnID(rows.Turns) != "task" || !strings.Contains(stoppedContinuationContext(rows), "TASK_DONE") {
		t.Fatal("provider scope change lost the dispatched stopped task")
	}
	rows.Turns[1].State = domain.TurnStateCompleted
	rows.Turns = append(rows.Turns, domain.ConversationTurn{ID: "swept", State: domain.TurnStateInterrupted})
	if ContinuableTurnID(rows.Turns) != "" || stoppedContinuationContext(rows) != "" {
		t.Fatal("a swept queue resurrected completed work")
	}
}

func TestStoppedContinuationContextPreservesVerifiedExcerpt(t *testing.T) {
	rows := continuationRows()
	rows.Messages[2].Text = "Fix this"
	rows.Messages[2].DeliveryContentJSON = `[{"type":"excerpt","excerpt":{"selectedText":"SELECTED_CODE","userMessage":"PAIRED_REQUEST","assistantMessage":"PAIRED_RESPONSE"}}]`
	reminder := stoppedContinuationContext(rows)
	for _, want := range []string{"SELECTED_CODE", "PAIRED_REQUEST", "PAIRED_RESPONSE"} {
		if !strings.Contains(reminder, want) {
			t.Fatalf("missing excerpt context %q", want)
		}
	}
}

func TestPrepareContinuationUsesBoundedPageAndKeepsVisibleText(t *testing.T) {
	fullRead := false
	pages := 0
	controller := Controller{
		continuationReader: SnapshotReaderFunc(func(context.Context, string) (ConversationRows, error) {
			fullRead = true
			return ConversationRows{}, errors.New("full read forbidden")
		}),
		continuationPageReader: SnapshotPageReaderFunc(func(_ context.Context, _ string, before, limit int64) (ConversationRows, error) {
			pages++
			if before != 0 || limit != 128 {
				t.Fatalf("unbounded page: %d/%d", before, limit)
			}
			return continuationRows(), nil
		}),
	}
	request := ports.ChatUserMessage{Text: "Continue from where you stopped.", Continuation: true}
	got, err := controller.prepareContinuation(context.Background(), request)
	if err != nil || fullRead || pages != 1 || got.Text != request.Text || len(got.Content) != 1 || !got.Content[0].Internal {
		t.Fatalf("provider context leaked or full history loaded: %#v, %v", got, err)
	}
	delivered := excerptDeliveryMessage(got)
	if len(delivered.Content) != 0 || !strings.Contains(delivered.Text, "TASK_DONE") {
		t.Fatal("text-only provider lost context")
	}
}

func TestPrepareContinuationBoundsMissingHistoryReads(t *testing.T) {
	pages := 0
	controller := Controller{continuationPageReader: SnapshotPageReaderFunc(func(context.Context, string, int64, int64) (ConversationRows, error) {
		pages++
		rows := continuationRows()
		rows.Messages = nil
		rows.HasMoreBefore, rows.OldestSequence = true, int64(1000-pages*128)
		return rows, nil
	})}
	if _, err := controller.prepareContinuation(context.Background(), ports.ChatUserMessage{Text: "Continue", Continuation: true}); err == nil || pages != 1 {
		t.Fatalf("unbounded or silently context-free continuation: pages=%d err=%v", pages, err)
	}
}

func TestStoppedContinuationContextCarriesOriginalOutsidePage(t *testing.T) {
	rows := continuationRows()
	reminder := stoppedContinuationContext(rows)
	content, _ := json.Marshal([]ports.ChatContent{{Type: "text", Internal: true, Text: reminder}})
	rows.Turns = []domain.ConversationTurn{{ID: "continued", State: domain.TurnStateInterrupted, ProviderTurnID: "p-continued"}}
	rows.Messages = []domain.ConversationMessage{
		{ID: "continued-user", TurnID: "continued", Sequence: 5, Role: domain.MessageRoleUser, Text: "Continue", Continuation: true, DeliveryContentJSON: string(content)},
		{ID: "continued-answer", TurnID: "continued", Sequence: 6, Role: domain.MessageRoleAssistant, Text: "NEWEST_TAIL"},
	}
	got := stoppedContinuationContext(rows)
	for _, want := range []string{"TASK_DONE", "Keep the", "NEWEST_TAIL"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost carried context %q: %s", want, got)
		}
	}
	if strings.Count(got, "Interrupted task context (JSON):") != 1 {
		t.Fatal("nested context")
	}
}

func TestContinuationNativeReplayMatchesDispatchedPromptWithOpaqueIDs(t *testing.T) {
	content, _ := json.Marshal([]ports.ChatContent{{Type: "text", Internal: true, Text: "FROZEN_TASK_CONTEXT"}})
	message := domain.ConversationMessage{ID: "continue-user", TurnID: "ao-turn", Role: domain.MessageRoleUser, Continuation: true, Text: "Continue from where you stopped.", DeliveryContentJSON: string(content)}
	index := indexNativeHistoryTurns([]domain.ConversationTurn{{ID: "ao-turn", ProviderTurnID: "old-opaque", State: domain.TurnStateCompleted}}, []domain.ConversationMessage{message}, nil)
	text := "Continue from where you stopped.\n\nFROZEN_TASK_CONTEXT"
	mapped, _ := index.mapReplay([]ports.ChatEvent{{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "new-opaque", Text: text}})
	if mapped["new-opaque"] == nil || mapped["new-opaque"].providerTurnID != "old-opaque" {
		t.Fatal("native replay duplicated the continued turn")
	}
	if mapped["new-opaque"].messages[nativeHistoryMessageFingerprint(domain.MessageRoleUser, text)] != 1 {
		t.Fatal("inherited replay compared visible text instead of dispatched text")
	}
	if message.Text != "Continue from where you stopped." {
		t.Fatal("provider matching leaked context into visible text")
	}
}

func TestPrepareContinuationRejectsStaleIntent(t *testing.T) {
	rows := continuationRows()
	rows.Turns[1].State = domain.TurnStateCompleted
	controller := Controller{continuationReader: SnapshotReaderFunc(func(context.Context, string) (ConversationRows, error) { return rows, nil })}
	got, err := controller.prepareContinuation(context.Background(), ports.ChatUserMessage{Text: "Continue", Continuation: true})
	if !errors.Is(err, ErrContinuationUnavailable) || len(got.Content) != 0 {
		t.Fatalf("stale intent started context-free work: %#v, %v", got, err)
	}
}
