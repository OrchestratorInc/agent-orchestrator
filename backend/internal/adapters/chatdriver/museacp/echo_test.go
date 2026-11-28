package museacp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// echoPlugin points the driver at a Muse binary and reports it signed in: the
// echo provider needs no login.
type echoPlugin struct{ binary string }

func (p echoPlugin) ResolveBinary(context.Context) (string, error) { return p.binary, nil }
func (p echoPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}

// Run with AO_LIVE_MUSE_ECHO=<path to muse>. It drives the real `muse acp`
// through AO's driver on Muse's echo provider, which needs no login and repeats
// each prompt: a session opens in AO's approval mode and two turns are each
// answered.
func TestLiveMuseACPEcho(t *testing.T) {
	binary := os.Getenv("AO_LIVE_MUSE_ECHO")
	if binary == "" {
		t.Skip("set AO_LIVE_MUSE_ECHO to a muse binary to run on its echo provider")
	}
	driver := nativeacp.New(echoPlugin{binary: binary}, nativeacp.Config{
		Harness: domain.HarnessMuse,
		Configure: func(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
			args, env, err := configure(ctx, cfg)
			return append(args, "--provider", "echo"), env, err
		},
		SessionMode:  sessionMode,
		HideCommand:  hiddenCommand,
		VersionProbe: versionProbe,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityApprovals: true},
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "live-muse-echo") })
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: "live-muse-echo", DataDir: dataDir, WorkspacePath: t.TempDir(),
		Env: env, Permissions: ports.PermissionModeAuto, SystemPrompt: "Standing instructions.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.Close()

	send := func(id, text string) string {
		t.Helper()
		ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
			Text: text, ClientMessageID: id, Origin: domain.MessageOriginHuman,
			Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeAuto},
		})
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
	// AO never sends a second prompt into a busy ACP turn: its chat service
	// queues the message and delivers it when the turn ends. So each turn runs
	// to completion before the next.
	turn := func(id, text string) string {
		t.Helper()
		providerTurn := send(id, text)
		var answer strings.Builder
		for {
			select {
			case event, ok := <-conversation.Events():
				if !ok {
					t.Fatalf("controller closed during %s; answer %q", id, answer.String())
				}
				// The chat service acknowledges each provider event once it has
				// stored it; the host takes no new prompt before that.
				if event.ProviderEventID != "" {
					if acker, ok := conversation.(ports.ChatProviderEventAcknowledger); ok {
						if err := acker.AcknowledgeProviderEvent(ctx, event.ProviderEventID); err != nil {
							t.Fatalf("acknowledge %s: %v", event.ProviderEventID, err)
						}
					}
				}
				if event.ProviderTurnID != providerTurn {
					continue
				}
				switch event.Kind {
				case ports.ChatEventMessageDelta:
					answer.WriteString(event.Delta)
				case ports.ChatEventTurnCompleted:
					if event.TurnState != domain.TurnStateCompleted {
						t.Fatalf("turn %s state = %q: %+v", id, event.TurnState, event)
					}
					return answer.String()
				}
			case <-ctx.Done():
				t.Fatalf("timed out in %s; answer %q", id, answer.String())
			}
		}
	}
	answers := map[string]string{}
	for _, tc := range []struct{ id, text string }{{"echo-1", "first message"}, {"echo-2", "second message"}} {
		answers[tc.id] = turn(tc.id, tc.text)
		if !strings.Contains(answers[tc.id], tc.text) {
			t.Fatalf("turn %s answered %q, want an echo of %q", tc.id, answers[tc.id], tc.text)
		}
	}
	t.Logf("first: %q; second: %q", answers["echo-1"], answers["echo-2"])
}
