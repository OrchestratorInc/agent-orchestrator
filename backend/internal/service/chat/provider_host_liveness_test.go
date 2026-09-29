package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// hostedConversation is a provider conversation backed by a persistent host:
// closing it only detaches AO, and a later attachment can adopt the same live
// provider without resuming it.
type hostedConversation struct {
	*fakeConversation
	live       bool
	closed     atomic.Bool
	terminated atomic.Bool
}

func newHostedConversation(live bool) *hostedConversation {
	return &hostedConversation{fakeConversation: newFakeConversation(), live: live}
}

func (c *hostedConversation) PreservesProviderOnClose() bool { return true }
func (c *hostedConversation) ReconnectedLive() bool          { return c.live }
func (c *hostedConversation) Close() error {
	c.closed.Store(true)
	return c.fakeConversation.Close()
}
func (c *hostedConversation) Terminate() error {
	c.terminated.Store(true)
	return c.Close()
}

// loseStream ends the provider stream the way a failed daemon attachment does:
// no AO code path requested the close, and the host keeps the provider alive.
func (c *hostedConversation) loseStream() {
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped})
	c.closeOnce.Do(func() { close(c.events) })
}

type providerHostProbe struct {
	mu    sync.Mutex
	alive bool
	err   error
	calls int
}

func (p *providerHostProbe) set(alive bool, err error) {
	p.mu.Lock()
	p.alive, p.err = alive, err
	p.mu.Unlock()
}

func (p *providerHostProbe) probe(context.Context, domain.SessionID) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.alive, p.err
}

type hostedHarness struct {
	st       *sqlite.Store
	svc      *chatsvc.Service
	probe    *providerHostProbe
	driver   *sequenceDriver
	first    *hostedConversation
	firstGen string
}

func newHostedHarness(t *testing.T, next ...ports.ChatConversation) *hostedHarness {
	t.Helper()
	st := openStore(t)
	ctx := context.Background()
	rec, _, err := st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Unix(100, 0).UTC()}
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	var ids atomic.Int32
	h := &hostedHarness{
		st: st, probe: &providerHostProbe{alive: true}, first: newHostedConversation(false),
	}
	h.driver = &sequenceDriver{conversations: append([]ports.ChatConversation{h.first}, next...)}
	h.svc = chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers:           fakeRegistry{driver: h.driver},
		Activity:          lifecycle.New(st, nil),
		Log:               slog.New(slog.DiscardHandler),
		NewID:             func() string { return fmt.Sprintf("hosted-%d", ids.Add(1)) },
		ProviderHostAlive: h.probe.probe,
	})
	t.Cleanup(func() { h.svc.StopAll(context.Background()) })
	controller, err := h.svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.firstGen = controller.Generation()
	return h
}

func (h *hostedHarness) session(t *testing.T) domain.SessionRecord {
	t.Helper()
	rec, _, err := h.st.GetSession(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func awaitCondition(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Issue #5790: a controller stream can end while the persistent host and its
// provider stay alive (for example, after the ACP SDK closed an overflowing
// connection). That is not provider death. The session must not become Exited,
// the dead attachment must be released, and AO must reattach to the same live
// provider under a new generation.
func TestControllerStreamLossWithLiveProviderHostReattaches(t *testing.T) {
	second := newHostedConversation(true)
	h := newHostedHarness(t, second)
	first, err := h.svc.Controller(testSession)
	if err != nil {
		t.Fatal(err)
	}

	h.first.loseStream()
	first.Wait()

	awaitCondition(t, "live reattachment", func() bool {
		current, err := h.svc.Controller(testSession)
		return err == nil && current != first && current.State() != ports.ChatControllerStopped
	})
	if !h.first.closed.Load() || h.first.terminated.Load() {
		t.Fatalf("lost attachment closed=%v terminated=%v, want detached without terminating the provider",
			h.first.closed.Load(), h.first.terminated.Load())
	}
	rec := h.session(t)
	if rec.Activity.State == domain.ActivityExited {
		t.Fatalf("activity = %+v, want non-exited while the provider host is alive", rec.Activity)
	}
	if rec.Metadata.ControllerGeneration == h.firstGen || rec.Metadata.ControllerGeneration == "" {
		t.Fatalf("generation = %q, want a fresh owner fencing %q", rec.Metadata.ControllerGeneration, h.firstGen)
	}
	// A late exit from the lost attachment is fenced by the new generation.
	if err := lifecycle.New(h.st, nil).ApplyActivitySignal(context.Background(), testSession, ports.ActivitySignal{
		Valid: true, State: domain.ActivityExited, Event: "chat.controller.stopped", ControllerGeneration: h.firstGen,
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.session(t).Activity.State; got == domain.ActivityExited {
		t.Fatal("the lost attachment's generation could still mark the session exited")
	}
}

func TestControllerStreamLossWithDeadProviderHostReportsExited(t *testing.T) {
	h := newHostedHarness(t)
	first, err := h.svc.Controller(testSession)
	if err != nil {
		t.Fatal(err)
	}
	h.probe.set(false, nil)

	h.first.loseStream()
	first.Wait()

	awaitCondition(t, "truthful exit", func() bool {
		return h.session(t).Activity.State == domain.ActivityExited
	})
	if h.svc.HasLiveChatController(testSession) {
		t.Fatal("a dead provider was reattached")
	}
}

func TestControllerStreamLossWithUnknownProviderHostDoesNotExit(t *testing.T) {
	h := newHostedHarness(t)
	first, err := h.svc.Controller(testSession)
	if err != nil {
		t.Fatal(err)
	}
	h.probe.set(false, errors.New("descriptor unreadable"))

	h.first.loseStream()
	first.Wait()

	// No queued replacement exists, so reattachment fails; an inconclusive
	// probe is still not proof that the provider died.
	awaitCondition(t, "registry release", func() bool { return !h.svc.HasLiveChatController(testSession) })
	time.Sleep(50 * time.Millisecond)
	if rec := h.session(t); rec.Activity.State == domain.ActivityExited {
		t.Fatalf("activity = %+v after an inconclusive probe, want preserved", rec.Activity)
	}
}

func TestExplicitStopWithLiveProviderHostStillReportsExited(t *testing.T) {
	h := newHostedHarness(t)
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	awaitCondition(t, "explicit stop exit", func() bool {
		return h.session(t).Activity.State == domain.ActivityExited
	})
	if !h.first.terminated.Load() {
		t.Fatal("explicit stop did not terminate the provider host")
	}
}

// A durable Exited written by an older build (or any false exit) is sticky:
// lifecycle drops ordinary activity on an exited row. Adopting the same live
// provider proves the agent is running, so the reconnect must clear it.
func TestLiveReconnectClearsStaleExitedActivity(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	rec, _, err := st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	rec.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Unix(100, 0).UTC()}
	rec.Metadata.ProviderConversationID = "thread-1"
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: newHostedConversation(true)}},
		Activity: lifecycle.New(st, nil),
		Log:      slog.New(slog.DiscardHandler),
		NewID:    func() string { return "reconnected-generation" },
	})
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	after, _, err := st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if after.Activity.State != domain.ActivityIdle {
		t.Fatalf("activity after live reconnect = %+v, want idle", after.Activity)
	}
}

// A live-only start (startup healing of a false exit) must never adopt a
// provider the driver launched or resumed in place of a vanished host, and must
// not claim ownership before refusing.
func TestRequireLiveReconnectRefusesReplacementProvider(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	before, _, err := st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	replacement := newHostedConversation(false)
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: replacement}},
		Activity: lifecycle.New(st, nil),
		Log:      slog.New(slog.DiscardHandler),
		NewID:    func() string { return "refused-generation" },
	})
	_, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", RequireLiveReconnect: true,
	})
	if !errors.Is(err, ports.ErrChatProviderNotLive) {
		t.Fatalf("Start = %v, want ErrChatProviderNotLive", err)
	}
	if !replacement.terminated.Load() {
		t.Fatal("replacement provider opened by the driver was not destroyed")
	}
	if svc.HasLiveChatController(testSession) {
		t.Fatal("refused live-only start published a controller")
	}
	after, _, err := st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if after.Metadata.ControllerGeneration != before.Metadata.ControllerGeneration {
		t.Fatalf("refused live-only start claimed generation %q", after.Metadata.ControllerGeneration)
	}
}
