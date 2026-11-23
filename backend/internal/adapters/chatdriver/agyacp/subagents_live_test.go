package agyacp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestLiveAgySubagentFilesItsWorkUnderItsLaunch runs a real Antigravity
// subagent through AO's driver and persistent host. Run explicitly with
// AO_LIVE_AGY=1 against agy 1.5.4 or newer (`agy acp`); it uses an empty
// private GEMINI_HOME, as the CLI's login lives outside it.
//
// It proves the launch is marked as one, the child's session is found and
// followed, its steps and messages carry the launch as their parent, and its
// send_message to the parent becomes the launch's hand-back.
func TestLiveAgySubagentFilesItsWorkUnderItsLaunch(t *testing.T) {
	if os.Getenv("AO_LIVE_AGY") != "1" {
		t.Skip("set AO_LIVE_AGY=1 to run against the local agy CLI and Google sign-in")
	}
	t.Setenv("GEMINI_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	driver := New(agy.New(), log)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("alpha-7341\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: "live-agy-subagent", DataDir: t.TempDir(), WorkspacePath: workspace,
		Env: envMap(), Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if terminator, ok := conversation.(ports.ChatProviderTerminator); ok {
			_ = terminator.Terminate()
		}
		_ = conversation.Close()
	}()

	sendTurn(ctx, t, conversation,
		"Use the invoke_subagent tool to launch exactly one subagent (TypeName research, Role \"File Reader\") "+
			"that reads a.txt in this folder and sends you its exact contents with send_message. "+
			"Do not read the file yourself. Wait for its report, then reply with what it said.",
		ports.ChatTurnSettings{})

	launches := map[string]map[string]any{}
	children := map[string][]string{}
	reports := map[string]string{}
	turnDone := false
	quiet := time.NewTimer(time.Hour)
	for {
		var event ports.ChatEvent
		select {
		case <-quiet.C:
			t.Fatalf("no hand-back within the quiet window; launches=%v children=%v", keys(launches), children)
		default:
		}
		eventCtx, cancelEvent := context.WithTimeout(ctx, 2*time.Minute)
		event = nextEvent(eventCtx, t, conversation)
		cancelEvent()
		switch event.Kind {
		case ports.ChatEventApprovalRequested:
			t.Logf("approval requested: %s", event.Summary)
			if err := conversation.ResolveRequest(ctx, event.RequestID, decide(ports.ChatDecisionAllowOnce)(event)); err != nil {
				t.Fatalf("ResolveRequest: %v", err)
			}
		case ports.ChatEventTurnCompleted:
			t.Logf("turn completed: %s", event.TurnState)
			turnDone = true
			// The child can report after the parent's turn ends.
			quiet.Reset(90 * time.Second)
		case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted:
			var detail map[string]any
			_ = json.Unmarshal(event.Detail, &detail)
			parent, _ := detail["parentProviderItemId"].(string)
			t.Logf("%s %s %q status=%s turn=%q parent=%q", event.Kind, event.ActivityKind, event.Summary,
				event.ActivityStatus, event.ProviderTurnID, parent)
			if detail["providerToolName"] == invokeSubagentTool {
				launches[event.ProviderItemID] = detail
			}
			if parent == "" {
				continue
			}
			if detail["providerToolName"] == "SubagentHandback" {
				input, _ := detail["input"].(map[string]any)
				message, _ := input["message"].(string)
				reports[parent] = message
			} else {
				children[parent] = append(children[parent], event.Summary)
				if event.ProviderTurnID != "" {
					t.Errorf("child step %q is filed on turn %q", event.Summary, event.ProviderTurnID)
				}
			}
		}
		if turnDone && len(launches) > 0 {
			done := true
			for id := range launches {
				if _, ok := reports[id]; !ok {
					done = false
				}
			}
			if done {
				break
			}
		}
	}

	for id, detail := range launches {
		input, _ := detail["input"].(map[string]any)
		if prompt, _ := input["prompt"].(string); !strings.Contains(prompt, "a.txt") {
			t.Errorf("launch %s prompt = %q", id, prompt)
		}
		if detail["background"] != true || detail["subagentType"] != "research" {
			t.Errorf("launch %s detail = %v", id, detail)
		}
		if len(children[id]) == 0 {
			t.Errorf("launch %s has no child steps", id)
		}
		if !strings.Contains(reports[id], "alpha-7341") {
			t.Errorf("launch %s report = %q", id, reports[id])
		}
	}
}

func keys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}
