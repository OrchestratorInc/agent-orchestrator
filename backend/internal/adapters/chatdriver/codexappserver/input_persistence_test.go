package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type inputCallerContextKey struct{}

type inputPersistenceDriver struct {
	ports.ChatDriver
}

func (d inputPersistenceDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	return capabilities(), nil
}

type inputPersistenceBarrier struct {
	chatsvc.Store
	pending      chan string
	applied      chan struct{}
	acknowledged chan struct{}
	once         sync.Once
}

func (s *inputPersistenceBarrier) UpsertActivity(ctx context.Context, conversationID, providerTurnID string, activity domain.ConversationActivity, now time.Time) error {
	if err := s.Store.UpsertActivity(ctx, conversationID, providerTurnID, activity, now); err != nil {
		return err
	}
	if activity.Kind == domain.ActivityKindUserInput {
		s.pending <- activity.RequestID
	}
	return nil
}

func (s *inputPersistenceBarrier) ResolveApproval(ctx context.Context, conversationID, requestID, detail string, now time.Time) error {
	caller, _ := ctx.Value(inputCallerContextKey{}).(bool)
	if caller {
		select {
		case <-s.applied:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-s.acknowledged:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := s.Store.ResolveApproval(ctx, conversationID, requestID, detail, now); err != nil {
		return err
	}
	if !caller {
		s.once.Do(func() { close(s.applied) })
	}
	return nil
}

func (s *inputPersistenceBarrier) SetProviderTitle(ctx context.Context, conversationID, title string, now time.Time) error {
	if err := s.Store.SetProviderTitle(ctx, conversationID, title, now); err != nil {
		return err
	}
	if title == "native input acknowledgement drained" {
		close(s.acknowledged)
	}
	return nil
}

func TestUserInputAcknowledgementBeforeControllerPersistenceRetainsAnswer(t *testing.T) {
	st := sqlitetest.MustOpenAt(t, t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	now := time.Now()
	record, err := st.CreateSession(ctx, domain.SessionRecord{ID: "input-race", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	driver, server := newTestDriver(t)
	store := &inputPersistenceBarrier{Store: st, pending: make(chan string, 1), applied: make(chan struct{}), acknowledged: make(chan struct{})}
	reader := chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshot(ctx, id)
		return chatsvc.ConversationRows{Conversation: rows.Conversation, Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities}, err
	})
	svc := chatsvc.New(chatsvc.Options{Store: store, Sessions: st, Reader: reader, Drivers: handoffDriverRegistry{inputPersistenceDriver{driver}}, NewID: uuid.NewString})
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: record.ID, Harness: record.Harness, WorkspacePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(ctx, record.ID, ports.ChatUserMessage{Text: "Ask a question", ClientMessageID: "input-race-prompt"}); err != nil {
		t.Fatal(err)
	}
	server.push(`{"id":"race","method":"item/tool/requestUserInput","params":` + inputParams + `}`)
	var requestID string
	select {
	case requestID = <-store.pending:
	case <-ctx.Done():
		t.Fatal("question was not persisted")
	}
	acknowledged := make(chan struct{})
	go func() {
		server.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == `"race"` && f.Method == "" })
		server.push(`{"method":"serverRequest/resolved","params":{"requestId":"race"}}`)
		server.push(`{"method":"thread/name/updated","params":{"threadId":"thread-1","threadName":"native input acknowledgement drained"}}`)
		close(acknowledged)
	}()
	response := ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"first_value": "Native"}}
	callerCtx := context.WithValue(ctx, inputCallerContextKey{}, true)
	if err := svc.ResolveInput(callerCtx, record.ID, requestID, response); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acknowledged:
	case <-ctx.Done():
		t.Fatal("provider did not acknowledge the native answer")
	}
	snapshot, err := svc.Snapshot(ctx, record.ID)
	if err != nil || len(snapshot.Activities) != 1 {
		t.Fatalf("snapshot = %#v, %v", snapshot, err)
	}
	activity := snapshot.Activities[0]
	var detail map[string]any
	if err := json.Unmarshal(activity.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if activity.Kind != domain.ActivityKindUserInput || activity.Status != domain.ActivityStatusResolved || detail["action"] != "accept" || !reflect.DeepEqual(detail["content"], response.Content) {
		t.Fatalf("acknowledgement won the persistence race and lost the answer: status=%s detail=%s", activity.Status, activity.Detail)
	}
	if err := svc.ResolveInput(ctx, record.ID, requestID, response); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("answered request became actionable again: %v", err)
	}
}

func TestUserInputPublishesTypedResolutionBeforeNativeAcknowledgement(t *testing.T) {
	driver, server := newTestDriver(t)
	conv, err := driver.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	server.push(`{"id":"ack","method":"item/tool/requestUserInput","params":` + inputParams + `}`)
	input := nextEvent(t, conv.Events(), ports.ChatEventInputRequested)
	responder := conv.(ports.ChatInputResponder)
	if err := responder.ResolveInput(t.Context(), input.RequestID, ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"first_value": "Native"}}); err != nil {
		t.Fatal(err)
	}
	resolved := nextEvent(t, conv.Events(), ports.ChatEventInputResolved)
	var detail map[string]any
	if err := json.Unmarshal(resolved.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if resolved.ActivityKind != domain.ActivityKindUserInput || resolved.ProviderTurnID != input.ProviderTurnID || resolved.ProviderItemID != input.ProviderItemID || detail["action"] != "accept" || !reflect.DeepEqual(detail["content"], map[string]any{"first_value": "Native"}) {
		t.Fatalf("resolution cannot project typed answer history: %#v", resolved)
	}
	server.push(`{"method":"serverRequest/resolved","params":{"requestId":"ack"}}`)
	server.push(`{"method":"thread/name/updated","params":{"threadId":"thread-1","threadName":"acknowledgement drained"}}`)
	for {
		select {
		case event := <-conv.Events():
			if event.Kind == ports.ChatEventThreadRenamed {
				return
			}
			if event.Kind == ports.ChatEventApprovalResolved || event.Kind == ports.ChatEventInputResolved {
				t.Fatalf("native acknowledgement republished a locally owned resolution: %#v", event)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("native acknowledgement was not drained")
		}
	}
}
