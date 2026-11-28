package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type flakyAttachmentHolder struct {
	failures int
	calls    int
	accepted []bool
}

func (f *flakyAttachmentHolder) HoldChatAttachmentLeases(context.Context, domain.SessionID, []string) ([]string, error) {
	return nil, nil
}

func (f *flakyAttachmentHolder) SettleChatAttachmentHolds(_ context.Context, _ domain.SessionID, _ []string, accepted bool) error {
	f.calls++
	if f.calls <= f.failures {
		return errors.New("database is locked")
	}
	f.accepted = append(f.accepted, accepted)
	return nil
}

func withoutSettleBackoff(t *testing.T) {
	t.Helper()
	saved := sideAttachmentSettleBackoff
	sideAttachmentSettleBackoff = []time.Duration{0, 0, 0}
	t.Cleanup(func() { sideAttachmentSettleBackoff = saved })
}

// A transient failure to settle a side question's holds is retried, so the
// held files are not kept until the session is deleted.
func TestSettleSideAttachmentHoldsRetriesFailedWrites(t *testing.T) {
	withoutSettleBackoff(t)
	holder := &flakyAttachmentHolder{failures: 2}
	if err := settleSideAttachmentHolds(context.Background(), holder, "s", []string{"attachment-a"}, false); err != nil {
		t.Fatalf("settle = %v, want success after retries", err)
	}
	if holder.calls != 3 || len(holder.accepted) != 1 || holder.accepted[0] {
		t.Fatalf("calls = %d, settled = %v", holder.calls, holder.accepted)
	}
}

// Retrying is bounded: a settle that keeps failing reports its error.
func TestSettleSideAttachmentHoldsGivesUpAfterBackoff(t *testing.T) {
	withoutSettleBackoff(t)
	holder := &flakyAttachmentHolder{failures: 100}
	if err := settleSideAttachmentHolds(context.Background(), holder, "s", []string{"attachment-a"}, true); err == nil {
		t.Fatal("settle succeeded, want the last error")
	}
	if want := len(sideAttachmentSettleBackoff) + 1; holder.calls != want {
		t.Fatalf("calls = %d, want %d", holder.calls, want)
	}
}
