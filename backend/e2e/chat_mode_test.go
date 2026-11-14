//go:build !windows

package e2e

import (
	"net/http"
	"testing"
	"time"
)

func TestChatModeSpawnPrecedenceAndDefaultIsolation(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "modes")

	d.mustCall("PATCH", "/settings/session-interface", http.StatusOK,
		map[string]any{"defaultSessionMode": "chat"}, nil)

	fromDefault := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "codex",
	})
	if fromDefault.Session.Mode != "chat" {
		t.Fatalf("session spawned under a chat default has mode %q", fromDefault.Session.Mode)
	}

	explicitTUI := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "codex", "mode": "tui",
	})
	if explicitTUI.Session.Mode != "tui" {
		t.Fatalf("explicit --mode tui produced %q", explicitTUI.Session.Mode)
	}

	d.mustCall("PATCH", "/settings/session-interface", http.StatusOK,
		map[string]any{"defaultSessionMode": "tui"}, nil)

	var reread struct {
		Session struct{ Mode string } `json:"session"`
	}
	d.mustCall("GET", "/sessions/"+fromDefault.Session.ID, http.StatusOK, nil, &reread)
	if reread.Session.Mode != "chat" {
		t.Fatalf("an existing chat session became %q after the default changed", reread.Session.Mode)
	}

	afterFlip := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "codex",
	})
	if afterFlip.Session.Mode != "tui" {
		t.Fatalf("session spawned after the flip has mode %q, want tui", afterFlip.Session.Mode)
	}

	chatSnap := d.conversation(fromDefault.Session.ID)
	if chatSnap.Mode != "chat" {
		t.Errorf("conversation reports mode %q", chatSnap.Mode)
	}
	status, body := d.callExpectingError("GET", "/sessions/"+explicitTUI.Session.ID+"/conversation", nil)
	if status == http.StatusOK {
		t.Fatal("a TUI session served a conversation snapshot")
	}
	if body.Code != "SESSION_MODE_MISMATCH" {
		t.Errorf("TUI conversation refusal code = %q, want SESSION_MODE_MISMATCH", body.Code)
	}
}

func TestChatSpawnForUnsupportedAgentLeavesNothingBehind(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "unsupported")

	var before struct {
		Sessions []struct{ ID string } `json:"sessions"`
	}
	d.mustCall("GET", "/sessions", http.StatusOK, nil, &before)

	harness := harnessWithoutChatDriver(t, d)

	status, body := d.callExpectingError("POST", "/sessions", map[string]any{
		"projectId": project, "kind": "worker", "harness": harness, "mode": "chat",
	})
	if status == http.StatusCreated {
		t.Fatal("chat mode was accepted for an agent with no chat driver")
	}
	if body.Code == "" {
		t.Errorf("refusal carried no error code: %+v", body)
	}

	var after struct {
		Sessions []struct{ ID string } `json:"sessions"`
	}
	d.mustCall("GET", "/sessions", http.StatusOK, nil, &after)
	if len(after.Sessions) != len(before.Sessions) {
		t.Fatalf("a refused chat spawn left %d session rows behind",
			len(after.Sessions)-len(before.Sessions))
	}
}

func TestChatSpawnPromptProducesAnAnsweredTurn(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "spawnprompt")

	session := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "codex", "mode": "chat",
		"prompt": "Reply with exactly: SPAWN-OK",
	}).Session.ID

	snap := d.awaitConversation(session, 3*time.Minute, "the spawn prompt to be answered",
		func(s snapshot) bool {
			return len(s.Turns) >= 1 && terminal(s.Turns[0].State) && len(s.Messages) >= 2
		})

	if got := snap.Turns[0].State; got != "completed" {
		t.Errorf("spawn turn state = %q, want completed (err=%q)", got, snap.Turns[0].ErrorMessage)
	}

	first := snap.Messages[0]
	if first.Role != "user" {
		t.Fatalf("first message role = %q, want the user's brief", first.Role)
	}
	if first.Origin != "human" {
		t.Errorf("spawn prompt origin = %q, want human", first.Origin)
	}
	if !contains(snap.assistantText(), "SPAWN-OK") {
		t.Errorf("agent never answered the brief:\n%s", describe(snap))
	}
}
