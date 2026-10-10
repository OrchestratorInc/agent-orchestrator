package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenInterpreterSessionContextIsPrivateAndMetadataOnly(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "oi-7")
	cfg := setConfigEnv(t)
	path := filepath.Join(cfg.dataDir, "prompts", "oi-7")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "system.md"), []byte("private standing instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	const id = "019f706d-1234-7123-8123-123456789abc"
	out, _, err := executeCLI(t, Deps{In: strings.NewReader(`{"session_id":"` + id + `","source":"resume"}`), ProcessAlive: func(int) bool { return true }}, "hooks", "open-interpreter", "session-start")
	if err != nil {
		t.Fatal(err)
	}
	var context sessionStartHookOutput
	if err := json.Unmarshal([]byte(out), &context); err != nil {
		t.Fatal(err)
	}
	if context.HookSpecificOutput.HookEventName != "SessionStart" || context.HookSpecificOutput.AdditionalContext != "private standing instructions" {
		t.Fatalf("context = %+v", context)
	}
	var request setActivityAPIRequest
	if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
		t.Fatal(err)
	}
	if request.State != "" || request.AgentSessionID != id || request.LatestUserPrompt != "" || request.CoordinationID != "" {
		t.Fatalf("metadata = %+v", request)
	}
}

func TestOpenInterpreterNestedHooksCannotOverwriteRoot(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "oi-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{In: strings.NewReader(`{"session_id":"019f706d-1234-7123-8123-123456789abc","agent_id":"child"}`), ProcessAlive: func(int) bool { return true }}, "hooks", "open-interpreter", "session-start")
	if err != nil || out != "" || capture.hits != 0 {
		t.Fatalf("nested hook output=%q hits=%d error=%v", out, capture.hits, err)
	}
}

func TestOpenInterpreterSubmitIsNotSemanticAcceptance(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "oi-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{In: strings.NewReader(`{"session_id":"019f706d-1234-7123-8123-123456789abc","prompt":"not yet accepted","turn_id":"turn"}`), ProcessAlive: func(int) bool { return true }}, "hooks", "open-interpreter", "user-prompt-submit")
	if err != nil {
		t.Fatal(err)
	}
	var request setActivityAPIRequest
	if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
		t.Fatal(err)
	}
	if request.State != "" || request.LatestUserPrompt != "" || request.CoordinationID != "" || request.ProviderTurnID != "" {
		t.Fatalf("premature acceptance: %+v", request)
	}
}
