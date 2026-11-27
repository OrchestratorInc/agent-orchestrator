package claudeacp

import (
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeCheckpointAfterAPIError(t *testing.T) {
	const prompt = `{"type":"user","sessionId":"native","uuid":"user-a","promptId":"a","message":{"content":"first"}}`
	const hook = `{"type":"attachment","sessionId":"native","uuid":"hook","parentUuid":"user-a","attachment":{"type":"hook_additional_context","hookEvent":"UserPromptSubmit","content":["AO transcript correlation ID: submission."]}}`
	const apiError = `{"type":"assistant","sessionId":"native","uuid":"error","parentUuid":"hook","isApiErrorMessage":true,"message":{"model":"<synthetic>","stop_reason":"stop_sequence","content":[{"type":"text","text":"Rate limit reached"}]}}`
	const answerA = `{"type":"assistant","sessionId":"native","uuid":"answer-a","parentUuid":"hook","message":{"stop_reason":"end_turn","content":"first answer"}}`
	const toolResult = `{"type":"user","sessionId":"native","uuid":"result","parentUuid":"error","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":"result"}]}}`
	const promptB = `{"type":"user","sessionId":"native","uuid":"user-b","parentUuid":"error","promptId":"b","message":{"content":"second"}}`
	const answerB = `{"type":"assistant","sessionId":"native","uuid":"answer-b","parentUuid":"user-b","message":{"stop_reason":"end_turn","content":"second answer"}}`
	afterAnswer := strings.Replace(apiError, `"parentUuid":"hook"`, `"parentUuid":"answer-a"`, 1)
	for _, tc := range []struct {
		name     string
		records  []string
		stopText string
		wantErr  bool
	}{
		{name: "error then a completed turn", records: []string{apiError, promptB, answerB}},
		{name: "error after a witnessed reply", records: []string{answerA, afterAnswer, promptB, answerB}, stopText: "first answer"},
		{name: "error after partial output cannot satisfy Stop", records: []string{strings.Replace(answerA, "end_turn", "tool_use", 1), afterAnswer, promptB, answerB}, stopText: "first answer", wantErr: true},
		{name: "error is not a witnessed reply", records: []string{apiError, promptB, answerB}, stopText: "Rate limit reached", wantErr: true},
		{name: "error alone cannot anchor replay", records: []string{apiError}, wantErr: true},
		{name: "unmarked synthetic message", records: []string{strings.Replace(apiError, `"isApiErrorMessage":true,`, "", 1), promptB, answerB}, wantErr: true},
		{name: "model stop sequence", records: []string{strings.Replace(apiError, "<synthetic>", "claude", 1), promptB, answerB}, wantErr: true},
		{name: "work continued after error", records: []string{apiError, toolResult, strings.Replace(promptB, `"parentUuid":"error"`, `"parentUuid":"result"`, 1), answerB}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []domain.NativeCheckpointObservation{{Submission: true, SubmissionID: "submission", PromptID: "a", Text: "first"}}
			if tc.stopText != "" {
				events = append(events, domain.NativeCheckpointObservation{PromptID: "a", Text: tc.stopText})
			}
			records := append([]string{prompt, hook}, tc.records...)
			boundary, err := verifyCheckpointTranscript(t.Context(), strings.NewReader(strings.Join(records, "\n")), ports.NativeCheckpointRequest{
				ProviderConversationID: "native", Evidence: checkpointEvidence(events...),
			})
			if tc.wantErr {
				if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
					t.Fatalf("got %v, want unsettled history", err)
				}
				return
			}
			if err != nil || boundary.UserMessageID != "user-b" || boundary.UserText != "second" || boundary.AssistantText != "second answer" || boundary.Interrupted {
				t.Fatalf("boundary=%+v error=%v", boundary, err)
			}
		})
	}
}
