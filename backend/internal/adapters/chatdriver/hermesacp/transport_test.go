package hermesacp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The package tests drive the real ACP transport. The test binary serves two
// child roles: the detached persistent chat host (spawned by the driver as
// os.Executable with `chat-host` argv) and a fake Hermes ACP agent (re-executed
// through a shell fixture with `acp` argv), so the transition below exercises
// the real driver, the real shared ACP conversation, and the real Hermes mode
// mapping end to end.
const (
	fakeAgentEnv     = "AO_TEST_HERMES_ACP_FAKE_AGENT"
	fakeAgentModeLog = "AO_TEST_HERMES_ACP_MODE_LOG"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 7 && os.Args[1] == "chat-host" {
		protocol := persistenthost.ProtocolRaw
		fingerprint := ""
		separator := 5
		if os.Args[5] == string(persistenthost.ProtocolACP) {
			protocol = persistenthost.ProtocolACP
			fingerprint = os.Args[6]
			separator = 7
		}
		if len(os.Args) <= separator || os.Args[separator] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Env: os.Environ(), Argv: os.Args[separator+1:], Protocol: protocol,
			OwnershipFingerprint: fingerprint,
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv(fakeAgentEnv) == "1" {
		serveFakeHermesAgent(os.Getenv(fakeAgentModeLog))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHermesACPTransportSendsExplicitDefaultModeOnPermissiveToDefaultTransition(t *testing.T) {
	modeLog := filepath.Join(t.TempDir(), "modes.json")
	binary := writeHermesACPExecutable(t, modeLog)
	driver := newDriver(
		&fakePlugin{status: ports.AgentAuthStatusAuthorized, binary: binary},
		nil,
		nil,
	)

	// A session that starts permissive and is then moved to Default must reach
	// Hermes as an explicit session/set_mode(default). With the old mapping an
	// empty mode string skipped session/set_mode, so Hermes stayed in
	// accept_edits and kept auto-allowing edits the new policy promises to
	// prompt for (review comment 4349829023 on PR #6635).
	conversation, err := driver.Start(context.Background(), ports.ChatStartConfig{
		SessionID: "hermes-acp", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeAcceptEdits,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conversation.(ports.ChatProviderTerminator).Terminate() }()

	ref, err := conversation.SendTurn(context.Background(), ports.ChatUserMessage{
		Text:     "switch approval to Default",
		Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeDefault},
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	deferred := conversation.(ports.ChatDeferredTurnStarter)
	if err := deferred.StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	waitForTurnCompletion(t, conversation.Events())

	modes := readModeLog(t, modeLog)
	want := []string{hermesModeAcceptEdits, hermesModeDefault}
	if len(modes) != len(want) {
		t.Fatalf("session/set_mode calls = %v, want %v", modes, want)
	}
	for i, mode := range want {
		if modes[i] != mode {
			t.Fatalf("session/set_mode calls = %v, want %v", modes, want)
		}
	}
}

// writeHermesACPExecutable installs a fake `hermes` binary whose `acp`
// entry point re-executes this test binary as the fake ACP agent.
func writeHermesACPExecutable(t *testing.T, modeLog string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "hermes")
	script := `#!/bin/sh
if [ "$1" = "acp" ]; then
  exec "$AO_TEST_HERMES_ACP_BINARY" -test.run=TestHermesACPFakeAgentHelper
fi
echo "unexpected hermes argv: $*" >&2
exit 1
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_TEST_HERMES_ACP_BINARY", os.Args[0])
	t.Setenv(fakeAgentEnv, "1")
	t.Setenv(fakeAgentModeLog, modeLog)
	return binary
}

func TestHermesACPFakeAgentHelper(t *testing.T) {
	if os.Getenv(fakeAgentEnv) != "1" {
		return
	}
	serveFakeHermesAgent(os.Getenv(fakeAgentModeLog))
}

func waitForTurnCompletion(t *testing.T, events <-chan ports.ChatEvent) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == ports.ChatEventTurnCompleted {
				if event.TurnState != domain.TurnStateCompleted {
					t.Fatalf("turn state = %q, want completed", event.TurnState)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the turn to complete")
		}
	}
}

func readModeLog(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake agent mode log: %v", err)
	}
	var modes []string
	if err := json.Unmarshal(raw, &modes); err != nil {
		t.Fatalf("decode fake agent mode log %q: %v", raw, err)
	}
	return modes
}

// ---- fake Hermes ACP agent (runs in the re-executed test binary) ----

func serveFakeHermesAgent(modeLog string) {
	agent := &fakeHermesAgent{modeLog: modeLog}
	agent.conn = acpsdk.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
	// Serve until the driver tears the process down; the SDK's pump goroutines
	// keep the runtime alive off these channels.
	block := make(chan struct{})
	<-block
}

type fakeHermesAgent struct {
	conn    *acpsdk.AgentSideConnection
	modeLog string

	mu    sync.Mutex
	modes []string
}

var _ acpsdk.Agent = (*fakeHermesAgent)(nil)

// hermesAdvertisedModes mirrors the mode list the Hermes ACP server advertises
// in session/new (acp_adapter/server.py _MODES).
var hermesAdvertisedModes = []acpsdk.SessionMode{
	{Id: hermesModeDefault, Name: "Default", Description: acpsdk.Ptr("Ask before edits.")},
	{Id: hermesModeAcceptEdits, Name: "Accept Edits"},
	{Id: hermesModeDontAsk, Name: "Don't Ask"},
}

func (a *fakeHermesAgent) Authenticate(context.Context, acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, nil
}

func (a *fakeHermesAgent) Initialize(context.Context, acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		AgentCapabilities: acpsdk.AgentCapabilities{
			SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
		},
	}, nil
}

func (a *fakeHermesAgent) NewSession(context.Context, acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	return acpsdk.NewSessionResponse{
		SessionId: "hermes-test-session-1",
		Modes: &acpsdk.SessionModeState{
			CurrentModeId:  hermesModeDefault,
			AvailableModes: hermesAdvertisedModes,
		},
	}, nil
}

func (a *fakeHermesAgent) SetSessionMode(_ context.Context, params acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	a.mu.Lock()
	a.modes = append(a.modes, string(params.ModeId))
	err := a.record()
	a.mu.Unlock()
	if err != nil {
		return acpsdk.SetSessionModeResponse{}, err
	}
	return acpsdk.SetSessionModeResponse{}, nil
}

func (a *fakeHermesAgent) record() error {
	raw, err := json.Marshal(a.modes)
	if err != nil {
		return err
	}
	return os.WriteFile(a.modeLog, raw, 0o600)
}

func (a *fakeHermesAgent) Prompt(context.Context, acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

func (a *fakeHermesAgent) ResumeSession(context.Context, acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, nil
}

func (a *fakeHermesAgent) LoadSession(context.Context, acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	return acpsdk.LoadSessionResponse{}, nil
}

func (a *fakeHermesAgent) UnstableForkSession(context.Context, acpsdk.UnstableForkSessionRequest) (acpsdk.UnstableForkSessionResponse, error) {
	return acpsdk.UnstableForkSessionResponse{}, nil
}

func (a *fakeHermesAgent) UnstableDeleteSession(context.Context, acpsdk.UnstableDeleteSessionRequest) (acpsdk.UnstableDeleteSessionResponse, error) {
	return acpsdk.UnstableDeleteSessionResponse{}, nil
}

func (a *fakeHermesAgent) ListSessions(context.Context, acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, nil
}

func (a *fakeHermesAgent) CloseSession(context.Context, acpsdk.CloseSessionRequest) (acpsdk.CloseSessionResponse, error) {
	return acpsdk.CloseSessionResponse{}, nil
}

func (a *fakeHermesAgent) SetSessionConfigOption(context.Context, acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, nil
}

func (a *fakeHermesAgent) Logout(context.Context, acpsdk.LogoutRequest) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}

func (a *fakeHermesAgent) Cancel(context.Context, acpsdk.CancelNotification) error { return nil }

func (a *fakeHermesAgent) HandleExtensionMethod(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, acpsdk.NewMethodNotFound(method)
}
