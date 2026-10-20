package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestHostedLifecycleWaitsForSlowProjection(t *testing.T) {
	c := &conversation{
		proc:   &process{terminate: func() error { return nil }},
		log:    slog.New(slog.DiscardHandler),
		events: make(chan ports.ChatEvent, 1),
	}
	c.events <- ports.ChatEvent{Kind: ports.ChatEventMessageCompleted}
	delivered := make(chan struct{})
	go func() {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "pending"})
		close(delivered)
	}()
	select {
	case <-delivered:
		t.Fatal("hosted terminal event was discarded while projection was slow")
	case <-time.After(5200 * time.Millisecond):
	}
	<-c.events
	select {
	case event := <-c.events:
		if event.Kind != ports.ChatEventTurnCompleted || event.ProviderTurnID != "pending" {
			t.Fatalf("event = %#v, want retained terminal", event)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal event was not delivered when projection caught up")
	}
	<-delivered
}

func TestHostedDeliveryCloseUnblocksSaturatedConsumer(t *testing.T) {
	for _, kind := range []ports.ChatEventKind{ports.ChatEventTurnCompleted, ports.ChatEventMessageDelta} {
		t.Run(string(kind), func(t *testing.T) {
			c := &conversation{
				proc: &process{terminate: func() error { return nil }},
				log:  slog.New(slog.DiscardHandler), events: make(chan ports.ChatEvent, 1),
				deliveryDone: make(chan struct{}),
			}
			c.events <- ports.ChatEvent{Kind: ports.ChatEventMessageCompleted}
			done := make(chan struct{})
			go func() { c.emit(ports.ChatEvent{Kind: kind}); close(done) }()
			select {
			case <-done:
				t.Fatal("hosted event dropped instead of applying backpressure")
			case <-time.After(20 * time.Millisecond):
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Close deadlocked against saturated event delivery")
			}
		})
	}
}

func reliableTestConversation(t *testing.T) (*conversation, *scriptedServer) {
	t.Helper()
	driver, server := newTestDriver(t)
	proc, err := driver.spawn(context.Background(), "codex", "/tmp/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	proc.reliableReplay = true
	proc.terminate = proc.stop
	c := newConversation(proc, driver.log, "scope")
	c.start("thread-1", "model", "")
	t.Cleanup(func() { _ = c.Close() })
	return c, server
}

func pushReliable(t *testing.T, server *scriptedServer, receipt string, replayed bool, native string) {
	t.Helper()
	delivery, err := json.Marshal(map[string]any{
		"method": persistenthost.CodexEventMethod,
		"params": persistenthost.CodexEvent{EventID: receipt, Frame: json.RawMessage(native), Replayed: replayed},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.push(string(delivery))
}

func collectReliable(t *testing.T, c *conversation, count int) []ports.ChatEvent {
	t.Helper()
	var events []ports.ChatEvent
	deadline := time.After(3 * time.Second)
	for len(events) < count {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatal("hosted event stream closed before its normalized events")
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatal("host frame did not produce its normalized events")
		}
	}
	return events
}

func TestReliableDeliveryRetainsStableDerivedEventsAndReceiptOrder(t *testing.T) {
	var original []ports.ChatEvent
	for _, replayed := range []bool{false, true} {
		c, server := reliableTestConversation(t)
		pushReliable(t, server, "host:1", replayed, `{"method":"error","params":{"threadId":"thread-1","turnId":"turn-1","willRetry":true,"error":{"message":"Retrying"}}}`)
		pushReliable(t, server, "host:2", replayed, `{"method":"item/agentMessage/delta","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"answer","delta":"Recovered"}}`)
		pushReliable(t, server, "host:3", replayed, `{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed","items":[]}}}`)
		pushReliable(t, server, "host:4", replayed, `{"method":"hook/started","params":{"threadId":"thread-1","turnId":"turn-1"}}`)
		events := collectReliable(t, c, 5)
		if len(events) != 5 || events[1].Kind != ports.ChatEventActivityCompleted || events[2].Kind != ports.ChatEventMessageDelta {
			t.Fatalf("normalized frame expansion = %#v", events)
		}
		if events[4].Kind != ports.ChatEventDeliveryReceipt {
			t.Fatalf("receipt moved ahead of the last derived event: %#v", events)
		}
		for _, id := range []string{events[1].ProviderEventID, "host:2", c.scopedID("history-event")} {
			require.NoError(t, c.AcknowledgeProviderEvent(context.Background(), id))
		}
		// The barrier proves earlier event/source/history IDs sent no ACK.
		require.NoError(t, c.conn.notify("test/receiptBarrier", nil))
		server.awaitFrame(func(f frame) bool { return f.Method == "test/receiptBarrier" })
		if server.sentMethod(persistenthost.CodexAckMethod) {
			t.Fatal("source frame acknowledged before its final derived event")
		}
		seen := map[string]bool{}
		for _, event := range events {
			require.NoError(t, event.Err)
			if event.ProviderEventID == "" || seen[event.ProviderEventID] {
				t.Fatalf("unstable/colliding semantic ID: %#v", event)
			}
			seen[event.ProviderEventID] = true
			if err := c.AcknowledgeProviderEvent(context.Background(), event.ProviderEventID); err != nil {
				t.Fatal(err)
			}
		}
		server.awaitFrame(func(f frame) bool {
			return f.Method == persistenthost.CodexAckMethod && strings.Contains(string(f.Params), "host:4")
		})
		server.mu.Lock()
		var acknowledgments int
		for _, f := range server.seen {
			if f.Method == persistenthost.CodexAckMethod {
				acknowledgments++
			}
		}
		server.mu.Unlock()
		if acknowledgments != 4 {
			t.Fatalf("sent %d ACKs, want exactly four native source receipts", acknowledgments)
		}
		if original == nil {
			original = events
		} else {
			require.Equal(t, original, events, "replayed derived events changed identities or content")
		}
	}
}

func TestReliableAcknowledgmentIdentifiesActualTransportLoss(t *testing.T) {
	c, server := reliableTestConversation(t)
	pushReliable(t, server, "host:1", false, `{"method":"hook/started","params":{"threadId":"thread-1","turnId":"turn-1"}}`)
	event := collectReliable(t, c, 1)[0]
	if err := c.conn.closeWrite(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"host:1", c.scopedID("history-event")} {
		if err := c.AcknowledgeProviderEvent(context.Background(), id); err != nil {
			t.Fatalf("unregistered identity attempted a transport acknowledgment: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.AcknowledgeProviderEvent(cancelled, event.ProviderEventID); !errors.Is(err, context.Canceled) || errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("caller cancellation classified as a disconnected host: %v", err)
	}
	if err := c.AcknowledgeProviderEvent(context.Background(), event.ProviderEventID); !errors.Is(err, ports.ErrChatRecoveryInconclusive) || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("actual acknowledgment write failure lost transport proof: %v", err)
	}
}

func TestReliableReplayCannotReplaceRootSuccessorWithOldOrChildTurn(t *testing.T) {
	c, server := reliableTestConversation(t)
	c.proc.reconnected = true
	c.proc.liveState = &persistenthost.CodexState{ActiveTurns: map[string]string{"thread-1": "successor"}}
	if err := c.ActivateLiveReconnect(context.Background(), c.scopedID("successor"), nil); err != nil {
		t.Fatal(err)
	}
	server.awaitFrame(func(f frame) bool { return f.Method == persistenthost.CodexReplayMethod })
	pushReliable(t, server, "host:1", true, `{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"old","status":"inProgress","items":[]}}}`)
	pushReliable(t, server, "host:2", true, `{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"old","status":"completed","items":[]}}}`)
	pushReliable(t, server, "host:3", true, `{"method":"turn/started","params":{"threadId":"child","turn":{"id":"successor","status":"inProgress","items":[]}}}`)
	pushReliable(t, server, "host:4", true, `{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"successor","status":"completed","items":[]}}}`)
	collectReliable(t, c, 4)
	if err := c.Interrupt(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	interrupt := server.awaitFrame(func(f frame) bool { return f.Method == "turn/interrupt" })
	var target struct{ ThreadID, TurnID string }
	if err := json.Unmarshal(interrupt.Params, &target); err != nil {
		t.Fatal(err)
	}
	if target.ThreadID != "thread-1" || target.TurnID != "successor" {
		t.Fatalf("interrupt targeted replayed work: %s", interrupt.Params)
	}
	pushReliable(t, server, "host:5", true, `{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"successor","status":"completed","items":[]}}}`)
	collectReliable(t, c, 1)
	c.mu.Lock()
	active := c.activeTurn
	c.mu.Unlock()
	if active != "" {
		t.Fatalf("matching terminal did not release active turn %q", active)
	}
	if server.sentMethod("thread/read") || server.sentMethod("initialize") || server.sentMethod("thread/resume") {
		t.Fatal("reliable reconnect required native history or repeated initialization")
	}
}

func TestReliableReconnectRejectsUnownedNativeTurn(t *testing.T) {
	for _, pending := range []string{"", "different"} {
		t.Run(pending, func(t *testing.T) {
			c, server := reliableTestConversation(t)
			c.proc.liveState = &persistenthost.CodexState{ActiveTurns: map[string]string{"thread-1": "actual"}}
			if err := c.ActivateLiveReconnect(context.Background(), c.scopedID(pending), nil); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
				t.Fatalf("ownership mismatch admitted: %v", err)
			}
			if server.sentMethod(persistenthost.CodexReplayMethod) {
				t.Fatal("released replay before restoring safe turn ownership")
			}
		})
	}
}

func TestReliableLiveTurnScopeExcludesOnlyProvenChild(t *testing.T) {
	c := &conversation{
		threadID: "root-thread", providerScopeID: "scope",
		proc: &process{reliableReplay: true, liveState: &persistenthost.CodexState{
			ActiveTurns:   map[string]string{"root-thread": "running-root", "child-thread": "running-child"},
			RetainedTurns: map[string][]string{"root-thread": {"completed-root", "shared-root-id"}, "child-thread": {"completed-child", "shared-root-id"}},
		}},
	}
	for _, tc := range []struct {
		native string
		child  bool
	}{
		{"running-root", false}, {"completed-root", false}, {"shared-root-id", false}, {"running-child", true}, {"completed-child", true}, {"bound-before-started", false}, {"", false},
	} {
		if got := c.IsChildProviderTurn(c.scopedID(tc.native)); got != tc.child {
			t.Fatalf("native turn %q child = %t, want %t", tc.native, got, tc.child)
		}
	}
	c.proc.liveState = nil
	if c.IsChildProviderTurn(c.scopedID("unknown")) {
		t.Fatal("reliable host without a scope snapshot guessed child ownership")
	}
	c.proc.reliableReplay = false
	if c.IsChildProviderTurn(c.scopedID("legacy-pending")) {
		t.Fatal("legacy host lost its native-history recovery path")
	}
}

func TestFreshReliableHostOpensDeliveryBeforeInitialize(t *testing.T) {
	driver, server := newTestDriver(t)
	proc, err := driver.spawn(context.Background(), "codex", "/tmp/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	driver.persistent = true
	driver.connectHost = func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
		if cfg.Protocol != persistenthost.ProtocolCodex || cfg.OwnershipFingerprint == "" {
			t.Fatalf("host omitted reliable profile or stable ownership: %#v", cfg)
		}
		return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout, CodexReplay: true}, nil
	}
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		SessionID: "fresh", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), ProviderIDsScoped: true, ProviderScopeID: "scope",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	server.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
	server.mu.Lock()
	first := server.seen[0].Method
	second := server.seen[1].Method
	server.mu.Unlock()
	if first != persistenthost.CodexReplayMethod || second != "initialize" {
		t.Fatalf("startup order = %q, %q; delivery must open before initialize", first, second)
	}
}

func TestMalformedReliableDeliveryStopsWithoutAcknowledgment(t *testing.T) {
	for _, raw := range []string{
		`{"method":"_ao/persistent_codex_event","params":{"frame":{"method":"turn/completed","params":{}}}}`,
		`{"method":"_ao/persistent_codex_event","params":{"eventId":"host:1","frame":{"id":1,"result":{}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			c, server := reliableTestConversation(t)
			server.push(raw)
			event := nextEvent(t, c.Events(), ports.ChatEventControllerState)
			if event.ControllerState != ports.ChatControllerStopped || !errors.Is(event.Err, ports.ErrChatRecoveryInconclusive) {
				t.Fatalf("malformed delivery lost durable ownership: %#v", event)
			}
			if err := c.AcknowledgeProviderEvent(context.Background(), "host:1"); err != nil {
				t.Fatal(err)
			}
			if server.sentMethod(persistenthost.CodexAckMethod) {
				t.Fatal("acknowledged a malformed or unprojected host frame")
			}
		})
	}
}

func TestReliableReplayPreservesOriginalObservationTime(t *testing.T) {
	observedAt := time.Date(2026, time.October, 17, 19, 16, 43, 0, time.UTC)
	for _, replayed := range []bool{false, true} {
		c, server := reliableTestConversation(t)
		delivery, err := json.Marshal(map[string]any{
			"method": persistenthost.CodexEventMethod,
			"params": persistenthost.CodexEvent{
				EventID: "host:1", Replayed: replayed, ObservedAt: observedAt,
				Frame: json.RawMessage(`{"method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":10,"resetsAt":4102444800}}}}`),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		server.push(string(delivery))
		events := collectReliable(t, c, 1)
		if len(events) != 1 || events[0].RateLimits == nil || events[0].RateLimits.CodexCapacity == nil || !events[0].RateLimits.CodexCapacity.ObservedAt.Equal(observedAt) {
			t.Fatalf("replay replaced the original provider observation time: %#v", events)
		}
	}
}

func TestReliableReplayRestoresCitationFormatterFromRetainedPrefix(t *testing.T) {
	var original []ports.ChatEvent
	for _, replayed := range []bool{false, true} {
		c, server := reliableTestConversation(t)
		search, _ := json.Marshal(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": testSearchItem()}})
		text := "Claim " + citationStart + "cite" + citationField + "turn0search0" + citationStop
		answer, _ := json.Marshal(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"type": "agentMessage", "id": "answer", "text": text}}})
		pushReliable(t, server, "host:1", replayed, string(search))
		pushReliable(t, server, "host:2", replayed, string(answer))
		events := collectReliable(t, c, 2)
		for _, event := range events {
			require.NoError(t, event.Err)
		}
		if events[len(events)-1].Text != "Claim [1](<https://example.com/first>)" {
			t.Fatalf("lost citation prefix state: %#v", events)
		}
		if original == nil {
			original = events
		} else {
			require.Equal(t, original, events, "citation replay changed projected facts")
		}
	}
}

func TestReliableDeliveryPreservesLateFactsWithoutChangingSuccessorOwnership(t *testing.T) {
	c, server := reliableTestConversation(t)
	pushReliable(t, server, "host:1", false, `{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"old","status":"completed","items":[]}}}`)
	collectReliable(t, c, 1)
	pushReliable(t, server, "host:2", false, `{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"new","status":"inProgress","items":[]}}}`)
	pushReliable(t, server, "host:3", false, `{"method":"thread/tokenUsage/updated","params":{"threadId":"thread-1","turnId":"old","tokenUsage":{"last":{"totalTokens":5},"total":{"totalTokens":10}}}}`)
	pushReliable(t, server, "host:4", false, `{"method":"item/commandExecution/outputDelta","params":{"threadId":"thread-1","turnId":"old","itemId":"background-command","delta":"background output"}}`)
	events := collectReliable(t, c, 3)
	if events[0].Kind != ports.ChatEventTurnStarted || events[1].Kind != ports.ChatEventUsage || events[1].Usage.TotalTokens != 10 || events[2].Kind != ports.ChatEventCommandOutputDelta || events[2].Delta != "background output" {
		t.Fatalf("late usage or background output was discarded: %#v", events)
	}
	c.mu.Lock()
	active := c.activeTurn
	c.mu.Unlock()
	if active != "new" {
		t.Fatalf("successor active turn = %q", active)
	}
}
