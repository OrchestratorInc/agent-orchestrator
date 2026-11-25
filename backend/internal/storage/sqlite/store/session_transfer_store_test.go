package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionTransferPendingCommitAndCancel(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "web")
	rec, err := s.CreateSession(ctx, sampleRecord("web"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	moved := func() (domain.SessionMovedTo, bool) {
		m, ok, err := s.SessionMovedTo(ctx, rec.ID)
		if err != nil {
			t.Fatal(err)
		}
		return m, ok
	}
	listed := func() bool {
		ids, err := s.ListMovedSessionIDs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ids[rec.ID]
	}

	if err := s.RecordPendingTransfer(ctx, rec.ID, domain.SessionMovedTo{TransferID: "tr-first00", HostID: "b", Label: "MacBook B"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPendingTransfer(ctx, rec.ID, domain.SessionMovedTo{TransferID: "tr-second0", HostID: "b", Label: "MacBook B"}, now); err != nil {
		t.Fatal(err)
	}
	if m, ok := moved(); !ok || !m.Pending() || m.TransferID != "tr-second0" || listed() {
		t.Fatalf("pending = %+v %v, listed %v; want the second pending move, not listed as moved", m, ok, listed())
	}

	cancel := func(transferID string, want bool) {
		t.Helper()
		if ok, err := s.CancelPendingTransfer(ctx, rec.ID, transferID); err != nil || ok != want {
			t.Fatalf("cancel %s = %v, %v; want %v", transferID, ok, err, want)
		}
	}
	finish := func(m domain.SessionMovedTo, want bool) {
		t.Helper()
		if ok, err := s.FinishPendingTransfer(ctx, rec.ID, m, now); err != nil || ok != want {
			t.Fatalf("finish %+v = %v, %v; want %v", m, ok, err, want)
		}
	}
	cancel("tr-first00", false)
	if _, ok := moved(); !ok {
		t.Fatal("cancelling another transfer id removed the pending move")
	}
	cancel("tr-second0", true)
	if _, ok := moved(); ok {
		t.Fatal("pending move survived its cancel")
	}

	done := domain.SessionMovedTo{TransferID: "tr-third00", HostID: "b", Label: "MacBook B", ProjectID: "web-2", SessionID: "web-9"}
	finish(done, false)
	if _, ok := moved(); ok {
		t.Fatal("finishing a cancelled move brought it back")
	}
	pending := done
	pending.SessionID = ""
	if err := s.RecordPendingTransfer(ctx, rec.ID, pending, now); err != nil {
		t.Fatal(err)
	}
	if m, ok := moved(); !ok || m != pending {
		t.Fatalf("pending = %+v %v, want %+v", m, ok, pending)
	}
	for _, other := range []domain.SessionMovedTo{
		{TransferID: "tr-other00", HostID: "b", ProjectID: "web-2", SessionID: "web-9"},
		{TransferID: "tr-third00", HostID: "c", ProjectID: "web-2", SessionID: "web-9"},
		{TransferID: "tr-third00", HostID: "b", ProjectID: "web", SessionID: "web-9"},
	} {
		finish(other, false)
	}
	finish(done, true)
	finish(done, true)
	other := done
	other.SessionID = "web-10"
	finish(other, false)
	if err := s.RecordPendingTransfer(ctx, rec.ID, domain.SessionMovedTo{TransferID: "tr-late000", HostID: "c", Label: "Other"}, now); err != nil {
		t.Fatal(err)
	}
	cancel("tr-third00", false)
	if m, ok := moved(); !ok || m.Pending() || m.SessionID != "web-9" || !listed() {
		t.Fatalf("moved = %+v %v, listed %v; a finished move must stay as committed", m, ok, listed())
	}
}
