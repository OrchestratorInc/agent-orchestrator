package chat_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestSideNativeCurrentUsesCapabilitiesAndIndependentHost(t *testing.T) {
	for _, provider := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessOpenCode, domain.HarnessOpenCodeV2, domain.HarnessKimi, domain.HarnessOMP, domain.HarnessPi} {
		t.Run(string(provider), func(t *testing.T) {
			h := newSideWorkflow(t, "", provider)
			caps := productionCaps()
			caps[ports.ChatCapabilityForkCurrent] = true
			h.conv.setCapabilities(caps)
			h.source(t)
			side := h.side(t, "native", true)
			if side.ContextMode != "native" || side.NativeAnchorID != "" || side.SeedHistory == "" {
				t.Fatalf("context mode: %+v", side)
			}
			h.driver.mu.Lock()
			defer h.driver.mu.Unlock()
			if len(h.driver.anchors) != 1 || h.driver.anchors[0] != "" || h.driver.sources[0] != h.conv.ProviderConversationID() ||
				h.driver.configs[0].SessionID == testSession || h.driver.configs[0].ProviderScopeID != side.ID {
				t.Fatal("did not create an isolated current-context fork")
			}
		})
	}
}

func TestSideNativeCurrentKeepsParentQuiescentAndReleasesFailedFork(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			h := newSideWorkflow(t, "", domain.HarnessKimi)
			caps := productionCaps()
			caps[ports.ChatCapabilityForkCurrent] = true
			h.conv.setCapabilities(caps)
			h.source(t)
			started, release := make(chan struct{}), make(chan struct{})
			h.driver.onFork = func(ctx context.Context) error {
				close(started)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-release:
				}
				if fail {
					return errors.New("fork failed")
				}
				return nil
			}
			side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "fenced", ForceNew: true})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("fork did not start")
			}
			sendCtx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			_, err = h.svc.Send(sendCtx, testSession, ports.ChatUserMessage{Text: "must not change the cutoff"})
			cancel()
			if err == nil {
				t.Fatal("parent accepted work while its current-context fork was being copied")
			}
			close(release)
			h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool {
				if fail {
					return s.Side.State == "failed"
				}
				return s.Side.State == "ready"
			})
			if _, err := h.svc.Send(t.Context(), testSession, ports.ChatUserMessage{Text: "parent continues", ClientMessageID: "after-fork"}); err != nil {
				t.Fatalf("parent intake remained fenced: %v", err)
			}
		})
	}
}

func TestSideCurrentForkReconstructsWhenParentHasLaterWork(t *testing.T) {
	h := newSideWorkflow(t, "", domain.HarnessOMP)
	caps := productionCaps()
	caps[ports.ChatCapabilityForkCurrent] = true
	h.conv.setCapabilities(caps)
	h.source(t)
	if _, err := h.svc.Send(t.Context(), testSession, ports.ChatUserMessage{Text: "in flight", ClientMessageID: "running"}); err != nil {
		t.Fatal(err)
	}
	side := h.side(t, "busy", true)
	if side.ContextMode != "reconstructed" || side.SeedHistory == "" || len(h.driver.anchors) != 0 {
		t.Fatalf("unsafe current-context fork: %+v", side)
	}
}

func TestSideCreateWakesHibernatedParentBeforeNativeFork(t *testing.T) {
	h, old := settledHibernationHarness(t, domain.TurnStateCompleted)
	if hibernated, err := h.svc.HibernateChat(t.Context(), testSession); err != nil || !hibernated {
		t.Fatalf("hibernate: %v %v", hibernated, err)
	}
	resumed := newFakeConversation()
	caps := productionCaps()
	caps[ports.ChatCapabilityForkCurrent] = true
	resumed.setCapabilities(caps)
	driver := &connectedSideForkDriver{fakeDriver: fakeDriver{conv: resumed}}
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: driver}, AppRunID: "wake-side",
		Activity: h.activity, Now: h.now,
		NewID: uuid.NewString,
	})
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		record, found, err := h.st.GetSession(ctx, id)
		if err != nil {
			return fmt.Errorf("load hibernated parent: %w", err)
		}
		if !found || record.HibernatedAt == nil {
			return errors.New("missing hibernated parent")
		}
		if _, err := h.st.SetSessionHibernated(ctx, id, record.Revision, nil); err != nil {
			return err
		}
		_, err = svc.Start(ctx, chatsvc.StartConfig{
			SessionID: id, ProjectID: testProject, Harness: domain.HarnessCodex,
			WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID(),
		})
		return err
	})
	if err := svc.InitializeSideChats(t.Context()); err != nil {
		t.Fatal(err)
	}
	side, err := svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "wake"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := svc.SideSnapshot(t.Context(), testSession, side.ID, time.Time{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Side.State == "failed" {
			t.Fatal(snapshot.Side.ErrorMessage)
		}
		if snapshot.Side.State == "ready" {
			if snapshot.Side.ContextMode != "native" || !svc.HasLiveChatController(testSession) {
				t.Fatal("parent was not resumed before forking")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("native side did not open after parent wake")
}

func TestSideCreateRetryDuringCurrentForkReturnsSameSide(t *testing.T) {
	h := newSideWorkflow(t, "", domain.HarnessKimi)
	caps := productionCaps()
	caps[ports.ChatCapabilityForkCurrent] = true
	h.conv.setCapabilities(caps)
	h.source(t)
	started, release := make(chan struct{}), make(chan struct{})
	h.driver.onFork = func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	defer close(release)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("fork did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	retry, err := h.svc.CreateIndependentSideChat(ctx, testSession, chatsvc.SideCreateRequest{IdempotencyKey: "retry"})
	if err != nil || retry.ID != side.ID {
		t.Fatalf("idempotent retry while opening: %s %v", retry.ID, err)
	}
}

func TestSideCurrentForkRetryAfterParentMovesUsesFrozenHistory(t *testing.T) {
	h := newSideWorkflow(t, "", domain.HarnessKimi)
	caps := productionCaps()
	caps[ports.ChatCapabilityForkCurrent] = true
	h.conv.setCapabilities(caps)
	h.source(t)
	h.driver.onFork = func(context.Context) error { return errors.New("fork unavailable") }
	side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "retry-failed"})
	if err != nil {
		t.Fatal(err)
	}
	h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return s.Side.State == "failed" })
	if _, err := h.svc.Send(t.Context(), testSession, ports.ChatUserMessage{Text: "later parent request", ClientMessageID: "later"}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RetrySideConnection(t.Context(), testSession, side.ID); err != nil {
		t.Fatal(err)
	}
	retried := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return s.Side.State == "ready" })
	if retried.Side.ContextMode != "reconstructed" || retried.Side.SeedHistory == "" || strings.Contains(retried.Side.SeedHistory, "later parent request") {
		t.Fatalf("retry lost the frozen cutoff: %+v", retried.Side)
	}
}

func TestSideUnknownForkCannotBeBlindlyRetried(t *testing.T) {
	h := newSideWorkflow(t, "", domain.HarnessKimi)
	caps := productionCaps()
	caps[ports.ChatCapabilityForkCurrent] = true
	h.conv.setCapabilities(caps)
	h.source(t)
	h.driver.onFork = func(context.Context) error { return ports.ErrChatRecoveryInconclusive }
	side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	failed := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return s.Side.State == "failed" })
	if !failed.Side.RecreateRequired {
		t.Fatal("unknown fork offered an unsafe retry")
	}
	if err := h.svc.RetrySideConnection(t.Context(), testSession, side.ID); !errors.Is(err, chatsvc.ErrSidePolicyRecreate) {
		t.Fatalf("unknown fork retry: %v", err)
	}
}

func TestSideCurrentForkDoesNotBlockParentDuringChildSetup(t *testing.T) {
	for _, stage := range []string{"before", "after"} {
		t.Run(stage, func(t *testing.T) {
			h := newSideWorkflow(t, "", domain.HarnessKimi)
			caps := productionCaps()
			caps[ports.ChatCapabilityForkCurrent] = true
			h.conv.setCapabilities(caps)
			h.source(t)
			started, release := make(chan struct{}), make(chan struct{})
			block := func(ctx context.Context) error {
				close(started)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if stage == "before" {
				h.driver.beforeFork = block
			} else {
				h.driver.afterFork = block
			}
			side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: stage})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("fork setup did not start")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			_, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "parent continues", ClientMessageID: "during-setup"})
			cancel()
			close(release)
			if err != nil {
				t.Fatalf("child setup blocked parent: %v", err)
			}
			opened := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return s.Side.State == "ready" })
			want := "native"
			if stage == "before" {
				want = "reconstructed"
			}
			if opened.Side.ContextMode != want {
				t.Fatalf("mode = %s, want %s", opened.Side.ContextMode, want)
			}
		})
	}
}

func TestSideCurrentForkWaitsParentSendUntilCopyCompletes(t *testing.T) {
	h := newSideWorkflow(t, "", domain.HarnessKimi)
	caps := productionCaps()
	caps[ports.ChatCapabilityForkCurrent] = true
	h.conv.setCapabilities(caps)
	h.source(t)
	started, release := make(chan struct{}), make(chan struct{})
	h.driver.onFork = func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	_, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "wait"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("fork did not start")
	}
	sent := make(chan error, 1)
	go func() {
		_, err := h.svc.Send(t.Context(), testSession, ports.ChatUserMessage{Text: "wait then send", ClientMessageID: "wait-send"})
		sent <- err
	}()
	select {
	case err := <-sent:
		t.Fatalf("send returned before copy completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("send was rejected: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("send did not resume")
	}
}

func TestSideCurrentForkCanBeStoppedOrKilled(t *testing.T) {
	for _, action := range []string{"stop", "kill", "close-side"} {
		t.Run(action, func(t *testing.T) {
			h := newSideWorkflow(t, "", domain.HarnessKimi)
			caps := productionCaps()
			caps[ports.ChatCapabilityForkCurrent] = true
			h.conv.setCapabilities(caps)
			h.source(t)
			started, cancelled := make(chan struct{}), make(chan struct{})
			h.driver.onFork = func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return ctx.Err()
			}
			side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: action})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("fork did not start")
			}
			done := make(chan error, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			go func() {
				switch action {
				case "stop":
					done <- h.svc.Interrupt(ctx, testSession)
				case "kill":
					done <- h.svc.Stop(ctx, testSession)
				case "close-side":
					done <- h.svc.CloseIndependentSideChat(ctx, testSession, side.ID)
				}
			}()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, chatsvc.ErrNoActiveTurn) {
					t.Fatalf("%s: %v", action, err)
				}
			case <-ctx.Done():
				t.Fatalf("%s waited for hung fork", action)
			}
			select {
			case <-cancelled:
			case <-ctx.Done():
				t.Fatal("fork was not cancelled")
			}
			if action == "stop" {
				if _, err := h.svc.Send(t.Context(), testSession, ports.ChatUserMessage{Text: "parent continues", ClientMessageID: "after-stop"}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
