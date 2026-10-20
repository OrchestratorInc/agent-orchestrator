package codexappserver

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

const largeReplayFrames = 6000

// A surviving host has already observed a completed turn, but its daemon lost
// the ACKs. Native turn/start must remain usable while that prefix is replayed.
func TestLargeReplayProviderHelper(t *testing.T) {
	if os.Getenv("AO_TEST_CODEX_LARGE_REPLAY") != "1" {
		return
	}
	dir := os.Getenv("AO_TEST_CODEX_LARGE_REPLAY_DIR")
	var starts int
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request frame
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			_, _ = fmt.Fprintf(os.Stdout, `{"id":%s,"result":{"userAgent":"replay-flow-test"}}`+"\n", *request.ID)
			_, _ = fmt.Fprintln(os.Stdout, `{"method":"turn/started","params":{"threadId":"root","turn":{"id":"old","status":"inProgress"}}}`)
			payload := strings.Repeat("x", 32<<10)
			for range largeReplayFrames {
				// Unknown item notifications still require an ordered checkpoint;
				// their large payload is discarded by semantic normalization.
				_, _ = fmt.Fprintf(os.Stdout, `{"method":"item/retained","params":{"threadId":"root","turnId":"old","payload":"%s"}}`+"\n", payload)
			}
			_, _ = fmt.Fprintln(os.Stdout, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"old","status":"completed"}}}`)
			_ = os.WriteFile(filepath.Join(dir, "prefix-written"), nil, 0o600)
		case "turn/start":
			starts++
			_ = os.WriteFile(filepath.Join(dir, "followup-accepted"), []byte(fmt.Sprint(starts)), 0o600)
			_, _ = fmt.Fprintf(os.Stdout, `{"id":%s,"result":{"turn":{"id":"followup"}}}`+"\n", *request.ID)
			_, _ = fmt.Fprintln(os.Stdout, `{"method":"turn/started","params":{"threadId":"root","turn":{"id":"followup","status":"inProgress"}}}`)
			_, _ = fmt.Fprintln(os.Stdout, `{"method":"item/completed","params":{"threadId":"root","turnId":"followup","item":{"id":"answer","type":"agentMessage","text":"FOLLOWUP_OK"}}}`)
			_, _ = fmt.Fprintln(os.Stdout, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"followup","status":"completed"}}}`)
		default:
			_ = os.WriteFile(filepath.Join(dir, "unexpected-method"), []byte(request.Method), 0o600)
		}
	}
	os.Exit(0)
}

type largeReplayStore struct {
	chatsvc.Store
	oldProviderTurnID string
	duplicates        atomic.Int32
}

func (s *largeReplayStore) ProjectProviderEvent(ctx context.Context, conversationID string, session domain.SessionID, generation, eventID, method, payload string, now time.Time, project func(context.Context) error) (bool, error) {
	var event struct {
		ProviderTurnID string `json:"providerTurnId"`
	}
	_ = json.Unmarshal([]byte(payload), &event)
	if method == string(ports.ChatEventDeliveryReceipt) || event.ProviderTurnID == s.oldProviderTurnID {
		// Simulate the old daemon's already-committed event IDs without writing
		// 6002 fixtures. Intake, turn binding and new output use real SQLite.
		s.duplicates.Add(1)
		return false, nil
	}
	return s.Store.ProjectProviderEvent(ctx, conversationID, session, generation, eventID, method, payload, now, project)
}

type gatedReplayReader struct {
	reader  *bufio.Reader
	release <-chan struct{}
	first   bool
	pending []byte
}

func (r *gatedReplayReader) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		line, err := r.reader.ReadBytes('\n')
		if err != nil {
			return 0, err
		}
		if !r.first && bytes.Contains(line, []byte(persistenthost.CodexEventMethod)) {
			r.first = true
			<-r.release
		}
		r.pending = line
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func TestReliableReplayDoesNotBlockConcurrentSendResponse(t *testing.T) {
	fixtureStarted := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	dataDir, workspace := t.TempDir(), t.TempDir()
	const sessionID domain.SessionID = "replay-1"
	const projectID domain.ProjectID = "replay"
	const scope = "conversation:root"
	oldProviderTurnID := (&conversation{providerScopeID: scope}).scopedID("old")
	owner, _ := json.Marshal([]string{filepath.Clean(workspace), string(domain.HarnessCodex), scope})
	config := persistenthost.Config{SessionID: string(sessionID), DataDir: dataDir, Workdir: workspace,
		Protocol: persistenthost.ProtocolCodex, OwnershipFingerprint: fmt.Sprintf("%x", sha256.Sum256(owner)),
		Argv: []string{os.Args[0], "-test.run=^TestLargeReplayProviderHelper$"},
		Env:  append(os.Environ(), "AO_TEST_CODEX_LARGE_REPLAY=1", "AO_TEST_CODEX_LARGE_REPLAY_DIR="+dataDir)}
	seed, err := persistenthost.ConnectOrStart(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = seed.Stdin.Close()
		stop, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = persistenthost.Shutdown(stop, dataDir, string(sessionID))
	})
	if _, err := io.WriteString(seed.Stdin, `{"id":1,"method":"initialize"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(seed.Stdout)
	if _, err := readFrame(reader); err != nil {
		t.Fatal(err)
	}
	awaitReliableReplay(ctx, t, "large prefix written", func() bool {
		_, err := os.Stat(filepath.Join(dataDir, "prefix-written"))
		return err == nil
	})
	// Query state without opening the replay gate. The terminal must already
	// be in the journal before the replacement advertises a ready controller.
	for id := 2; ; id++ {
		if _, err := fmt.Fprintf(seed.Stdin, `{"id":%d,"method":%q,"params":{"threadId":"root"}}`+"\n", id, persistenthost.CodexReplayMethod); err != nil {
			t.Fatal(err)
		}
		line, err := readFrame(reader)
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Result persistenthost.CodexState }
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatal(err)
		}
		if response.Result.ActiveTurns["root"] == "" && len(response.Result.RetainedTurns["root"]) == 1 {
			break
		}
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
	}
	before := readReliableHost(t, dataDir, sessionID)
	_ = seed.Stdin.Close()

	st := sqlitetest.MustOpenAt(t, t.TempDir())
	now := time.Now().UTC()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: string(projectID), Path: workspace, RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: projectID, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	conversation, err := st.CreateConversation(ctx, "conversation", domain.ConversationScopeSession, projectID, sessionID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimChatControllerGeneration(ctx, sessionID, "previous"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendUserMessage(ctx, conversation.ID, sessionID, "previous", domain.ConversationMessage{ID: "original", Text: "original", Origin: domain.MessageOriginHuman}, "old-turn", now); err != nil {
		t.Fatal(err)
	}
	if err := st.BindTurnToProvider(ctx, "old-turn", oldProviderTurnID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SettleTurn(ctx, conversation.ID, oldProviderTurnID, domain.TurnStateCompleted, "", now); err != nil {
		t.Fatal(err)
	}
	storage := &largeReplayStore{Store: st, oldProviderTurnID: oldProviderTurnID}
	t.Logf("prepared %d retained payload bytes and a durable completed turn in %s", largeReplayFrames*(32<<10), time.Since(fixtureStarted))
	release := make(chan struct{})
	var releaseOnce sync.Once
	driver, _ := newTestDriver(t)
	driver.persistent = true
	driver.connectHost = func(ctx context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
		transport, err := persistenthost.ConnectOrStart(ctx, cfg)
		if err == nil {
			transport.Stdout = &gatedReplayReader{reader: bufio.NewReader(transport.Stdout), release: release}
		}
		return transport, err
	}
	snapshotReader := chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshot(ctx, id)
		return chatsvc.ConversationRows{Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities}, err
	})
	svc := chatsvc.New(chatsvc.Options{Store: storage, Sessions: st, Reader: snapshotReader, Drivers: handoffDriverRegistry{driver}, NewID: uuid.NewString})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		stop, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		svc.StopAll(stop)
	})
	controller, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: sessionID, ProjectID: projectID, Harness: domain.HarnessCodex, WorkspacePath: workspace, DataDir: dataDir, ProviderConversationID: "root", ReconnectOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if controller.State() != ports.ChatControllerReady {
		t.Fatal("settled native turn did not restore a ready controller")
	}
	type result struct {
		turn domain.ConversationTurn
		err  error
	}
	sent := make(chan result, 1)
	sendCtx, sendCancel := context.WithTimeout(ctx, 5*time.Second)
	defer sendCancel()
	go func() {
		turn, err := controller.Send(sendCtx, ports.ChatUserMessage{Text: "followup"})
		sent <- result{turn, err}
	}()
	awaitReliableReplay(ctx, t, "followup accepted before replay", func() bool {
		_, err := os.Stat(filepath.Join(dataDir, "followup-accepted"))
		return err == nil
	})
	releaseOnce.Do(func() { close(release) })
	got := <-sent
	if got.err != nil || got.turn.State != domain.TurnStateRunning {
		t.Fatalf("retained replay blocked an accepted turn/start response: state=%s error=%v", got.turn.State, got.err)
	}
	awaitReliableReplay(ctx, t, "large replay and followup settled", func() bool {
		turn, _ := st.TurnByID(ctx, got.turn.ID)
		return turn.State == domain.TurnStateCompleted && controller.State() == ports.ChatControllerReady && storage.duplicates.Load() == largeReplayFrames+2
	})
	awaitReliableReplay(ctx, t, "acknowledged prefix reclaimed", func() bool {
		journals, err := filepath.Glob(filepath.Join(dataDir, "chat-hosts", string(sessionID), "codex-events*.journal"))
		if err != nil || len(journals) != 1 {
			return false
		}
		info, err := os.Stat(journals[0])
		return err == nil && info.Size() == 0
	})
	after := readReliableHost(t, dataDir, sessionID)
	if before.PID != after.PID || !before.StartedAt.Equal(after.StartedAt) {
		t.Fatal("large replay replaced the provider host")
	}
	count, err := os.ReadFile(filepath.Join(dataDir, "followup-accepted"))
	if err != nil || string(count) != "1" {
		t.Fatalf("followup was resent: %s %v", count, err)
	}
	rows, err := st.LoadConversationSnapshot(ctx, controller.ConversationID())
	if err != nil || len(rows.Turns) != 2 || len(rows.Messages) != 3 || rows.Messages[2].Text != "FOLLOWUP_OK" {
		t.Fatalf("followup output lost or duplicated: %+v %v", rows.Messages, err)
	}
	old, err := st.TurnByID(ctx, "old-turn")
	if err != nil || old.State != domain.TurnStateCompleted {
		t.Fatalf("replay changed the original durable completion: %+v %v", old, err)
	}
	if method, err := os.ReadFile(filepath.Join(dataDir, "unexpected-method")); err == nil {
		t.Fatalf("replay required native-history recovery: %s", method)
	}
	t.Logf("same host replayed %d retained source frames (%d payload bytes); accepted followup completed once and journal emptied", largeReplayFrames+2, largeReplayFrames*(32<<10))
}
