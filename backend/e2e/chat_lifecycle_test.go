//go:build !windows

package e2e

import (
	"net/http"
	"testing"
	"time"
)

func TestChatSurvivesADaemonRestartWithNativeContext(t *testing.T) {
	requireE2E(t)
	dataDir := t.TempDir()
	d := startDaemon(t, dataDir)
	project := seedProject(t, d, "restart")

	session := chatSession(t, d, project,
		"Remember this number for later: 4271. Reply with exactly: STORED")

	before := d.conversation(session)
	if before.ConversationID == "" {
		t.Fatal("session has no conversation to restore")
	}
	itemsBefore := len(before.Messages) + len(before.Activities)

	d.stop()
	restarted := startDaemon(t, dataDir)

	after := restarted.awaitLiveController(session, 90*time.Second)

	if after.ConversationID != before.ConversationID {
		t.Fatalf("conversation id changed across the restart: %s -> %s",
			before.ConversationID, after.ConversationID)
	}
	if got := len(after.Messages) + len(after.Activities); got < itemsBefore {
		t.Errorf("timeline lost items across the restart: %d -> %d", itemsBefore, got)
	}

	send(t, restarted, session, "What number did I ask you to remember? Reply with just the number.", "recall")
	recalled := restarted.awaitConversation(session, 3*time.Minute, "the recall answer",
		func(s snapshot) bool { return terminal(s.Turns[len(s.Turns)-1].State) })

	if !contains(recalled.assistantText(), "4271") {
		t.Fatalf("the agent lost its context across the restart:\n%s", describe(recalled))
	}
}

func TestChatRestartMidTurnKeepsCodexRunning(t *testing.T) {
	requireE2E(t)
	dataDir := t.TempDir()
	d := startDaemon(t, dataDir)
	project := seedProject(t, d, "midturn")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	send(t, d, session, "Run the shell command `sleep 20`, then reply with exactly: SURVIVED-SIGKILL", "long")
	d.awaitConversation(session, 90*time.Second, "the long turn to start running", func(s snapshot) bool {
		return s.Turns[len(s.Turns)-1].State == "running"
	})
	hostBefore := persistentHostPID(t, dataDir, session)

	d.kill()
	if !processAlive(hostBefore) {
		t.Fatalf("detached host %d died with the killed daemon", hostBefore)
	}
	restarted := startDaemon(t, dataDir)
	restarted.awaitLiveController(session, 90*time.Second)
	snap := restarted.awaitConversation(session, 3*time.Minute, "the surviving turn to finish",
		func(s snapshot) bool { return terminal(s.Turns[len(s.Turns)-1].State) })

	last := snap.Turns[len(snap.Turns)-1]
	hostAfter := persistentHostPID(t, dataDir, session)
	t.Logf("abrupt daemon simulation: host_pid=%d->%d turn_state=%s", hostBefore, hostAfter, last.State)
	if hostAfter != hostBefore || last.State != "completed" || !contains(snap.assistantText(), "SURVIVED-SIGKILL") {
		t.Fatalf("real Codex turn did not survive abrupt daemon replacement:\n%s", describe(snap))
	}
}

func TestChatQueuedMessageIsAccountedForAcrossARestart(t *testing.T) {
	requireE2E(t)
	dataDir := t.TempDir()
	d := startDaemon(t, dataDir)
	project := seedProject(t, d, "queuecrash")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	send(t, d, session, "Run the shell command `sleep 20`, then reply with exactly: FIRST-SURVIVED", "qc-slow")
	d.awaitConversation(session, 90*time.Second, "the slow turn to start running", func(s snapshot) bool {
		return s.Turns[len(s.Turns)-1].State == "running"
	})
	_, state := send(t, d, session, "Reply with exactly: QUEUED-ACROSS-RESTART", "qc-queued")
	if state != "queued" {
		t.Fatalf("mid-turn send reported %q, want queued", state)
	}

	d.kill()
	restarted := startDaemon(t, dataDir)
	restarted.awaitLiveController(session, 90*time.Second)

	snap := restarted.awaitConversation(session, 3*time.Minute, "the queued message to be delivered",
		func(s snapshot) bool {
			return terminal(s.userTurnStates()["Reply with exactly: QUEUED-ACROSS-RESTART"]) &&
				contains(s.assistantText(), "QUEUED-ACROSS-RESTART")
		})

	state = snap.userTurnStates()["Reply with exactly: QUEUED-ACROSS-RESTART"]
	if state == "" {
		t.Fatalf("the queued message disappeared across the restart:\n%s", describe(snap))
	}
	if state != "completed" || !contains(snap.assistantText(), "QUEUED-ACROSS-RESTART") {
		t.Errorf("queued message was not completed after the surviving turn: state=%q\n%s", state, describe(snap))
	}
	t.Logf("queued-across-restart resolved as %q", state)
}

func TestChatKillTerminatesAndReleasesTheController(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "kill")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	d.mustCall("POST", "/sessions/"+session+"/kill", http.StatusOK, nil, nil)

	deadline := time.Now().Add(60 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		var read struct {
			Session struct {
				Status       string `json:"status"`
				IsTerminated bool   `json:"isTerminated"`
			} `json:"session"`
		}
		d.mustCall("GET", "/sessions/"+session, http.StatusOK, nil, &read)
		status = read.Session.Status
		if read.Session.IsTerminated {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if status != "terminated" {
		t.Fatalf("killed chat session status = %q, want terminated", status)
	}

	code, body := d.callExpectingError("POST", "/sessions/"+session+"/conversation/messages",
		map[string]any{"text": "are you there?", "clientMessageId": "after-kill"})
	if code < 400 {
		t.Errorf("a terminated chat session accepted a message (status %d)", code)
	} else if body.Code == "" {
		t.Errorf("refusal carried no error code: %+v", body)
	}
}
