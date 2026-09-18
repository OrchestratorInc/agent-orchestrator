package opencodeacp

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/ptyexec"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestMain lets this test binary stand in for the `ao` executable when the
// persistent chat host re-executes os.Executable(). Without it, Resume spawns a
// detached copy of the test binary that re-runs the suite instead of publishing
// host.json, and the resume fails with a missing descriptor. This mirrors the
// same hook in the acp package's driver_test.go.
func TestMain(m *testing.M) {
	if len(os.Args) >= 7 && os.Args[1] == "chat-host" {
		protocol := persistenthost.ProtocolRaw
		fingerprint := ""
		separator := 5
		if os.Args[5] == string(persistenthost.ProtocolACP) {
			protocol = persistenthost.ProtocolACP
			if len(os.Args) > 6 {
				fingerprint = os.Args[6]
			}
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
	os.Exit(m.Run())
}

// TestLiveOpenCodeTUIToChatHandoff is the reverse-direction proof that PR #3703
// was closed for lacking: an id created by OpenCode's *terminal* surface must be
// loadable over ACP, replay its transcript, and still accept a live prompt.
//
// The existing assertLiveTUIRestore covers the opposite direction (an ACP id
// resumed by `opencode --session`). Both are required before the OpenCode
// adapter may claim ports.AgentInterfaceHandoff, because the capability asserts
// that one native conversation is addressable from both surfaces.
//
// A passing run establishes three separate facts, in order:
//
//  1. the TUI wrote a durable native session for the workspace;
//  2. ACP session/load accepted that id and replayed the terminal turn;
//  3. a prompt on the loaded session answered from the terminal's context,
//     which distinguishes a resumable adoption from a read-only playback.
//
// Run explicitly with AO_LIVE_OPENCODE_ACP=1. It drives the user's own OpenCode
// executable, configuration, provider, and credentials; CI never depends on any
// of them.
func TestLiveOpenCodeTUIToChatHandoff(t *testing.T) {
	if os.Getenv("AO_LIVE_OPENCODE_ACP") != "1" {
		t.Skip("set AO_LIVE_OPENCODE_ACP=1 to run against the local OpenCode account")
	}

	plugin := opencode.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	driver := New(plugin, nil)
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	// A per-run marker keeps the assertions immune to transcripts left behind by
	// earlier runs against the same OpenCode store.
	marker := fmt.Sprintf("AOHANDOFF%d", time.Now().UnixNano())
	workspace := t.TempDir()
	dataDir := t.TempDir()

	beforeLaunch := time.Now().Add(-1 * time.Minute)
	launchOpenCodeTUI(ctx, t, plugin, workspace, dataDir, marker)

	// Step 1: the terminal surface must have persisted a native session. Reading
	// OpenCode's own store (rather than AO's hook pipeline) keeps this test
	// scoped to the provider-compatibility question the capability turns on.
	nativeID := latestOpenCodeSessionID(t, workspace, beforeLaunch)
	t.Logf("TUI-created native session id: %s", nativeID)

	// Step 2: ACP must adopt that exact id and replay the terminal's turn.
	conversation, err := driver.Resume(ctx, ports.ChatResumeConfig{
		SessionID:              "live-opencode-tui-to-chat",
		ProviderConversationID: nativeID,
		DataDir:                dataDir,
		WorkspacePath:          workspace,
		Env:                    envMap(),
		SystemPrompt:           "Answer in one short sentence.",
	})
	if err != nil {
		t.Fatalf("Resume(%q) — ACP refused the TUI-created id: %v", nativeID, err)
	}
	defer conversation.(ports.ChatProviderTerminator).Terminate()

	reader, ok := conversation.(ports.ChatHistoryReader)
	if !ok {
		t.Fatal("resumed conversation does not implement ChatHistoryReader, so native history cannot be imported")
	}
	history, err := reader.ReadHistory(ctx)
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	replay := historyText(history)
	if !strings.Contains(replay, marker) {
		t.Fatalf("ACP replay is missing the terminal turn %q; got %d events: %q",
			marker, len(history), truncate(replay, 1500))
	}
	t.Logf("ACP session/load replayed %d events containing the terminal turn", len(history))

	// Step 3: the adopted session must still be live. Answering from the
	// terminal-era context is what separates a resumable conversation from a
	// transcript AO could merely have read off disk.
	answer := sendLiveTurn(ctx, t, conversation,
		"What was the exact marker word I gave you earlier? Reply with just that word.")
	if !strings.Contains(answer, marker) {
		t.Fatalf("loaded session did not answer from the terminal context: want %q, got %q",
			marker, truncate(answer, 500))
	}
	t.Logf("reverse handoff proven: TUI id %s replayed and answered %q over ACP", nativeID, marker)
}

// launchOpenCodeTUI runs the adapter-produced interactive command on AO's shared
// PTY path and waits for the agent to echo the marker, which is the only signal
// that a turn actually settled and a session was written.
func launchOpenCodeTUI(
	ctx context.Context,
	t *testing.T,
	plugin *opencode.Plugin,
	workspace, dataDir, marker string,
) {
	t.Helper()

	// OpenCode has no system-prompt flag, so its adapter requires the prompt file
	// that backs the generated agent config.
	promptDir := t.TempDir()
	promptFile := filepath.Join(promptDir, "system-prompt.md")
	systemPrompt := "You are running inside an AO live test. Answer briefly."
	if err := os.WriteFile(promptFile, []byte(systemPrompt), 0o600); err != nil {
		t.Fatalf("write system prompt file: %v", err)
	}

	cmd, err := plugin.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:        "live-opencode-tui-source",
		DataDir:          dataDir,
		WorkspacePath:    workspace,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: promptFile,
		Prompt: fmt.Sprintf(
			"Remember this exact marker word: %s. Reply with just the marker word and nothing else.",
			marker),
	})
	if err != nil {
		t.Fatalf("GetLaunchCommand: %v", err)
	}
	t.Logf("TUI launch argv: %v", cmd)

	t.Chdir(workspace)
	stream, err := ptyexec.Spawn(ctx, cmd, liveTUIEnv(), 40, 120)
	if err != nil {
		t.Fatalf("spawn OpenCode TUI: %v", err)
	}
	defer stream.Close()

	// The TUI paints the marker as it echoes the prompt, so wait for a second
	// occurrence: the first is the user's own line, the second is the answer.
	output, err := readUntilNthOccurrence(stream, marker, 2, 5*time.Minute)
	if err != nil {
		t.Fatalf("OpenCode TUI never completed a turn: %v; output=%q", err, truncate(output, 2000))
	}
}

// latestOpenCodeSessionID returns the newest session OpenCode recorded for the
// workspace. OpenCode 1.18 keeps sessions in a single SQLite store shared by
// every surface, which is the property the handoff capability depends on.
func latestOpenCodeSessionID(t *testing.T, workspace string, notBefore time.Time) string {
	t.Helper()

	dbPath := filepath.Join(openCodeDataDir(t), "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("OpenCode store not found at %s: %v", dbPath, err)
	}

	// Copy the store (with its WAL) so a live OpenCode process cannot make the
	// read fail or observe a partially checkpointed database.
	snapshot := filepath.Join(t.TempDir(), "opencode.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyIfExists(dbPath+suffix, snapshot+suffix); err != nil {
			t.Fatalf("snapshot OpenCode store: %v", err)
		}
	}

	db, err := sql.Open("sqlite", snapshot)
	if err != nil {
		t.Fatalf("open OpenCode store: %v", err)
	}
	defer func() { _ = db.Close() }()

	// OpenCode stores the workspace with forward slashes on every platform.
	want := strings.ReplaceAll(workspace, `\`, "/")
	rows, err := db.Query(
		`SELECT id, directory, time_created FROM session ORDER BY time_created DESC LIMIT 50`)
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	defer func() { _ = rows.Close() }()

	floor := notBefore.UnixMilli()
	for rows.Next() {
		var id, directory string
		var created int64
		if err := rows.Scan(&id, &directory, &created); err != nil {
			t.Fatalf("scan session row: %v", err)
		}
		if created < floor {
			continue
		}
		if sameDir(directory, want) {
			return id
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sessions: %v", err)
	}
	t.Fatalf("OpenCode recorded no session for %s after the TUI launch", want)
	return ""
}

func sendLiveTurn(
	ctx context.Context,
	t *testing.T,
	conversation ports.ChatConversation,
	text string,
) string {
	t.Helper()

	ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
		Text: text, ClientMessageID: "live-reverse-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if starter, ok := conversation.(ports.ChatDeferredTurnStarter); ok {
		if err := starter.StartDeferredTurn(ref.ProviderTurnID); err != nil {
			t.Fatalf("StartDeferredTurn: %v", err)
		}
	}

	var answer strings.Builder
	for {
		select {
		case event, ok := <-conversation.Events():
			if !ok {
				t.Fatalf("controller closed before completion; answer=%q", answer.String())
			}
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				answer.WriteString(event.Delta)
			case ports.ChatEventTurnCompleted:
				if event.TurnState != domain.TurnStateCompleted {
					t.Fatalf("turn state = %q; answer=%q", event.TurnState, answer.String())
				}
				return answer.String()
			}
		case <-ctx.Done():
			t.Fatalf("live turn timed out: %v; answer=%q", ctx.Err(), answer.String())
		}
	}
}

func historyText(events []ports.ChatEvent) string {
	var out strings.Builder
	for _, event := range events {
		out.WriteString(event.Text)
		out.WriteString(event.Delta)
		out.WriteString("\n")
	}
	return out.String()
}

// readUntilNthOccurrence waits for marker to appear n times, so a caller can
// skip the terminal's echo of its own prompt.
func readUntilNthOccurrence(
	stream ports.Stream,
	marker string,
	n int,
	timeout time.Duration,
) (string, error) {
	type result struct {
		output string
		err    error
	}
	results := make(chan result, 1)
	go func() {
		var output bytes.Buffer
		buf := make([]byte, 4096)
		for {
			read, err := stream.Read(buf)
			if read > 0 {
				output.Write(buf[:read])
				if bytes.Count(output.Bytes(), []byte(marker)) >= n {
					results <- result{output: output.String()}
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				results <- result{output: output.String(), err: err}
				return
			}
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case got := <-results:
		if strings.Count(got.output, marker) < n {
			if got.err == nil {
				got.err = fmt.Errorf("terminal closed before %q appeared %d times", marker, n)
			}
			return got.output, got.err
		}
		return got.output, nil
	case <-timer.C:
		_ = stream.Close()
		got := <-results
		return got.output, fmt.Errorf("timed out waiting for %q to appear %d times", marker, n)
	}
}

func liveTUIEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, pair := range os.Environ() {
		if strings.HasPrefix(pair, "TERM=") || strings.HasPrefix(pair, "COLORTERM=") ||
			strings.HasPrefix(pair, "NO_COLOR=") {
			continue
		}
		env = append(env, pair)
	}
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
}

func openCodeDataDir(t *testing.T) string {
	t.Helper()
	if dir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return filepath.Join(dir, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home dir: %v", err)
	}
	return filepath.Join(home, ".local", "share", "opencode")
}

func copyIfExists(src, dst string) error {
	data, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func sameDir(a, b string) bool {
	norm := func(s string) string {
		s = strings.ReplaceAll(s, `\`, "/")
		s = strings.TrimSuffix(s, "/")
		return strings.ToLower(s)
	}
	return norm(a) == norm(b)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
