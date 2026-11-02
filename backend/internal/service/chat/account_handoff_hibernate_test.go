package chat_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// restartedHandoffService is the daemon after a restart: a fresh service on the
// same store, whose wake callback resumes the hibernated native thread. Wakes
// fail while failWakes is positive.
func restartedHandoffService(t *testing.T, hh *handoffHarness, old *hibernationConversation, failWakes int32) (*chatsvc.Service, *fakeConversation, *atomic.Int32) {
	t.Helper()
	ctx := context.Background()
	resumed := newFakeConversation()
	resumed.turnSeq = 100
	svc := chatsvc.New(chatsvc.Options{
		Store: hh.st, Reader: fullSnapshotReader(hh.st), Sessions: hh.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumed}}, NewID: uuid.NewString, Now: hh.now,
	})
	t.Cleanup(func() { svc.StopAll(ctx) })
	var wakes, failures atomic.Int32
	failures.Store(failWakes)
	svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		wakes.Add(1)
		if failures.Add(-1) >= 0 {
			return errors.New("native resume refused")
		}
		record, _, err := hh.st.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if applied, err := hh.st.SetSessionHibernated(ctx, id, record.Revision, nil); err != nil || !applied {
			return errors.New("could not clear hibernation marker")
		}
		_, err = svc.Start(ctx, chatsvc.StartConfig{SessionID: id, ProjectID: testProject,
			Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID()})
		return err
	})
	hh.wire(svc)
	return svc, resumed, &wakes
}

// A committed move whose retry has not run yet survives the session sleeping, a
// failed wake, and two restarts: the prompt is retried exactly once, on the
// account the move bound.
func TestAccountHandoffPendingRetrySurvivesHibernationAndRestart(t *testing.T) {
	h, old := settledHibernationHarness(t, "")
	hh := handoffHarnessFor(t, h, domain.HarnessCodex)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, usageLimitMessage(domain.HarnessCodex, true, h.now()))
	prompt := h.conv.sentMessages()[0].Text
	hh.tick(t)
	hh.mover.finish(t, domain.SessionInterfaceTransitionCompleted)
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept {
		t.Fatalf("hibernate = %v, %v", slept, err)
	}

	svc, resumed, wakes := restartedHandoffService(t, hh, old, 1)
	if err := svc.HandoffUsageLimitedTurns(ctx); err == nil {
		t.Fatal("a failed wake reported success")
	}
	// The move committed, so the note is written and the consent is kept.
	if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffMoved || rec.TargetAccountID != handoffTarget {
		t.Fatalf("handoff after failed wake = %+v", rec)
	}
	if got := len(resumed.sentMessages()); got != 0 {
		t.Fatalf("dispatches after failed wake = %d", got)
	}
	// The next tick inside the backoff does not wake again.
	if err := svc.HandoffUsageLimitedTurns(ctx); err != nil || wakes.Load() != 1 {
		t.Fatalf("tick inside backoff: err=%v wakes=%d", err, wakes.Load())
	}

	h.advance(time.Minute)
	var ticks sync.WaitGroup
	for range 8 {
		ticks.Go(func() { _ = svc.HandoffUsageLimitedTurns(ctx) })
	}
	ticks.Wait()
	for range 2 {
		if err := svc.HandoffUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sent := resumed.sentMessages()
	if wakes.Load() != 2 || len(sent) != 1 || sent[0].Text != prompt {
		t.Fatalf("wakes=%d sends=%+v, want one retry of %q", wakes.Load(), sent, prompt)
	}
	if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffRetried {
		t.Fatalf("handoff = %+v", rec)
	}
	retryID, found, err := hh.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID)
	if err != nil || !found {
		t.Fatalf("retry link: %v %v", found, err)
	}
	if retry, err := hh.st.TurnByID(ctx, retryID); err != nil || retry.RetryOfTurnID != source.ID {
		t.Fatalf("retry lineage = %+v, %v", retry, err)
	}
	if notes := handoffNotes(t, h, source.ID); notes != 1 {
		t.Fatalf("handoff notes = %d", notes)
	}
	if len(hh.mover.moves()) != 1 || hh.boundAccount(t) != handoffTarget {
		t.Fatalf("moves=%d account=%s", len(hh.mover.moves()), hh.boundAccount(t))
	}

	// Another restart finds nothing left to do.
	svc.StopAll(ctx)
	again, againConv, againWakes := restartedHandoffService(t, hh, old, 0)
	for range 3 {
		if err := again.HandoffUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if againWakes.Load() != 0 || len(againConv.sentMessages()) != 0 || len(hh.mover.moves()) != 1 {
		t.Fatalf("second restart: wakes=%d sends=%d moves=%d", againWakes.Load(), len(againConv.sentMessages()), len(hh.mover.moves()))
	}
}

// A refusal that kept its account (the session is pinned, or no account has
// room) keeps that choice and its auto-resume consent through hibernation and a
// restart: nothing moves it, and auto-resume retries once at the reset.
func TestAccountHandoffFallbackSurvivesHibernationAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pinned bool
		target string
	}{
		{"pinned", true, handoffTarget},
		{"no-candidate", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, old := settledHibernationHarness(t, "")
			hh := handoffHarnessFor(t, h, domain.HarnessCodex)
			hh.accounts.target = tc.target
			ctx := context.Background()
			if tc.pinned {
				if err := hh.st.SetSessionAccountPinned(ctx, testSession, true, h.now()); err != nil {
					t.Fatal(err)
				}
			}
			source := failUsageLimitedTurn(t, h, usageLimitMessage(domain.HarnessCodex, true, h.now()))
			hh.tick(t)
			if rec := hh.handoff(t, source.ID); rec.Phase != domain.ChatAccountHandoffStayed {
				t.Fatalf("handoff = %+v", rec)
			}
			if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept {
				t.Fatalf("hibernate = %v, %v", slept, err)
			}

			svc, resumed, wakes := restartedHandoffService(t, hh, old, 0)
			for range 3 {
				if err := svc.HandoffUsageLimitedTurns(ctx); err != nil {
					t.Fatal(err)
				}
			}
			stored, err := hh.st.TurnByID(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			pinned, err := hh.st.SessionAccountPinned(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			if wakes.Load() != 0 || len(resumed.sentMessages()) != 0 || len(hh.mover.moves()) != 0 ||
				!stored.UsageLimit.AutoResume || pinned != tc.pinned || hh.boundAccount(t) != handoffSource {
				t.Fatalf("after restart: wakes=%d sends=%d moves=%d autoResume=%v pinned=%v account=%s",
					wakes.Load(), len(resumed.sentMessages()), len(hh.mover.moves()), stored.UsageLimit.AutoResume, pinned, hh.boundAccount(t))
			}

			h.advance(2 * time.Minute)
			for range 2 {
				if err := svc.ResumeUsageLimitedTurns(ctx); err != nil {
					t.Fatal(err)
				}
				if err := svc.HandoffUsageLimitedTurns(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if wakes.Load() != 1 || len(resumed.sentMessages()) != 1 || len(hh.mover.moves()) != 0 {
				t.Fatalf("at reset: wakes=%d sends=%d moves=%d", wakes.Load(), len(resumed.sentMessages()), len(hh.mover.moves()))
			}
			if after, err := hh.st.TurnByID(ctx, source.ID); err != nil || after.UsageLimit.AutoResume {
				t.Fatalf("auto-resume consent not consumed: %+v, %v", after.UsageLimit, err)
			}
		})
	}
}
