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

	if err := s.CancelPendingTransfer(ctx, rec.ID, "tr-first00"); err != nil {
		t.Fatal(err)
	}
	if _, ok := moved(); !ok {
		t.Fatal("cancelling another transfer id removed the pending move")
	}
	if err := s.CancelPendingTransfer(ctx, rec.ID, "tr-second0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := moved(); ok {
		t.Fatal("pending move survived its cancel")
	}

	if err := s.RecordPendingTransfer(ctx, rec.ID, domain.SessionMovedTo{TransferID: "tr-third00", HostID: "b", Label: "MacBook B"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSessionTransfer(ctx, rec.ID, domain.SessionMovedTo{TransferID: "tr-third00", HostID: "b", Label: "MacBook B", SessionID: "web-9"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPendingTransfer(ctx, rec.ID, domain.SessionMovedTo{TransferID: "tr-late000", HostID: "c", Label: "Other"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPendingTransfer(ctx, rec.ID, "tr-third00"); err != nil {
		t.Fatal(err)
	}
	if m, ok := moved(); !ok || m.Pending() || m.SessionID != "web-9" || !listed() {
		t.Fatalf("moved = %+v %v, listed %v; a finished move must stay as committed", m, ok, listed())
	}
}
