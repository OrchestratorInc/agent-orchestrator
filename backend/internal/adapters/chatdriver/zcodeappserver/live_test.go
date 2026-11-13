package zcodeappserver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/zcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMain(m *testing.M) {
	// The persistent host launches `<os.Executable()> chat-host …`; in a test
	// binary that is this process, so it plays the host when asked (the codex
	// live test does the same).
	if len(os.Args) >= 7 && os.Args[1] == "chat-host" {
		if os.Args[5] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Env: os.Environ(), Argv: os.Args[6:], Protocol: persistenthost.ProtocolRaw,
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestLiveZcodeAppServer drives the real `zcode app-server --stdio` through
// the wrapper. It is skipped unless AO_ZCODE_LIVE=1, because it needs a local
// zcode install, a provider credential (an AO-owned personal provider config
// named by ZCODE_PERSONAL_PROVIDER_CONFIG_FILE in the environment, which the
// wrapper honours when preset), and it makes real model calls. Everything
// else in this package runs against pipes.
//
// Run it after changing the protocol layer, on a machine that has the wrapper:
//
//	AO_ZCODE_LIVE=1 go test ./internal/adapters/chatdriver/zcodeappserver/ -run Live -v
//
// AO_ZCODE_BIN overrides the wrapper path (default: `zcode` on PATH, then
// ~/.local/bin/zcode). The test never reads the credential itself; stderr of
// the provider is discarded by the driver, and nothing from the environment
// is logged.
func TestLiveZcodeAppServer(t *testing.T) {
	if os.Getenv("AO_ZCODE_LIVE") != "1" {
		t.Skip("set AO_ZCODE_LIVE=1 to run against a real zcode app-server")
	}
	bin := liveBinary(t)

	workspace := t.TempDir()
	dataDir := t.TempDir()
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "ao-live") })

	d := New(livePlugin{bin: bin, Plugin: zcode.New()}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if missing := ports.MissingProductionCapabilities(caps); len(missing) != 0 {
		t.Fatalf("missing production capabilities: %v", missing)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	conv, err := d.Start(ctx, ports.ChatStartConfig{
		SessionID:     "ao-live",
		DataDir:       dataDir,
		WorkspacePath: workspace,
		Env:           envMap(),
		Permissions:   ports.PermissionModeDefault,
		SystemPrompt:  "You are in an automated test. Answer in one short sentence and use no tools.",
		// One stdio server, so the session does not load the operator's global
		// MCP list (DESIGN-zcode-context-trim.md). `true` exits at once; the
		// server never connects and the turn runs without it.
		MCPServers: []ports.ChatMCPServerConfig{{Name: "ao-live-noop", Type: "stdio", Command: "true"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	sessionID := conv.ProviderConversationID()
	if sessionID == "" {
		t.Fatal("no provider conversation id after Start")
	}
	t.Logf("session %s", sessionID)
	const codeword = "teal-orbit-7319"

	models, err := conv.(ports.ChatModelLister).ListModels(ctx)
	if err != nil || len(models) == 0 {
		t.Fatalf("ListModels = %+v, %v", models, err)
	}
	t.Logf("models: %d, default %q efforts %v", len(models), models[0].ID, models[0].Efforts)

	answer := runLiveTurn(ctx, t, conv,
		"Remember this codeword for my next message: "+codeword+". Reply with exactly the word: acknowledged", "live-1")
	if !strings.Contains(strings.ToLower(answer), "acknowledged") {
		t.Errorf("first answer = %q, want acknowledged", answer)
	}

	hibernator := conv.(ports.ChatProviderHibernator)
	can, err := hibernator.CanHibernate(ctx)
	if err != nil || !can {
		t.Fatalf("CanHibernate = %v, %v; want true after an idle turn", can, err)
	}
	if err := hibernator.Hibernate(); err != nil {
		t.Fatalf("Hibernate: %v", err)
	}

	// Resume in a replacement process must recover the same session with its
	// memory — the daemon-restart path (p8-resume.ndjson live).
	resumed, err := d.Resume(ctx, ports.ChatResumeConfig{
		SessionID:              "ao-live",
		DataDir:                dataDir,
		ProviderConversationID: sessionID,
		WorkspacePath:          workspace,
		Env:                    envMap(),
		Permissions:            ports.PermissionModeDefault,
		HasPriorTurns:          true,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = resumed.Close() }()
	if got := resumed.ProviderConversationID(); got != sessionID {
		t.Fatalf("resumed session = %q, want %q", got, sessionID)
	}
	history, err := resumed.(ports.ChatHistoryReader).ReadHistory(ctx)
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if countKind(history, ports.ChatEventUserMessageCompleted) < 1 || countKind(history, ports.ChatEventMessageCompleted) < 1 {
		t.Errorf("history lacks the first exchange: %+v", history)
	}

	answer = runLiveTurn(ctx, t, resumed,
		"What codeword did I ask you to remember in my preceding message? Reply with only that codeword.", "live-2")
	if !strings.Contains(strings.ToLower(answer), codeword) {
		t.Fatalf("resumed session lost earlier context: answer = %q, want %q", answer, codeword)
	}
}

// runLiveTurn sends one message and returns the settled reply text.
func runLiveTurn(ctx context.Context, t *testing.T, conv ports.ChatConversation, text, clientID string) string {
	t.Helper()
	ref, err := conv.SendTurn(ctx, ports.ChatUserMessage{Text: text, ClientMessageID: clientID, Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	var streamed, answer string
	for {
		select {
		case ev, ok := <-conv.Events():
			if !ok {
				t.Fatal("event stream closed before the turn completed")
			}
			switch ev.Kind {
			case ports.ChatEventMessageDelta:
				streamed += ev.Delta
			case ports.ChatEventMessageCompleted:
				answer = ev.Text
			case ports.ChatEventApprovalRequested:
				t.Errorf("unexpected approval request: %s", ev.Summary)
				_ = conv.ResolveRequest(ctx, ev.RequestID, ports.ChatDecision{ID: ev.Decisions[0].ID})
			case ports.ChatEventTurnCompleted:
				if ev.ProviderTurnID != ref.ProviderTurnID {
					t.Fatalf("turn.completed for %q, want %q", ev.ProviderTurnID, ref.ProviderTurnID)
				}
				if ev.TurnState != domain.TurnStateCompleted {
					t.Fatalf("turn state = %q (%v), want completed", ev.TurnState, ev.Err)
				}
				if streamed == "" {
					t.Error("no streaming deltas observed")
				}
				if answer == "" {
					answer = streamed
				}
				return answer
			case ports.ChatEventControllerState:
				if ev.ControllerState == ports.ChatControllerStopped {
					t.Fatalf("controller stopped before the turn completed: %v", ev.Err)
				}
			}
		case <-ctx.Done():
			t.Fatalf("timed out: %v", ctx.Err())
		}
	}
}

// livePlugin is the real zcode plugin for the launch-env pin, with the binary
// and auth answers fixed so the test exercises the driver rather than binary
// discovery or the credential probe.
type livePlugin struct {
	*zcode.Plugin
	bin string
}

func (p livePlugin) ResolveBinary(context.Context) (string, error) { return p.bin, nil }
func (p livePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}

func liveBinary(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("AO_ZCODE_BIN"); bin != "" {
		return bin
	}
	if bin, err := exec.LookPath("zcode"); err == nil {
		return bin
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no zcode wrapper: %v", err)
	}
	bin := filepath.Join(home, ".local", "bin", "zcode")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("no zcode wrapper at %s: %v", bin, err)
	}
	return bin
}

func envMap() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}
