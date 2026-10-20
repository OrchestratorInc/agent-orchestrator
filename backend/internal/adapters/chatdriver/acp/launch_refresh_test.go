package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFreshACPLaunchRecordsRevisionForLaterAdoption(t *testing.T) {
	agent := &fakeAgent{}
	driver := New(Config{
		Harness:        domain.HarnessClaudeCode,
		Launch:         func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		LaunchRevision: func(LaunchConfig) string { return "task-tools-v1" },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	agent.mu.Lock()
	revision := agent.initParams.Meta[persistenthost.ACPLaunchRevisionMetaKey]
	agent.mu.Unlock()
	if revision != "task-tools-v1" || conv.(ports.ChatProviderLaunchRefresher).NeedsLaunchRefresh() {
		t.Fatalf("fresh launch revision=%v refresh=%v", revision, conv.(ports.ChatProviderLaunchRefresher).NeedsLaunchRefresh())
	}
}

func TestAdoptedACPLaunchRevisionDoesNotInterruptLiveHost(t *testing.T) {
	for _, tc := range []struct {
		name, required, actual string
		want                   bool
	}{
		{"legacy host", "task-tools-v1", "", true},
		{"current host", "task-tools-v1", "task-tools-v1", false},
		{"older revision", "task-tools-v1", "older", true},
		{"opted out", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initialize, _ := json.Marshal(acpsdk.InitializeResponse{ProtocolVersion: acpsdk.ProtocolVersionNumber, AgentCapabilities: acpsdk.AgentCapabilities{SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}}}})
			session, _ := json.Marshal(acpsdk.NewSessionResponse{SessionId: "native-session"})
			daemon, host := net.Pipe()
			t.Cleanup(func() { _ = host.Close() })
			driver := New(Config{Harness: domain.HarnessClaudeCode, Capabilities: ports.ChatCapabilities{ports.ChatCapabilityResume: true}, LaunchRevision: func(LaunchConfig) string { return tc.required }}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			driver.connectHost = func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
				return &persistenthost.Transport{Stdin: daemon, Stdout: daemon, Reconnected: true, ACPState: &persistenthost.ACPState{InitializeResult: initialize, SessionResult: session, SessionID: "native-session", ActivePrompt: true, LaunchRevision: tc.actual}}, nil
			}
			opened, err := driver.Resume(context.Background(), ports.ChatResumeConfig{SessionID: "ao", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), ProviderConversationID: "native-session"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = opened.Close() })
			if got := opened.(ports.ChatProviderLaunchRefresher).NeedsLaunchRefresh(); got != tc.want {
				t.Fatalf("refresh=%v want %v", got, tc.want)
			}
			if !opened.(ports.ChatLiveReconnector).ReconnectedLive() || opened.ProviderConversationID() != "native-session" {
				t.Fatal("live native conversation was replaced")
			}
		})
	}
}
