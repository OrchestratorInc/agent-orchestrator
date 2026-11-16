package chat_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestWaitForWakeReturnsOnceTheMovedProviderStarted(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	h.advance(refreshIdle)
	started := make(chan struct{})
	proceed := make(chan struct{})
	h.svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		close(started)
		<-proceed
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

	if stopped, err := h.svc.SwitchToInstalledRelease(ctx, testSession, refreshIdle); err != nil || !stopped {
		t.Fatalf("switch = %v, %v", stopped, err)
	}
	<-started
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := h.svc.WaitForWake(short, testSession); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait while the provider is starting = %v, want deadline exceeded", err)
	}
	close(proceed)
	waitCtx, cancelWait := context.WithTimeout(ctx, 5*time.Second)
	defer cancelWait()
	if err := h.svc.WaitForWake(waitCtx, testSession); err != nil {
		t.Fatalf("wait after the provider started = %v", err)
	}
	if !h.svc.HasLiveChatController(testSession) {
		t.Fatal("no live controller after the wait")
	}
}
