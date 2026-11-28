package museacp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Run explicitly with AO_LIVE_MUSE_ACP=1. It uses the user's own Muse Code
// executable, login and models; CI never depends on them. Two prompts sent back
// to back must both be answered, in order: muse queues the second.
func TestLiveMuseACP(t *testing.T) {
	if os.Getenv("AO_LIVE_MUSE_ACP") != "1" {
		t.Skip("set AO_LIVE_MUSE_ACP=1 to run against the local Muse Code login")
	}
	driver := New(muse.New(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if key, value, ok := strings.Cut(kv, "="); ok {
			env[key] = value
		}
	}
	dataDir := t.TempDir()
	// The chat host outlives the conversation; stop it with the test.
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "live-muse-acp") })
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: "live-muse-acp", DataDir: dataDir, WorkspacePath: t.TempDir(),
		Env: env, Permissions: ports.PermissionModeDefault,
		SystemPrompt: "When asked for the code word, answer exactly: PAPAYA-17.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.Close()

	send := func(id, text string) string {
		t.Helper()
		ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{Text: text, ClientMessageID: id, Origin: domain.MessageOriginHuman})
		if err != nil {
			t.Fatalf("SendTurn %s: %v", id, err)
		}
		if starter, ok := conversation.(ports.ChatDeferredTurnStarter); ok {
			if err := starter.StartDeferredTurn(ref.ProviderTurnID); err != nil {
				t.Fatalf("StartDeferredTurn %s: %v", id, err)
			}
		}
		return ref.ProviderTurnID
	}
	first := send("live-1", "What is the code word? Reply with only it.")
	second := send("live-2", "Reply with exactly: AO Muse ACP works")

	answers := map[string]*strings.Builder{first: {}, second: {}}
	var completed []string
	for len(completed) < 2 {
		select {
		case event, ok := <-conversation.Events():
			if !ok {
				t.Fatalf("controller closed; completed %v", completed)
			}
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				if b := answers[event.ProviderTurnID]; b != nil {
					b.WriteString(event.Delta)
				}
			case ports.ChatEventTurnCompleted:
				if event.TurnState != domain.TurnStateCompleted {
					t.Fatalf("turn %s state = %q", event.ProviderTurnID, event.TurnState)
				}
				completed = append(completed, event.ProviderTurnID)
			}
		case <-ctx.Done():
			t.Fatalf("timed out; completed %v", completed)
		}
	}
	if completed[0] != first || completed[1] != second {
		t.Fatalf("turns completed in order %v, want %s then %s", completed, first, second)
	}
	if got := answers[first].String(); !strings.Contains(got, "PAPAYA-17") {
		t.Fatalf("code word answer = %q: the standing instructions did not reach Muse", got)
	}
	if got := answers[second].String(); !strings.Contains(got, "AO Muse ACP works") {
		t.Fatalf("second answer = %q", got)
	}
}
