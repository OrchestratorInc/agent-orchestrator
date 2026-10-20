package codexappserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const reliableReplayDeltas = 64

// The subprocess speaks native Codex JSONL; the real host, adapter, service and
// SQLite projector are used by the parent. No provider history endpoint exists.
func TestReliableDeliveryProviderHelper(t *testing.T) {
	if os.Getenv("AO_TEST_CODEX_RELIABLE") != "1" {
		return
	}
	root := os.Getenv("AO_TEST_CODEX_RELIABLE_DIR")
	var turn int
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request frame
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			_, _ = fmt.Fprintf(os.Stdout, `{"id":%s,"result":{"userAgent":"reliable-test"}}`+"\n", *request.ID)
		case "thread/start":
			_, _ = fmt.Fprintf(os.Stdout, `{"id":%s,"result":{"thread":{"id":"reliable-thread"}}}`+"\n", *request.ID)
		case "turn/start":
			turn++
			id := fmt.Sprintf("turn-%d", turn)
			_, _ = fmt.Fprintf(os.Stdout, `{"id":%s,"result":{"turn":{"id":%q,"status":"inProgress","items":[]}}}`+"\n", *request.ID, id)
			_, _ = fmt.Fprintf(os.Stdout, `{"method":"turn/started","params":{"threadId":"reliable-thread","turn":{"id":%q,"status":"inProgress","items":[]}}}`+"\n", id)
			if turn == 1 {
				_, _ = fmt.Fprintln(os.Stdout, `{"method":"item/agentMessage/delta","params":{"threadId":"reliable-thread","turnId":"turn-1","itemId":"answer-1","delta":"~"}}`)
				if os.Getenv("AO_TEST_CODEX_RELIABLE_CHILD") == "1" {
					_, _ = fmt.Fprintln(os.Stdout, `{"method":"turn/started","params":{"threadId":"child-thread","turn":{"id":"child-turn","status":"inProgress","items":[]}}}`)
				}
				deadline := time.Now().Add(time.Minute)
				for {
					if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						os.Exit(3)
					}
					time.Sleep(time.Millisecond)
				}
				for range reliableReplayDeltas {
					_, _ = fmt.Fprintln(os.Stdout, `{"method":"item/agentMessage/delta","params":{"threadId":"reliable-thread","turnId":"turn-1","itemId":"answer-1","delta":"x"}}`)
				}
			}
			text := "FOLLOWUP_OK"
			if turn == 1 {
				text = "~" + strings.Repeat("x", reliableReplayDeltas)
			}
			_, _ = fmt.Fprintf(os.Stdout, `{"method":"item/completed","params":{"threadId":"reliable-thread","turnId":%q,"item":{"id":%q,"type":"agentMessage","text":%q}}}`+"\n", id, "answer-"+fmt.Sprint(turn), text)
			_, _ = fmt.Fprintf(os.Stdout, `{"method":"turn/completed","params":{"threadId":"reliable-thread","turn":{"id":%q,"status":"completed","items":[]}}}`+"\n", id)
			if turn == 1 && os.Getenv("AO_TEST_CODEX_RELIABLE_CHILD") == "1" {
				_, _ = fmt.Fprintln(os.Stdout, `{"method":"turn/completed","params":{"threadId":"child-thread","turn":{"id":"child-turn","status":"completed","items":[]}}}`)
			}
			_ = os.WriteFile(filepath.Join(root, id+"-completed"), nil, 0o600)
		case "initialized":
		default:
			_ = os.WriteFile(filepath.Join(root, "unexpected-method"), []byte(request.Method), 0o600)
			_, _ = fmt.Fprintf(os.Stdout, `{"id":%s,"error":{"code":-32601,"message":"history recovery is forbidden in this replay test"}}`+"\n", *request.ID)
		}
	}
	os.Exit(0)
}

type reliableReplayStore struct {
	chatsvc.Store
	rollbackOutput    atomic.Bool
	loseTerminalACK   atomic.Bool
	terminalCommitted atomic.Bool
}

func (s *reliableReplayStore) ProjectProviderEvent(ctx context.Context, conversationID string, session domain.SessionID, generation, eventID, method, payload string, now time.Time, project func(context.Context) error) (bool, error) {
	projected, err := s.Store.ProjectProviderEvent(ctx, conversationID, session, generation, eventID, method, payload, now, func(txCtx context.Context) error {
		if err := project(txCtx); err != nil {
			return err
		}
		if s.rollbackOutput.Load() && method == string(ports.ChatEventMessageDelta) && strings.Contains(payload, `"delta":"x"`) {
			return errors.New("injected output transaction rollback")
		}
		return nil
	})
	if err == nil && projected && method == string(ports.ChatEventTurnCompleted) {
		s.terminalCommitted.Store(true)
	}
	return projected, err
}

type loseReliableACK struct {
	io.WriteCloser
	store *reliableReplayStore
}

func (w *loseReliableACK) Write(p []byte) (int, error) {
	var f frame
	if json.Unmarshal(bytes.TrimSpace(p), &f) == nil && f.Method == persistenthost.CodexAckMethod &&
		w.store.terminalCommitted.Load() && w.store.loseTerminalACK.CompareAndSwap(true, false) {
		_ = w.Close()
		return 0, io.ErrClosedPipe // Database COMMIT succeeded; the host never sees its ACK.
	}
	return w.WriteCloser.Write(p)
}

func TestReliableCodexReplayCommitsExactlyOnceAcrossAttachments(t *testing.T) {
	for _, fault := range []string{"daemon detached before completion", "output transaction rolled back", "completion committed but ACK lost", "root and child completed while detached"} {
		t.Run(fault, func(t *testing.T) {
			dataDir, workspace := t.TempDir(), t.TempDir()
			st := sqlitetest.MustOpenAt(t, t.TempDir())
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			now := time.Now().UTC()
			const sessionID domain.SessionID = "reliable-1"
			const projectID domain.ProjectID = "reliable"
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: string(projectID), Path: workspace, RegisteredAt: now}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateSession(ctx, domain.SessionRecord{ID: sessionID, ProjectID: projectID, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			storage := &reliableReplayStore{Store: st}
			withChild := fault == "root and child completed while detached"
			storage.rollbackOutput.Store(fault == "output transaction rolled back")
			storage.loseTerminalACK.Store(fault == "completion committed but ACK lost")
			reader := chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
				rows, err := st.LoadConversationSnapshot(ctx, id)
				return chatsvc.ConversationRows{Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities}, err
			})
			var attachment int
			newService := func() *chatsvc.Service {
				d, _ := newTestDriver(t) // Admission probing is scripted; conversation delivery uses the actual host.
				d.persistent = true
				d.connectHost = func(ctx context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
					cfg.Prepare = nil // This injected provider owns its launch arguments and environment.
					cfg.Argv = []string{os.Args[0], "-test.run=^TestReliableDeliveryProviderHelper$"}
					cfg.Env = append(os.Environ(), "AO_TEST_CODEX_RELIABLE=1", "AO_TEST_CODEX_RELIABLE_DIR="+dataDir)
					if withChild {
						cfg.Env = append(cfg.Env, "AO_TEST_CODEX_RELIABLE_CHILD=1")
					}
					tr, err := persistenthost.ConnectOrStart(ctx, cfg)
					if err == nil {
						attachment++
						if !tr.CodexReplay {
							t.Error("actual host did not negotiate reliable Codex replay")
						}
						if attachment == 1 {
							tr.Stdin = &loseReliableACK{WriteCloser: tr.Stdin, store: storage}
						}
					}
					return tr, err
				}
				svc := chatsvc.New(chatsvc.Options{Store: storage, Sessions: st, Reader: reader, Drivers: handoffDriverRegistry{d}, NewID: uuid.NewString, DataDir: dataDir})
				t.Cleanup(func() {
					stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer stopCancel()
					svc.StopAll(stopCtx)
				})
				return svc
			}
			t.Cleanup(func() {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer stopCancel()
				_ = persistenthost.Shutdown(stopCtx, dataDir, string(sessionID))
			})
			start := chatsvc.StartConfig{SessionID: sessionID, ProjectID: projectID, Harness: domain.HarnessCodex, WorkspacePath: workspace, DataDir: dataDir}
			firstService := newService()
			first, err := firstService.Start(ctx, start)
			if err != nil {
				t.Fatal(err)
			}
			turn, err := first.Send(ctx, ports.ChatUserMessage{Text: "finish through attachment replacement"})
			if err != nil {
				t.Fatal(err)
			}
			awaitReliableReplay(ctx, t, "seed committed", func() bool {
				rows, err := st.LoadConversationSnapshot(ctx, first.ConversationID())
				return err == nil && len(rows.Messages) == 2 && rows.Messages[1].Text == "~" && (!withChild || len(rows.Turns) == 2)
			})
			followup, err := first.Send(ctx, ports.ChatUserMessage{Text: "followup queued behind original"})
			if err != nil || followup.State != domain.TurnStateQueued {
				t.Fatalf("followup = %+v, %v", followup, err)
			}
			before := readReliableHost(t, dataDir, sessionID)
			if fault == "daemon detached before completion" || withChild {
				firstService.StopAll(ctx)
			}
			if err := os.WriteFile(filepath.Join(dataDir, "release"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			awaitReliableReplay(ctx, t, "provider completed", func() bool {
				_, err := os.Stat(filepath.Join(dataDir, "turn-1-completed"))
				return err == nil
			})
			if fault != "daemon detached before completion" && !withChild {
				awaitReliableReplay(ctx, t, "failed controller detached", func() bool { return first.State() == ports.ChatControllerStopped })
				first.Wait()
			}
			original, err := st.TurnByID(ctx, turn.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantBefore := domain.TurnStateRunning
			if fault == "completion committed but ACK lost" {
				wantBefore = domain.TurnStateCompleted
			}
			if original.State != wantBefore {
				t.Fatalf("before replay original=%s, want %s", original.State, wantBefore)
			}
			queued, err := st.TurnByID(ctx, followup.ID)
			if err != nil || queued.State != domain.TurnStateQueued {
				t.Fatalf("unacknowledged completion released queued work: %+v, %v", queued, err)
			}
			storage.rollbackOutput.Store(false)
			start.ProviderConversationID, start.ReconnectOnly = first.ProviderConversationID(), true
			secondService := newService()
			second, err := secondService.Start(ctx, start)
			if err != nil {
				t.Fatalf("reattach: %v", err)
			}
			// Reconnect itself must recover already accepted intake, including a
			// terminal COMMIT whose lost ACK bypassed the old controller's drain.
			// Calling DrainQueued here would conceal that failure.
			recoveryCtx, stopRecovery := context.WithTimeout(ctx, 5*time.Second)
			defer stopRecovery()
			awaitReliableReplay(recoveryCtx, t, "original and queued followup automatically completed after reconnect", func() bool {
				original, _ := st.TurnByID(ctx, turn.ID)
				queued, _ := st.TurnByID(ctx, followup.ID)
				return original.State == domain.TurnStateCompleted && queued.State == domain.TurnStateCompleted && second.State() == ports.ChatControllerReady
			})
			after := readReliableHost(t, dataDir, sessionID)
			if before.PID != after.PID || !before.StartedAt.Equal(after.StartedAt) || after.Protocol != persistenthost.ProtocolCodex {
				t.Fatal("replay replaced the surviving Codex host")
			}
			rows, err := st.LoadConversationSnapshot(ctx, second.ConversationID())
			if err != nil {
				t.Fatal(err)
			}
			answers := map[string]int{}
			for _, message := range rows.Messages {
				if message.Role == domain.MessageRoleAssistant {
					if message.Streaming {
						t.Fatal("completed answer remains streaming")
					}
					answers[message.Text]++
				}
			}
			wantTurns := 2
			if withChild {
				wantTurns++
			}
			if len(answers) != 2 || answers["~"+strings.Repeat("x", reliableReplayDeltas)] != 1 || answers["FOLLOWUP_OK"] != 1 || len(rows.Turns) != wantTurns {
				t.Fatalf("lost or duplicated projected output: answers=%v turns=%d", answers, len(rows.Turns))
			}
			archive, err := st.ProviderEventsSince(ctx, second.ConversationID(), 0, 1000)
			if err != nil {
				t.Fatal(err)
			}
			var deltas, completions int
			for _, event := range archive {
				switch event.Method {
				case string(ports.ChatEventMessageDelta):
					deltas++
				case string(ports.ChatEventTurnCompleted):
					completions++
				}
			}
			if deltas != reliableReplayDeltas+1 || completions != wantTurns {
				t.Fatalf("archive contains %d deltas/%d completions, want %d/%d", deltas, completions, reliableReplayDeltas+1, wantTurns)
			}
			if method, err := os.ReadFile(filepath.Join(dataDir, "unexpected-method")); err == nil {
				t.Fatalf("reliable reconnect issued unexpected method %s", method)
			}
			t.Logf("same host replayed after %s; exactly %d deltas, %d completions, two settled answers; no native-history recovery", fault, deltas, completions)
		})
	}
}

func awaitReliableReplay(ctx context.Context, t *testing.T, stage string, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout waiting for %s: %v", stage, ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func readReliableHost(t *testing.T, dataDir string, sessionID domain.SessionID) persistenthost.Descriptor {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "chat-hosts", string(sessionID), "host.json"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor persistenthost.Descriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	return descriptor
}
