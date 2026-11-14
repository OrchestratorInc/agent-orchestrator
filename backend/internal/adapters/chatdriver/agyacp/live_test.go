package agyacp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestLiveAntigravityACPConformance is the registration gate for this binding.
// Run explicitly with AO_LIVE_AGY=1 and AO_AGY_ACP_SERVER_PATH (or a managed
// copy). It uses the operator's Google sign-in through a private GEMINI_HOME, so
// it never writes the operator's ~/.gemini. CI never depends on it.
//
// It proves, against the real server: standing instructions reach the model;
// Reject blocks a tool; yolo runs one without asking; a later switch back to
// default asks again (no cached allow_always grant); interrupt ends a turn and
// the conversation continues; and a closed conversation resumes with context.
func TestLiveAntigravityACPConformance(t *testing.T) {
	if os.Getenv("AO_LIVE_AGY") != "1" {
		t.Skip("set AO_LIVE_AGY=1 to run against the local Antigravity ACP server and Google sign-in")
	}
	geminiHome := privateGeminiHome(t)
	t.Setenv("GEMINI_HOME", geminiHome)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	driver := New(agy.New(), nil)
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	workspace := t.TempDir()
	dataDir := t.TempDir()
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: "live-agy-acp", DataDir: dataDir, WorkspacePath: workspace,
		Env: envMap(), Permissions: ports.PermissionModeDefault,
		SystemPrompt: "Standing rule from AO: end every reply with the single word BANANA.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Only the separate server needs AO's sign-in bootstrap; `agy acp` reuses
	// the CLI's login.
	if target, err := newResolver(agy.New().ResolveBinary).target(ctx); err == nil && !target.cli {
		if _, err := os.Stat(filepath.Join(geminiHome, "antigravity-acp", "settings.json")); err != nil {
			t.Fatalf("auth bootstrap did not persist the sign-in choice: %v", err)
		}
	}

	t.Run("standing instructions", func(t *testing.T) {
		// agy acp inspects the workspace first unless told not to; tools are not
		// what this checks.
		turn := runTurn(ctx, t, conversation, "Do not use any tools. Reply with exactly: pong", ports.ChatTurnSettings{}, nil)
		if !strings.Contains(turn.answer, "BANANA") {
			t.Fatalf("answer %q ignored the standing instructions", turn.answer)
		}
	})

	t.Run("reject blocks the tool", func(t *testing.T) {
		turn := runTurn(ctx, t, conversation, "Run exactly this shell command and nothing else: touch rejected.txt",
			ports.ChatTurnSettings{}, decide(ports.ChatDecisionRejectOnce))
		if turn.approvals == 0 {
			t.Fatal("default mode ran a shell command without asking")
		}
		assertExists(t, filepath.Join(workspace, "rejected.txt"), false)
	})

	t.Run("yolo runs without asking", func(t *testing.T) {
		turn := runTurn(ctx, t, conversation, "Run exactly this shell command and nothing else: touch yolo.txt",
			ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions}, decide(ports.ChatDecisionRejectOnce))
		if turn.approvals != 0 {
			t.Fatalf("bypass asked %d times", turn.approvals)
		}
		assertExists(t, filepath.Join(workspace, "yolo.txt"), true)
	})

	t.Run("downgrade asks again", func(t *testing.T) {
		turn := runTurn(ctx, t, conversation, "Run exactly this shell command and nothing else: touch downgraded.txt",
			ports.ChatTurnSettings{Approval: ports.PermissionModeDefault}, decide(ports.ChatDecisionAllowOnce))
		if turn.approvals == 0 {
			t.Fatal("switching back to default did not re-enable approvals")
		}
		assertExists(t, filepath.Join(workspace, "downgraded.txt"), true)
	})

	t.Run("interrupt then continue", func(t *testing.T) {
		ref := sendTurn(ctx, t, conversation, "Write a detailed 1500-word essay about compilers. Do not use tools.", ports.ChatTurnSettings{})
		interrupted := false
		for !interrupted {
			event := nextEvent(ctx, t, conversation)
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				if err := conversation.Interrupt(ctx, ref.ProviderTurnID); err != nil {
					t.Fatalf("Interrupt: %v", err)
				}
				interrupted = true
			case ports.ChatEventTurnCompleted:
				t.Fatalf("turn finished (%q) before it could be interrupted", event.TurnState)
			}
		}
		for {
			event := nextEvent(ctx, t, conversation)
			if event.Kind == ports.ChatEventTurnCompleted {
				if event.TurnState == domain.TurnStateCompleted {
					t.Fatal("interrupted turn reported completed")
				}
				break
			}
		}
		runTurn(ctx, t, conversation, "Reply with exactly: still here", ports.ChatTurnSettings{}, nil)
	})

	providerID := conversation.ProviderConversationID()
	resumeConfig := ports.ChatResumeConfig{
		SessionID: "live-agy-acp", ProviderConversationID: providerID, DataDir: dataDir,
		WorkspacePath: workspace, Env: envMap(), Permissions: ports.PermissionModeDefault,
		SystemPrompt: "Standing rule from AO: end every reply with the single word BANANA.",
	}

	t.Run("resume after the provider exits", func(t *testing.T) {
		terminator, ok := conversation.(ports.ChatProviderTerminator)
		if !ok {
			t.Fatal("conversation cannot terminate its provider")
		}
		if err := terminator.Terminate(); err != nil {
			t.Fatalf("Terminate: %v", err)
		}
		resumed, err := driver.Resume(ctx, resumeConfig)
		if err != nil {
			t.Fatalf("Resume (session/load): %v", err)
		}
		defer func() { _ = resumed.Close() }()
		turn := runTurn(ctx, t, resumed, "Which file names did you create earlier in this conversation? Answer briefly.",
			ports.ChatTurnSettings{}, nil)
		if !strings.Contains(turn.answer, "yolo") {
			t.Fatalf("resumed answer %q lost the earlier context", turn.answer)
		}
	})
}

// TestLiveAgyACPSteersARunningTurn proves steering against the real `agy acp`
// (agy CLI 1.5.4 or newer). Run explicitly with AO_LIVE_AGY=1. It uses an empty
// private GEMINI_HOME (the CLI's login lives outside it) and the default
// approval mode: it approves the one sleep command and requests that stay in
// the test's temporary folders, and rejects anything else. While that command runs it steers, then
// checks that the turn took the guidance and AO saw one turn, not two.
func TestLiveAgyACPSteersARunningTurn(t *testing.T) {
	if os.Getenv("AO_LIVE_AGY") != "1" {
		t.Skip("set AO_LIVE_AGY=1 to run against the local agy CLI and Google sign-in")
	}
	t.Setenv("GEMINI_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	driver := New(agy.New(), nil)
	workspace := t.TempDir()
	for _, name := range []string{"prod.yaml", "staging.yaml"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("timeout: 30\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: "live-agy-steer", DataDir: t.TempDir(), WorkspacePath: workspace,
		Env: envMap(), Permissions: ports.PermissionModeDefault,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// End the provider too: a closed conversation leaves its persistent host and
	// agy running for a later resume.
	defer func() {
		if terminator, ok := conversation.(ports.ChatProviderTerminator); ok {
			_ = terminator.Terminate()
		}
		_ = conversation.Close()
	}()
	if !conversation.Capabilities()[ports.ChatCapabilitySteer] {
		t.Skip("the installed agy has no `agy acp` that steers (needs agy 1.5.4 or newer)")
	}
	steerer, ok := conversation.(ports.ChatSteerer)
	if !ok {
		t.Fatal("conversation cannot steer")
	}

	ref := sendTurn(ctx, t, conversation,
		"In the shell, run exactly this command and wait for it: "+liveSteerCommand+"\n"+
			"After it finishes, reply with one short sentence naming the file you will edit next. "+
			"prod.yaml and staging.yaml are both in the current folder; do not look for them. "+
			"The plan is to edit prod.yaml. Do not edit any file.", ports.ChatTurnSettings{})

	steered := make(chan error, 1)
	steering := false
	var reply strings.Builder
	var completions int
	for completions == 0 {
		event := nextEvent(ctx, t, conversation)
		switch event.Kind {
		case ports.ChatEventMessageDelta:
			reply.WriteString(event.Delta)
		case ports.ChatEventApprovalRequested:
			decision := decide(ports.ChatDecisionRejectOnce)(event)
			if liveSteerApproves(event.Summary, workspace) {
				decision = decide(ports.ChatDecisionAllowOnce)(event)
			}
			t.Logf("approval %s %q -> %s", event.ActivityKind, event.Summary, decision.ID)
			if err := conversation.ResolveRequest(ctx, event.RequestID, decision); err != nil {
				t.Fatalf("ResolveRequest: %v", err)
			}
		case ports.ChatEventActivityStarted:
			t.Logf("tool started: %s %s", event.ActivityKind, event.Summary)
			if !steering && strings.Contains(event.Summary+string(event.Detail), "sleep 20") {
				steering = true
				go func() {
					time.Sleep(4 * time.Second)
					_, err := steerer.Steer(ctx, ref.ProviderTurnID, ports.ChatUserMessage{
						Text: "STEER-ME: change of plan, edit staging.yaml instead of prod.yaml. " +
							"End your final reply with NEXT-FILE-STAGING if the file you will edit next is " +
							"staging.yaml, or NEXT-FILE-PROD if it is prod.yaml.",
						ClientMessageID: "live-steer", Origin: domain.MessageOriginHuman,
					})
					steered <- err
				}()
			}
		case ports.ChatEventTurnCompleted:
			completions++
			if event.TurnState != domain.TurnStateCompleted {
				t.Fatalf("turn state = %q; reply=%q; err=%v", event.TurnState, reply.String(), event.Err)
			}
		}
	}
	if !steering {
		t.Fatalf("the sleep command never started; reply=%q", reply.String())
	}
	if err := <-steered; err != nil {
		t.Fatalf("Steer: %v", err)
	}
	answer := reply.String()
	// The markers exist only in the steer, so either one shows the model read
	// it, and the one it picks says which file it chose. agy streams other text
	// before the answer, so the reply as a whole is not compared.
	if !strings.Contains(answer, "NEXT-FILE-STAGING") || strings.Contains(answer, "NEXT-FILE-PROD") ||
		!strings.Contains(strings.ToLower(answer), "staging.yaml") {
		t.Fatalf("the turn did not take the guidance: reply=%q", answer)
	}
	t.Logf("reply: %q", answer)
	// Nothing else may complete: the handoff was not a second turn.
	quiet := time.After(3 * time.Second)
	for waiting := true; waiting; {
		select {
		case event := <-conversation.Events():
			if event.Kind == ports.ChatEventTurnCompleted {
				t.Fatalf("a second turn completed after the steer: %+v", event)
			}
		case <-quiet:
			waiting = false
		}
	}
}

// liveSteerCommand is the slow command the live steering test has the agent
// run, and the only command it approves outright.
const liveSteerCommand = "sleep 20; echo slow-check-done"

// liveSteerApproves reports whether the live steering test approves a request:
// exactly liveSteerCommand, or a read that stays in its temporary folders.
func liveSteerApproves(summary, workspace string) bool {
	return summary == liveSteerCommand || staysInTestFolders(summary, workspace)
}

// staysInTestFolders reports whether an approval touches only the test's
// temporary folders: a read-only command run in them, or a file under them,
// such as agy reading its own log of the backgrounded command. The model
// sometimes takes such a look after the steer, and rejecting it ends the turn.
func staysInTestFolders(summary, workspace string) bool {
	root := filepath.Dir(workspace)
	roots := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		roots = append(roots, resolved)
	}
	under := func(path string) bool {
		if strings.HasPrefix(path, "~") {
			return false // the shell expands it to the real home
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		path = filepath.Clean(path)
		for _, r := range roots {
			if path == r || strings.HasPrefix(path, r+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	if strings.ContainsAny(summary, "\r\n") {
		return false // a second line would be a second command
	}
	if file, ok := strings.CutPrefix(summary, "File "); ok {
		return under(strings.TrimSpace(file))
	}
	fields := strings.Fields(summary)
	if len(fields) == 0 || strings.ContainsAny(summary, ";|&<>`$()") {
		return false
	}
	switch fields[0] {
	case "ls", "find", "cat", "head", "tail", "pwd", "wc":
	default:
		return false
	}
	for _, arg := range fields[1:] {
		if !strings.HasPrefix(arg, "-") && !under(arg) {
			return false
		}
	}
	return true
}

// TestLiveAntigravityACPAndCLIShareConversationAcrossCopiedDatabases verifies
// the provider's shared UUID/schema claim across its separate storage roots.
// The test copies the closed SQLite file in each direction, then exercises the
// real `agy --conversation` and ACP `session/load` resume paths. AO's automatic
// database bridge is a separate implementation step.
func TestLiveAntigravityACPAndCLIShareConversationAcrossCopiedDatabases(t *testing.T) {
	if os.Getenv("AO_LIVE_AGY") != "1" {
		t.Skip("set AO_LIVE_AGY=1 to run against the local Antigravity ACP server, CLI, and Google sign-in")
	}
	geminiHome := privateGeminiHome(t)
	t.Setenv("GEMINI_HOME", geminiHome)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	driver := New(agy.New(), nil)
	workspace := t.TempDir()
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: "live-agy-cross-interface", DataDir: t.TempDir(), WorkspacePath: workspace,
		Env: envMap(), Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Start ACP conversation: %v", err)
	}
	defer func() { _ = conversation.Close() }()
	providerID := conversation.ProviderConversationID()
	marker := "AGY_CROSS_INTERFACE_MARKER_" + time.Now().Format("150405.000000")
	runTurn(ctx, t, conversation, "Remember this exact marker for later: "+marker,
		ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions}, nil)

	terminator, ok := conversation.(ports.ChatProviderTerminator)
	if !ok {
		t.Fatal("conversation cannot terminate its ACP provider")
	}
	if err := terminator.Terminate(); err != nil {
		t.Fatalf("Terminate ACP provider: %v", err)
	}
	if err := conversation.Close(); err != nil {
		t.Fatalf("Close ACP conversation: %v", err)
	}

	acpDB := filepath.Join(geminiHome, "antigravity-acp", "conversations", providerID+".db")
	cliDB := filepath.Join(geminiHome, "antigravity-cli", "conversations", providerID+".db")
	copyConversationDB(t, acpDB, cliDB)

	command, resumable, err := agy.New().GetRestoreCommand(ctx, ports.RestoreConfig{
		Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: providerID}},
	})
	if err != nil || !resumable {
		t.Fatalf("GetRestoreCommand = (%v, %v), want resumable command", err, resumable)
	}
	command = append(command, "--print", "Repeat the exact marker I asked you to remember.")
	cli := exec.CommandContext(ctx, command[0], command[1:]...)
	cli.Env = append(os.Environ(), "GEMINI_HOME="+geminiHome)
	output, err := cli.CombinedOutput()
	if err != nil {
		t.Fatalf("agy --conversation failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), marker) {
		t.Fatalf("agy did not resume ACP history: output %q does not contain marker %q", output, marker)
	}

	copyConversationDB(t, cliDB, acpDB)
	resumed, err := driver.Resume(ctx, ports.ChatResumeConfig{
		SessionID: "live-agy-cross-interface", ProviderConversationID: providerID,
		WorkspacePath: workspace, Env: envMap(), Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("ACP session/load after terminal write: %v", err)
	}
	defer func() { _ = resumed.Close() }()
	turn := runTurn(ctx, t, resumed, "Repeat the exact marker I asked you to remember.",
		ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions}, nil)
	if !strings.Contains(turn.answer, marker) {
		t.Fatalf("ACP session/load did not resume the copied terminal database: answer %q lacks marker %q", turn.answer, marker)
	}
}

func copyConversationDB(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read conversation database %s: %v", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("create conversation directory: %v", err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		t.Fatalf("write conversation database %s: %v", destination, err)
	}
}

type liveTurn struct {
	answer    string
	approvals int
}

func runTurn(
	ctx context.Context,
	t *testing.T,
	conversation ports.ChatConversation,
	text string,
	settings ports.ChatTurnSettings,
	answer func(ports.ChatEvent) ports.ChatDecision,
) liveTurn {
	t.Helper()
	sendTurn(ctx, t, conversation, text, settings)
	var turn liveTurn
	var reply strings.Builder
	for {
		event := nextEvent(ctx, t, conversation)
		switch event.Kind {
		case ports.ChatEventMessageDelta:
			reply.WriteString(event.Delta)
		case ports.ChatEventActivityStarted:
			t.Logf("tool: %s %s", event.ActivityKind, event.Summary)
		case ports.ChatEventApprovalRequested:
			turn.approvals++
			t.Logf("approval requested: %s | %.300s", event.Summary, event.Detail)
			decision := decide(ports.ChatDecisionRejectOnce)(event)
			if answer != nil {
				decision = answer(event)
			}
			if err := conversation.ResolveRequest(ctx, event.RequestID, decision); err != nil {
				t.Fatalf("ResolveRequest: %v", err)
			}
		case ports.ChatEventTurnCompleted:
			if event.TurnState != domain.TurnStateCompleted {
				t.Fatalf("turn state = %q; reply=%q; text=%q; err=%v", event.TurnState, reply.String(), event.Text, event.Err)
			}
			turn.answer = reply.String()
			return turn
		}
	}
}

func sendTurn(
	ctx context.Context,
	t *testing.T,
	conversation ports.ChatConversation,
	text string,
	settings ports.ChatTurnSettings,
) ports.ChatTurnRef {
	t.Helper()
	ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
		Text: text, ClientMessageID: "live-" + time.Now().Format("150405.000000"),
		Origin: domain.MessageOriginHuman, Settings: settings,
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if starter, ok := conversation.(ports.ChatDeferredTurnStarter); ok {
		if err := starter.StartDeferredTurn(ref.ProviderTurnID); err != nil {
			t.Fatalf("StartDeferredTurn: %v", err)
		}
	}
	return ref
}

func nextEvent(ctx context.Context, t *testing.T, conversation ports.ChatConversation) ports.ChatEvent {
	t.Helper()
	select {
	case event, ok := <-conversation.Events():
		if !ok {
			t.Fatal("controller closed")
		}
		if event.Kind == ports.ChatEventError {
			t.Fatalf("controller error: %v", event.Err)
		}
		// The daemon acknowledges each persisted event; the persistent host
		// refuses the next prompt until the previous one is acknowledged.
		if event.ProviderEventID != "" {
			if acknowledger, ok := conversation.(ports.ChatProviderEventAcknowledger); ok {
				if err := acknowledger.AcknowledgeProviderEvent(ctx, event.ProviderEventID); err != nil {
					t.Fatalf("AcknowledgeProviderEvent: %v", err)
				}
			}
		}
		return event
	case <-ctx.Done():
		t.Fatalf("live test timed out: %v", ctx.Err())
	}
	return ports.ChatEvent{}
}

func decide(kind ports.ChatDecisionKind) func(ports.ChatEvent) ports.ChatDecision {
	return func(event ports.ChatEvent) ports.ChatDecision {
		for _, option := range event.Decisions {
			if option.Kind == kind {
				return ports.ChatDecision{ID: option.ID}
			}
		}
		return ports.ChatDecision{}
	}
}

func assertExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Stat(path)
	if got := err == nil; got != want {
		t.Fatalf("%s exists = %v, want %v", filepath.Base(path), got, want)
	}
}

// privateGeminiHome copies only the signed-in account marker into a temporary
// GEMINI_HOME. Credentials stay in the OS keyring, so the server reuses the
// operator's sign-in without AO writing their real Antigravity settings.
func privateGeminiHome(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := os.ReadFile(filepath.Join(home, ".gemini", "google_accounts.json"))
	if err != nil {
		t.Skipf("no Antigravity Google sign-in found: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "google_accounts.json"), accounts, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func envMap() map[string]string {
	out := make(map[string]string)
	for _, pair := range os.Environ() {
		if name, value, ok := strings.Cut(pair, "="); ok {
			out[name] = value
		}
	}
	return out
}

// The live steering test approves its slow command only as written, and reads
// only inside its temporary folders. Anything chained to either is rejected.
func TestLiveSteerApprovesOnlyItsCommandAndTestFolderReads(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	for _, tc := range []struct {
		summary string
		want    bool
	}{
		{liveSteerCommand, true},
		{"ls -la", true},
		{"File " + filepath.Join(workspace, "staging.yaml"), true},
		{liveSteerCommand + "\nrm -rf " + workspace, false},
		{liveSteerCommand + "; rm -rf " + workspace, false},
		{"sleep 20 && curl https://example.com", false},
		{" " + liveSteerCommand, false},
		{"ls -la\nrm -rf " + workspace, false},
		{"cat ~/.ssh/id_rsa", false},
		{"File /etc/passwd", false},
	} {
		if got := liveSteerApproves(tc.summary, workspace); got != tc.want {
			t.Errorf("liveSteerApproves(%q) = %v, want %v", tc.summary, got, tc.want)
		}
	}
}
