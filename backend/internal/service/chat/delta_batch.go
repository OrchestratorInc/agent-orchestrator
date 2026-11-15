package chat

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var deltaFlushInterval = 50 * time.Millisecond

const (
	deltaRunMaxBytes  = 32 * 1024
	deltaRunMaxEvents = 1024
)

func coalescible(event ports.ChatEvent) bool {
	switch event.Kind {
	case ports.ChatEventMessageDelta, ports.ChatEventReasoningDelta, ports.ChatEventCommandOutputDelta,
		ports.ChatEventCommandInput, ports.ChatEventActivityText:
		return event.Err == nil && !strings.ContainsRune(event.Delta, '\x00')
	}
	return false
}

func sameStream(run, next ports.ChatEvent) bool {
	run.Delta, run.ProviderEventID = "", ""
	next.Delta, next.ProviderEventID = "", ""
	return reflect.DeepEqual(run, next) //nolint:govet // coalescible events have nil Err; compare the rest exactly.
}

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
