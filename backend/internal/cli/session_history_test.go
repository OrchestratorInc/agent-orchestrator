package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionHistory_PrintsTurnsForTheCurrentSession(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "demo-1")
	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Method + " " + r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"turns":[
			{"userRequest":"second ask","agentResponse":"second answer","at":"2026-10-11T09:02:00Z","harness":"codex"},
			{"userRequest":"first ask","agentResponse":"","at":"2026-10-11T09:01:00Z"}],"total":5,"offset":2}`))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "history", "--offset", "2", "--limit", "2")
	if err != nil {
		t.Fatalf("session history failed: %v\nstderr=%s", err, errOut)
	}
	if requested != "GET /api/v1/sessions/demo-1/history?limit=2&offset=2" {
		t.Fatalf("request = %q", requested)
	}
	for _, want := range []string{
		"## Turn 3 of 5", "codex", "second ask", "second answer",
		"## Turn 2 of 5", "first ask", "(no final response recorded)",
		"1 older turns remain. Run `ao session history demo-1 --offset 4 --limit 2`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestSessionHistory_RequiresASessionOutsideAO(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "history")
	if err == nil || !strings.Contains(err.Error(), "session id is required") {
		t.Fatalf("err = %v, want a usage error naming the missing session", err)
	}
}

func TestHooks_StopFinalMessageFeedsTurnHistoryForEveryAgent(t *testing.T) {
	const payload = `{"turn_id":"turn-1","last_assistant_message":"all done"}`
	for _, tt := range []struct {
		agent, payload               string
		wantFinal, wantReplayAnswers bool
	}{
		// Codex reports its final message without making it a replay checkpoint.
		{"codex", payload, true, false},
		{"claude-code", payload, true, true},
		{"codex", `{"agent_id":"child","last_assistant_message":"all done"}`, false, false},
		{"opencode", `{"session_id":"ses-1","model":"m","last_assistant_message":"all done"}`, true, false},
	} {
		t.Run(tt.agent, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "ao-7")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:           strings.NewReader(tt.payload),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", tt.agent, "stop")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := strings.Contains(capture.body, `"finalAssistantMessage":"all done"`); got != tt.wantFinal {
				t.Fatalf("final message in turn history = %v, want %v: %s", got, tt.wantFinal, capture.body)
			}
			if got := strings.Contains(capture.body, `"latestAssistantUpdate":"all done"`); got != tt.wantReplayAnswers {
				t.Fatalf("final message as replay checkpoint = %v, want %v: %s", got, tt.wantReplayAnswers, capture.body)
			}
		})
	}
	if other := hookConversationFacts(domain.HarnessCodex, "post-tool-use", []byte(payload)); other.FinalAssistantMessage != "" {
		t.Fatalf("a non-stop event reported a final message: %+v", other)
	}
}

func TestHookTurnHistoryFactsForwardsOpenCodeAndGrokText(t *testing.T) {
	for _, tt := range []struct {
		name, event, payload, request, response string
	}{
		{"prompt", "user-prompt-submit", `{"session_id":"s","prompt":"add a retry"}`, "add a retry", ""},
		{"early empty prompt", "user-prompt-submit", `{"session_id":"s","prompt":""}`, "", ""},
		{"stop with final message", "stop", `{"last_assistant_message":"retry added"}`, "", "retry added"},
		{"camel-case prompt", "user-prompt-submit", `{"userPrompt":"add a retry"}`, "add a retry", ""},
		{"Gemini-style response", "stop", `{"prompt":"add a retry","prompt_response":"retry added"}`, "", "retry added"},
		{"stop without one", "stop", `{"session_id":"s"}`, "", ""},
		{"AO handoff note", "user-prompt-submit", `{"prompt":"<ao-handoff-request switch-id=\"x\">note</ao-handoff-request>"}`, "", ""},
		{"subagent", "user-prompt-submit", `{"prompt":"child task","agent_id":"child"}`, "", ""},
		{"other event", "pre-tool-use", `{"prompt":"add a retry","last_assistant_message":"x"}`, "", ""},
		{"mistyped payload", "stop", `{"agent_id":7,"last_assistant_message":"child done"}`, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			facts := hookConversationFacts(domain.HarnessOpenCode, tt.event, []byte(tt.payload))
			request, response := facts.TurnUserRequest, facts.FinalAssistantMessage
			if request != tt.request || response != tt.response {
				t.Fatalf("got request %q response %q, want %q and %q", request, response, tt.request, tt.response)
			}
		})
	}
}

func TestHooks_OpenCodeAndGrokPromptsReachTurnHistoryOnly(t *testing.T) {
	for _, agent := range []string{"opencode", "grok"} {
		t.Run(agent, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "ao-7")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:           strings.NewReader(`{"session_id":"ses-1","prompt":"fix this"}`),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", agent, "user-prompt-submit")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(capture.body, `"turnUserRequest":"fix this"`) {
				t.Fatalf("the prompt did not reach the turn history field: %s", capture.body)
			}
			// The daemon drops a signal whose agent is not the one the session
			// runs, so a previous agent's leftover hooks cannot speak for it.
			if !strings.Contains(capture.body, `"harness":"`+agent+`"`) {
				t.Fatalf("the hook did not name its agent: %s", capture.body)
			}
			// The replay-checkpoint field stays empty for these agents.
			if strings.Contains(capture.body, `"latestUserPrompt"`) {
				t.Fatalf("the prompt leaked into the replay checkpoint: %s", capture.body)
			}
		})
	}
}
