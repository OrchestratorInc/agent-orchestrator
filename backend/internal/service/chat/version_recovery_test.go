package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestVersionMoveKeepsQueuedTurnAfterServiceRecreation(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		t.Run(fmt.Sprintf("in_place=%v", inPlace), func(t *testing.T) {
			h, _, _ := versionRefreshHarness(t)
			ctx := context.Background()
			markStale(t, h)
			h.advance(refreshIdle)
			var stopped bool
			var err error
			if inPlace {
				stopped, err = h.svc.HibernateChatForUpdate(ctx, testSession)
			} else {
				stopped, err = h.svc.HibernateChatForVersionRefresh(ctx, testSession, refreshIdle)
			}
			if err != nil || !stopped {
				t.Fatalf("stop = %v, %v", stopped, err)
			}
			moves, err := h.st.ListSessionVersionMoves(ctx)
			if err != nil || len(moves) != 1 || moves[0].Stage != domain.VersionMoveRetry {
				t.Fatalf("stop was not durable: %+v, %v", moves, err)
			}
			var next atomic.Int32
			after := chatsvc.New(chatsvc.Options{
				Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
				Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
				NewID: func() string { return fmt.Sprintf("recreated-%d", next.Add(1)) },
			})
			t.Cleanup(func() { _ = after.Stop(context.Background(), testSession) })
			after.SetWakeCallback(func(context.Context, domain.SessionID) error { return errors.New("replacement unavailable") })
			turn, err := after.Send(ctx, testSession, ports.ChatUserMessage{Text: "keep this work", ClientMessageID: "after-restart"})
			if err != nil || turn.State != domain.TurnStateQueued {
				t.Fatalf("send = %+v, %v", turn, err)
			}
			wait, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := after.WaitForWake(wait, testSession); err != nil {
				t.Fatal(err)
			}
			snapshot, err := after.Snapshot(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			last := snapshot.Turns[len(snapshot.Turns)-1]
			if last.State != domain.TurnStateQueued {
				t.Fatalf("work lost after recreation: state=%s error=%s", last.State, last.ErrorMessage)
			}
			rec, _, err := h.st.GetSession(ctx, testSession)
			if err != nil || rec.StaleBinary == nil || rec.StaleBinary.SwitchFailedAt == nil {
				t.Fatalf("failed wake has no recovery action: stale=%+v err=%v", rec.StaleBinary, err)
			}
		})
	}
}

func TestVersionMoveRejectedHibernateLeavesNoRecoveryIntent(t *testing.T) {
	h, source, _ := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	h.advance(refreshIdle)
	source.rejectMarker = true
	if stopped, err := h.svc.HibernateChatForVersionRefresh(ctx, testSession, refreshIdle); err != nil || stopped {
		t.Fatalf("rejected stop = %v, %v", stopped, err)
	}
	moves, err := h.st.ListSessionVersionMoves(ctx)
	if err != nil || len(moves) != 0 || source.calls.Load() != 0 {
		t.Fatalf("rejected stop changed source or left intent: moves=%+v stops=%d err=%v", moves, source.calls.Load(), err)
	}
}
