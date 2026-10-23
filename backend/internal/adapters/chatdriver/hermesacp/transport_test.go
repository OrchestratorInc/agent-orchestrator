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
	// fakeAgentLiveReplies switches the fake into the replay-test persona: live
	// prompts stream reply text, and a load_session replays the persisted shape.
	fakeAgentLiveReplies = "AO_TEST_HERMES_ACP_LIVE_REPLIES"
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

// Killing the chat host and resuming replays Hermes' persisted transcript in a
// different shape than the live stream: id-less chunks, stripped reply text,
// and the failed-turn boundary row on the interrupted turn. The reconciler
// must produce the two stored turns without duplicating the reply or importing
// the boundary notice as a reply.
func TestHermesACPTransportReplayAfterHostDeathDoesNotDuplicateTurns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	modeLog := filepath.Join(t.TempDir(), "modes.json")
	binary := writeHermesACPExecutable(t, modeLog, "")
	t.Setenv(fakeAgentLiveReplies, "1")
	driver := newDriver(
		&fakePlugin{status: ports.AgentAuthStatusAuthorized, binary: binary},
		nil,
		nil,
	)

	ctx := context.Background()
	start := ports.ChatStartConfig{
		SessionID: "hermes-acp-replay", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeDefault,
	}
	conversation, err := driver.Start(ctx, start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	startTurn := func(text string) ports.ChatTurnRef {
		t.Helper()
		ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{Text: text})
		if err != nil {
			t.Fatalf("SendTurn: %v", err)
		}
		if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
			t.Fatalf("StartDeferredTurn: %v", err)
		}
		return ref
	}

	startTurn("What is X?")
	finishTurn(t, conversation, conversation.Events(), domain.TurnStateCompleted)

	// Interrupt the second turn once its partial reply streamed, the way a
	// host kill or a user interrupt leaves it in Hermes' transcript.
	ref := startTurn("Do turn two.")
	deadline := time.After(30 * time.Second)
	interrupted := false
events:
	for {
		select {
		case event := <-conversation.Events():
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				if err := conversation.Interrupt(ctx, ref.ProviderTurnID); err != nil {
					t.Fatalf("Interrupt: %v", err)
				}
			case ports.ChatEventTurnCompleted:
				if event.TurnState != domain.TurnStateInterrupted {
					t.Fatalf("turn state = %q, want interrupted (err=%v)", event.TurnState, event.Err)
				}
				if err := conversation.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(
					ctx, event.ProviderEventID); err != nil {
					t.Fatal(err)
				}
				interrupted = true
				break events
			}
		case <-deadline:
			t.Fatal("timed out waiting for the interrupted turn to settle")
		}
	}
	if !interrupted {
		t.Fatal("second turn did not settle interrupted")
	}
	providerID := conversation.ProviderConversationID()
	if err := conversation.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	resumed, err := driver.Resume(ctx, ports.ChatResumeConfig{
		SessionID: start.SessionID, ProviderConversationID: providerID,
		DataDir: start.DataDir, WorkspacePath: start.WorkspacePath,
		Permissions: start.Permissions, HasPriorTurns: true,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = resumed.(ports.ChatProviderTerminator).Terminate() }()
	history, err := resumed.(ports.ChatHistoryReader).ReadHistory(ctx)
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}

	var userTexts, replies, turnStates []string
	for _, event := range history {
		switch event.Kind {
		case ports.ChatEventUserMessageCompleted:
			userTexts = append(userTexts, event.Text)
		case ports.ChatEventMessageCompleted:
			replies = append(replies, event.Text)
		case ports.ChatEventTurnCompleted:
			turnStates = append(turnStates, string(event.TurnState))
		}
	}
	wantUsers := []string{"What is X?", "Do turn two."}
	if len(userTexts) != len(wantUsers) {
		t.Fatalf("replayed user messages = %q, want exactly %q", userTexts, wantUsers)
	}
	for i, text := range wantUsers {
		if userTexts[i] != text {
			t.Fatalf("replayed user messages = %q, want %q", userTexts, wantUsers)
		}
	}
	if len(replies) != 1 || replies[0] != "Answer one." {
		t.Fatalf("replayed replies = %q, want exactly [Answer one.] with no boundary notice", replies)
	}
	if len(turnStates) != 2 {
		t.Fatalf("replayed turns = %d, want 2", len(turnStates))
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
	agent := &fakeHermesAgent{
		modeLog: modeLog, responseLog: responseLog,
		liveReplies: os.Getenv(fakeAgentLiveReplies) == "1",
		approved:    map[string]bool{},
	}
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
	liveReplies bool

	mu        sync.Mutex
	modes     []string
	responses []string
	prompts   int

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
		// Hermes advertises load_session (acp_adapter/server.py initialize).
		AgentCapabilities: acpsdk.AgentCapabilities{
			LoadSession:         true,
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
	if a.liveReplies {
		return a.livePrompt(ctx, params)
	}
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

// livePrompt streams replies the way Hermes' live session does: chunks carry
// UUID-shaped message ids and the model's raw text, boundary whitespace
// included. The second prompt never finishes, so the turn it belongs to
// settles interrupted.
func (a *fakeHermesAgent) livePrompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	a.mu.Lock()
	n := a.prompts
	a.prompts++
	a.mu.Unlock()
	if n > 0 {
		_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: params.SessionId,
			Update:    acpsdk.UpdateAgentMessageText("partial "),
		})
		<-ctx.Done()
		return acpsdk.PromptResponse{}, ctx.Err()
	}
	_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: params.SessionId,
		Update:    acpsdk.UpdateAgentMessageText("\n\nAnswer one.\n"),
	})
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

// hermesReplayUpdates mirrors acp_adapter/server.py _history_replay_updates:
// persisted text comes back as id-less user/agent chunks, flattened with a
// string strip, and an interrupted turn carries the Hermes-authored boundary
// row (agent/turn_failure_copy.py) as ordinary assistant text.
func (a *fakeHermesAgent) hermesReplayUpdates(ctx context.Context, sessionID acpsdk.SessionId) {
	updates := []acpsdk.SessionUpdate{
		acpsdk.UpdateUserMessageText("What is X?"),
		acpsdk.UpdateAgentMessageText("Answer one."),
		acpsdk.UpdateUserMessageText("Do turn two."),
		acpsdk.UpdateAgentMessageText(hermesFailedTurnNotices[0]),
	}
	for _, update := range updates {
		_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: sessionID, Update: update})
	}
}

func (a *fakeHermesAgent) ResumeSession(context.Context, acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, nil
}

func (a *fakeHermesAgent) LoadSession(ctx context.Context, params acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	// Per ACP spec Hermes streams the replayed transcript via session/update
	// before responding (acp_adapter/server.py _session_response_fields).
	if a.liveReplies {
		a.hermesReplayUpdates(ctx, params.SessionId)
	}
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
