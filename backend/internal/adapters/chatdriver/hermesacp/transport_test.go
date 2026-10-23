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
	fakeAgentEnv         = "AO_TEST_HERMES_ACP_FAKE_AGENT"
	fakeAgentModeLog     = "AO_TEST_HERMES_ACP_MODE_LOG"
	fakeAgentResponseLog = "AO_TEST_HERMES_ACP_RESPONSE_LOG"
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
		serveFakeHermesAgent(os.Getenv(fakeAgentModeLog), os.Getenv(fakeAgentResponseLog))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHermesACPTransportSendsExplicitDefaultModeOnPermissiveToDefaultTransition(t *testing.T) {
	modeLog := filepath.Join(t.TempDir(), "modes.json")
	binary := writeHermesACPExecutable(t, modeLog, "")
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

// finishTurn waits for the turn to settle, answering any approval the provider
// parks for a person, acknowledges the durable receipt, and waits for the
// controller to release the next turn: the persistent host rejects a
// session/prompt sent before both happened.
func finishTurn(
	t *testing.T,
	conversation ports.ChatConversation,
	events <-chan ports.ChatEvent,
	want domain.TurnState,
) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	completed := false
	for {
		select {
		case event := <-events:
			switch event.Kind {
			case ports.ChatEventApprovalRequested:
				if err := conversation.ResolveRequest(context.Background(), event.RequestID,
					ports.ChatDecision{ID: "allow_once"}); err != nil {
					t.Fatalf("ResolveRequest: %v", err)
				}
			case ports.ChatEventTurnCompleted:
				if event.TurnState != want {
					t.Fatalf("turn state = %q, want %q (err=%v)", event.TurnState, want, event.Err)
				}
				if err := conversation.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(
					context.Background(), event.ProviderEventID); err != nil {
					t.Fatal(err)
				}
				completed = true
			case ports.ChatEventControllerState:
				if completed && event.ControllerState == ports.ChatControllerReady {
					return
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for the turn to finish")
		}
	}
}

// An automatic bypass approval must answer allow_once. Hermes persists the
// allow_session/allow_always kinds in a session cache that set_session_mode
// (default) does not clear, so a command approved during bypass under those
// kinds re-runs unattended after the user selects Default. The fake agent
// reproduces that cache: after the mode switch, the same command must reach AO
// as a fresh permission request instead of riding the earlier grant.
func TestHermesACPTransportBypassApprovalAnswersAllowOnceAndRepromptsAfterDefault(t *testing.T) {
	modeLog := filepath.Join(t.TempDir(), "modes.json")
	responseLog := filepath.Join(t.TempDir(), "responses.json")
	binary := writeHermesACPExecutable(t, modeLog, responseLog)
	driver := newDriver(
		&fakePlugin{status: ports.AgentAuthStatusAuthorized, binary: binary},
		nil,
		nil,
	)

	ctx := context.Background()
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: "hermes-acp-bypass", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conversation.(ports.ChatProviderTerminator).Terminate() }()

	startTurn := func(settings ports.ChatTurnSettings) {
		t.Helper()
		ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
			Text: dangerousCommand, Settings: settings,
		})
		if err != nil {
			t.Fatalf("SendTurn: %v", err)
		}
		if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
			t.Fatalf("StartDeferredTurn: %v", err)
		}
	}

	startTurn(ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions})
	finishTurn(t, conversation, conversation.Events(), domain.TurnStateCompleted)

	// Move the session to Default; AO's policy no longer answers approvals, so
	// the same command must re-prompt instead of riding the earlier grant.
	startTurn(ports.ChatTurnSettings{Approval: ports.PermissionModeDefault})
	finishTurn(t, conversation, conversation.Events(), domain.TurnStateCompleted)

	responses := readResponseLog(t, responseLog)
	want := []string{"allow_once", "allow_once"}
	if len(responses) != len(want) {
		t.Fatalf("received permission responses = %v, want %v", responses, want)
	}
	for i, option := range want {
		if responses[i] != option {
			t.Fatalf("received permission responses = %v, want %v", responses, want)
		}
	}
}

// writeHermesACPExecutable installs a fake `hermes` binary whose `acp`
// entry point re-executes this test binary as the fake ACP agent.
func writeHermesACPExecutable(t *testing.T, modeLog, responseLog string) string {
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
	t.Setenv(fakeAgentResponseLog, responseLog)
	return binary
}

func TestHermesACPFakeAgentHelper(t *testing.T) {
	if os.Getenv(fakeAgentEnv) != "1" {
		return
	}
	serveFakeHermesAgent(os.Getenv(fakeAgentModeLog), os.Getenv(fakeAgentResponseLog))
}

func waitForTurnCompletion(t *testing.T, events <-chan ports.ChatEvent) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == ports.ChatEventTurnCompleted {
				if event.TurnState != domain.TurnStateCompleted {
					t.Fatalf("turn state = %q, want completed (err=%v)", event.TurnState, event.Err)
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

func readResponseLog(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake agent response log: %v", err)
	}
	var responses []string
	if err := json.Unmarshal(raw, &responses); err != nil {
		t.Fatalf("decode fake agent response log %q: %v", raw, err)
	}
	return responses
}

// ---- fake Hermes ACP agent (runs in the re-executed test binary) ----

func serveFakeHermesAgent(modeLog, responseLog string) {
	agent := &fakeHermesAgent{modeLog: modeLog, responseLog: responseLog, approved: map[string]bool{}}
	agent.conn = acpsdk.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
	// Serve until the driver tears the process down; the SDK's pump goroutines
	// keep the runtime alive off these channels.
	block := make(chan struct{})
	<-block
}

type fakeHermesAgent struct {
	conn        *acpsdk.AgentSideConnection
	modeLog     string
	responseLog string

	mu        sync.Mutex
	modes     []string
	responses []string

	approved map[string]bool
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

// dangerousCommand is the prompt whose execution Hermes gates behind a
// dangerous-command approval.
const dangerousCommand = "rm -rf build"

// hermesAdvertisedPermissionOptions mirrors acp_adapter/permissions.py
// _build_permission_options: allow_once first, then the session-scoped and
// permanent grants, all persistent kinds carrying the allow_always ACP kind.
func hermesAdvertisedPermissionOptions() []acpsdk.PermissionOption {
	return []acpsdk.PermissionOption{
		{OptionId: "allow_once", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow once"},
		{OptionId: "allow_session", Kind: acpsdk.PermissionOptionKindAllowAlways, Name: "Allow for session"},
		{OptionId: "allow_always", Kind: acpsdk.PermissionOptionKindAllowAlways, Name: "Allow always"},
		{OptionId: "deny", Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "Deny"},
	}
}

func (a *fakeHermesAgent) Prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	if a.responseLog == "" {
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	// Emulates Hermes' approval cache (tools/approval.py is_approved): a
	// persisted session/always grant suppresses the next request entirely.
	a.mu.Lock()
	approved := a.approved[dangerousCommand]
	a.mu.Unlock()
	if !approved {
		response, err := a.conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
			SessionId: params.SessionId,
			ToolCall: acpsdk.ToolCallUpdate{
				ToolCallId: "perm-check-1", Kind: acpsdk.Ptr(acpsdk.ToolKindExecute),
				Title: acpsdk.Ptr(dangerousCommand),
			},
			Options: hermesAdvertisedPermissionOptions(),
		})
		if err != nil {
			return acpsdk.PromptResponse{}, err
		}
		selected := ""
		if response.Outcome.Selected != nil {
			selected = string(response.Outcome.Selected.OptionId)
		}
		if err := a.recordResponse(selected); err != nil {
			return acpsdk.PromptResponse{}, err
		}
		// _persist_choice keeps only the session/always grants; allow_once asks
		// again on the next identical command.
		if selected == "allow_session" || selected == "allow_always" {
			a.mu.Lock()
			a.approved[dangerousCommand] = true
			a.mu.Unlock()
		}
	}
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

func (a *fakeHermesAgent) recordResponse(optionID string) error {
	a.mu.Lock()
	a.responses = append(a.responses, optionID)
	raw, err := json.Marshal(a.responses)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(a.responseLog, raw, 0o600)
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
