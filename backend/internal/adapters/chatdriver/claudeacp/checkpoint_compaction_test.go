package claudeacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const compactionSummaryText = claudeCompactionSummary + " The summary below covers the earlier portion of the conversation.\n\nSummary:\n1. Primary Request and Intent:"

func compactionLines(t *testing.T, records ...map[string]any) string {
	t.Helper()
	var lines []string
	for _, record := range records {
		record["sessionId"] = "native"
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

func compactionPrompt(n int, parent, text string) []map[string]any {
	user, attachment := fmt.Sprintf("user-%d", n), fmt.Sprintf("submission-attachment-%d", n)
	return []map[string]any{
		{"type": "user", "uuid": user, "parentUuid": parent, "promptId": fmt.Sprintf("prompt-%d", n), "message": map[string]any{"content": text}},
		{"type": "attachment", "uuid": attachment, "parentUuid": user, "attachment": map[string]any{
			"type": "hook_additional_context", "hookEvent": "UserPromptSubmit",
			"content": []string{domain.NativeSubmissionContext(fmt.Sprintf("submission-%d", n))}}},
	}
}

func compactionAnswer(id, parent, text, stop string) map[string]any {
	return map[string]any{"type": "assistant", "uuid": id, "parentUuid": parent,
		"message": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "stop_reason": stop}}
}

// compactionBoundary is the record Claude Code writes when it compacts: no
// parent, and metadata naming the messages it kept before the boundary.
func compactionBoundary(id, logicalParent string, metadata map[string]any) map[string]any {
	return map[string]any{"type": "system", "subtype": "compact_boundary", "uuid": id, "parentUuid": nil,
		"logicalParentUuid": logicalParent, "compactMetadata": metadata}
}

// compactionSummary keeps the prompt ID of the turn being compacted.
func compactionSummary(id, boundary, promptID string) map[string]any {
	return map[string]any{"type": "user", "uuid": id, "parentUuid": boundary, "promptId": promptID,
		"isCompactSummary": true, "isVisibleInTranscriptOnly": true, "message": map[string]any{"content": compactionSummaryText}}
}

func keptMessages(anchor string, ids ...string) map[string]any {
	return map[string]any{"trigger": "auto", "preservedMessages": map[string]any{"anchorUuid": anchor, "uuids": ids}}
}

func verifyCompacted(transcript string, events ...domain.NativeCheckpointObservation) (ports.NativeCheckpointBoundary, error) {
	return verifyCheckpointTranscript(context.Background(), strings.NewReader(transcript),
		ports.NativeCheckpointRequest{ProviderConversationID: "native", Evidence: checkpointEvidence(events...)})
}

func submitObservation(n int, text string) domain.NativeCheckpointObservation {
	return domain.NativeCheckpointObservation{Submission: true, SubmissionID: fmt.Sprintf("submission-%d", n), PromptID: fmt.Sprintf("prompt-%d", n), Text: text}
}

func stopObservation(n int, text string) domain.NativeCheckpointObservation {
	return domain.NativeCheckpointObservation{PromptID: fmt.Sprintf("prompt-%d", n), Text: text}
}

// A then compaction then B, the shape Claude Code writes after an automatic
// compaction between turns. A's final answer is kept and re-parented after the
// summary at load time.
func compactedBetweenTurns(t *testing.T, metadata map[string]any, tailStop string) string {
	records := compactionPrompt(0, "", "A")
	records = append(records,
		compactionAnswer("assistant-0", "submission-attachment-0", "answer A", "end_turn"),
		compactionBoundary("boundary", "assistant-0", metadata),
		compactionSummary("summary", "boundary", "prompt-0"),
	)
	records = append(records, compactionPrompt(1, "summary", "B")...)
	records = append(records, compactionAnswer("assistant-1", "submission-attachment-1", "answer B", tailStop))
	return compactionLines(t, records...)
}

func TestNativeCheckpointSettlesFromLatestCompaction(t *testing.T) {
	evidence := []domain.NativeCheckpointObservation{
		submitObservation(0, "A"), stopObservation(0, "answer A"), submitObservation(1, "B"), stopObservation(1, "answer B"),
	}
	for name, metadata := range map[string]map[string]any{
		"preserved messages": keptMessages("summary", "assistant-0"),
		"preserved segment": {"trigger": "auto", "preservedSegment": map[string]any{
			"headUuid": "assistant-0", "anchorUuid": "summary", "tailUuid": "assistant-0"}},
		"manual compact without kept messages": {"trigger": "manual"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := verifyCompacted(compactedBetweenTurns(t, metadata, "end_turn"), evidence...)
			if err != nil || got.UserMessageID != "user-1" || got.UserText != "B" || got.AssistantText != "answer B" {
				t.Fatalf("boundary=%+v error=%v", got, err)
			}
		})
	}
}

func TestNativeCheckpointCompactionKeepsSafetyChecks(t *testing.T) {
	kept := keptMessages("summary", "assistant-0")
	for _, test := range []struct {
		name       string
		transcript string
		events     []domain.NativeCheckpointObservation
		reason     string
	}{
		{
			name:       "pending tail after compaction",
			transcript: compactedBetweenTurns(t, kept, "tool_use"),
			events:     []domain.NativeCheckpointObservation{submitObservation(1, "B")},
			reason:     "observed native turn not completed",
		},
		{
			name:       "Stop for a prompt the transcript never recorded",
			transcript: compactedBetweenTurns(t, kept, "end_turn"),
			events:     []domain.NativeCheckpointObservation{submitObservation(1, "B"), stopObservation(9, "answer C")},
			reason:     "observed native prompt not flushed",
		},
		{
			name:       "submission the transcript never recorded",
			transcript: compactedBetweenTurns(t, kept, "end_turn"),
			events:     []domain.NativeCheckpointObservation{submitObservation(9, "C")},
			reason:     "native submission attachment not flushed",
		},
		{
			name: "compaction not yet followed by its summary",
			transcript: compactionLines(t, append(compactionPrompt(0, "", "A"),
				compactionAnswer("assistant-0", "submission-attachment-0", "answer A", "end_turn"),
				compactionBoundary("boundary", "assistant-0", kept))...),
			events: []domain.NativeCheckpointObservation{submitObservation(0, "A"), stopObservation(0, "answer A")},
			reason: "no native user boundary",
		},
		{
			// Replay starts at the summary, which Chat skips, so there is no user
			// turn to match until the next prompt.
			name: "no prompt since the compaction",
			transcript: compactionLines(t, append(compactionPrompt(0, "", "A"),
				compactionAnswer("assistant-0", "submission-attachment-0", "answer A", "end_turn"),
				compactionBoundary("boundary", "assistant-0", kept),
				compactionSummary("summary", "boundary", "prompt-0"))...),
			events: []domain.NativeCheckpointObservation{submitObservation(0, "A"), stopObservation(0, "answer A")},
			reason: "no native user prompt since compaction",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := verifyCompacted(test.transcript, test.events...)
			if !errors.Is(err, ports.ErrChatHistoryUnsettled) || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("boundary=%+v error=%v, want %q", got, err, test.reason)
			}
		})
	}
}

// compactionContext is what Claude Code 2.1.306 to 2.1.318 writes between the
// boundary and the summary when it compacts mid-turn: the instructions,
// session_context and date attachments, each under the one before.
func compactionContext(boundary string) []map[string]any {
	var records []map[string]any
	parent := boundary
	for _, kind := range []string{"instructions", "session_context", "date"} {
		id := "context-" + kind
		records = append(records, map[string]any{"type": "attachment", "uuid": id, "parentUuid": parent,
			"attachment": map[string]any{"type": kind}})
		parent = id
	}
	return records
}

// Compacting in the middle of a turn keeps the turn going after the summary,
// which carries its prompt ID. Its Stop must still see the turn complete and
// match the answer Claude writes after the summary.
func TestNativeCheckpointTurnRunsThroughCompaction(t *testing.T) {
	toolResult := map[string]any{"type": "user", "uuid": "tool-result-0", "parentUuid": "tool-call-0",
		"message": map[string]any{"content": []map[string]string{{"type": "tool_result", "tool_use_id": "tool"}}}}
	for _, shape := range []struct {
		name    string
		context bool
	}{
		{name: "summary under the boundary"},
		{name: "summary under the mid-turn context attachments", context: true},
	} {
		t.Run(shape.name, func(t *testing.T) {
			transcript := func(end string) string {
				records := compactionPrompt(0, "", "A")
				records = append(records,
					compactionAnswer("tool-call-0", "submission-attachment-0", "checking", "tool_use"),
					toolResult,
					compactionBoundary("boundary", "tool-result-0", keptMessages("summary", "tool-call-0", "tool-result-0")),
				)
				summaryParent := "boundary"
				if shape.context {
					records = append(records, compactionContext("boundary")...)
					summaryParent = "context-date"
				}
				records = append(records,
					compactionSummary("summary", summaryParent, "prompt-0"),
					compactionAnswer("continued-0", "summary", "answer A", end),
				)
				records = append(records, compactionPrompt(1, "continued-0", "B")...)
				return compactionLines(t, append(records, compactionAnswer("assistant-1", "submission-attachment-1", "answer B", "end_turn"))...)
			}
			events := []domain.NativeCheckpointObservation{
				submitObservation(0, "A"), stopObservation(0, "answer A"), submitObservation(1, "B"), stopObservation(1, "answer B"),
			}
			got, err := verifyCompacted(transcript("end_turn"), events...)
			if err != nil || got.UserMessageID != "user-1" || got.UserText != "B" || got.AssistantText != "answer B" {
				t.Fatalf("boundary=%+v error=%v", got, err)
			}
			if _, err := verifyCompacted(transcript("tool_use"), events...); !errors.Is(err, ports.ErrChatHistoryUnsettled) {
				t.Fatalf("unfinished carried turn admitted: %v", err)
			}
			events[1] = stopObservation(0, "a different answer")
			if _, err := verifyCompacted(transcript("end_turn"), events...); !errors.Is(err, ports.ErrChatHistoryUnsettled) ||
				!strings.Contains(err.Error(), "Stop answer does not match") {
				t.Fatalf("carried turn's Stop text not checked: %v", err)
			}
		})
	}
}

func TestFollowsCompactionCrossesOnlyAttachments(t *testing.T) {
	records := map[string]checkpointRecord{"boundary": {UUID: "boundary", Type: "system", Subtype: "compact_boundary"}}
	for _, record := range compactionContext("boundary") {
		id, parent := record["uuid"].(string), record["parentUuid"].(string)
		records[id] = checkpointRecord{UUID: id, ParentUUID: parent, Type: "attachment"}
	}
	records["answer"] = checkpointRecord{UUID: "answer", ParentUUID: "context-date", Type: "assistant"}
	for parent, want := range map[string]bool{
		"boundary":     true,  // between turns
		"context-date": true,  // mid-turn
		"answer":       false, // an ordinary reply, even one after a compaction
		"missing":      false,
		"":             false,
	} {
		if got := followsCompaction(records, parent); got != want {
			t.Errorf("followsCompaction(%q) = %v, want %v", parent, got, want)
		}
	}
}

// Only the latest compaction bounds replay. Evidence from every earlier
// segment was flushed before it.
func TestNativeCheckpointRepeatedCompactions(t *testing.T) {
	records := compactionPrompt(0, "", "A")
	records = append(records,
		compactionAnswer("assistant-0", "submission-attachment-0", "answer A", "end_turn"),
		compactionBoundary("boundary-1", "assistant-0", keptMessages("summary-1", "assistant-0")),
		compactionSummary("summary-1", "boundary-1", "prompt-0"),
	)
	records = append(records, compactionPrompt(1, "summary-1", "B")...)
	records = append(records,
		compactionAnswer("assistant-1", "submission-attachment-1", "answer B", "end_turn"),
		compactionBoundary("boundary-2", "assistant-1", keptMessages("summary-2", "assistant-1")),
		compactionSummary("summary-2", "boundary-2", "prompt-1"),
	)
	records = append(records, compactionPrompt(2, "summary-2", "C")...)
	records = append(records, compactionAnswer("assistant-2", "submission-attachment-2", "answer C", "end_turn"))
	got, err := verifyCompacted(compactionLines(t, records...),
		submitObservation(0, "A"), stopObservation(0, "answer A"),
		submitObservation(1, "B"), stopObservation(1, "answer B"),
		submitObservation(2, "C"), stopObservation(2, "answer C"))
	if err != nil || got.UserMessageID != "user-2" || got.UserText != "C" || got.AssistantText != "answer C" {
		t.Fatalf("boundary=%+v error=%v", got, err)
	}
}

func TestRelinkCompactedTranscriptMatchesSDK(t *testing.T) {
	record := func(id, parent string) checkpointRecord { return checkpointRecord{UUID: id, ParentUUID: parent} }
	boundary := func(metadata string) checkpointRecord {
		var r checkpointRecord
		if err := json.Unmarshal([]byte(`{"uuid":"boundary","type":"system","subtype":"compact_boundary","compactMetadata":`+metadata+`}`), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, test := range []struct {
		name, metadata string
		want           map[string]string
	}{
		{
			name:     "kept messages are chained after the summary",
			metadata: `{"preservedMessages":{"anchorUuid":"summary","uuids":["k1","k2"]}}`,
			want:     map[string]string{"k1": "summary", "k2": "k1", "next": "k2", "summary": "boundary"},
		},
		{
			name:     "a missing kept message leaves the log as written",
			metadata: `{"preservedMessages":{"anchorUuid":"summary","uuids":["k1","gone"]}}`,
			want:     map[string]string{"k1": "old", "k2": "k1", "next": "summary"},
		},
		{
			name:     "kept segment hangs from the summary",
			metadata: `{"preservedSegment":{"headUuid":"k1","anchorUuid":"summary","tailUuid":"k2"}}`,
			want:     map[string]string{"k1": "summary", "k2": "k1", "next": "k2"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			records := map[string]checkpointRecord{
				"k1": record("k1", "old"), "k2": record("k2", "k1"), "boundary": boundary(test.metadata),
				"summary": record("summary", "boundary"), "next": record("next", "summary"),
			}
			relinkCompactedTranscript(records, []string{"boundary"})
			for id, parent := range test.want {
				if records[id].ParentUUID != parent {
					t.Errorf("%s parent = %q, want %q", id, records[id].ParentUUID, parent)
				}
			}
		})
	}
}

// A manual /compact writes the command and its output after the summary,
// under the summary's prompt ID. Replay skips both.
func TestNativeCheckpointManualCompactCommand(t *testing.T) {
	records := compactionPrompt(0, "", "A")
	records = append(records,
		compactionAnswer("assistant-0", "submission-attachment-0", "answer A", "end_turn"),
		compactionBoundary("boundary", "assistant-0", map[string]any{"trigger": "manual"}),
		compactionSummary("summary", "boundary", "prompt-compact"),
		map[string]any{"type": "user", "uuid": "caveat", "parentUuid": "summary", "promptId": "prompt-compact", "isMeta": true,
			"message": map[string]any{"content": "<local-command-caveat>Caveat: generated by local commands.</local-command-caveat>"}},
		map[string]any{"type": "user", "uuid": "command", "parentUuid": "caveat", "promptId": "prompt-compact",
			"message": map[string]any{"content": "<command-name>/compact</command-name>\n<command-message>compact</command-message>\n<command-args></command-args>"}},
		map[string]any{"type": "user", "uuid": "stdout", "parentUuid": "command", "promptId": "prompt-compact",
			"message": map[string]any{"content": "<local-command-stdout>Compacted </local-command-stdout>"}},
	)
	records = append(records, compactionPrompt(1, "stdout", "B")...)
	records = append(records, compactionAnswer("assistant-1", "submission-attachment-1", "answer B", "end_turn"))
	got, err := verifyCompacted(compactionLines(t, records...),
		submitObservation(0, "A"), stopObservation(0, "answer A"), submitObservation(1, "B"), stopObservation(1, "answer B"))
	if err != nil || got.UserMessageID != "user-1" || got.AssistantText != "answer B" {
		t.Fatalf("boundary=%+v error=%v", got, err)
	}
}

func TestClaudeLocalCommandMetadata(t *testing.T) {
	for text, want := range map[string]bool{
		"<command-name>/compact</command-name>\n<command-message>compact</command-message>\n<command-args></command-args>": true,
		"<local-command-stdout>Compacted </local-command-stdout>":                                                          true,
		"<local-command-stderr>failed</local-command-stderr>":                                                              true,
		"<command-name>/review</command-name> please look at auth":                                                         false,
		"<command-name>unclosed":         false,
		"what does <command-name> mean?": false,
		"plain prompt":                   false,
		"":                               false,
	} {
		if got := claudeLocalCommandMetadata(text); got != want {
			t.Errorf("claudeLocalCommandMetadata(%q) = %v", text, got)
		}
	}
}

// Long sessions are the ones that compact. History before the latest
// compaction is dropped as it is read, so the size bound applies to each
// stretch between compactions, not to the whole file.
func TestNativeCheckpointSizeBoundCountsOnlySinceCompaction(t *testing.T) {
	previous := checkpointRetainedBytes
	checkpointRetainedBytes = 12 << 10
	t.Cleanup(func() { checkpointRetainedBytes = previous })
	long := strings.Repeat("x", 8<<10)
	transcript := func(compact bool) string {
		records := compactionPrompt(0, "", "A")
		records = append(records, compactionAnswer("assistant-0", "submission-attachment-0", long, "end_turn"))
		parent := "assistant-0"
		if compact {
			records = append(records,
				compactionBoundary("boundary", "assistant-0", map[string]any{"trigger": "auto"}),
				compactionSummary("summary", "boundary", "prompt-0"))
			parent = "summary"
		}
		records = append(records, compactionPrompt(1, parent, "B")...)
		return compactionLines(t, append(records, compactionAnswer("assistant-1", "submission-attachment-1", long, "end_turn"))...)
	}
	events := []domain.NativeCheckpointObservation{
		submitObservation(0, "A"), stopObservation(0, long), submitObservation(1, "B"), stopObservation(1, long),
	}
	if got, err := verifyCompacted(transcript(true), events...); err != nil || got.UserMessageID != "user-1" {
		t.Fatalf("boundary=%+v error=%v", got, err)
	}
	if _, err := verifyCompacted(transcript(false), events...); err == nil || !strings.Contains(err.Error(), "exceeds readable bounds") {
		t.Fatalf("uncompacted oversized transcript: %v", err)
	}
}
