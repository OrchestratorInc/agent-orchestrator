//go:build !windows

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func chatSession(t *testing.T, d *daemon, project, prompt string) string {
	t.Helper()
	id := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "codex", "mode": "chat",
		"prompt": prompt,
	}).Session.ID
	d.awaitConversation(id, 3*time.Minute, "the session to finish its first turn",
		func(s snapshot) bool {
			return len(s.Turns) >= 1 && terminal(s.Turns[0].State)
		})
	return id
}

func send(t *testing.T, d *daemon, session, text, clientID string) (turnID, state string) {
	t.Helper()
	var out struct {
		TurnID    string `json:"turnId"`
		State     string `json:"state"`
		Duplicate bool   `json:"duplicate"`
	}
	d.mustCall("POST", "/sessions/"+session+"/conversation/messages", http.StatusAccepted,
		map[string]any{"text": text, "clientMessageId": clientID}, &out)
	return out.TurnID, out.State
}

func TestChatTurnIsProjectedAndArchived(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "projection")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	send(t, d, session, "List the files in this repo, then reply with exactly: LISTED", "c-list")

	snap := d.awaitConversation(session, 3*time.Minute, "the turn to complete",
		func(s snapshot) bool {
			return len(s.Turns) >= 2 && terminal(s.Turns[1].State)
		})

	if got := snap.Turns[1].State; got != "completed" {
		t.Errorf("turn state = %q, want completed (err=%q)", got, snap.Turns[1].ErrorMessage)
	}
	if !contains(snap.assistantText(), "LISTED") {
		t.Errorf("agent's answer missing from the timeline:\n%s", describe(snap))
	}

	seen := map[int64]string{}
	var previous int64 = -1
	for _, m := range snap.Messages {
		if where, dup := seen[m.Sequence]; dup {
			t.Fatalf("sequence %d handed out twice (%s and message %s)", m.Sequence, where, m.ID)
		}
		seen[m.Sequence] = "message " + m.ID
		if m.Sequence <= previous {
			t.Fatalf("messages are not in increasing sequence order: %d after %d", m.Sequence, previous)
		}
		previous = m.Sequence
	}
	for _, a := range snap.Activities {
		if where, dup := seen[a.Sequence]; dup {
			t.Fatalf("sequence %d handed out twice (%s and activity %s)", a.Sequence, where, a.ID)
		}
		seen[a.Sequence] = "activity " + a.ID
	}

	for _, m := range snap.Messages {
		if m.Role != "assistant" {
			continue
		}
		if m.Stream {
			t.Errorf("assistant message %s still marked streaming after the turn completed", m.ID)
		}
	}

	commands := 0
	for _, a := range snap.Activities {
		if a.Kind == "command" {
			commands++
		}
	}
	if commands == 0 {
		t.Errorf("no command activity recorded for a prompt that had to read the repo:\n%s", describe(snap))
	}
}

func TestChatSendIsIdempotentOnClientMessageID(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "idempotent")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	before := len(d.conversation(session).Turns)

	first, _ := send(t, d, session, "Reply with exactly: ONCE", "retry-me")
	if first == "" {
		t.Fatal("first send returned no turn id")
	}

	var second struct {
		TurnID    string `json:"turnId"`
		Duplicate bool   `json:"duplicate"`
	}
	d.mustCall("POST", "/sessions/"+session+"/conversation/messages", http.StatusAccepted,
		map[string]any{"text": "Reply with exactly: ONCE", "clientMessageId": "retry-me"}, &second)
	if !second.Duplicate {
		t.Errorf("a retry under the same client message id was not reported as a duplicate")
	}

	snap := d.awaitConversation(session, 3*time.Minute, "the turn to settle", func(s snapshot) bool {
		return len(s.Turns) > before && terminal(s.Turns[len(s.Turns)-1].State)
	})
	if got := len(snap.Turns); got != before+1 {
		t.Fatalf("turns = %d, want %d: a retry created a second turn\n%s", got, before+1, describe(snap))
	}
	sent := 0
	for _, m := range snap.Messages {
		if m.Role == "user" && strings.Contains(m.Text, "ONCE") {
			sent++
		}
	}
	if sent != 1 {
		t.Errorf("the retried message was recorded %d times", sent)
	}
}

func TestChatQueueHoldsMidTurnMessagesAndDrainsInOrder(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "queue")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	send(t, d, session, "Sleep for 30 seconds using a shell command, then reply with exactly: SLEPT", "q-slow")
	d.awaitConversation(session, 90*time.Second, "the slow turn to start running", func(s snapshot) bool {
		return s.Turns[len(s.Turns)-1].State == "running"
	})

	_, stateA := send(t, d, session, "Reply with exactly: SECOND", "q-a")
	_, stateB := send(t, d, session, "Reply with exactly: THIRD", "q-b")
	if stateA != "queued" || stateB != "queued" {
		t.Fatalf("mid-turn sends reported states %q and %q, want queued", stateA, stateB)
	}

	snap := d.awaitConversation(session, 5*time.Minute, "the queue to drain", func(s snapshot) bool {
		states := s.userTurnStates()
		return terminal(states["Reply with exactly: SECOND"]) && terminal(states["Reply with exactly: THIRD"])
	})

	states := snap.userTurnStates()
	for _, text := range []string{
		"Sleep for 30 seconds using a shell command, then reply with exactly: SLEPT",
		"Reply with exactly: SECOND",
		"Reply with exactly: THIRD",
	} {
		if states[text] != "completed" {
			t.Errorf("turn for %q = %q, want completed", trim(text, 40), states[text])
		}
	}

	answers := snap.assistantText()
	slept, second, third := strings.Index(answers, "SLEPT"), strings.Index(answers, "SECOND"), strings.Index(answers, "THIRD")
	if slept < 0 || second < 0 || third < 0 {
		t.Fatalf("not every queued message was answered (SLEPT=%d SECOND=%d THIRD=%d)\n%s",
			slept, second, third, describe(snap))
	}
	if slept >= second || second >= third {
		t.Errorf("answers arrived out of order: SLEPT@%d SECOND@%d THIRD@%d", slept, second, third)
	}
}

func TestChatInterruptCancelsTheQueueWithTheTurn(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "interrupt")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	send(t, d, session, "Sleep for 60 seconds using a shell command, then reply with exactly: NEVER", "i-slow")
	d.awaitConversation(session, 90*time.Second, "the slow turn to start running", func(s snapshot) bool {
		return s.Turns[len(s.Turns)-1].State == "running"
	})
	send(t, d, session, "Reply with exactly: ALSO-NEVER", "i-queued")

	d.mustCall("POST", "/sessions/"+session+"/conversation/interrupt", http.StatusNoContent, nil, nil)

	snap := d.awaitConversation(session, 2*time.Minute, "the turn and its queue to settle",
		func(s snapshot) bool {
			states := s.userTurnStates()
			return terminal(states["Reply with exactly: ALSO-NEVER"])
		})

	states := snap.userTurnStates()
	if got := states["Sleep for 60 seconds using a shell command, then reply with exactly: NEVER"]; got != "interrupted" {
		t.Errorf("interrupted turn state = %q, want interrupted", got)
	}
	if got := states["Reply with exactly: ALSO-NEVER"]; got != "interrupted" {
		t.Errorf("queued turn state = %q, want interrupted", got)
	}
	if contains(snap.assistantText(), "ALSO-NEVER") {
		t.Error("stop released the queued message instead of cancelling it")
	}
}

func TestChatInterruptWithNoActiveTurnIsRefusedNotAnError(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "nointerrupt")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	status, body := d.callExpectingError("POST", "/sessions/"+session+"/conversation/interrupt", nil)
	if status >= 500 {
		t.Fatalf("interrupting an idle session returned %d (%+v); a client mistake is not a server fault", status, body)
	}
	if status == http.StatusNoContent {
		t.Skip("provider reported an active turn; nothing to assert about the idle case")
	}
	if body.Code == "" {
		t.Errorf("refusal carried no error code: %+v", body)
	}
}

func TestChatRelayThroughTheSendEndpointIsAttributedToAutomation(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "relay")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	d.mustCall("POST", "/sessions/"+session+"/send", http.StatusOK,
		map[string]any{"message": "Reply with exactly: RELAYED"}, nil)

	snap := d.awaitConversation(session, 3*time.Minute, "the relayed message to be answered",
		func(s snapshot) bool {
			return contains(s.assistantText(), "RELAYED")
		})

	var relayed *message
	for i := range snap.Messages {
		if snap.Messages[i].Role == "user" && strings.Contains(snap.Messages[i].Text, "RELAYED") {
			relayed = &snap.Messages[i]
		}
	}
	if relayed == nil {
		t.Fatalf("the relayed message is not in the timeline:\n%s", describe(snap))
		return
	}
	if relayed.Origin != "automation" {
		t.Errorf("relay origin = %q, want automation", relayed.Origin)
	}
}

func TestChatApprovalRoundTrip(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "approvals")
	setPermissions(t, d, project, "accept-edits")

	session := chatSession(t, d, project, "Reply with exactly: READY")
	send(t, d, session,
		`Run this exact shell command and tell me its exit code: touch "$HOME/.ao-e2e-approval-probe"`,
		"approval-1")

	snap := d.awaitConversation(session, 2*time.Minute, "an approval request", func(s snapshot) bool {
		_, ok := s.pendingApproval()
		return ok
	})
	approval, _ := snap.pendingApproval()

	if approval.RequestID == "" {
		t.Fatal("approval carries no request id; nothing could answer it safely")
	}
	offeredDecisions := approval.decisions()
	if len(offeredDecisions) == 0 {
		t.Fatalf("approval offered no decisions, so the UI has no buttons to render:\n%s", describe(snap))
	}
	offered := map[string]bool{}
	for _, option := range offeredDecisions {
		if option.Label == "" {
			t.Errorf("decision %q has no label, so its button would render blank", option.ID)
		}
		offered[option.ID] = true
	}
	if !offered["accept"] {
		t.Fatalf("no accept option offered: %+v", offeredDecisions)
	}

	var read struct {
		Session struct{ Status string } `json:"session"`
	}
	d.mustCall("GET", "/sessions/"+session, http.StatusOK, nil, &read)
	if read.Session.Status != "needs_input" {
		t.Errorf("session status while blocked on an approval = %q, want needs_input", read.Session.Status)
	}

	status, body := d.callExpectingError("POST",
		"/sessions/"+session+"/conversation/approvals/"+approval.RequestID+"/resolve",
		map[string]any{"decisionId": "definitely-not-a-real-decision"})
	if status < 400 {
		t.Errorf("AO accepted a decision the provider never offered (status %d)", status)
	} else if body.Code == "" {
		t.Errorf("refusal carried no error code: %+v", body)
	}

	status, _ = d.callExpectingError("POST",
		"/sessions/"+session+"/conversation/approvals/does-not-exist/resolve",
		map[string]any{"decisionId": "accept"})
	if status < 400 {
		t.Errorf("resolving an unknown request id was accepted (status %d)", status)
	}

	d.mustCall("POST",
		"/sessions/"+session+"/conversation/approvals/"+approval.RequestID+"/resolve",
		http.StatusNoContent, map[string]any{"decisionId": "accept"}, nil)

	resolved := d.awaitConversation(session, 2*time.Minute, "the approval to resolve", func(s snapshot) bool {
		for _, a := range s.Activities {
			if a.RequestID == approval.RequestID {
				return a.Status != "pending"
			}
		}
		return false
	})
	for _, a := range resolved.Activities {
		if a.RequestID == approval.RequestID && a.Status != "resolved" {
			t.Errorf("approval %s ended as %q, want resolved", a.RequestID, a.Status)
		}
	}

	d.awaitConversation(session, 3*time.Minute, "the approved turn to settle", func(s snapshot) bool {
		return terminal(s.Turns[len(s.Turns)-1].State)
	})
}

func TestChatAgentCanRunTheAOCLI(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "aopath")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	send(t, d, session,
		"Run the shell command `ao --version` and reply with its exact output, or reply NOT-FOUND if the command does not exist.",
		"ao-path")

	snap := d.awaitConversation(session, 3*time.Minute, "the ao probe to finish", func(s snapshot) bool {
		return terminal(s.Turns[len(s.Turns)-1].State)
	})
	answer := snap.assistantText()
	if contains(answer, "NOT-FOUND") || contains(answer, "command not found") {
		t.Fatalf("the agent could not run `ao` inside a chat session:\n%s", describe(snap))
	}
}

func TestChatProviderEventsAreArchived(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "archive")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	snap := d.conversation(session)
	if snap.LatestSequence == 0 {
		t.Fatal("conversation has no items to check the archive against")
	}

	for _, a := range snap.Activities {
		if len(a.DetailJSON) == 0 {
			continue
		}
		var decoded any
		if err := json.Unmarshal(a.DetailJSON, &decoded); err != nil {
			t.Errorf("activity %s detail does not decode: %v: %s", a.ID, err, a.DetailJSON)
		}
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func TestChatModelSelectionComesFromTheProviderAndAppliesToTheNextTurn(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "models")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	var catalog struct {
		Models []struct {
			ID          string   `json:"id"`
			DisplayName string   `json:"displayName"`
			Default     bool     `json:"default"`
			Efforts     []string `json:"efforts"`
		} `json:"models"`
		Selected struct {
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoningEffort"`
			ApprovalMode    string `json:"approvalMode"`
		} `json:"selected"`
	}
	d.mustCall("GET", "/sessions/"+session+"/conversation/models", http.StatusOK, nil, &catalog)

	if len(catalog.Models) == 0 {
		t.Fatal("the provider offered no models; a chat session cannot expose a choice")
	}
	if catalog.Selected.Model != "" {
		t.Errorf("a fresh conversation already has model %q selected", catalog.Selected.Model)
	}
	var defaults int
	for _, model := range catalog.Models {
		if model.ID == "" || model.DisplayName == "" {
			t.Errorf("model %+v is missing an id or a label, so it cannot be rendered", model)
		}
		if model.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("provider reported %d default models, want exactly 1", defaults)
	}

	var chosen string
	var effort string
	for _, model := range catalog.Models {
		if !model.Default {
			chosen = model.ID
			if len(model.Efforts) > 0 {
				effort = model.Efforts[0]
			}
			break
		}
	}
	if chosen == "" {
		t.Skip("provider offers only one model; nothing to switch to")
	}

	var applied struct {
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoningEffort"`
		ApprovalMode    string `json:"approvalMode"`
	}
	d.mustCall("PATCH", "/sessions/"+session+"/conversation/settings", http.StatusOK,
		map[string]any{"model": chosen, "reasoningEffort": effort, "approvalMode": "default"}, &applied)
	if applied.Model != chosen {
		t.Fatalf("settings echoed model %q, want %q", applied.Model, chosen)
	}

	snap := d.conversation(session)
	if snap.Settings.Model != chosen {
		t.Errorf("snapshot reports model %q, want %q", snap.Settings.Model, chosen)
	}

	send(t, d, session, "Reply with exactly: SWITCHED", "model-switch")
	answered := d.awaitConversation(session, 3*time.Minute, "a turn on the chosen model",
		func(s snapshot) bool { return terminal(s.Turns[len(s.Turns)-1].State) })
	last := answered.Turns[len(answered.Turns)-1]
	if last.State != "completed" {
		t.Fatalf("turn on model %q ended as %q (err=%q)", chosen, last.State, last.ErrorMessage)
	}
	if !contains(answered.assistantText(), "SWITCHED") {
		t.Errorf("the agent did not answer on the chosen model:\n%s", describe(answered))
	}

	status, body := d.callExpectingError("PATCH", "/sessions/"+session+"/conversation/settings",
		map[string]any{"approvalMode": "acceptEdits"})
	if status != http.StatusBadRequest {
		t.Errorf("camelCase approval mode returned %d, want 400 (%+v)", status, body)
	}
}
