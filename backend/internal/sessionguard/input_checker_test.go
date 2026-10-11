package sessionguard

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type checkedInputLease struct {
	held     bool
	checkErr error
	checked  bool
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
			lease.checkErr = errors.New("pending native decision or draft")
		}
		messenger := &fakeMessenger{}
		g := New(&fakeStore{rec: record(domain.ActivityIdle, false), ok: true}, messenger, nil)
		g.SetInputLease(lease)
		recorded := false
		outcome, err := g.DeliverWithPostWrite(context.Background(), "s1", "task", func(context.Context) error { recorded = true; return nil })
		if !lease.checked || lease.held {
			t.Fatal("composer not checked under a released lease")
		}
		if rejected {
			if err == nil || outcome == Sent || recorded || len(messenger.sent) != 0 {
				t.Fatal("rejected terminal input was delivered/recorded")
			}
		} else if err != nil || outcome != Sent || !recorded || len(messenger.sent) != 1 {
			t.Fatalf("safe input failed: %v,%v", outcome, err)
		}
	}
}
