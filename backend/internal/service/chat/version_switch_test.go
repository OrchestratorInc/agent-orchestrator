package chat_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

const refreshIdle = 10 * time.Minute

// versionRefreshHarness is a settled, idle Codex chat with idle hibernation
// off and version refresh on. A resume starts resumed, standing in for the
// newly installed release.
func versionRefreshHarness(t *testing.T) (*harness, *hibernationConversation, *fakeConversation) {
	t.Helper()
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
		opts.VersionRefresh = func() (time.Duration, bool) { return refreshIdle, true }
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	return h, conv, resumed
}

func markStale(t *testing.T, h *harness) {
	t.Helper()
	if _, err := h.st.MarkHarnessSessionsStale(context.Background(), domain.HarnessCodex, "2.0.0", h.now()); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := h.st.GetSession(context.Background(), testSession)
	if rec.StaleBinary == nil {
		t.Fatal("session was not marked stale")
	}
}

// wakeOnNewRelease resumes the hibernated session the way Session Manager does.
func wakeOnNewRelease(t *testing.T, h *harness, old *hibernationConversation, wakeErr error) <-chan struct{} {
	t.Helper()
	woke := make(chan struct{}, 4)
	h.svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		woke <- struct{}{}
		if wakeErr != nil {
			return wakeErr
		}
		rec, found, err := h.st.GetSession(ctx, id)
		if err != nil || !found || rec.HibernatedAt == nil {
			return fmt.Errorf("read hibernated session: found=%v err=%w", found, err)
		}
		if cleared, err := h.st.SetSessionHibernated(ctx, id, rec.Revision, nil); err != nil || !cleared {
			return fmt.Errorf("clear hibernation: %v %w", cleared, err)
		}
		_, err = h.svc.Start(ctx, chatsvc.StartConfig{
			SessionID: id, ProjectID: testProject, Harness: domain.HarnessCodex,
			WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID(),
		})
		return err
	})
	return woke
}

func TestVersionRefreshRunsWithHibernationOffAndWaitsForQuiet(t *testing.T) {
	h, conv, _ := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	if stopped, err := h.svc.HibernateChat(ctx, testSession); err != nil || stopped {
		t.Fatalf("idle hibernation with the setting off = %v, %v", stopped, err)
	}
	if stopped, err := h.svc.HibernateChatForVersionRefresh(ctx, testSession, refreshIdle); err != nil || stopped {
		t.Fatalf("refresh before the session was quiet = %v, %v", stopped, err)
	}
	h.advance(refreshIdle)
	if stopped, err := h.svc.HibernateChatForVersionRefresh(ctx, testSession, refreshIdle); err != nil || !stopped || conv.calls.Load() != 1 {
		t.Fatalf("refresh of a quiet stale session = %v, %v, stops=%d", stopped, err, conv.calls.Load())
	}
}

func TestVersionRefreshLeavesCurrentAndViewedSessionsAlone(t *testing.T) {
	h, conv, _ := versionRefreshHarness(t)
	ctx := context.Background()
	h.advance(refreshIdle)
	if stopped, err := h.svc.HibernateChatForVersionRefresh(ctx, testSession, refreshIdle); err != nil || stopped {
		t.Fatalf("refresh of a current session = %v, %v", stopped, err)
	}
	markStale(t, h)
	if err := h.svc.SetChatView(ctx, testSession, "view-1", true); err != nil {
		t.Fatal(err)
	}
	if stopped, err := h.svc.HibernateChatForVersionRefresh(ctx, testSession, refreshIdle); err != nil || stopped || conv.calls.Load() != 0 {
		t.Fatalf("refresh of a viewed session = %v, %v, stops=%d", stopped, err, conv.calls.Load())
	}
}

func TestVersionRefreshNeverStopsBusySession(t *testing.T) {
	h, conv, _ := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	h.advance(refreshIdle)
	conv.backgroundRunning = true
	if stopped, err := h.svc.HibernateChatForVersionRefresh(ctx, testSession, refreshIdle); err != nil || stopped || conv.calls.Load() != 0 {
		t.Fatalf("refresh with background work = %v, %v, stops=%d", stopped, err, conv.calls.Load())
	}
}

func TestSendMovesQuietStaleSessionAndDeliversOnceOnNewRelease(t *testing.T) {
	h, old, resumed := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	h.advance(refreshIdle)
	// The user is looking at the session; the message they send moves it.
	if err := h.svc.SetChatView(ctx, testSession, "view-1", true); err != nil {
		t.Fatal(err)
	}
	woke := wakeOnNewRelease(t, h, old, nil)
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after update", ClientMessageID: "refresh-1"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued for the new release", turn, err)
	}
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not wake on the new release")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := resumed.sentTexts(); len(got) == 1 && got[0] == "after update" {
			if sent := old.sentTexts(); len(sent) != 1 {
				t.Fatalf("old release received %v; want only the first message", sent)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("new release received %v, old %v", resumed.sentTexts(), old.sentTexts())
}

func TestSendKeepsRecentlyActiveStaleSessionOnItsRelease(t *testing.T) {
	h, old, resumed := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "still warm", ClientMessageID: "warm-1"}); err != nil {
		t.Fatal(err)
	}
	if old.calls.Load() != 0 || len(resumed.sentTexts()) != 0 {
		t.Fatalf("a warm session moved: stops=%d resumed=%v", old.calls.Load(), resumed.sentTexts())
	}
}

func TestFailedSwitchKeepsQueuedMessageAndRecordsFailure(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	h.advance(refreshIdle)
	woke := wakeOnNewRelease(t, h, old, errors.New("new release would not start"))
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after update", ClientMessageID: "refresh-fail-1"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v", turn, err)
	}
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("wake was not attempted")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, _, _ := h.st.GetSession(ctx, testSession)
		if rec.StaleBinary != nil && rec.StaleBinary.SwitchFailedAt != nil {
			snapshot, err := h.svc.Snapshot(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			last := snapshot.Turns[len(snapshot.Turns)-1]
			if last.State != domain.TurnStateQueued {
				t.Fatalf("queued message after failed switch = %s, want still queued", last.State)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("failed switch was not recorded")
}
