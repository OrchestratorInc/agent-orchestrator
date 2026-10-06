package workerexec

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestClaudeACPConversationRestore(t *testing.T) {
	for _, test := range []struct {
		name            string
		identity        string
		transcript      bool
		loadError       int
		wantFresh       bool
		wantError       bool
		identityFailure bool
	}{
		{name: "existing conversation", identity: "native-1", transcript: true},
		{name: "missing transcript", identity: "missing-1", loadError: -32002, wantFresh: true},
		{name: "adapter cannot restore existing transcript", identity: "missing-1", transcript: true, loadError: -32002, wantFresh: true},
		{name: "authentication failure", identity: "missing-1", transcript: true, loadError: -32000, wantError: true},
		{name: "internal failure", identity: "missing-1", transcript: true, loadError: -32603, wantError: true},
		{name: "cancelled load", identity: "missing-1", transcript: true, loadError: -32800, wantError: true},
		{name: "identity persistence failure", identity: "missing-1", loadError: -32002, wantFresh: true, wantError: true, identityFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dataDir, "claude"))
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(binDir, "claude-agent-acp")
			wrapper := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestCloudPromptProviderHelper$' -- \"$@\"\n"
			if err := os.WriteFile(binary, []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			if test.transcript {
				if err := writePrivateFile(filepath.Join(dataDir, "claude", "projects", "project", test.identity+".jsonl"), nil); err != nil {
					t.Fatal(err)
				}
			}
			capture := filepath.Join(t.TempDir(), "requests.jsonl")
			credential := worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "test-secret"}
			control := &acpRestoreControl{promptControl: promptControl{controlStub: controlStub{credential: credential}}, identityFailure: test.identityFailure}
			builder := HarnessBuilder{
				DataDir: dataDir, Binaries: map[string]string{"claude-code": binary},
				Launch: worker.LaunchContext{SessionID: "session-1", Kind: "orchestrator", Harness: "claude-code"},
				Env:    map[string]string{"AO_PROMPT_TEST": "1", "AO_PROMPT_CAPTURE": capture, "AO_PROMPT_LOAD_ERROR_CODE": strconv.Itoa(test.loadError)},
			}
			supervisor := &Supervisor{Control: control, Builder: builder, UseProviderProtocol: true, Workspace: workspace, CancelInterval: time.Millisecond, CompletionRetry: time.Millisecond}
			turn := worker.Turn{ID: "turn-1", Attempt: 1, Harness: "claude-code", Mode: "trusted", Prompt: "spawn a worker", AgentSessionID: test.identity}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := supervisor.execute(ctx, turn); err != nil {
				t.Fatal(err)
			}
			methods, sessions := acpRestoreRequests(t, capture)
			if test.wantError {
				wantMethods := []string{"session/load"}
				if test.wantFresh {
					wantMethods = append(wantMethods, "session/new")
				}
				if control.failed == "" || control.completed || len(control.activity) != 0 || !reflect.DeepEqual(methods, wantMethods) {
					t.Fatalf("restore error silently replaced history: failure=%q completed=%v activity=%v methods=%v", control.failed, control.completed, control.activity, methods)
				}
				return
			}
			if control.failed != "" || !control.completed {
				t.Fatalf("Chat did not recover: failure=%q completed=%v", control.failed, control.completed)
			}
			wantMethods := []string{"session/load", "session/prompt"}
			if test.wantFresh {
				wantMethods = []string{"session/load", "session/new", "session/prompt"}
				if len(control.activity) == 0 || control.activity[0].AgentSessionID != "native-1" {
					t.Fatalf("replacement identity was not durably published: %v", control.activity)
				}
			}
			if !reflect.DeepEqual(methods, wantMethods) || sessions[len(sessions)-1] != "native-1" {
				t.Fatalf("requests=%v sessions=%v, want %v using restored/replacement identity", methods, sessions, wantMethods)
			}
			// The next turn consumes the identity published to the control plane,
			// rather than repeatedly creating a conversation for the stale ID.
			if test.wantFresh {
				turn.ID, turn.AgentSessionID = "turn-2", control.activity[0].AgentSessionID
				if err := supervisor.execute(ctx, turn); err != nil {
					t.Fatal(err)
				}
				methods, _ = acpRestoreRequests(t, capture)
				if control.failed != "" || !reflect.DeepEqual(methods, []string{"session/load", "session/prompt"}) {
					t.Fatalf("next turn did not resume replacement: failure=%q methods=%v", control.failed, methods)
				}
			}
		})
	}
}

type acpRestoreControl struct {
	promptControl
	identityFailure bool
}

func (c *acpRestoreControl) PublishActivity(ctx context.Context, activity worker.ActivityEvent) error {
	if c.identityFailure {
		return errors.New("durable identity write failed")
	}
	return c.controlStub.PublishActivity(ctx, activity)
}

func acpRestoreRequests(t *testing.T, capture string) ([]string, []string) {
	t.Helper()
	file, err := os.Open(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var methods, sessions []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var request struct {
			Method string `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			t.Fatal(err)
		}
		if request.Method == "session/load" || request.Method == "session/new" || request.Method == "session/prompt" {
			methods = append(methods, request.Method)
			sessions = append(sessions, request.Params.SessionID)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return methods, sessions
}
