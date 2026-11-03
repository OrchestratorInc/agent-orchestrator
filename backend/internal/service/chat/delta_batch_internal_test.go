package chat

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func streamDelta(kind ports.ChatEventKind, item, text string) ports.ChatEvent {
	return ports.ChatEvent{Kind: kind, ProviderTurnID: "turn", ProviderItemID: item, Delta: text}
}

func messageDelta(item, text string) ports.ChatEvent {
	return streamDelta(ports.ChatEventMessageDelta, item, text)
}

// writes renders each unit the pump handed over as one line, so a failure reads
// as the sequence of SQLite writes it would have produced.
func writes(units [][]ports.ChatEvent) []string {
	lines := make([]string, 0, len(units))
	for _, unit := range units {
		var text strings.Builder
		for _, event := range unit {
			text.WriteString(event.Delta)
		}
		lines = append(lines, fmt.Sprintf("%s %s %q", unit[0].Kind, unit[0].ProviderItemID, text.String()))
	}
	return lines
}

// pumpQueued runs the pump over events that were all queued before it started,
// the shape of a backlog that built up while the previous write was running.
func pumpQueued(t *testing.T, interval time.Duration, events ...ports.ChatEvent) [][]ports.ChatEvent {
	t.Helper()
	queued := make(chan ports.ChatEvent, len(events))
	for _, event := range events {
		queued <- event
	}
	close(queued)
	var units [][]ports.ChatEvent
	if !pumpProviderEvents(queued, nil, interval, func(unit []ports.ChatEvent) bool {
		units = append(units, unit)
		return true
	}) {
		t.Fatal("pump stopped although every write succeeded")
	}
	return units
}

// startPump runs the pump in the test's synctest bubble and exposes each unit as
// it is written. The returned end closes the stream once; deferring it lets a
// failed assertion end the bubble cleanly instead of reporting a deadlock.
func startPump(
	t *testing.T,
	events chan ports.ChatEvent,
	flushRequests <-chan chan struct{},
	project func([]ports.ChatEvent) bool,
) (units <-chan []ports.ChatEvent, ended <-chan bool, end func()) {
	t.Helper()
	written := make(chan []ports.ChatEvent, 64)
	done := make(chan bool, 1)
	var once sync.Once
	end = func() { once.Do(func() { close(events) }) }
	go func() {
		done <- pumpProviderEvents(events, flushRequests, 50*time.Millisecond, func(unit []ports.ChatEvent) bool {
			written <- unit
			if project != nil {
				return project(unit)
			}
			return true
		})
	}()
	return written, done, end
}

func drainUnits(units <-chan []ports.ChatEvent) [][]ports.ChatEvent {
	var got [][]ports.ChatEvent
	for {
		select {
		case unit := <-units:
			got = append(got, unit)
		default:
			return got
		}
	}
}

func assertWrites(t *testing.T, got [][]ports.ChatEvent, want ...string) {
	t.Helper()
	if lines := writes(got); !slices.Equal(lines, want) {
		t.Fatalf("writes:\n  got  %q\n  want %q", lines, want)
	}
}

// A run is one stream: the same kind of delta for the same provider item. Any
// other event, including a delta for another item or another column of the same
// item, writes the run first so the archive and projection keep arrival order.
func TestPumpKeepsArrivalOrderAcrossItemsAndEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		units := pumpQueued(t, 50*time.Millisecond,
			messageDelta("msg", "Hel"),
			messageDelta("msg", "lo"),
			ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: "turn", ProviderItemID: "cmd"},
			streamDelta(ports.ChatEventCommandOutputDelta, "cmd", "out1 "),
			streamDelta(ports.ChatEventCommandInput, "cmd", "typed"),
			streamDelta(ports.ChatEventCommandOutputDelta, "cmd", "out2"),
			messageDelta("other", "x"),
			messageDelta("msg", " again"),
			streamDelta(ports.ChatEventReasoningDelta, "rs", "think"),
			streamDelta(ports.ChatEventReasoningDelta, "rs", "ing"),
		)
		assertWrites(t, units,
			`message.delta msg "Hello"`,
			`activity.started cmd ""`,
			`command.output.delta cmd "out1 "`,
			`command.input cmd "typed"`,
			`command.output.delta cmd "out2"`,
			`message.delta other "x"`,
			`message.delta msg " again"`,
			`reasoning.delta rs "thinking"`,
		)
	})
}

// Deltas that differ in anything but their text stay apart, so a merged write
// never drops metadata one of them carried.
func TestPumpOnlyJoinsIdenticalStreams(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := messageDelta("msg", "a")
		otherTurn := messageDelta("msg", "b")
		otherTurn.ProviderTurnID = "turn-2"
		nested := messageDelta("msg", "c")
		nested.ProviderConversationID = "child-thread"
		failed := messageDelta("msg", "d")
		failed.Err = fmt.Errorf("provider failure")
		identified := messageDelta("msg", "e")
		identified.ProviderEventID = "native-1"
		sameStreamIdentified := messageDelta("msg", "f")
		sameStreamIdentified.ProviderEventID = "native-2"

		units := pumpQueued(t, 50*time.Millisecond, base, otherTurn, nested, failed, identified, sameStreamIdentified)
		assertWrites(t, units,
			`message.delta msg "a"`,
			`message.delta msg "b"`,
			`message.delta msg "c"`,
			`message.delta msg "d"`,
			// A native event identity is not stream metadata: identified deltas
			// batch, and the unit keeps every event so each identity is archived.
			`message.delta msg "ef"`,
		)
		if last := units[len(units)-1]; len(last) != 2 || last[0].ProviderEventID != "native-1" || last[1].ProviderEventID != "native-2" {
			t.Fatalf("identified run = %+v, want both native events in order", last)
		}
	})
}

// A backlog is bounded in bytes and in events, so one archive row or
// transaction cannot grow without limit while the writer is slow.
func TestPumpCapsRunSize(t *testing.T) {
	for _, tc := range []struct {
		name      string
		chunk     string
		count     int
		wantBytes []int
		wantRuns  []int
	}{
		{
			name: "bytes", chunk: strings.Repeat("x", deltaRunMaxBytes/4), count: 10,
			wantBytes: []int{deltaRunMaxBytes, deltaRunMaxBytes, deltaRunMaxBytes / 2},
			wantRuns:  []int{4, 4, 2},
		},
		{
			name: "events", chunk: "x", count: 2*deltaRunMaxEvents + 3,
			wantBytes: []int{deltaRunMaxEvents, deltaRunMaxEvents, 3},
			wantRuns:  []int{deltaRunMaxEvents, deltaRunMaxEvents, 3},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				events := make([]ports.ChatEvent, tc.count)
				for i := range events {
					events[i] = messageDelta("msg", tc.chunk)
				}
				units := pumpQueued(t, 50*time.Millisecond, events...)
				var sizes, runs []int
				var text strings.Builder
				for _, unit := range units {
					size := 0
					for _, event := range unit {
						size += len(event.Delta)
						text.WriteString(event.Delta)
					}
					sizes = append(sizes, size)
					runs = append(runs, len(unit))
				}
				if !slices.Equal(sizes, tc.wantBytes) || !slices.Equal(runs, tc.wantRuns) {
					t.Fatalf("runs = %v events / %v bytes, want %v / %v", runs, sizes, tc.wantRuns, tc.wantBytes)
				}
				if text.String() != strings.Repeat(tc.chunk, tc.count) {
					t.Fatal("capping runs changed the text")
				}
			})
		})
	}
}

// Zero turns batching off: every delta is its own write, as before batching.
func TestPumpWithoutIntervalWritesEveryDelta(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		units := pumpQueued(t, 0, messageDelta("msg", "a"), messageDelta("msg", "b"), messageDelta("msg", "c"))
		assertWrites(t, units,
			`message.delta msg "a"`,
			`message.delta msg "b"`,
			`message.delta msg "c"`,
		)
	})
}

// An idle writer adds no latency: the first delta is written at once. While the
// stream keeps going, later deltas wait for the window so a reply costs a write
// per interval rather than one per token.
func TestPumpWritesAtMostOncePerInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan ports.ChatEvent)
		units, ended, end := startPump(t, events, nil, nil)
		defer end()

		events <- messageDelta("msg", "first")
		synctest.Wait()
		assertWrites(t, drainUnits(units), `message.delta msg "first"`)

		time.Sleep(10 * time.Millisecond)
		events <- messageDelta("msg", " second")
		time.Sleep(20 * time.Millisecond)
		events <- messageDelta("msg", " third")
		time.Sleep(19 * time.Millisecond)
		synctest.Wait()
		if got := drainUnits(units); len(got) != 0 {
			t.Fatalf("wrote %q before the window closed", writes(got))
		}

		time.Sleep(time.Millisecond)
		synctest.Wait()
		assertWrites(t, drainUnits(units), `message.delta msg " second third"`)

		end()
		if !<-ended {
			t.Fatal("pump reported a stop for a stream that ended")
		}
	})
}

// A new item's first delta creates its row and takes its timeline position, so
// it is written at once even right after another stream's write; only the same
// stream continuing waits for the window.
func TestPumpWritesTheFirstDeltaOfANewStreamAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan ports.ChatEvent)
		units, _, end := startPump(t, events, nil, nil)
		defer end()

		events <- streamDelta(ports.ChatEventReasoningDelta, "rs", "think")
		time.Sleep(10 * time.Millisecond)
		events <- ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "turn", ProviderItemID: "rs"}
		time.Sleep(10 * time.Millisecond)
		events <- messageDelta("msg", "Hello")
		synctest.Wait()
		assertWrites(t, drainUnits(units),
			`reasoning.delta rs "think"`,
			`activity.completed rs ""`,
			`message.delta msg "Hello"`,
		)

		time.Sleep(10 * time.Millisecond)
		events <- messageDelta("msg", " world")
		synctest.Wait()
		if got := drainUnits(units); len(got) != 0 {
			t.Fatalf("wrote %q before the window closed", writes(got))
		}
		time.Sleep(40 * time.Millisecond)
		synctest.Wait()
		assertWrites(t, drainUnits(units), `message.delta msg " world"`)
	})
}

// Closing the stream is controller shutdown: whatever is still batched is
// written before the pump returns, without waiting for its window.
func TestPumpFlushesPendingRunWhenTheStreamEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan ports.ChatEvent)
		units, ended, end := startPump(t, events, nil, nil)
		defer end()

		events <- messageDelta("msg", "first")
		synctest.Wait()
		events <- messageDelta("msg", " pending")
		synctest.Wait()
		end()
		if !<-ended {
			t.Fatal("pump reported a stop for a stream that ended")
		}
		assertWrites(t, drainUnits(units), `message.delta msg "first"`, `message.delta msg " pending"`)
	})
}

// A non-delta event is never held back: it and the run before it are written
// immediately, run first.
func TestPumpWritesPendingRunBeforeALaterEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan ports.ChatEvent)
		units, _, end := startPump(t, events, nil, nil)
		defer end()

		events <- messageDelta("msg", "first")
		synctest.Wait()
		events <- messageDelta("msg", " pending")
		events <- ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "turn", ProviderItemID: "msg", Text: "first pending"}
		synctest.Wait()
		assertWrites(t, drainUnits(units),
			`message.delta msg "first"`,
			`message.delta msg " pending"`,
			`message.completed msg ""`,
		)
	})
}

// Deltas that arrive while a write is still running queue up and join the next
// run, which is written as soon as the writer is free again.
func TestPumpGrowsRunsWhileTheWriterIsBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan ports.ChatEvent, 16)
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
		units, _, end := startPump(t, events, nil, func(unit []ports.ChatEvent) bool {
			if unit[0].Delta == "slow" {
				<-release
			}
			return true
		})
		defer end()
		defer releaseWriter()

		events <- messageDelta("msg", "slow")
		synctest.Wait()
		for _, text := range []string{" a", " b", " c", " d"} {
			events <- messageDelta("msg", text)
		}
		time.Sleep(200 * time.Millisecond)
		releaseWriter()
		synctest.Wait()
		assertWrites(t, drainUnits(units), `message.delta msg "slow"`, `message.delta msg " a b c d"`)
	})
}

// A flush request writes the pending run before it is answered, so a write the
// requester makes next cannot overtake text the provider already sent.
func TestPumpFlushRequestWritesPendingRunFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan ports.ChatEvent)
		requests := make(chan chan struct{})
		units, _, end := startPump(t, events, requests, nil)
		defer end()
		flush := func() {
			t.Helper()
			done := make(chan struct{})
			select {
			case requests <- done:
			case <-time.After(time.Second):
				t.Fatal("pump never took the flush request")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("pump never answered the flush request")
			}
		}

		flush()
		if got := drainUnits(units); len(got) != 0 {
			t.Fatalf("idle flush wrote %q", writes(got))
		}

		events <- messageDelta("msg", "first")
		synctest.Wait()
		events <- messageDelta("msg", " pending")
		synctest.Wait()
		flush()
		assertWrites(t, drainUnits(units), `message.delta msg "first"`, `message.delta msg " pending"`)
	})
}

// A failed write that must stop the controller stops the pump: nothing after it
// is projected, batched or not.
func TestPumpStopsWhenProjectionStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queued := make(chan ports.ChatEvent, 4)
		queued <- messageDelta("msg", "a")
		queued <- ports.ChatEvent{Kind: ports.ChatEventUsage}
		queued <- messageDelta("msg", "b")
		close(queued)
		var units [][]ports.ChatEvent
		if pumpProviderEvents(queued, nil, 50*time.Millisecond, func(unit []ports.ChatEvent) bool {
			units = append(units, unit)
			return unit[0].Kind != ports.ChatEventUsage
		}) {
			t.Fatal("pump reported a clean end after projection stopped")
		}
		assertWrites(t, units, `message.delta msg "a"`, `usage  ""`)
	})
}
