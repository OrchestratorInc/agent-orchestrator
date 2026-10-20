package codexappserver

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type acceptedTurnStore struct {
	chatsvc.Store
	blockBinding atomic.Bool
	denyEvents   atomic.Bool
	accepted     chan string
	detached     <-chan struct{}
}

func (s *acceptedTurnStore) BindTurnToProvider(ctx context.Context, turnID, providerID string, now time.Time) error {
	if s.blockBinding.CompareAndSwap(true, false) {
		s.accepted <- turnID
		select {
		case <-s.detached:
			return errors.New("injected daemon loss after native acceptance, before binding")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.BindTurnToProvider(ctx, turnID, providerID, now)
}

func (s *acceptedTurnStore) ProjectProviderEvent(ctx context.Context, conversationID string, session domain.SessionID, generation, eventID, method, payload string, now time.Time, project func(context.Context) error) (bool, error) {
	if s.denyEvents.Load() {
		return false, errors.New("injected old daemon projector unavailable")
	}
	return s.Store.ProjectProviderEvent(ctx, conversationID, session, generation, eventID, method, payload, now, project)
}

type signalAcceptedDetach struct {
	io.WriteCloser
	once sync.Once
	done chan struct{}
}

func (w *signalAcceptedDetach) Close() error {
	err := w.WriteCloser.Close()
	w.once.Do(func() { close(w.done) })
	return err
}

func TestReliableCodexRecoversAcceptedTurnBeforeDurableBinding(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, suppliedKey := range []bool{false, true} {
			name := "active"
			if completed {
				name = "completed while detached"
			}
			if suppliedKey {
				name += "/external client key"
			} else {
				name += "/AO turn fallback key"
			}
			t.Run(name, func(t *testing.T) {
				dataDir, workspace := t.TempDir(), t.TempDir()
				st := sqlitetest.MustOpenAt(t, t.TempDir())
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				now := time.Now().UTC()
				const sessionID domain.SessionID = "accepted-1"
				const projectID domain.ProjectID = "accepted"
				if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: string(projectID), Path: workspace, RegisteredAt: now}); err != nil {
					t.Fatal(err)
				}
				if _, err := st.CreateSession(ctx, domain.SessionRecord{ID: sessionID, ProjectID: projectID, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				detached := make(chan struct{})
				storage := &acceptedTurnStore{Store: st, accepted: make(chan string, 1), detached: detached}
				storage.blockBinding.Store(true)
				reader := chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
					rows, err := st.LoadConversationSnapshot(ctx, id)
					return chatsvc.ConversationRows{Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities}, err
				})
				var attachment int
				newService := func() *chatsvc.Service {
					d, _ := newTestDriver(t)
					d.persistent = true
					d.connectHost = func(ctx context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
						cfg.Prepare = nil
						cfg.Argv = []string{os.Args[0], "-test.run=^TestReliableDeliveryProviderHelper$"}
						cfg.Env = append(os.Environ(), "AO_TEST_CODEX_RELIABLE=1", "AO_TEST_CODEX_RELIABLE_DIR="+dataDir)
						tr, err := persistenthost.ConnectOrStart(ctx, cfg)
						if err == nil {
							attachment++
							if !tr.CodexReplay {
								t.Error("actual host did not negotiate reliable Codex replay")
							}
							if attachment == 1 {
								tr.Stdin = &signalAcceptedDetach{WriteCloser: tr.Stdin, done: detached}
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
				key := ""
				if suppliedKey {
					key = "external-accepted-key"
				}
				sent := make(chan error, 1)
				go func() {
					_, err := first.Send(ctx, ports.ChatUserMessage{Text: "accepted exactly once", ClientMessageID: key})
					sent <- err
				}()
				var originalID string
				select {
				case originalID = <-storage.accepted:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				original, err := st.TurnByID(ctx, originalID)
				if err != nil || original.State != domain.TurnStateRunning || original.ProviderTurnID != "" {
					t.Fatalf("native accepted before durable binding: turn=%+v err=%v", original, err)
				}
				beforeRows, err := st.LoadConversationSnapshot(ctx, first.ConversationID())
				if err != nil || len(beforeRows.Messages) != 1 {
					t.Fatalf("original prompt before binding: %+v, %v", beforeRows.Messages, err)
				}
				originalMessage := beforeRows.Messages[0]
				record, found, err := st.GetSession(ctx, sessionID)
				if err != nil || !found {
					t.Fatalf("session: %+v %v", record, err)
				}
				const queuedID = "queued-after-acceptance"
				if created, err := st.AppendUserMessage(ctx, first.ConversationID(), sessionID, record.Metadata.ControllerGeneration,
					domain.ConversationMessage{ID: "queued-message", Text: "followup after accepted turn", Origin: domain.MessageOriginHuman, ClientMessageID: "queued-client-key"}, queuedID, now); err != nil || !created {
					t.Fatalf("durable queued followup: created=%v err=%v", created, err)
				}
				beforeHost := readReliableHost(t, dataDir, sessionID)
				storage.denyEvents.Store(true)
				closed := make(chan error, 1)
				go func() { closed <- first.Close(ctx) }()
				if err := <-sent; err == nil {
					t.Fatal("binding loss did not interrupt the original dispatch")
				}
				if err := <-closed; err != nil {
					t.Fatalf("detach old controller: %v", err)
				}
				storage.denyEvents.Store(false)
				if completed {
					if err := os.WriteFile(filepath.Join(dataDir, "release"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
					awaitReliableReplay(ctx, t, "completion while unbound and detached", func() bool {
						_, err := os.Stat(filepath.Join(dataDir, "turn-1-completed"))
						return err == nil
					})
				}
				start.ProviderConversationID, start.ReconnectOnly = first.ProviderConversationID(), true
				secondService := newService()
				second, err := secondService.Start(ctx, start)
				if err != nil {
					t.Fatalf("reattach accepted original: %v", err)
				}
				if err := secondService.DrainQueued(ctx, sessionID); err != nil {
					t.Fatal(err)
				}
				if !completed {
					bound, err := st.TurnByID(ctx, originalID)
					queued, queueErr := st.TurnByID(ctx, queuedID)
					if err != nil || queueErr != nil || bound.State != domain.TurnStateRunning || bound.ProviderTurnID == "" || queued.State != domain.TurnStateQueued || second.State() != ports.ChatControllerBusy {
						t.Fatalf("recovered active turn released queue: original=%+v queued=%+v state=%s errors=%v,%v", bound, queued, second.State(), err, queueErr)
					}
					if err := os.WriteFile(filepath.Join(dataDir, "release"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				awaitReliableReplay(ctx, t, "original and queued turn completed", func() bool {
					original, _ := st.TurnByID(ctx, originalID)
					queued, _ := st.TurnByID(ctx, queuedID)
					return original.State == domain.TurnStateCompleted && queued.State == domain.TurnStateCompleted && second.State() == ports.ChatControllerReady
				})
				afterRows, err := st.LoadConversationSnapshot(ctx, second.ConversationID())
				if err != nil || len(afterRows.Turns) != 2 || len(afterRows.Messages) != 4 {
					t.Fatalf("original rows lost or duplicated: turns=%+v messages=%+v err=%v", afterRows.Turns, afterRows.Messages, err)
				}
				var prompts, answers int
				for _, message := range afterRows.Messages {
					if message.ID == originalMessage.ID && message.TurnID == originalID && message.Text == originalMessage.Text && message.ClientMessageID == originalMessage.ClientMessageID {
						prompts++
					}
					if message.Role == domain.MessageRoleAssistant && message.Text == "~"+strings.Repeat("x", reliableReplayDeltas) && message.TurnID == originalID {
						answers++
					}
				}
				if prompts != 1 || answers != 1 {
					t.Fatalf("accepted output did not repair original AO turn: prompts=%d answers=%d", prompts, answers)
				}
				afterHost := readReliableHost(t, dataDir, sessionID)
				if beforeHost.PID != afterHost.PID || !beforeHost.StartedAt.Equal(afterHost.StartedAt) {
					t.Fatal("accepted turn recovery replaced the native host")
				}
				awaitReliableReplay(ctx, t, "acknowledged journal reclaimed", func() bool {
					files, _ := filepath.Glob(filepath.Join(dataDir, "chat-hosts", string(sessionID), "codex-events*.journal"))
					if len(files) != 1 {
						return false
					}
					info, err := os.Stat(files[0])
					return err == nil && info.Size() == 0
				})
				if method, err := os.ReadFile(filepath.Join(dataDir, "unexpected-method")); err == nil {
					t.Fatalf("accepted-turn replay requested native history: %s", method)
				}
				t.Log("recovered original AO turn across native acceptance/binding loss; original prompt preserved, followup once, same host, no history, empty journal")
			})
		}
	}
}
