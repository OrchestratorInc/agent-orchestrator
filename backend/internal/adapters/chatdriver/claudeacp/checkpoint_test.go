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

func checkpointFixture(t *testing.T, prompts, answers []string) string {
	t.Helper()
	var lines []string
	parent := ""
	for i, prompt := range prompts {
		userID, assistantID := fmt.Sprintf("user-%d", i), fmt.Sprintf("assistant-%d", i)
		attachmentID := fmt.Sprintf("submission-attachment-%d", i)
		for _, record := range []map[string]any{
			{"type": "user", "sessionId": "native", "uuid": userID, "parentUuid": parent, "promptId": fmt.Sprintf("prompt-%d", i),
				"message": map[string]any{"content": prompt}},
			{"type": "attachment", "sessionId": "native", "uuid": attachmentID, "parentUuid": userID,
				"attachment": map[string]any{"type": "hook_additional_context", "hookEvent": "UserPromptSubmit",
					"content": []string{domain.NativeSubmissionContext(fmt.Sprintf("submission-%d", i))}}},
			{"type": "assistant", "sessionId": "native", "uuid": assistantID, "parentUuid": attachmentID,
				"message": map[string]any{"content": []map[string]string{{"type": "text", "text": answers[i]}}, "stop_reason": "end_turn"}},
		} {
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(data))
		}
		parent = assistantID
	}
	return strings.Join(lines, "\n") + "\n"
}

func checkpointEvidence(events ...domain.NativeCheckpointObservation) string {
	evidence := ""
	submission := 0
	for _, event := range events {
		event.Generation = "launch"
		if event.Submission && event.SubmissionID == "" {
			event.SubmissionID = fmt.Sprintf("submission-%d", submission)
			submission++
		}
		evidence = domain.AppendNativeCheckpoint(evidence, "native", event)
	}
	return evidence
}

func TestNativeCheckpointQueuedSubmissionAndDelayedStops(t *testing.T) {
	submit := func(id, text string) domain.NativeCheckpointObservation {
		return domain.NativeCheckpointObservation{Submission: true, PromptID: id, Text: text}
	}
	stop := func(id, text string) domain.NativeCheckpointObservation {
		return domain.NativeCheckpointObservation{PromptID: id, Text: text}
	}
	tests := []struct {
		name             string
		events           []domain.NativeCheckpointObservation
		prompts, answers []string
		wantError        bool
	}{
		{"real queued ID collision", []domain.NativeCheckpointObservation{
			submit("prompt-0", "A"), submit("prompt-0", "B"), stop("prompt-0", "answer A"), stop("prompt-1", "answer B")},
			[]string{"A", "B"}, []string{"answer A", "answer B"}, false},
		{"delayed duplicate Stop", []domain.NativeCheckpointObservation{
			submit("prompt-0", "A"), stop("prompt-0", "answer A"), submit("prompt-1", "B"), stop("prompt-1", "answer B"), stop("prompt-0", "answer A")},
			[]string{"A", "B"}, []string{"answer A", "answer B"}, false},
		{"repeated queued text cannot use anchor twice", []domain.NativeCheckpointObservation{
			submit("prompt-0", "continue"), submit("prompt-0", "continue"), stop("prompt-0", "Done")},
			[]string{"continue"}, []string{"Done"}, true},
		{"repeated queued text with both boundaries", []domain.NativeCheckpointObservation{
			submit("prompt-0", "continue"), submit("prompt-0", "continue"), stop("prompt-0", "Done"), stop("prompt-1", "Done")},
			[]string{"continue", "continue"}, []string{"Done", "Done"}, false},
		{"two queued submissions require two successors", []domain.NativeCheckpointObservation{
			submit("prompt-0", "continue"), submit("prompt-0", "continue"), submit("prompt-0", "continue"), stop("prompt-0", "Done"), stop("prompt-1", "Done")},
			[]string{"continue", "continue"}, []string{"Done", "Done"}, true},
		{"later arriving old Stop cannot erase unflushed C", []domain.NativeCheckpointObservation{
			submit("prompt-0", "A"), stop("prompt-2", "answer C"), stop("prompt-1", "answer B")},
			[]string{"A", "B"}, []string{"answer A", "answer B"}, true},
		{"cross-turn answer rejected", []domain.NativeCheckpointObservation{
			submit("prompt-0", "A"), stop("prompt-1", "answer A")},
			[]string{"A", "B"}, []string{"answer A", "answer B"}, true},
		{"missing Stop ID is unwaivable", []domain.NativeCheckpointObservation{
			submit("prompt-0", "A"), stop("", "answer A")},
			[]string{"A"}, []string{"answer A"}, true},
		{"lost first hook cannot alias queued repeated input", []domain.NativeCheckpointObservation{
			{Submission: true, PromptID: "prompt-0", SubmissionID: "submission-1", Text: "continue"}, stop("prompt-0", "Done")},
			[]string{"continue"}, []string{"Done"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := verifyCheckpointTranscript(context.Background(),
				strings.NewReader(checkpointFixture(t, test.prompts, test.answers)),
				ports.NativeCheckpointRequest{ProviderConversationID: "native", Evidence: checkpointEvidence(test.events...)})
			if test.wantError {
				if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
					t.Fatalf("admitted incomplete evidence: %+v, %v", got, err)
				}
			} else if err != nil || got.UserMessageID != fmt.Sprintf("user-%d", len(test.prompts)-1) {
				t.Fatalf("boundary=%+v error=%v", got, err)
			}
		})
	}
}

func TestNativeCheckpointTurnCompanionIsNotAUserBoundary(t *testing.T) {
	evidence := checkpointEvidence(
		domain.NativeCheckpointObservation{Submission: true, SubmissionID: "submission", PromptID: "prompt", Text: "A"},
		domain.NativeCheckpointObservation{PromptID: "prompt", Text: "answer"},
	)
	for _, test := range []struct {
		name      string
		companion string
		parent    string
		wantError bool
	}{
		{
			name:      "meta turn companion",
			companion: `{"type":"user","sessionId":"native","uuid":"companion","parentUuid":"user","promptId":"prompt","isMeta":true,"turnCompanion":true,"message":{"content":[{"type":"text","text":"auxiliary context"}]}}`,
			parent:    "companion",
		},
		{
			// getSessionMessages drops isMeta records, so replay never shows one.
			name:      "meta only",
			companion: `{"type":"user","sessionId":"native","uuid":"meta","parentUuid":"user","promptId":"prompt","isMeta":true,"message":{"content":"B"}}`,
			parent:    "meta",
		},
		{
			name:      "interrupt marker",
			companion: `{"type":"user","sessionId":"native","uuid":"marker","parentUuid":"user","promptId":"prompt","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
			parent:    "marker",
		},
		{
			name:      "turn companion only",
			companion: `{"type":"user","sessionId":"native","uuid":"companion","parentUuid":"user","promptId":"prompt","turnCompanion":true,"message":{"content":"B"}}`,
			parent:    "companion",
			wantError: true,
		},
		{
			name:      "tool result only",
			companion: `{"type":"user","sessionId":"native","uuid":"tool-result","parentUuid":"user","promptId":"prompt","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":"result"}]}}`,
			parent:    "tool-result",
		},
		{
			name:      "genuine duplicate user",
			companion: `{"type":"user","sessionId":"native","uuid":"duplicate","parentUuid":"user","promptId":"prompt","message":{"content":"B"}}`,
			parent:    "duplicate",
			wantError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transcript := strings.Join([]string{
				`{"type":"user","sessionId":"native","uuid":"user","promptId":"prompt","message":{"content":"A"}}`,
				test.companion,
				`{"type":"attachment","sessionId":"native","uuid":"attachment","parentUuid":"` + test.parent + `","attachment":{"type":"hook_additional_context","hookEvent":"UserPromptSubmit","content":["AO transcript correlation ID: submission."]}}`,
				`{"type":"assistant","sessionId":"native","uuid":"assistant","parentUuid":"attachment","message":{"content":"answer","stop_reason":"end_turn"}}`,
			}, "\n") + "\n"
			got, err := verifyCheckpointTranscript(context.Background(), strings.NewReader(transcript), ports.NativeCheckpointRequest{
				ProviderConversationID: "native",
				Evidence:               evidence,
			})
			if test.wantError {
				if !errors.Is(err, ports.ErrChatHistoryUnsettled) || !strings.Contains(err.Error(), "ambiguous native prompt ID") {
					t.Fatalf("error=%v", err)
				}
			} else if err != nil || got.UserMessageID != "user" {
				t.Fatalf("boundary=%+v error=%v", got, err)
			}
		})
	}
}

func TestNativeCheckpointNotificationCycleBelongsToPreviousTurn(t *testing.T) {
	// Claude answers a finished task's notification, or a peer message, on its
	// own. Chat replay skips that note, so the boundary is the user turn before.
	user := `{"type":"user","sessionId":"native","uuid":"user","promptId":"prompt","message":{"content":"A"}}`
	answer := strings.Join([]string{
		`{"type":"attachment","sessionId":"native","uuid":"attachment","parentUuid":"user","attachment":{"type":"hook_additional_context","hookEvent":"UserPromptSubmit","content":["AO transcript correlation ID: submission."]}}`,
		`{"type":"assistant","sessionId":"native","uuid":"answer","parentUuid":"attachment","message":{"content":"answer","stop_reason":"end_turn"}}`,
	}, "\n")
	reply := `{"type":"assistant","sessionId":"native","uuid":"reply","parentUuid":"note","message":{"content":"still running","stop_reason":"end_turn"}}`
	evidence := checkpointEvidence(
		domain.NativeCheckpointObservation{Submission: true, SubmissionID: "submission", PromptID: "prompt", Text: "A"},
		domain.NativeCheckpointObservation{PromptID: "prompt", Text: "answer"},
		domain.NativeCheckpointObservation{PromptID: "cycle", Text: "still running"},
	)
	verify := func(lines ...string) (ports.NativeCheckpointBoundary, error) {
		return verifyCheckpointTranscript(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), ports.NativeCheckpointRequest{
			ProviderConversationID: "native", Evidence: evidence,
		})
	}
	for _, note := range []string{
		`{"type":"user","sessionId":"native","uuid":"note","parentUuid":"answer","promptId":"cycle","message":{"content":"<task-notification>\n<status>running</status>\n</task-notification>\nThis is a check-in, not a completion."}}`,
		`{"type":"user","sessionId":"native","uuid":"note","parentUuid":"answer","promptId":"cycle","isMeta":true,"origin":{"kind":"peer"},"message":{"content":"peer says hi"}}`,
	} {
		got, err := verify(user, answer, note, reply)
		if err != nil || got.UserMessageID != "user" || got.UserText != "A" || got.AssistantText != "still running" {
			t.Fatalf("note %s: boundary=%+v error=%v", note, got, err)
		}
	}
	// With no user turn before the note, replay has no boundary to match.
	orphan := `{"type":"user","sessionId":"native","uuid":"note","promptId":"cycle","message":{"content":"<task-notification>done</task-notification>"}}`
	evidence = checkpointEvidence(domain.NativeCheckpointObservation{PromptID: "cycle", Text: "still running"})
	if _, err := verify(orphan, reply); !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("orphan note error=%v", err)
	}
	for text, want := range map[string]bool{
		"[Request interrupted by user]":              true,
		"[Request interrupted by user for tool use]": true,
		"[Request interrupted by user] keep going":   false,
		"what does <task-notification> mean?":        false,
		// Both summary forms Claude Code 2.1 writes when it compacts.
		"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\nSummary:\n1. Primary Request and Intent:": true,
		"This session is being continued from a previous conversation that ran out of context. The summary of that conversation is not included here: it was withheld from Remote Control.":                   true,
		`why did I get "This session is being continued from a previous conversation that ran out of context."?`:                                                                                              false,
	} {
		if claudeTranscriptNotice(text) != want {
			t.Errorf("claudeTranscriptNotice(%q) = %v", text, !want)
		}
	}
}

func TestNativeCheckpointRejectsUnprovenAncestry(t *testing.T) {
	source := checkpointFixture(t, []string{"A", "B"}, []string{"answer A", "answer B"})
	evidence := checkpointEvidence(domain.NativeCheckpointObservation{PromptID: "prompt-1", Text: "answer B"})
	for name, transcript := range map[string]string{
		"lagged tail":                   checkpointFixture(t, []string{"A"}, []string{"answer A"}),
		"sidechain":                     strings.ReplaceAll(source, `"promptId":"prompt-1"`, `"isSidechain":true,"promptId":"prompt-1"`),
		"wrong identity":                strings.ReplaceAll(source, `"sessionId":"native"`, `"sessionId":"other"`),
		"broken parent":                 strings.ReplaceAll(source, `"parentUuid":"user-1"`, `"parentUuid":"missing"`),
		"pending tail":                  strings.ReplaceAll(source, `"stop_reason":"end_turn"`, `"stop_reason":"tool_use"`),
		"partial record":                source + `{"uuid":`,
		"missing next native prompt ID": strings.ReplaceAll(source, `"promptId":"prompt-1",`, ""),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := verifyCheckpointTranscript(context.Background(), strings.NewReader(transcript),
				ports.NativeCheckpointRequest{ProviderConversationID: "native", Evidence: evidence})
			if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestNativeSubmissionCannotAttachToEarlierUserWhenNextPromptIDMissing(t *testing.T) {
	source := checkpointFixture(t, []string{"continue", "continue"}, []string{"Done", "Done"})
	source = strings.ReplaceAll(source, `"promptId":"prompt-1",`, "")
	evidence := checkpointEvidence(
		domain.NativeCheckpointObservation{PromptID: "prompt-0", Submission: true, SubmissionID: "submission-1", Text: "continue"},
		domain.NativeCheckpointObservation{PromptID: "prompt-0", Text: "Done"})
	_, err := verifyCheckpointTranscript(context.Background(), strings.NewReader(source),
		ports.NativeCheckpointRequest{ProviderConversationID: "native", Evidence: evidence})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("admitted wrong native user: %v", err)
	}
}

func TestNativeCheckpointIgnoresUnrelatedStringAttachmentContent(t *testing.T) {
	source := checkpointFixture(t, []string{"A"}, []string{"answer A"})
	source += `{"type":"attachment","sessionId":"native","uuid":"hook-success","parentUuid":"assistant-0","attachment":{"type":"hook_success","hookEvent":"Stop","content":"hook succeeded"}}` + "\n"
	source += `{"type":"attachment","sessionId":"native","uuid":"skills","parentUuid":"hook-success","attachment":{"type":"skill_listing","content":"Available skills"}}` + "\n"
	evidence := checkpointEvidence(
		domain.NativeCheckpointObservation{Submission: true, PromptID: "prompt-0", Text: "A"},
		domain.NativeCheckpointObservation{PromptID: "prompt-0", Text: "answer A"})
	got, err := verifyCheckpointTranscript(context.Background(), strings.NewReader(source),
		ports.NativeCheckpointRequest{ProviderConversationID: "native", Evidence: evidence})
	if err != nil || got.UserMessageID != "user-0" {
		t.Fatalf("boundary=%+v error=%v", got, err)
	}
}

func queuedCommandRecord(id, parent, prompt string) string {
	return fmt.Sprintf(`{"type":"attachment","sessionId":"native","uuid":%q,"parentUuid":%q,"attachment":{"type":"queued_command","prompt":%q,"commandMode":"prompt"}}`, id, parent, prompt) + "\n"
}

func submissionHookRecord(id, parent string, n int) string {
	return fmt.Sprintf(`{"type":"attachment","sessionId":"native","uuid":%q,"parentUuid":%q,"attachment":{"type":"hook_additional_context","hookEvent":"UserPromptSubmit","content":[%q]}}`,
		id, parent, domain.NativeSubmissionContext(fmt.Sprintf("submission-%d", n))) + "\n"
}

// Issue #6870: input sent while Claude is mid-turn is written as a
// queued_command attachment plus its submission hook inside the running turn,
// not as a new user record. Its text must match the queued prompt.
func TestNativeCheckpointQueuedCommandInsideTurn(t *testing.T) {
	transcript := func(secondQueued string) string {
		source := `{"type":"user","sessionId":"native","uuid":"user-0","parentUuid":"","promptId":"prompt-0","message":{"content":"Write the notes"}}` + "\n"
		source += submissionHookRecord("hook-0", "user-0", 0)
		source += `{"type":"assistant","sessionId":"native","uuid":"tool-call","parentUuid":"hook-0","message":{"content":[{"type":"text","text":"writing"}],"stop_reason":"tool_use"}}` + "\n"
		source += queuedCommandRecord("queued-1", "tool-call", "Also, make n8 about mango.")
		source += submissionHookRecord("hook-1", "queued-1", 1)
		source += queuedCommandRecord("queued-2", "hook-1", secondQueued)
		source += submissionHookRecord("hook-2", "queued-2", 2)
		source += `{"type":"assistant","sessionId":"native","uuid":"answer","parentUuid":"hook-2","message":{"content":[{"type":"text","text":"DONE-NOTES PLUM"}],"stop_reason":"end_turn"}}` + "\n"
		return source
	}
	submit := func(text string) domain.NativeCheckpointObservation {
		return domain.NativeCheckpointObservation{Submission: true, PromptID: "prompt-0", Text: text}
	}
	evidence := checkpointEvidence(submit("Write the notes"), submit("Also, make n8 about mango."),
		submit("And reply PLUM too."), domain.NativeCheckpointObservation{PromptID: "prompt-0", Text: "DONE-NOTES PLUM"})
	request := ports.NativeCheckpointRequest{ProviderConversationID: "native", Evidence: evidence}

	got, err := verifyCheckpointTranscript(context.Background(), strings.NewReader(transcript("And reply PLUM too.")), request)
	if err != nil || got.UserMessageID != "user-0" || got.AssistantText != "DONE-NOTES PLUM" {
		t.Fatalf("queued input inside a completed turn: boundary=%+v error=%v", got, err)
	}
	if _, err := verifyCheckpointTranscript(context.Background(), strings.NewReader(transcript("Something else.")), request); !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("queued text that does not match its submission was admitted: %v", err)
	}
}

// A queued prompt belongs only to the submission hook whose parent is that
// queued record. Text must never carry over to a hook elsewhere in the chain.
func TestNativeCheckpointQueuedCommandBindsByAncestry(t *testing.T) {
	const queued = "Also, make n8 about mango."
	start := `{"type":"user","sessionId":"native","uuid":"user-0","parentUuid":"","promptId":"prompt-0","message":{"content":"Write the notes"}}` + "\n" +
		submissionHookRecord("hook-0", "user-0", 0) +
		`{"type":"assistant","sessionId":"native","uuid":"tool-call","parentUuid":"hook-0","message":{"content":[{"type":"text","text":"writing"}],"stop_reason":"tool_use"}}` + "\n"
	answer := func(parent string) string {
		return fmt.Sprintf(`{"type":"assistant","sessionId":"native","uuid":"answer","parentUuid":%q,"message":{"content":[{"type":"text","text":"DONE"}],"stop_reason":"end_turn"}}`, parent) + "\n"
	}
	tests := []struct {
		name, transcript string
	}{
		{"record between queued command and its hook", start +
			queuedCommandRecord("queued-1", "tool-call", queued) +
			`{"type":"attachment","sessionId":"native","uuid":"reminder","parentUuid":"queued-1","attachment":{"type":"total_tokens_reminder"}}` + "\n" +
			submissionHookRecord("hook-1", "reminder", 1) + answer("hook-1")},
		{"hook whose parent is not the queued command", start +
			queuedCommandRecord("queued-1", "tool-call", queued) +
			`{"type":"assistant","sessionId":"native","uuid":"tool-call-2","parentUuid":"queued-1","message":{"content":[{"type":"text","text":"still writing"}],"stop_reason":"tool_use"}}` + "\n" +
			submissionHookRecord("hook-1", "tool-call-2", 1) + answer("hook-1")},
	}
	evidence := checkpointEvidence(
		domain.NativeCheckpointObservation{Submission: true, PromptID: "prompt-0", Text: "Write the notes"},
		domain.NativeCheckpointObservation{Submission: true, PromptID: "prompt-0", Text: queued},
		domain.NativeCheckpointObservation{PromptID: "prompt-0", Text: "DONE"})
	request := ports.NativeCheckpointRequest{ProviderConversationID: "native", Evidence: evidence}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := verifyCheckpointTranscript(context.Background(), strings.NewReader(test.transcript), request)
			if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
				t.Fatalf("queued text bound to an unrelated hook: boundary=%+v error=%v", got, err)
			}
		})
	}
}

// Records shaped as Claude Code 2.1.303 writes them when Esc interrupts a
// terminal turn: no Stop fires, the turn never gets an end_turn reply, and a
// user record in the prompt's own cycle holds only the marker. A tool use cut
// short may or may not have a (rejected) result. When the conversation resumes,
// Claude adds a synthetic "No response requested." reply after the marker.
func TestNativeCheckpointSettlesEscInterruptedTurns(t *testing.T) {
	record := func(fields string) string {
		return `{"sessionId":"native",` + fields + `}`
	}
	prompt := func(id, promptID, parent, text string) string {
		return record(`"type":"user","uuid":"` + id + `","parentUuid":"` + parent + `","promptId":"` + promptID +
			`","message":{"role":"user","content":` + jsonString(text) + `}`)
	}
	attachment := func(id, parent, submission string) string {
		return record(`"type":"attachment","uuid":"` + id + `","parentUuid":"` + parent +
			`","attachment":{"type":"hook_additional_context","hookEvent":"UserPromptSubmit","content":["` +
			domain.NativeSubmissionContext(submission) + `"]}`)
	}
	assistant := func(id, parent, stop, content string) string {
		return record(`"type":"assistant","uuid":"` + id + `","parentUuid":"` + parent +
			`","message":{"model":"claude-haiku-4-5-20251001","content":` + content + `,"stop_reason":` + stop + `}`)
	}
	marker := func(id, promptID, parent, text string) string {
		return record(`"type":"user","uuid":"` + id + `","parentUuid":"` + parent + `","promptId":"` + promptID +
			`","message":{"role":"user","content":[{"type":"text","text":"` + text + `"}]}`)
	}
	toolResult := func(id, promptID, parent string) string {
		return record(`"type":"user","uuid":"` + id + `","parentUuid":"` + parent + `","promptId":"` + promptID +
			`","message":{"role":"user","content":[{"type":"tool_result","content":"The user doesn't want to proceed with this tool use.","is_error":true,"tool_use_id":"toolu_1"}]}`)
	}
	synthetic := func(id, parent string) string {
		return record(`"type":"assistant","uuid":"` + id + `","parentUuid":"` + parent +
			`","message":{"model":"<synthetic>","content":[{"type":"text","text":"No response requested."}],"stop_reason":"stop_sequence"}`)
	}
	const (
		textInterrupt = "[Request interrupted by user]"
		toolInterrupt = "[Request interrupted by user for tool use]"
		toolUse       = `[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"sleep 60"}}]`
	)
	submitA := domain.NativeCheckpointObservation{Submission: true, SubmissionID: "submission-a", PromptID: "prompt-a", Text: "A"}
	submitB := domain.NativeCheckpointObservation{Submission: true, SubmissionID: "submission-b", PromptID: "prompt-b", Text: "B"}
	stopB := domain.NativeCheckpointObservation{PromptID: "prompt-b", Text: "hi"}
	// The interrupted prompt A, up to (not including) its marker.
	startA := []string{prompt("user-a", "prompt-a", "", "A"), attachment("attach-a", "user-a", "submission-a")}
	// A normal turn B after the record parent.
	thenB := func(parent string) []string {
		return []string{
			prompt("user-b", "prompt-b", parent, "B"), attachment("attach-b", "user-b", "submission-b"),
			assistant("answer-b", "attach-b", `"end_turn"`, `[{"type":"text","text":"hi"}]`),
		}
	}
	join := func(groups ...[]string) []string {
		var lines []string
		for _, group := range groups {
			lines = append(lines, group...)
		}
		return lines
	}
	tests := []struct {
		name       string
		lines      []string
		events     []domain.NativeCheckpointObservation
		wantUser   string
		wantAnswer string
		wantError  string
	}{
		{
			name: "interrupted while replying",
			lines: join(startA, []string{
				assistant("partial-a", "attach-a", "null", `[{"type":"text","text":"tick 0 tick 1 "}]`),
				marker("marker-a", "prompt-a", "partial-a", textInterrupt),
			}),
			events:   []domain.NativeCheckpointObservation{submitA},
			wantUser: "user-a", wantAnswer: "tick 0 tick 1 ",
		},
		{
			name:     "interrupted before any reply",
			lines:    join(startA, []string{marker("marker-a", "prompt-a", "attach-a", textInterrupt)}),
			events:   []domain.NativeCheckpointObservation{submitA},
			wantUser: "user-a",
		},
		{
			name: "interrupted tool use with no tool result",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				marker("marker-a", "prompt-a", "tool-a", toolInterrupt),
			}),
			events:   []domain.NativeCheckpointObservation{submitA},
			wantUser: "user-a",
		},
		{
			name: "interrupted tool use with a rejected result",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				toolResult("result-a", "prompt-a", "tool-a"),
				marker("marker-a", "prompt-a", "result-a", toolInterrupt),
			}),
			events:   []domain.NativeCheckpointObservation{submitA},
			wantUser: "user-a",
		},
		{
			name: "interrupted turn resumed with Claude's filler",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				marker("marker-a", "prompt-a", "tool-a", toolInterrupt),
				synthetic("filler", "marker-a"),
			}),
			events:   []domain.NativeCheckpointObservation{submitA},
			wantUser: "user-a",
		},
		{
			// The repro: the interrupted turn stays in the evidence journal, so
			// every later switch must still settle it.
			name: "interrupt, then a normal turn",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				toolResult("result-a", "prompt-a", "tool-a"),
				marker("marker-a", "prompt-a", "result-a", toolInterrupt),
				synthetic("filler", "marker-a"),
			}, thenB("filler")),
			events:   []domain.NativeCheckpointObservation{submitA, submitB, stopB},
			wantUser: "user-b", wantAnswer: "hi",
		},
		{
			name: "interrupt, then a normal turn without filler",
			lines: join(startA, []string{
				assistant("partial-a", "attach-a", "null", `[{"type":"text","text":"tick"}]`),
				marker("marker-a", "prompt-a", "partial-a", textInterrupt),
			}, thenB("marker-a")),
			events:   []domain.NativeCheckpointObservation{submitA, submitB, stopB},
			wantUser: "user-b", wantAnswer: "hi",
		},
		{
			name:      "turn still running a tool",
			lines:     join(startA, []string{assistant("tool-a", "attach-a", `"tool_use"`, toolUse)}),
			events:    []domain.NativeCheckpointObservation{submitA},
			wantError: "observed native turn not completed",
		},
		{
			name: "turn still running after its tool result",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				toolResult("result-a", "prompt-a", "tool-a"),
			}),
			events:    []domain.NativeCheckpointObservation{submitA},
			wantError: "observed native turn not completed",
		},
		{
			name: "turn went on after the marker",
			lines: join(startA, []string{
				marker("marker-a", "prompt-a", "attach-a", textInterrupt),
				assistant("tool-a", "marker-a", `"tool_use"`, toolUse),
			}),
			events:    []domain.NativeCheckpointObservation{submitA},
			wantError: "observed native turn not completed",
		},
		{
			name: "tool result after the marker",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				marker("marker-a", "prompt-a", "tool-a", toolInterrupt),
				toolResult("result-a", "prompt-a", "marker-a"),
			}),
			events:    []domain.NativeCheckpointObservation{submitA},
			wantError: "observed native turn not completed",
		},
		{
			// A typed prompt is recorded as a string, however it reads.
			name: "marker text as a plain string",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				prompt("marker-a", "prompt-a", "tool-a", textInterrupt),
			}),
			events:    []domain.NativeCheckpointObservation{submitA},
			wantError: "observed native turn not completed",
		},
		{
			name: "marker beside another block",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				record(`"type":"user","uuid":"marker-a","parentUuid":"tool-a","promptId":"prompt-a","message":{"role":"user","content":[{"type":"text","text":"` +
					textInterrupt + `"},{"type":"text","text":"more"}]}`),
			}),
			events: []domain.NativeCheckpointObservation{submitA},
			// Not a note at all: a second user message in the prompt's cycle.
			wantError: "ambiguous native prompt ID",
		},
		{
			name: "meta record holding the marker",
			lines: join(startA, []string{
				assistant("tool-a", "attach-a", `"tool_use"`, toolUse),
				record(`"type":"user","uuid":"marker-a","parentUuid":"tool-a","promptId":"prompt-a","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"` +
					textInterrupt + `"}]}`),
			}),
			events:    []domain.NativeCheckpointObservation{submitA},
			wantError: "observed native turn not completed",
		},
		{
			name: "a later turn still running after an interrupt",
			lines: join(startA, []string{
				marker("marker-a", "prompt-a", "attach-a", textInterrupt),
				prompt("user-b", "prompt-b", "marker-a", "B"), attachment("attach-b", "user-b", "submission-b"),
				assistant("tool-b", "attach-b", `"tool_use"`, toolUse),
			}),
			events:    []domain.NativeCheckpointObservation{submitA, submitB},
			wantError: "observed native turn not completed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := verifyCheckpointTranscript(context.Background(),
				strings.NewReader(strings.Join(test.lines, "\n")+"\n"),
				ports.NativeCheckpointRequest{ProviderConversationID: "native", Evidence: checkpointEvidence(test.events...)})
			if test.wantError != "" {
				if !errors.Is(err, ports.ErrChatHistoryUnsettled) || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("boundary=%+v error=%v, want %q", got, err, test.wantError)
				}
				return
			}
			if err != nil || got.UserMessageID != test.wantUser {
				t.Fatalf("boundary=%+v error=%v, want user %s", got, err, test.wantUser)
			}
			wantInterrupted := test.wantUser == "user-a"
			if got.Interrupted != wantInterrupted || got.AssistantText != test.wantAnswer {
				t.Fatalf("boundary=%+v, want interrupted=%v answer %q", got, wantInterrupted, test.wantAnswer)
			}
		})
	}
}

func jsonString(text string) string {
	data, _ := json.Marshal(text)
	return string(data)
}
