package chat

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// deltaFlushInterval is the longest a streamed delta waits for its write while
// more of the same stream keeps arriving. A var so tests can change it.
var deltaFlushInterval = 50 * time.Millisecond

// deltaRunMaxBytes and deltaRunMaxEvents bound one run of deltas, and with it
// the archive row and the transaction a backlog can produce. The event bound
// matters for deltas with native identities, which keep an archive row each:
// without it, a replayed backlog of tiny chunks would hold the shared writer
// for one very long transaction.
const (
	deltaRunMaxBytes  = 32 * 1024
	deltaRunMaxEvents = 1024
)

// coalescible reports whether event only appends text to one provider item.
func coalescible(event ports.ChatEvent) bool {
	switch event.Kind {
	case ports.ChatEventMessageDelta, ports.ChatEventReasoningDelta, ports.ChatEventCommandOutputDelta,
		ports.ChatEventCommandInput, ports.ChatEventActivityText:
		// SQLite's capped appends stop at NUL; joining would discard later deltas too.
		return event.Err == nil && !strings.ContainsRune(event.Delta, '\x00')
	}
	return false
}

// sameStream reports whether next appends to the same column of the same row as
// run, carrying the same metadata. Only the text and the native event identity
// may differ.
func sameStream(run, next ports.ChatEvent) bool {
	run.Delta, run.ProviderEventID = "", ""
	next.Delta, next.ProviderEventID = "", ""
	return reflect.DeepEqual(run, next) //nolint:govet // coalescible events have nil Err; compare the rest exactly.
}

// pumpProviderEvents hands provider events to project in arrival order, one unit
// at a time: a single event, or a run of consecutive deltas for the same stream,
// which project commits as one transaction instead of one per token.
//
// A run is written before any other event, when the stream ends, when a flush is
// requested, when it reaches its size bounds, and no later than interval after
// its first delta. Once nothing more is queued, a run is written at once if it
// starts a different stream from the last run written or no run was written in
// the last interval: an idle writer adds no latency, and a new item's first
// delta, which creates its row and takes its timeline position, is not held
// back. While one stream keeps going, its runs are written at most once per
// interval. Deltas that arrive while a write is running wait in the channel and
// join the next run. It reports false when project stops consumption.
func pumpProviderEvents(
	events <-chan ports.ChatEvent,
	flushRequests <-chan chan struct{},
	interval time.Duration,
	project func([]ports.ChatEvent) bool,
) bool {
	var (
		run       []ports.ChatEvent
		runBytes  int
		runSince  time.Time
		lastWrite time.Time
		lastHead  ports.ChatEvent
	)
	timer := time.NewTimer(interval)
	timer.Stop()
	defer timer.Stop()
	flush := func() bool {
		if len(run) == 0 {
			return true
		}
		unit := run
		run, runBytes = nil, 0
		lastWrite, lastHead = time.Now(), unit[0]
		return project(unit)
	}
	answer := func(done chan struct{}) bool {
		written := flush()
		close(done)
		return written
	}
	for {
		if len(run) > 0 && time.Since(runSince) >= interval {
			if !flush() {
				return false
			}
			continue
		}
		var (
			event ports.ChatEvent
			ok    bool
		)
		select {
		case event, ok = <-events:
		case done := <-flushRequests:
			if !answer(done) {
				return false
			}
			continue
		default:
			if len(run) > 0 && (time.Since(lastWrite) >= interval || !sameStream(lastHead, run[0])) {
				if !flush() {
					return false
				}
				continue
			}
			var due <-chan time.Time
			if len(run) > 0 {
				timer.Reset(time.Until(lastWrite.Add(interval)))
				due = timer.C
			}
			select {
			case event, ok = <-events:
				timer.Stop()
			case <-due:
				if !flush() {
					return false
				}
				continue
			case done := <-flushRequests:
				timer.Stop()
				if !answer(done) {
					return false
				}
				continue
			}
		}
		if !ok {
			return flush()
		}
		if coalescible(event) && len(run) > 0 && len(run) < deltaRunMaxEvents && sameStream(run[0], event) &&
			runBytes+len(event.Delta) <= deltaRunMaxBytes {
			run = append(run, event)
			runBytes += len(event.Delta)
			continue
		}
		if !flush() {
			return false
		}
		if coalescible(event) {
			run, runBytes, runSince = []ports.ChatEvent{event}, len(event.Delta), time.Now()
			continue
		}
		if !project([]ports.ChatEvent{event}) {
			return false
		}
	}
}

// flushDeltas asks the projector to write the run it is batching and waits until
// that write has finished, the controller stopped, or ctx ended.
func (c *Controller) flushDeltas(ctx context.Context) {
	done := make(chan struct{})
	select {
	case c.flushRequests <- done:
	case <-c.stopped:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-done:
	case <-c.stopped:
	case <-ctx.Done():
	}
}

// projectDeltas commits a run of deltas for one stream in one transaction.
//
// Deltas without a native event identity are never deduplicated, so the run is
// archived as one event holding the joined text. Deltas with one keep a row
// apiece: a persistent host replays an unacknowledged prompt from its start, and
// a replay can split the stream into different runs, so only per-event
// identities let the archive skip exactly the deltas it already holds. The first
// delta not yet archived opens the transaction; the rest join it, and only text
// the archive had not seen is applied.
func (c *Controller) projectDeltas(ctx context.Context, run []ports.ChatEvent) (bool, error) {
	merged := run[0]
	if !slices.ContainsFunc(run, func(event ports.ChatEvent) bool { return event.ProviderEventID != "" }) {
		var text strings.Builder
		for _, event := range run {
			text.WriteString(event.Delta)
		}
		merged.Delta = text.String()
		projected, _, err := c.projectEvent(ctx, merged)
		return projected, err
	}
	merged.ProviderEventID = ""
	for i, head := range run {
		projected, err := c.archive(ctx, head, func(txCtx context.Context) error {
			var text strings.Builder
			text.WriteString(head.Delta)
			for _, next := range run[i+1:] {
				fresh, err := c.archive(txCtx, next, func(context.Context) error { return nil })
				if err != nil {
					return err
				}
				if fresh {
					text.WriteString(next.Delta)
				}
			}
			merged.Delta = text.String()
			return c.apply(txCtx, merged)
		})
		if err != nil || projected {
			return projected, err
		}
	}
	return false, nil
}
