package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func isDeltaMethod(method string) bool {
	switch ports.ChatEventKind(method) {
	case ports.ChatEventMessageDelta, ports.ChatEventReasoningDelta, ports.ChatEventCommandOutputDelta,
		ports.ChatEventCommandInput, ports.ChatEventActivityText:
		return true
	}
	return false
}

// commitCountingStore counts the write transactions the projector opens for
// streamed deltas, and how many of them have returned without error. An archive
// call made inside another one shares its parent's transaction and is not
// counted.
type commitCountingStore struct {
	*sqlite.Store
	depth          atomic.Int32
	deltaCommits   atomic.Int32
	deltaCommitted atomic.Int32
}

func (s *commitCountingStore) ProjectProviderEvent(
	ctx context.Context, conversationID string, session domain.SessionID,
	generation, providerEventID, method, payloadJSON string, now time.Time,
	project func(context.Context) error,
) (bool, error) {
	top := s.depth.Add(1) == 1 && isDeltaMethod(method)
	defer s.depth.Add(-1)
	if top {
		s.deltaCommits.Add(1)
	}
	projected, err := s.Store.ProjectProviderEvent(ctx, conversationID, session, generation,
		providerEventID, method, payloadJSON, now, project)
	if top && err == nil {
		s.deltaCommitted.Add(1)
	}
	return projected, err
}

// deltaScript is one turn with every streamed column: reasoning past the
// streamed-text cap, command output past the output cap, terminal input, and a
// long reply. Each delta stream is followed by the event that settles its item.
func deltaScript(identified bool) (events []ports.ChatEvent, streamBytes []int) {
	const turn = "pt-batch"
	add := func(event ports.ChatEvent) {
		if identified {
			event.ProviderEventID = fmt.Sprintf("native-%04d", len(events))
		}
		events = append(events, event)
	}
	stream := func(kind ports.ChatEventKind, item string, count, size int) {
		for i := range count {
			chunk := fmt.Sprintf("%s-%03d|", item, i)
			add(ports.ChatEvent{Kind: kind, ProviderTurnID: turn, ProviderItemID: item,
				Delta: chunk + strings.Repeat(".", size-len(chunk))})
		}
		streamBytes = append(streamBytes, count*size)
	}
	add(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn})
	add(ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: turn, ProviderItemID: "rs",
		ActivityKind: domain.ActivityKindReasoning, ActivityStatus: domain.ActivityStatusRunning, Summary: "Reasoning"})
	stream(ports.ChatEventReasoningDelta, "rs", 170, 200)
	add(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turn, ProviderItemID: "rs",
		ActivityKind: domain.ActivityKindReasoning, ActivityStatus: domain.ActivityStatusCompleted, Summary: "Reasoning"})
	add(ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: turn, ProviderItemID: "cmd",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusRunning, Summary: "make"})
	stream(ports.ChatEventCommandOutputDelta, "cmd", 170, 400)
	stream(ports.ChatEventCommandInput, "cmd", 10, 20)
	add(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turn, ProviderItemID: "cmd",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted, Summary: "make"})
	stream(ports.ChatEventMessageDelta, "msg", 170, 200)
	var reply strings.Builder
	for _, event := range events {
		if event.Kind == ports.ChatEventMessageDelta {
			reply.WriteString(event.Delta)
		}
	}
	add(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: turn, ProviderItemID: "msg", Text: reply.String()})
	add(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn, TurnState: domain.TurnStateCompleted})
	return events, streamBytes
}

// transcriptRow is what a reader of the conversation sees for one timeline row:
// everything except AO-minted ids, timestamps and revision counters.
type transcriptRow struct {
	Sequence               int64
	Role                   domain.MessageRole
	Kind                   domain.ActivityKind
	Status                 domain.ActivityStatus
	Item                   string
	Turn                   string
	Summary                string
	Text                   string
	Streaming              bool
	StreamedText           string
	StreamedTextTruncated  bool
	CommandOutput          string
	CommandOutputTruncated bool
}

func transcriptOf(snapshot store.ConversationSnapshot) ([]transcriptRow, []string) {
	providerTurn := map[string]string{}
	var turns []string
	for _, turn := range snapshot.Turns {
		providerTurn[turn.ID] = turn.ProviderTurnID
		turns = append(turns, turn.ProviderTurnID+":"+string(turn.State))
	}
	var rows []transcriptRow
	for _, message := range snapshot.Messages {
		rows = append(rows, transcriptRow{
			Sequence: message.Sequence, Role: message.Role, Item: message.ProviderItemID,
			Turn: providerTurn[message.TurnID], Text: message.Text, Streaming: message.Streaming,
		})
	}
	for _, activity := range snapshot.Activities {
		rows = append(rows, transcriptRow{
			Sequence: activity.Sequence, Kind: activity.Kind, Status: activity.Status, Item: activity.ProviderItemID,
			Turn: providerTurn[activity.TurnID], Summary: activity.Summary,
			StreamedText: activity.StreamedText, StreamedTextTruncated: activity.StreamedTextTruncated,
			CommandOutput: activity.CommandOutput, CommandOutputTruncated: activity.CommandOutputTruncated,
		})
	}
	slices.SortFunc(rows, func(a, b transcriptRow) int { return int(a.Sequence - b.Sequence) })
	return rows, turns
}

type archivedEvent struct {
	Method, EventID, Item, Delta string
}

// archiveOf reads the provider archive. Joined merges consecutive delta rows of
// one stream, which is how a reader reconstructs the stream whatever the run
// boundaries were.
func archiveOf(t *testing.T, st *sqlite.Store, conversationID string, joined bool) []archivedEvent {
	t.Helper()
	rows, err := st.ProviderEventsSince(context.Background(), conversationID, 0, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	var events []archivedEvent
	for _, row := range rows {
		var payload struct {
			ProviderEventID string `json:"providerEventId"`
			ProviderItemID  string `json:"providerItemId"`
			Delta           string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(row.PayloadJson), &payload); err != nil {
			t.Fatal(err)
		}
		event := archivedEvent{Method: row.Method, EventID: payload.ProviderEventID, Item: payload.ProviderItemID, Delta: payload.Delta}
		if last := len(events) - 1; joined && last >= 0 && isDeltaMethod(event.Method) &&
			events[last].Method == event.Method && events[last].Item == event.Item {
			events[last].Delta += event.Delta
			continue
		}
		events = append(events, event)
	}
	return events
}

type scriptRun struct {
	transcript []transcriptRow
	turns      []string
	archive    []archivedEvent
	commits    int
}

// projectScript queues the whole script before the controller starts, the shape
// of a backlog that built while the writer was busy, and returns what landed.
func projectScript(t *testing.T, interval time.Duration, script []ports.ChatEvent, joinArchive bool) scriptRun {
	t.Helper()
	restore := chatsvc.SetDeltaFlushInterval(interval)
	defer restore()
	conv := newFakeConversation()
	conv.events = make(chan ports.ChatEvent, len(script))
	conv.emit(script...)
	var counter *commitCountingStore
	h := newHarnessWithConversationAndStore(t, conv, func(st *sqlite.Store) chatsvc.Store {
		counter = &commitCountingStore{Store: st}
		return counter
	})
	deadline := time.Now().Add(time.Minute)
	for {
		snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Turns) == 1 && snapshot.Turns[0].State.Terminal() {
			transcript, turns := transcriptOf(snapshot)
			return scriptRun{
				transcript: transcript, turns: turns,
				archive: archiveOf(t, h.st, h.ctrl.ConversationID(), joinArchive),
				commits: int(counter.deltaCommits.Load()),
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("script never completed its turn; turns = %+v", snapshot.Turns)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A backlog of N deltas costs a transaction per run instead of one per delta,
// and what lands (timeline rows, sequences, caps, truncation flags, archive
// order) is exactly what the unbatched projector produced.
func TestStreamedDeltasCommitInRunsWithTheUnbatchedTranscript(t *testing.T) {
	for _, identified := range []bool{false, true} {
		name := "anonymous deltas"
		if identified {
			name = "deltas with native event ids"
		}
		t.Run(name, func(t *testing.T) {
			script, streamBytes := deltaScript(identified)
			deltas := 0
			for _, event := range script {
				if isDeltaMethod(string(event.Kind)) {
					deltas++
				}
			}
			// Anonymous runs are archived as one row each; identified deltas keep a
			// row apiece so replay dedupe stays exact. Either way, joining each
			// stream's consecutive rows must give the unbatched archive.
			batched := projectScript(t, time.Hour, script, !identified)
			unbatched := projectScript(t, 0, script, !identified)

			if unbatched.commits != deltas {
				t.Fatalf("unbatched projector committed %d delta transactions, want one per delta (%d)", unbatched.commits, deltas)
			}
			bound := 0
			for _, size := range streamBytes {
				bound += (size + chatsvc.DeltaRunMaxBytes - 1) / chatsvc.DeltaRunMaxBytes
			}
			if batched.commits > bound {
				t.Fatalf("batched projector committed %d delta transactions for %d deltas, want at most %d", batched.commits, deltas, bound)
			}
			if !slices.Equal(batched.turns, unbatched.turns) {
				t.Fatalf("turns differ:\n batched   %v\n unbatched %v", batched.turns, unbatched.turns)
			}
			if !slices.Equal(batched.transcript, unbatched.transcript) {
				t.Fatalf("transcript differs:\n batched   %+v\n unbatched %+v", batched.transcript, unbatched.transcript)
			}
			if !slices.Equal(batched.archive, unbatched.archive) {
				t.Fatalf("archive differs: batched %d rows, unbatched %d rows", len(batched.archive), len(unbatched.archive))
			}
			// The caps still bite at the same place.
			for _, row := range batched.transcript {
				if row.Item == "rs" && (!row.StreamedTextTruncated || len(row.StreamedText) != store.MaxStreamedTextChars) {
					t.Fatalf("reasoning row = %d chars truncated=%v, want capped", len(row.StreamedText), row.StreamedTextTruncated)
				}
				if row.Item == "cmd" && (!row.CommandOutputTruncated || len(row.CommandOutput) != store.MaxCommandOutputChars) {
					t.Fatalf("command row = %d chars truncated=%v, want capped", len(row.CommandOutput), row.CommandOutputTruncated)
				}
			}
			t.Logf("%d deltas: %d delta transactions batched, %d unbatched", deltas, batched.commits, unbatched.commits)
		})
	}
}

// The persistent Claude ACP path from #4657 stamps every delta with a native
// identity and needs each one acknowledged once it is durable. A run keeps that
// contract, in stream order, while the whole reply costs one transaction.
func TestPersistentDeltaRunAcknowledgesEachEventAfterCommit(t *testing.T) {
	restore := chatsvc.SetDeltaFlushInterval(time.Hour)
	defer restore()
	const deltas = 50
	var (
		counter *commitCountingStore
		acked   []string
		early   []string
		reply   strings.Builder
	)
	conv := &receiptConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}}
	conv.ack = func(id string) error {
		// Runs on the projector goroutine. counter is set before Start launches
		// it; a delta must not be acknowledged before its transaction returned.
		if strings.HasPrefix(id, "d-") && counter.deltaCommitted.Load() == 0 {
			early = append(early, id)
		}
		acked = append(acked, id)
		return nil
	}
	conv.events = make(chan ports.ChatEvent, deltas+2)
	want := []string{"start"}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "pt-ack", ProviderEventID: "start"})
	for i := range deltas {
		id := fmt.Sprintf("d-%02d", i)
		reply.WriteString(id + "|")
		want = append(want, id)
		conv.emit(ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "pt-ack", ProviderItemID: "msg",
			ProviderEventID: id, Delta: id + "|"})
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "pt-ack", ProviderEventID: "done",
		TurnState: domain.TurnStateCompleted})
	want = append(want, "done")

	h := newHarnessWithConversationAndStore(t, conv, func(st *sqlite.Store) chatsvc.Store {
		counter = &commitCountingStore{Store: st}
		return counter
	})
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != reply.String() {
		t.Fatalf("reply = %+v, want every delta once", snapshot.Messages)
	}
	if got := counter.deltaCommits.Load(); got != 1 {
		t.Fatalf("%d deltas took %d transactions, want one run", deltas, got)
	}
	// Stop waits for the projector, so its acknowledgements are complete.
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(acked, want) {
		t.Fatalf("acknowledged %v, want %v", acked, want)
	}
	if len(early) != 0 {
		t.Fatalf("acknowledged %v before their transaction returned", early)
	}
}

// A persistent host replays every frame of an unacknowledged prompt, and the
// replay may batch differently from the original stream. Deltas the archive
// already holds are skipped one by one, so a run that mixes replayed and new
// deltas appends only the new text, exactly once.
func TestReplayedIdentifiedDeltasProjectExactlyOnce(t *testing.T) {
	restore := chatsvc.SetDeltaFlushInterval(time.Hour)
	defer restore()
	delta := func(i int) ports.ChatEvent {
		return ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "pt-replay", ProviderItemID: "msg",
			ProviderEventID: fmt.Sprintf("native-%d", i), Delta: string(rune('a' + i))}
	}
	conv := newFakeConversation()
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "pt-replay", ProviderEventID: "native-start"})
	for i := range 5 {
		conv.emit(delta(i))
	}
	h := newHarnessWithConversation(t, conv)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) == 1 && s.Messages[0].Text == "abcde"
	})

	// The replay starts from the top of the prompt and carries three new deltas.
	for i := range 8 {
		conv.emit(delta(i))
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "pt-replay",
		ProviderEventID: "native-done", TurnState: domain.TurnStateCompleted})
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != "abcdefgh" {
		t.Fatalf("replayed reply = %+v, want each delta exactly once", snapshot.Messages)
	}
	var ids []string
	for _, event := range archiveOf(t, h.st, h.ctrl.ConversationID(), false) {
		if event.Method == string(ports.ChatEventMessageDelta) {
			ids = append(ids, event.EventID)
		}
	}
	want := []string{"native-0", "native-1", "native-2", "native-3", "native-4", "native-5", "native-6", "native-7"}
	if !slices.Equal(ids, want) {
		t.Fatalf("archived delta ids = %v, want %v", ids, want)
	}
}

// ChatEvent.Text documents an empty settle as "the provider settled nothing".
// A completion without text must stop the streaming marker without erasing the
// reply the user watched arrive.
func TestEmptyMessageCompletionKeepsStreamedText(t *testing.T) {
	h := newHarness(t)
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "pt-1"},
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "pt-1", ProviderItemID: "msg", Delta: "Hello"},
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "pt-1", ProviderItemID: "msg", Delta: " world"},
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "pt-1", ProviderItemID: "msg"},
	)
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) == 1 && !s.Messages[0].Streaming
	})
	if got := snapshot.Messages[0].Text; got != "Hello world" {
		t.Fatalf("settled text = %q, want the streamed reply kept", got)
	}
}

// Stop can settle a running turn directly when the controller's memory and the
// durable turn disagree. Deltas still waiting in a run are committed first, so
// they land before the settle instead of reopening the settled reply as
// streaming.
func TestInterruptCommitsBatchedDeltasBeforeSettlingTheTurn(t *testing.T) {
	restore := chatsvc.SetDeltaFlushInterval(time.Hour)
	defer restore()
	h := newHarness(t)
	ctx := context.Background()
	const providerTurnID = "stale-running-turn"
	if err := h.st.AdoptProviderTurn(ctx, h.ctrl.ConversationID(), testSession,
		h.ctrl.Generation(), "stale-turn-id", providerTurnID, h.now()); err != nil {
		t.Fatalf("AdoptProviderTurn: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: providerTurnID, ProviderItemID: "msg", Delta: "first"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) == 1 && s.Messages[0].Text == "first"
	})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: providerTurnID, ProviderItemID: "msg", Delta: " pending"})
	for deadline := time.Now().Add(5 * time.Second); len(h.conv.events) > 0; {
		if time.Now().After(deadline) {
			t.Fatal("projector never read the second delta")
		}
		time.Sleep(time.Millisecond)
	}

	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != "first pending" || snapshot.Messages[0].Streaming {
		t.Fatalf("reply when Stop returned = %+v, want all received text, settled", snapshot.Messages)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	snapshot, err = h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Messages[0].Streaming || snapshot.Turns[0].State != domain.TurnStateInterrupted {
		t.Fatalf("after shutdown: message streaming=%v turn=%s, want settled and interrupted",
			snapshot.Messages[0].Streaming, snapshot.Turns[0].State)
	}
}
