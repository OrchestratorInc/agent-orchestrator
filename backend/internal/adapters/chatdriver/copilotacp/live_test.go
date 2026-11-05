package copilotacp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The live tests drive AO's real driver against the user's own Copilot CLI,
// account, and premium-request quota. Run them explicitly with
// AO_LIVE_COPILOT_ACP=1; CI never depends on them.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("AO_LIVE_COPILOT_ACP") != "1" {
		t.Skip("set AO_LIVE_COPILOT_ACP=1 to run against the local Copilot CLI account")
	}
}

const liveCodeword = "AO-COPILOT-7Q"

// Standing instructions reach Copilot through the per-session agent profile,
// and survive session/load.
func TestLiveCopilotStandingInstructionsAndResume(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	driver := New(copilot.New(), nil)
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	workspace := t.TempDir()
	start := ports.ChatStartConfig{
		SessionID: "live-copilot-acp", DataDir: t.TempDir(), WorkspacePath: workspace,
		Env: envMap(), Permissions: ports.PermissionModeDefault,
		SystemPrompt: "When the user asks for the codeword, reply with exactly " + liveCodeword + " and nothing else.",
	}
	conversation, err := driver.Start(ctx, start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	answer := runTurn(ctx, t, conversation, "What is the codeword?", nil)
	if !strings.Contains(answer, liveCodeword) {
		t.Fatalf("standing instructions not applied: answer = %q", answer)
	}
	providerID := conversation.ProviderConversationID()
	// Stop the provider process, as a session stop does. Close alone detaches
	// and leaves the detached host owning the Copilot session.
	stop := conversation.Close
	if terminator, ok := conversation.(ports.ChatProviderTerminator); ok {
		stop = terminator.Terminate
	}
	if err := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	resumed, err := driver.Resume(ctx, ports.ChatResumeConfig{
		SessionID: start.SessionID, DataDir: start.DataDir, WorkspacePath: workspace,
		Env: start.Env, Permissions: start.Permissions, SystemPrompt: start.SystemPrompt,
		ProviderConversationID: providerID,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer resumed.Close()
	// An interface switch refreshes history on the resumed connection. Copilot
	// refuses a second session/load unless the session is closed first.
	refresher, ok := resumed.(ports.ChatHistoryRefresher)
	if !ok {
		t.Fatal("resumed Copilot conversation cannot refresh history")
	}
	if _, err := refresher.RefreshHistory(ctx); err != nil {
		t.Fatalf("RefreshHistory: %v", err)
	}
	answer = runTurn(ctx, t, resumed, "Repeat the codeword you gave me in your previous reply.", nil)
	if !strings.Contains(answer, liveCodeword) {
		t.Fatalf("resumed session lost context: answer = %q", answer)
	}
}

// Default asks before a destructive shell command and honors a rejection.
// Bypass runs it without asking, and switching back to default asks again:
// Copilot cannot leave bypass when launched with --allow-all, which is why
// the binding selects bypass through allow_all.
func TestLiveCopilotApprovals(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	driver := New(copilot.New(), nil)
	workspace := t.TempDir()
	sentinel := filepath.Join(workspace, "sentinel.txt")
	writeSentinel := func() {
		if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const deletePrompt = "Use your shell tool to run exactly: rm sentinel.txt"

	for _, launch := range []ports.PermissionMode{ports.PermissionModeDefault, ports.PermissionModeBypassPermissions} {
		t.Run(string(launch), func(t *testing.T) {
			conversation, err := driver.Start(ctx, ports.ChatStartConfig{
				SessionID: domain.SessionID("live-copilot-approval-" + string(launch)), DataDir: t.TempDir(),
				WorkspacePath: workspace, Env: envMap(), Permissions: launch,
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer conversation.Close()

			writeSentinel()
			asked := false
			runTurn(ctx, t, conversation, deletePrompt, func(event ports.ChatEvent) {
				asked = true
				reject := ""
				for _, decision := range event.Decisions {
					if decision.Kind == ports.ChatDecisionRejectOnce {
						reject = decision.ID
					}
				}
				if reject == "" {
					t.Fatalf("approval offers no reject_once: %+v", event.Decisions)
				}
				if err := conversation.ResolveRequest(ctx, event.RequestID, ports.ChatDecision{ID: reject}); err != nil {
					t.Fatalf("ResolveRequest: %v", err)
				}
			})
			_, statErr := os.Stat(sentinel)
			if launch == ports.PermissionModeDefault {
				if !asked || statErr != nil {
					t.Fatalf("default: asked=%v sentinel err=%v, want asked and file kept", asked, statErr)
				}
				return
			}
			if asked || statErr == nil {
				t.Fatalf("bypass: asked=%v sentinel err=%v, want no approval and file removed", asked, statErr)
			}

			writeSentinel()
			asked = false
			runTurnWith(ctx, t, conversation, ports.ChatUserMessage{
				Text: deletePrompt, ClientMessageID: "live-approval-default", Origin: domain.MessageOriginHuman,
				Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeDefault},
			}, func(event ports.ChatEvent) {
				asked = true
				for _, decision := range event.Decisions {
					if decision.Kind == ports.ChatDecisionRejectOnce {
						_ = conversation.ResolveRequest(ctx, event.RequestID, ports.ChatDecision{ID: decision.ID})
					}
				}
			})
			if _, err := os.Stat(sentinel); !asked || err != nil {
				t.Fatalf("bypass -> default: asked=%v sentinel err=%v, want asked and file kept", asked, err)
			}
		})
	}
}

func runTurn(ctx context.Context, t *testing.T, conversation ports.ChatConversation, text string, onApproval func(ports.ChatEvent)) string {
	t.Helper()
	return runTurnWith(ctx, t, conversation, ports.ChatUserMessage{
		Text: text, ClientMessageID: "live-" + time.Now().Format("150405.000000"), Origin: domain.MessageOriginHuman,
	}, onApproval)
}

func runTurnWith(ctx context.Context, t *testing.T, conversation ports.ChatConversation, msg ports.ChatUserMessage, onApproval func(ports.ChatEvent)) string {
	t.Helper()
	ref, err := conversation.SendTurn(ctx, msg)
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
				t.Fatalf("conversation closed mid-turn; answer=%q", answer.String())
			}
			// Commit each event the way the chat controller does, so the provider
			// accepts the next prompt.
			if acker, ok := conversation.(ports.ChatProviderEventAcknowledger); ok && event.ProviderEventID != "" {
				if err := acker.AcknowledgeProviderEvent(ctx, event.ProviderEventID); err != nil {
					t.Fatalf("AcknowledgeProviderEvent: %v", err)
				}
			}
			if os.Getenv("AO_LIVE_COPILOT_TRACE") == "1" && event.Kind != ports.ChatEventMessageDelta {
				t.Logf("event %s state=%s turn=%s err=%v", event.Kind, event.TurnState, event.ProviderTurnID, event.Err)
			}
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				answer.WriteString(event.Delta)
			case ports.ChatEventApprovalRequested:
				if onApproval == nil {
					t.Fatalf("unexpected approval request: %+v", event.Decisions)
				}
				onApproval(event)
			case ports.ChatEventTurnCompleted:
				t.Logf("turn %s: %q", event.TurnState, answer.String())
				return answer.String()
			case ports.ChatEventError:
				t.Fatalf("turn error: %v", event.Err)
			}
		case <-ctx.Done():
			t.Fatalf("turn timed out: %v; answer=%q", ctx.Err(), answer.String())
		}
	}
}

func envMap() map[string]string {
	out := make(map[string]string)
	for _, pair := range os.Environ() {
		name, value, ok := strings.Cut(pair, "=")
		if ok {
			out[name] = value
		}
	}
	return out
}
