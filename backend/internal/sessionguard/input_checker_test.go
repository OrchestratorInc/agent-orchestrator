package sessionguard

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type checkedInputLease struct {
	held     bool
	checkErr error
	checked  bool
}

// A guarded harness's rendered surface refuses a paste for different reasons,
// and callers answer each differently: a draft and a running turn are retried,
// while only a native decision asks the human to answer it in the terminal.
func TestRenderedSurfaceRefusalsMapToTheirOwnOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		checkErr error
		want     Outcome
	}{
		{"unsent draft", errors.Join(ports.ErrTerminalMessageNotReady, ports.ErrTerminalComposerDraft), SuppressedComposerBusy},
		{"active turn", errors.Join(ports.ErrTerminalMessageNotReady, ports.ErrTerminalTurnActive), SuppressedBusy},
		{"native decision", ports.ErrTerminalMessageNotReady, SuppressedAwaitingUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lease := &checkedInputLease{checkErr: tc.checkErr}
			messenger := &fakeMessenger{}
			g := New(&fakeStore{rec: record(domain.ActivityIdle, false), ok: true}, messenger, nil)
			g.SetInputLease(lease)
			outcome, err := g.Deliver(context.Background(), "s1", "task")
			if err != nil || outcome != tc.want || len(messenger.sent) != 0 || lease.held {
				t.Fatalf("outcome = %v, err = %v, sent = %d, held = %v; want %v with nothing written", outcome, err, len(messenger.sent), lease.held, tc.want)
			}
		})
	}
}

func (l *checkedInputLease) AcquireSessionInput(domain.SessionID) (func(), bool) {
	l.held = true
	return func() { l.held = false }, true
}
func (l *checkedInputLease) ValidateSessionMessageInput(context.Context, domain.SessionRecord, string) error {
	if !l.held {
		return errors.New("lease was released before checking composer")
	}
	l.checked = true
	return l.checkErr
}

func TestRenderedInputRejectionCannotWriteOrRecordDelivery(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		lease := &checkedInputLease{}
		if rejected {
			lease.checkErr = errors.New("rendered terminal is unavailable")
		}
		messenger := &fakeMessenger{}
		g := New(&fakeStore{rec: record(domain.ActivityIdle, false), ok: true}, messenger, nil)
		g.SetInputLease(lease)
		recorded := false
		outcome, err := g.DeliverWithComposerCheck(context.Background(), "s1", "task", nil, func(context.Context) error { recorded = true; return nil })
		if !lease.checked || lease.held {
			t.Fatal("composer not checked under a released lease")
		}
		if rejected {
			if err == nil || outcome != SuppressedUnknown || recorded || len(messenger.sent) != 0 {
				t.Fatalf("rejected terminal input was delivered/recorded: %v,%v", outcome, err)
			}
		} else if err != nil || outcome != Sent || !recorded || len(messenger.sent) != 1 {
			t.Fatalf("safe input failed: %v,%v", outcome, err)
		}
	}
}
