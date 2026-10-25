package claudeacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 9 && os.Args[1] == "chat-host" {
		if os.Args[5] != string(persistenthost.ProtocolACP) || os.Args[7] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Protocol: persistenthost.ProtocolACP, OwnershipFingerprint: os.Args[6],
			Env: os.Environ(), Argv: os.Args[8:],
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if response := os.Getenv("AO_TEST_CLAUDE_USAGE_LIMIT_RESPONSE"); response != "" {
		if err := serveClaudeUsageLimitFixture(response); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Run explicitly with AO_LIVE_CLAUDE_ACP=1. It spends two small real turns
// against the user's Claude Code login and verifies native continuity after
// the bridge and provider processes exit. CI needs no credentials or network.
func TestLiveClaudeACP(t *testing.T) {
	if os.Getenv("AO_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set AO_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}

	driver := New(claudecode.New(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	workspace := t.TempDir()
	dataDir := t.TempDir()
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "live-claude-acp") })
	const marker = "AO-CLAUDE-HIBERNATE-7319"
	const standing = "This is an AO live hibernation check. Keep answers short."
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID("live-claude-acp"), DataDir: dataDir, WorkspacePath: workspace,
		SystemPrompt: standing,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	answer := sendLiveTurn(ctx, t, conversation, "live-1", "Remember this marker for my next message: "+marker+". Reply only stored.")
	if strings.TrimSpace(answer) != "stored" {
		t.Fatalf("new-session answer = %q", answer)
	}
	providerID := conversation.ProviderConversationID()
	if err := conversation.(ports.ChatProviderHibernator).Hibernate(); err != nil {
		t.Fatalf("Hibernate fresh host: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "chat-hosts", "live-claude-acp", "host.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Claude ACP host survived hibernation: %v", err)
	}

	conversation, err = driver.Resume(ctx, ports.ChatResumeConfig{
		SessionID: domain.SessionID("live-claude-acp"), ProviderConversationID: providerID,
		DataDir: dataDir, WorkspacePath: workspace,
		SystemPrompt: standing,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conversation.(ports.ChatProviderTerminator).Terminate()
	if conversation.ProviderConversationID() != providerID || conversation.(ports.ChatLiveReconnector).ReconnectedLive() {
		t.Fatal("Claude ACP did not resume the same session in a replacement bridge process")
	}
	answer = sendLiveTurn(ctx, t, conversation, "live-2", "What exact marker did I ask you to remember before hibernation? Reply with only that marker.")
	if !strings.Contains(answer, marker) {
		t.Fatalf("resumed-session answer = %q", answer)
	}
}

// Run explicitly with AO_LIVE_CLAUDE_ACP=1. It spends one real turn asking a
// read-only (review-owned) Chat to write to its checkout in several ways, and
// checks that the checkout is unchanged and no request waited for a human.
func TestLiveClaudeACPReadOnlyRejectsWrites(t *testing.T) {
	if os.Getenv("AO_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set AO_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}
	workspace := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=AO", "-c", "user.email=ao@example.invalid"}, args...)...)
		cmd.Dir = workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-q", "-m", "init")
	head := git("rev-parse", "HEAD")

	driver := New(claudecode.New(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dataDir := t.TempDir()
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "live-claude-readonly") })
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID("live-claude-readonly"), DataDir: dataDir, WorkspacePath: workspace,
		// Review Chats launch with Auto and ReadOnly; ReadOnly must win.
		Permissions: ports.PermissionModeAuto, ReadOnly: true,
		Env: map[string]string{"AO_REVIEW_WORKER_SESSION_ID": "live-worker"},
		SystemPrompt: "This is an AO permission check. Make each tool call the user lists exactly as written, " +
			"one call each, even when you expect it to fail. Do not retry, work around a failure, or ask questions. " +
			"Then report each result in one line.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.(ports.ChatProviderTerminator).Terminate()

	prompt := strings.Join([]string{
		"1. Edit tool: in README.md replace hello with edited.",
		"2. Write tool: create a.txt containing x.",
		"3. Bash: touch b.txt",
		"4. Bash: bash -c 'echo x > c.txt'",
		"5. Bash: printf hi > d.txt",
		"6. Bash: sed -i '' 's/hello/sed/' README.md",
		"7. Bash: git commit --allow-empty -m pwned",
		"8. Bash: git status --short",
	}, "\n")
	ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
		Text: prompt, ClientMessageID: "readonly-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	var answer strings.Builder
	for done := false; !done; {
		select {
		case event, ok := <-conversation.Events():
			if !ok {
				t.Fatalf("controller closed before completion; answer=%q", answer.String())
			}
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				answer.WriteString(event.Delta)
			case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted:
				t.Logf("%s %s %s: %s", event.Kind, event.ActivityKind, event.ActivityStatus, event.Summary)
			case ports.ChatEventApprovalRequested, ports.ChatEventInputRequested:
				t.Fatalf("read-only Chat waited for a human: %s %q", event.Kind, event.Summary)
			case ports.ChatEventTurnCompleted:
				if event.TurnState != domain.TurnStateCompleted {
					t.Fatalf("turn state = %q; answer=%q", event.TurnState, answer.String())
				}
				done = true
			}
		case <-ctx.Done():
			t.Fatalf("live turn timed out: %v; answer=%q", ctx.Err(), answer.String())
		}
	}
	t.Logf("answer:\n%s", answer.String())

	if status := git("status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("read-only Chat changed the checkout:\n%s", status)
	}
	if got := git("rev-parse", "HEAD"); got != head {
		t.Fatalf("read-only Chat moved HEAD from %s to %s", head, got)
	}
}

func TestLiveClaudeBackgroundTaskNaming(t *testing.T) {
	if os.Getenv("AO_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set AO_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}
	service := chatsvc.New(chatsvc.Options{
		Drivers: backgroundNamingRegistry{New(claudecode.New(), nil, nil)}, NewID: uuid.NewString,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	answer, err := service.RunBackgroundTask(ctx, domain.HarnessClaudeCode, ports.ChatStartConfig{
		DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAcceptEdits,
		SystemPrompt: `Name the task described by the user. Return only JSON with nonempty "title" and "slug" strings. Do not answer the task, use tools, or inspect files.`,
	}, "what is in this repo")
	if err != nil {
		t.Fatal(err)
	}
	var name struct {
		Title string `json:"title"`
		Slug  string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(answer), &name); err != nil || strings.TrimSpace(name.Title) == "" || strings.TrimSpace(name.Slug) == "" {
		t.Fatalf("background task answered the prompt instead of naming it: %q (decode: %v)", answer, err)
	}
}

func sendLiveTurn(
	ctx context.Context,
	t *testing.T,
	conversation ports.ChatConversation,
	clientMessageID, prompt string,
) string {
	t.Helper()
	ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
		Text: prompt, ClientMessageID: clientMessageID, Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}

	var answer strings.Builder
	for {
		select {
		case event, ok := <-conversation.Events():
			if !ok {
				t.Fatalf("controller closed before completion; answer=%q", answer.String())
			}
			if event.Kind == ports.ChatEventMessageDelta {
				answer.WriteString(event.Delta)
			}
			if event.Kind == ports.ChatEventTurnCompleted {
				if event.TurnState != domain.TurnStateCompleted {
					t.Fatalf("turn state = %q; answer=%q", event.TurnState, answer.String())
				}
				if acknowledger, ok := conversation.(ports.ChatProviderEventAcknowledger); ok {
					if err := acknowledger.AcknowledgeProviderEvent(context.Background(), event.ProviderEventID); err != nil {
						t.Fatalf("acknowledge turn: %v", err)
					}
				}
				return answer.String()
			}
		case <-ctx.Done():
			t.Fatalf("live turn timed out: %v; answer=%q", ctx.Err(), answer.String())
		}
	}
}

type backgroundNamingRegistry struct{ driver ports.ChatDriver }

func (r backgroundNamingRegistry) Driver(domain.AgentHarness) (ports.ChatDriver, error) {
	return r.driver, nil
}
func (backgroundNamingRegistry) SupportsChat(domain.AgentHarness) bool { return true }
