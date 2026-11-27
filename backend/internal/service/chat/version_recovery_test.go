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

func TestVersionMoveSuccessfulServiceRecoveryKeepsQueuedTurn(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		t.Run(fmt.Sprintf("in_place=%v", inPlace), func(t *testing.T) {
			h, source, _ := versionRefreshHarness(t)
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
			queued, err := h.svc.QueueUserMessage(ctx, testSession, ports.ChatUserMessage{Text: "survive the successful restart", ClientMessageID: "durable-recovery-queue"})
			if err != nil || queued.State != domain.TurnStateQueued {
				t.Fatalf("queue = %+v, %v", queued, err)
			}
			rec, _, err := h.st.GetSession(ctx, testSession)
			if err != nil || rec.StaleBinary == nil || rec.StaleBinary.SwitchFailedAt != nil {
				t.Fatalf("fixture unexpectedly has failure protection: %+v, %v", rec.StaleBinary, err)
			}
			if applied, err := h.st.SetSessionHibernated(ctx, testSession, rec.Revision, nil); err != nil || !applied {
				t.Fatalf("clear marker = %v, %v", applied, err)
			}
			resumed := newFakeConversation()
			resumed.turnSeq = 1
			var next atomic.Int32
			after := chatsvc.New(chatsvc.Options{
				Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
				Drivers:  fakeRegistry{driver: fakeDriver{conv: resumed}},
				Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
				NewID: func() string { return fmt.Sprintf("recovered-success-%d", next.Add(1)) },
			})
			t.Cleanup(func() { _ = after.Stop(context.Background(), testSession) })
			if _, err := after.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: source.ProviderConversationID()}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := after.Snapshot(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			if last := snapshot.Turns[len(snapshot.Turns)-1]; last.State != domain.TurnStateQueued {
				t.Fatalf("start settled queued work before drain: %s (%s)", last.State, last.ErrorMessage)
			}
			if err := after.DrainQueued(ctx, testSession); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if sent := resumed.sentTexts(); len(sent) == 1 && sent[0] == "survive the successful restart" {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("recovered delivery = %v", resumed.sentTexts())
		})
	}
}

type versionAdmissionConversation struct {
	*fakeConversation
	cleanupErr error
	stops      int
}

func (c *versionAdmissionConversation) Terminate() error {
	c.stops++
	return c.cleanupErr
}

func TestVersionMoveAdmissionCleanupFailureWithholdsRetry(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup_fails=%v", fails), func(t *testing.T) {
			conv := &versionAdmissionConversation{fakeConversation: newFakeConversation()}
			conv.setCapabilities(ports.ChatCapabilities{})
			if fails {
				conv.cleanupErr = errors.New("provider host shutdown failed")
			}
			st := openStore(t)
			next := 0
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
				Log:     slog.New(slog.DiscardHandler),
				NewID: func() string {
					next++
					return fmt.Sprintf("admission-%d", next)
				},
			})
			_, err := svc.Start(context.Background(), chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(), ProviderConversationID: conv.ProviderConversationID(),
			})
			var admissionErr *ports.ChatCapabilityError
			if !errors.As(err, &admissionErr) || conv.stops != 1 {
				t.Fatalf("admission rejection = %v, host stops = %d", err, conv.stops)
			}
			if fails {
				if !errors.Is(err, ports.ErrChatRecoveryInconclusive) || !errors.Is(err, conv.cleanupErr) {
					t.Fatalf("uncertain provider host permits retry: %v", err)
				}
			} else if err != admissionErr {
				t.Fatalf("successful cleanup changed admission error: %v", err)
			}
			if _, err := svc.Controller(testSession); !errors.Is(err, chatsvc.ErrNoController) {
				t.Fatalf("rejected controller was published: %v", err)
			}
		})
	}
}
