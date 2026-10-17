package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestLiveReconnectRecoversCompletionLostBeforeDaemonRestart(t *testing.T) {
	for _, tc := range []struct {
		name, nativeAnswerID string
		settled, ambiguous   bool
	}{
		{name: "stable item ID", nativeAnswerID: "answer"},
		{name: "regenerated streaming item ID", nativeAnswerID: "item-2"},
		{name: "regenerated completed item ID", nativeAnswerID: "item-2", settled: true},
		{name: "ambiguous streaming prefixes stay separate", nativeAnswerID: "item-2", ambiguous: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := sqlitetest.MustOpenAt(t, t.TempDir())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			now := time.Now().UTC()
			const sessionID domain.SessionID = "restart-1"
			const projectID domain.ProjectID = "restart"
			const threadID = "thread-survived"
			const nativeTurnID = "turn-finished-while-daemon-stopped"
			const oldGeneration = "old-daemon"
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: string(projectID), Path: t.TempDir(), RegisteredAt: now}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateSession(ctx, domain.SessionRecord{
				ID: sessionID, ProjectID: projectID, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now,
				Metadata: domain.SessionMetadata{ProviderConversationID: threadID, ControllerGeneration: oldGeneration},
			}); err != nil {
				t.Fatal(err)
			}
			retained, err := st.CreateConversation(ctx, "retained-conversation", domain.ConversationScopeSession, projectID, sessionID, now)
			if err != nil {
				t.Fatal(err)
			}
			branch, err := st.ConversationBranch(ctx, retained.ID, retained.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			// The preceding daemon committed dispatch and partial output, then stopped
			// consuming notifications while the same native Codex process completed.
			scope := conversation{providerScopeID: branch.ProviderScopeID}
			providerTurnID := scope.scopedID(nativeTurnID)
			if _, err := st.AppendUserMessage(ctx, retained.ID, sessionID, oldGeneration, domain.ConversationMessage{
				ID: "original-message", Origin: domain.MessageOriginHuman, Text: "finish the work", ClientMessageID: "original-client-message",
			}, "original-turn", now); err != nil {
				t.Fatal(err)
			}
			if err := st.BindTurnToProvider(ctx, "original-turn", providerTurnID, now); err != nil {
				t.Fatal(err)
			}
			if err := st.AppendAssistantDelta(ctx, retained.ID, scope.scopedID("answer"), providerTurnID, "finished", "partial-answer", now); err != nil {
				t.Fatal(err)
			}
			if tc.settled {
				if err := st.SettleAssistantMessage(ctx, retained.ID, scope.scopedID("answer"), providerTurnID, "finished answer", "partial-answer", now); err != nil {
					t.Fatal(err)
				}
			}
			if tc.ambiguous {
				if err := st.AppendAssistantDelta(ctx, retained.ID, scope.scopedID("another-answer"), providerTurnID, "finished", "another-partial-answer", now); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.UpsertActivity(ctx, retained.ID, providerTurnID, domain.ConversationActivity{ID: "running-reasoning", ProviderItemID: scope.scopedID("reasoning-live"), Kind: domain.ActivityKindReasoning, Status: domain.ActivityStatusRunning}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendActivityStreamedText(ctx, retained.ID, scope.scopedID("reasoning-live"), "Analyzing", now); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertActivity(ctx, retained.ID, providerTurnID, domain.ConversationActivity{
				ID: "running-command", ProviderItemID: scope.scopedID("command"), Kind: domain.ActivityKindCommand,
				Status: domain.ActivityStatusRunning, Summary: "pwd",
			}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendUserMessage(ctx, retained.ID, sessionID, oldGeneration, domain.ConversationMessage{
				ID: "followup-message", Origin: domain.MessageOriginHuman, Text: "continue after restart", ClientMessageID: "followup-client-message",
			}, "followup-turn", now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}

			driver, server := newTestDriver(t)
			proc, err := driver.spawn(ctx, "codex", "/tmp/ws", nil)
			if err != nil {
				t.Fatal(err)
			}
			driver.persistent = true
			driver.connectHost = func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
				if cfg.SessionID != string(sessionID) {
					t.Errorf("adopted session = %q, want %q", cfg.SessionID, sessionID)
				}
				return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: true, NextRequestID: 41}, nil
			}
			// No turn/completed notification is replayed for the original turn: it was
			// delivered to the old daemon's socket before that daemon was killed.
			server.reply("thread/read", fmt.Sprintf(`{"thread":{"id":"thread-survived","turns":[{"id":"turn-finished-while-daemon-stopped","status":"completed","items":[{"id":%q,"type":"agentMessage","text":"finished answer"},{"id":"item-3","type":"reasoning","summary":["Analyzing the result"],"content":[]},{"id":"command","type":"commandExecution","command":"pwd","cwd":"/tmp/ws","status":"completed","aggregatedOutput":"/tmp/ws\n","exitCode":0}]}]}}`, tc.nativeAnswerID))
			server.reply("turn/start", `{"turn":{"id":"followup-native-turn","status":"inProgress","items":[]}}`)
			reader := chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
				rows, err := st.LoadConversationSnapshot(ctx, id)
				return chatsvc.ConversationRows{
					Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns,
					Messages: rows.Messages, Activities: rows.Activities,
				}, err
			})
			svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Reader: reader, Drivers: handoffDriverRegistry{driver}, NewID: uuid.NewString})
			t.Cleanup(func() {
				_ = server.toClient.Close()
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
				defer cleanupCancel()
				svc.StopAll(cleanupCtx)
			})
			controller, err := svc.Start(ctx, chatsvc.StartConfig{
				SessionID: sessionID, ProjectID: projectID, Harness: domain.HarnessCodex,
				WorkspacePath: "/tmp/ws", ProviderConversationID: threadID,
			})
			if err != nil {
				t.Fatalf("restart Start: %v", err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				original, readErr := st.TurnByID(ctx, "original-turn")
				if readErr != nil {
					t.Fatal(readErr)
				}
				followup, readErr := st.TurnByID(ctx, "followup-turn")
				if readErr != nil {
					t.Fatal(readErr)
				}
				if original.State == domain.TurnStateCompleted && followup.State == domain.TurnStateRunning {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("native turn completed before restart, but AO retained original=%s followup=%s controller=%s; want completed/running/busy", original.State, followup.State, controller.State())
				}
				time.Sleep(5 * time.Millisecond)
			}
			if server.sentMethod("initialize") || server.sentMethod("thread/resume") {
				t.Fatal("restart reinitialized or resumed the surviving native process")
			}
			// Old socket frames can still reach the pump after native recovery. They
			// must neither append to the restored answer nor reopen the settled turn.
			server.push(`{"method":"turn/started","params":{"threadId":"thread-survived","turn":{"id":"followup-native-turn","status":"inProgress","items":[]}}}`)
			server.push(`{"method":"turn/started","params":{"threadId":"thread-survived","turn":{"id":"turn-finished-while-daemon-stopped","status":"inProgress","items":[]}}}`)
			server.push(`{"method":"item/agentMessage/delta","params":{"threadId":"thread-survived","turnId":"turn-finished-while-daemon-stopped","itemId":"answer","delta":" stale duplicate"}}`)
			server.push(`{"method":"turn/completed","params":{"threadId":"thread-survived","turn":{"id":"turn-finished-while-daemon-stopped","status":"completed","items":[]}}}`)
			server.push(`{"method":"turn/completed","params":{"threadId":"thread-survived","turn":{"id":"turn-finished-while-daemon-stopped","status":"completed","items":[]}}}`)
			server.push(`{"method":"thread/name/updated","params":{"threadId":"thread-survived","threadName":"replay fence"}}`)
			deadline = time.Now().Add(3 * time.Second)
			for {
				value, readErr := svc.Snapshot(ctx, sessionID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if value.Conversation.ProviderTitle == "replay fence" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("stale replay fence was not projected")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if controller.State() != ports.ChatControllerBusy {
				t.Fatalf("duplicate original completion released the running successor: %s", controller.State())
			}
			snapshot, err := svc.Snapshot(ctx, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			var answerCopies, commandCopies, reasoningCopies, finalAnswerCopies int
			for _, message := range snapshot.Messages {
				if message.Role == domain.MessageRoleAssistant && message.Text == "finished answer" {
					finalAnswerCopies++
				}
				if message.ProviderItemID == scope.scopedID("answer") {
					answerCopies++
					want := "finished answer"
					if tc.ambiguous {
						want = "finished"
					}
					if message.Text != want || message.Streaming {
						t.Fatalf("recovered answer = %q, streaming=%v", message.Text, message.Streaming)
					}
				}
			}
			for _, activity := range snapshot.Activities {
				if activity.ProviderItemID == scope.scopedID("reasoning-live") {
					reasoningCopies++
					if activity.StreamedText != "Analyzing the result" || activity.Status != domain.ActivityStatusCompleted {
						t.Fatalf("recovered reasoning = %+v", activity)
					}
				}
				if activity.ProviderItemID == scope.scopedID("command") {
					commandCopies++
					if activity.Status != domain.ActivityStatusCompleted {
						t.Fatalf("recovered command = %s", activity.Status)
					}
				}
			}
			if answerCopies != 1 || commandCopies != 1 || reasoningCopies != 1 || finalAnswerCopies != 1 {
				t.Fatalf("recovered answer/command/reasoning/final-text rows = %d/%d/%d/%d, want 1/1/1/1", answerCopies, commandCopies, reasoningCopies, finalAnswerCopies)
			}
			server.mu.Lock()
			var starts []frame
			for _, seen := range server.seen {
				if seen.Method == "turn/start" {
					starts = append(starts, seen)
				}
			}
			server.mu.Unlock()
			if len(starts) != 1 {
				t.Fatalf("followup dispatches = %d, want exactly one", len(starts))
			}
			var started struct {
				ThreadID string `json:"threadId"`
				Input    []struct {
					Text string `json:"text"`
				} `json:"input"`
			}
			if err := json.Unmarshal(starts[0].Params, &started); err != nil {
				t.Fatal(err)
			}
			if started.ThreadID != threadID || len(started.Input) != 1 || started.Input[0].Text != "continue after restart" {
				t.Fatalf("followup dispatch = %+v", started)
			}
			server.push(`{"method":"turn/completed","params":{"threadId":"thread-survived","turn":{"id":"followup-native-turn","status":"completed","items":[]}}}`)
			deadline = time.Now().Add(3 * time.Second)
			for {
				followup, readErr := st.TurnByID(ctx, "followup-turn")
				if readErr != nil {
					t.Fatal(readErr)
				}
				if followup.State == domain.TurnStateCompleted && controller.State() == ports.ChatControllerReady {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("followup settled as %s, controller=%s; want completed/ready", followup.State, controller.State())
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

func openLiveReconnectConversation(t *testing.T) (*conversation, *scriptedServer) {
	t.Helper()
	driver, server := newTestDriver(t)
	proc, err := driver.spawn(context.Background(), "codex", "/tmp/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	proc.reconnected = true
	conv := newConversation(proc, driver.log, "recovery-scope")
	conv.start("thread-1", "gpt-test", "")
	t.Cleanup(func() { _ = conv.Close() })
	return conv, server
}

// A wire notification fences the pump without sleeps or private queue inspection.
func liveReconnectEventsThroughFence(t *testing.T, conv *conversation, server *scriptedServer) []ports.ChatEvent {
	t.Helper()
	server.push(`{"method":"thread/name/updated","params":{"threadId":"thread-1","threadName":"replay fence"}}`)
	var events []ports.ChatEvent
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event, ok := <-conv.Events():
			if !ok {
				t.Fatal("reconnected event stream closed before fence")
			}
			if event.Kind == ports.ChatEventThreadRenamed && event.Title == "replay fence" {
				return events
			}
			events = append(events, event)
		case <-deadline:
			t.Fatal("timed out waiting for reconnect replay fence")
		}
	}
}

func TestLiveReconnectRequiresExactTerminalProof(t *testing.T) {
	for _, tc := range []struct {
		name, nativeID, status string
		readFailure            bool
	}{
		{name: "still running", nativeID: "pending", status: "inProgress"},
		{name: "queued", nativeID: "pending", status: "queued"},
		{name: "unknown state", nativeID: "pending", status: "future-status"},
		{name: "missing pending turn", nativeID: "other-completed-turn", status: "completed"},
		{name: "native read unavailable", readFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, server := openLiveReconnectConversation(t)
			if tc.readFailure {
				server.replyError("thread/read", -32600, "synthetic native read failure")
			} else {
				server.reply("thread/read", fmt.Sprintf(`{"thread":{"id":"thread-1","turns":[{"id":%q,"status":%q,"items":[{"id":"answer","type":"agentMessage","text":"must not import"}]}]}}`, tc.nativeID, tc.status))
			}
			if err := conv.ActivateLiveReconnect(context.Background(), conv.scopedID("pending"), nil); err != nil {
				t.Fatal(err)
			}
			if events := liveReconnectEventsThroughFence(t, conv, server); len(events) != 0 {
				t.Fatalf("inconclusive native read emitted projection events: %+v", events)
			}
			// The same attachment can still finish naturally after an inconclusive
			// read; no guessed failure or terminal guard may swallow that outcome.
			server.push(`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"pending","status":"completed","items":[]}}}`)
			completed := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
			if completed.ProviderTurnID != conv.scopedID("pending") || completed.TurnState != domain.TurnStateCompleted {
				t.Fatalf("later native completion = %+v", completed)
			}
		})
	}
}

func TestLiveReconnectPreservesConnectionOnWrongThreadOrCancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cancel    bool
		wantError error
	}{
		{name: "wrong native thread", wantError: ports.ErrChatRecoveryInconclusive},
		{name: "caller cancelled", cancel: true, wantError: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, server := openLiveReconnectConversation(t)
			var detached, terminated bool
			stop := conv.proc.stop
			conv.proc.stop = func() error { detached = true; return stop() }
			conv.proc.terminate = func() error { terminated = true; return stop() }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel() // No scripted response: only caller cancellation can finish this read.
			} else {
				server.reply("thread/read", `{"thread":{"id":"another-thread","turns":[{"id":"pending","status":"completed","items":[]}]}}`)
			}
			err := conv.ActivateLiveReconnect(ctx, conv.scopedID("pending"), nil)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("activation error = %v, want %v", err, tc.wantError)
			}
			if detached || terminated {
				t.Fatalf("failed observation detached=%v terminated=%v", detached, terminated)
			}
			if events := liveReconnectEventsThroughFence(t, conv, server); len(events) != 0 {
				t.Fatalf("failed observation settled provider work: %+v", events)
			}
		})
	}
}

func TestLiveReconnectRestoresVerifiedTerminalOutcomes(t *testing.T) {
	for _, state := range []domain.TurnState{domain.TurnStateCompleted, domain.TurnStateFailed, domain.TurnStateInterrupted} {
		t.Run(string(state), func(t *testing.T) {
			conv, server := openLiveReconnectConversation(t)
			server.reply("thread/read", fmt.Sprintf(`{"thread":{"id":"thread-1","turns":[{"id":"pending","status":%q,"items":[{"id":"user","type":"userMessage","content":[{"type":"text","text":"already durable"}]},{"id":"answer","type":"agentMessage","text":"settled reply"}]},{"id":"other","status":"inProgress","items":[]}]}}`, state))
			if err := conv.ActivateLiveReconnect(context.Background(), conv.scopedID("pending"), nil); err != nil {
				t.Fatal(err)
			}
			events := liveReconnectEventsThroughFence(t, conv, server)
			if len(events) != 2 || events[0].Kind != ports.ChatEventMessageCompleted || events[1].Kind != ports.ChatEventTurnCompleted {
				t.Fatalf("recovered events = %+v, want only settled output and completion", events)
			}
			for _, event := range events {
				if event.ProviderTurnID != conv.scopedID("pending") || event.ProviderConversationID != "thread-1" || event.NativeTurnID != "pending" || event.ProviderEventID == "" {
					t.Fatalf("recovery lost exact native/scoped identity: %+v", event)
				}
			}
			if events[0].Text != "settled reply" || events[1].TurnState != state {
				t.Fatalf("recovery outcome = %+v", events)
			}
		})
	}
}

func TestLiveReconnectDropsOnlyStaleRecoveredRootFrames(t *testing.T) {
	conv, server := openLiveReconnectConversation(t)
	server.reply("thread/read", `{"thread":{"id":"thread-1","turns":[{"id":"pending","status":"completed","items":[]}]}}`)
	if err := conv.ActivateLiveReconnect(context.Background(), conv.scopedID("pending"), nil); err != nil {
		t.Fatal(err)
	}
	_ = nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
	server.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"pending","status":"inProgress","items":[]}}}`)
	server.push(`{"method":"item/agentMessage/delta","params":{"threadId":"thread-1","turnId":"pending","itemId":"stale","delta":"stale root output"}}`)
	server.push(`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"pending","status":"completed","items":[]}}}`)
	server.push(`{"method":"item/agentMessage/delta","params":{"threadId":"child-thread","turnId":"pending","itemId":"child","delta":"child output survives"}}`)
	server.push(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"next-root","status":"inProgress","items":[]}}}`)
	server.push(`{"method":"item/agentMessage/delta","params":{"threadId":"thread-1","turnId":"next-root","itemId":"next","delta":"new root output survives"}}`)
	events := liveReconnectEventsThroughFence(t, conv, server)
	if len(events) != 3 || events[0].Kind != ports.ChatEventMessageDelta || events[0].Delta != "child output survives" ||
		events[1].Kind != ports.ChatEventTurnStarted || events[1].ProviderTurnID != conv.scopedID("next-root") ||
		events[2].Kind != ports.ChatEventMessageDelta || events[2].Delta != "new root output survives" {
		t.Fatalf("events after root recovery = %+v", events)
	}
}
