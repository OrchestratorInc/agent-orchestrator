package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// legacyArchiveRow is the archive record projectEvent wrote before empty fields were
// dropped: all 26 keys on every row, "" or null for the ones an event leaves empty.
// Archives already on disk hold rows of this shape, so it is kept here as a fixture.
func legacyArchiveRow(t *testing.T, event ports.ChatEvent) string {
	t.Helper()
	detail := json.RawMessage("null")
	if len(event.Detail) > 0 {
		detail = event.Detail
	}
	record := map[string]any{
		"kind":                   event.Kind,
		"providerEventId":        event.ProviderEventID,
		"providerTurnId":         event.ProviderTurnID,
		"providerConversationId": event.ProviderConversationID,
		"providerItemId":         event.ProviderItemID,
		"providerItemAliases":    event.ProviderItemAliases,
		"clientMessageId":        event.ClientMessageID,
		"turnState":              event.TurnState,
		"delta":                  event.Delta,
		"text":                   event.Text,
		"activityKind":           event.ActivityKind,
		"activityStatus":         event.ActivityStatus,
		"summary":                event.Summary,
		"detail":                 detail,
		"requestId":              event.RequestID,
		"decisions":              event.Decisions,
		"controllerState":        event.ControllerState,
		"usage":                  event.Usage,
		"rateLimits":             event.RateLimits,
		"title":                  event.Title,
		"plan":                   event.Plan,
		"reroute":                event.Reroute,
		"account":                event.Account,
		"threadState":            event.ThreadState,
		"mcpServers":             event.MCPServers,
		"diff":                   event.Diff,
	}
	if event.Input != nil {
		record["input"] = event.Input
	}
	if event.Err != nil {
		record["error"] = event.Err.Error()
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode legacy archive row: %v", err)
	}
	return string(encoded)
}

// decodedArchiveRow decodes an archive row into typed values, so a row compares by what
// it says rather than by which empty spelling it used.
type decodedArchiveRow struct {
	Kind                   ports.ChatEventKind
	ProviderEventID        string
	ProviderTurnID         string
	ProviderConversationID string
	ProviderItemID         string
	ProviderItemAliases    []string
	ClientMessageID        string
	TurnState              domain.TurnState
	Delta                  string
	Text                   string
	ActivityKind           domain.ActivityKind
	ActivityStatus         domain.ActivityStatus
	Summary                string
	Detail                 any
	RequestID              string
	Decisions              []ports.ChatDecisionOption
	ControllerState        ports.ChatControllerState
	Usage                  *ports.ChatUsage
	RateLimits             *ports.ChatRateLimits
	Title                  string
	Plan                   *domain.ConversationPlan
	Reroute                *ports.ChatModelReroute
	Account                *ports.ChatAccount
	ThreadState            *ports.ChatThreadState
	MCPServers             []ports.ChatMCPServer
	Diff                   *ports.ChatTurnDiff
	Input                  *ports.ChatInputRequest
	// Error is a pointer because its presence is meaningful: it is archived only for an
	// event that has an error, and an empty message is still an error.
	Error *string
}

func decodeArchived(t *testing.T, row string) decodedArchiveRow {
	t.Helper()
	var decoded decodedArchiveRow
	if err := json.Unmarshal([]byte(row), &decoded); err != nil {
		t.Fatalf("decode archive row %s: %v", row, err)
	}
	return decoded
}

// archiveCorpus gives every key of the record a non-empty value in at least one event.
func archiveCorpus() []ports.ChatEvent {
	failure := errors.New("boom")
	return []ports.ChatEvent{
		{Kind: ports.ChatEventControllerState, ProviderEventID: "ev-controller", ControllerState: ports.ChatControllerReady},
		{Kind: ports.ChatEventTurnStarted, ProviderEventID: "ev-started", ProviderTurnID: "pt-1", ProviderConversationID: "thread-1"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "ev-user", ProviderTurnID: "pt-1",
			ProviderItemID: "user-1", ClientMessageID: "client-1", Text: "run the tests"},
		{Kind: ports.ChatEventMessageDelta, ProviderEventID: "ev-delta", ProviderTurnID: "pt-1",
			ProviderItemID: "msg-1", Delta: "Running"},
		{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "ev-message", ProviderTurnID: "pt-1",
			ProviderItemID: "msg-1", Text: "Running the tests"},
		{Kind: ports.ChatEventActivityStarted, ProviderEventID: "ev-activity", ProviderTurnID: "pt-1",
			ProviderItemID: "cmd-1", ProviderItemAliases: []string{"cmd-1-legacy"},
			ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusRunning,
			Summary: "go test ./...", Detail: []byte(`{"command":"go test ./..."}`)},
		{Kind: ports.ChatEventApprovalRequested, ProviderEventID: "ev-approval", ProviderTurnID: "pt-1",
			ProviderItemID: "approval-1", RequestID: "req-1", Summary: "Run a command",
			Decisions: []ports.ChatDecisionOption{{ID: "accept", Label: "Accept", Kind: ports.ChatDecisionAllowOnce}}},
		{Kind: ports.ChatEventInputRequested, ProviderEventID: "ev-input", ProviderTurnID: "pt-1",
			ProviderItemID: "input-1", Input: &ports.ChatInputRequest{Mode: ports.ChatInputModeForm, Message: "Which one?"}},
		{Kind: ports.ChatEventUsage, ProviderEventID: "ev-usage",
			Usage: &ports.ChatUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, TotalsKnown: true}},
		{Kind: ports.ChatEventRateLimits, ProviderEventID: "ev-limits",
			RateLimits: &ports.ChatRateLimits{PrimaryUsedPercent: 12, SecondaryUsedPercent: -1, PlanLabel: "pro"}},
		{Kind: ports.ChatEventThreadRenamed, ProviderEventID: "ev-title", Title: "Fix the flaky test"},
		{Kind: ports.ChatEventPlanUpdated, ProviderEventID: "ev-plan", ProviderTurnID: "pt-1", Summary: "Plan",
			Plan: &domain.ConversationPlan{Steps: []domain.ConversationPlanStep{{Text: "write the test", Status: domain.PlanStepPending}}}},
		{Kind: ports.ChatEventModelRerouted, ProviderEventID: "ev-reroute", ProviderTurnID: "pt-1",
			Reroute: &ports.ChatModelReroute{FromModel: "big", ToModel: "small", Reason: "capacity"}},
		{Kind: ports.ChatEventAccountChanged, ProviderEventID: "ev-account",
			Account: &ports.ChatAccount{AuthMode: "chatgpt", PlanLabel: "pro"}},
		{Kind: ports.ChatEventThreadState, ProviderEventID: "ev-thread",
			ThreadState: &ports.ChatThreadState{Status: domain.ThreadStatusActive}},
		{Kind: ports.ChatEventMCPServers, ProviderEventID: "ev-mcp",
			MCPServers: []ports.ChatMCPServer{{Name: "fs", Status: "ready"}}},
		{Kind: ports.ChatEventTurnDiff, ProviderEventID: "ev-diff", ProviderTurnID: "pt-1",
			Diff: &ports.ChatTurnDiff{Files: []ports.ChatDiffFile{{Path: "a.go", Additions: 1, Status: "modified"}}}},
		{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "ev-completed", ProviderTurnID: "pt-1",
			TurnState: domain.TurnStateCompleted},
		{Kind: ports.ChatEventTurnStarted, ProviderEventID: "ev-started-failed", ProviderTurnID: "pt-2"},
		{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "ev-failed", ProviderTurnID: "pt-2", Err: failure},
		{Kind: ports.ChatEventTurnStarted, ProviderEventID: "ev-started-silent", ProviderTurnID: "pt-3"},
		// An error with no message is still an error.
		{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "ev-silent-failure", ProviderTurnID: "pt-3", Err: errors.New("")},
	}
}

// emitCorpus sends every corpus event through a real controller and store and returns
// the archived rows by provider event id.
func emitCorpus(t *testing.T) map[string]string {
	t.Helper()
	h := newHarnessForHarness(t, domain.HarnessClaudeCode)
	corpus := archiveCorpus()
	h.conv.emit(corpus...)

	deadline := time.Now().Add(10 * time.Second)
	for {
		events, err := h.st.ProviderEventsSince(context.Background(), h.ctrl.ConversationID(), 0, 1000)
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		rows := map[string]string{}
		for _, event := range events {
			rows[event.ProviderEventID] = event.PayloadJson
		}
		missing := ""
		for _, event := range corpus {
			if _, ok := rows[event.ProviderEventID]; !ok {
				missing = event.ProviderEventID
				break
			}
		}
		if missing == "" {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("event %s was never archived; archived %d of %d", missing, len(rows), len(corpus))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func rowKeys(t *testing.T, row string) map[string]json.RawMessage {
	t.Helper()
	keys := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(row), &keys); err != nil {
		t.Fatalf("decode archive row keys %s: %v", row, err)
	}
	return keys
}

func TestArchiveRowOfADeltaHoldsOnlyWhatTheDeltaSays(t *testing.T) {
	rows := emitCorpus(t)

	var got []string
	for key := range rowKeys(t, rows["ev-delta"]) {
		got = append(got, key)
	}
	slices.Sort(got)
	want := []string{"delta", "kind", "providerEventId", "providerItemId", "providerTurnId"}
	if !slices.Equal(got, want) {
		t.Fatalf("delta row keys = %v, want %v: %s", got, want, rows["ev-delta"])
	}
}

// Presence is meaningful for these two keys: they are archived only when the event
// has an input request or an error, and SelectVerifiedAuthenticationTurn treats a
// present error, however empty its message, as a failed completion.
var archiveKeysKeptWhenPresent = []string{"input", "error"}

func TestArchiveRowsOmitEmptyKeysAndKeepEverythingElse(t *testing.T) {
	rows := emitCorpus(t)

	for _, event := range archiveCorpus() {
		legacy := rowKeys(t, legacyArchiveRow(t, event))
		slim := rowKeys(t, rows[event.ProviderEventID])
		var problems []string
		for key, want := range legacy {
			got, kept := slim[key]
			switch {
			case slices.Contains(archiveKeysKeptWhenPresent, key) && !kept:
				problems = append(problems, fmt.Sprintf("%s dropped but its presence is meaningful", key))
			case !kept && !isEmptyJSON(want):
				problems = append(problems, fmt.Sprintf("%s=%s dropped", key, want))
			case kept && !bytes.Equal(got, want):
				problems = append(problems, fmt.Sprintf("%s=%s was %s", key, got, want))
			}
		}
		for key, value := range slim {
			if _, ok := legacy[key]; !ok {
				problems = append(problems, fmt.Sprintf("unexpected %s=%s", key, value))
			}
			if isEmptyJSON(value) && !slices.Contains(archiveKeysKeptWhenPresent, key) {
				problems = append(problems, fmt.Sprintf("%s archived empty (%s)", key, value))
			}
		}
		if len(problems) > 0 {
			slices.Sort(problems)
			t.Errorf("%s (%d keys, the full envelope has %d): %s",
				event.ProviderEventID, len(slim), len(legacy), strings.Join(problems, "; "))
		}
	}
}

// The reader a future replay tool would be: it decodes a row into typed values.
// A full row and the slim row for the same event must decode to the same values.
func TestSlimArchiveRowsDecodeToTheValuesOfFullRows(t *testing.T) {
	rows := emitCorpus(t)

	for _, event := range archiveCorpus() {
		full := decodeArchived(t, legacyArchiveRow(t, event))
		slim := decodeArchived(t, rows[event.ProviderEventID])
		if !reflect.DeepEqual(full, slim) {
			t.Errorf("%s decodes differently:\n full %+v\n slim %+v", event.ProviderEventID, full, slim)
		}
	}
}

func TestArchiveRowKeepsAnErrorWithAnEmptyMessage(t *testing.T) {
	rows := emitCorpus(t)

	for id, want := range map[string]string{"ev-failed": `"boom"`, "ev-silent-failure": `""`} {
		got, ok := rowKeys(t, rows[id])["error"]
		if !ok || string(got) != want {
			t.Errorf("%s: error = %s (present %v), want %s: %s", id, got, ok, want, rows[id])
		}
	}
	if _, ok := rowKeys(t, rows["ev-completed"])["error"]; ok {
		t.Errorf("a completion without an error archived one: %s", rows["ev-completed"])
	}
}

func isEmptyJSON(value json.RawMessage) bool {
	switch string(value) {
	case "null", `""`, "[]":
		return true
	}
	return false
}
