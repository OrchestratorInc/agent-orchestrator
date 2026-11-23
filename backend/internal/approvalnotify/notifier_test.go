package approvalnotify

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeWriter struct {
	mu       sync.Mutex
	notified []ports.NotificationIntent
	resolved []ports.NotificationResolution
	changed  chan struct{}
}

func newFakeWriter() *fakeWriter { return &fakeWriter{changed: make(chan struct{}, 64)} }

func (f *fakeWriter) Notify(_ context.Context, in ports.NotificationIntent) error {
	f.mu.Lock()
	f.notified = append(f.notified, in)
	f.mu.Unlock()
	f.changed <- struct{}{}
	return nil
}

func (f *fakeWriter) Resolve(_ context.Context, res ports.NotificationResolution) error {
	f.mu.Lock()
	f.resolved = append(f.resolved, res)
	f.mu.Unlock()
	f.changed <- struct{}{}
	return nil
}

func (f *fakeWriter) waitFor(t *testing.T, notified, resolved int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		f.mu.Lock()
		n, r := len(f.notified), len(f.resolved)
		f.mu.Unlock()
		if n >= notified && r >= resolved {
			return
		}
		select {
		case <-f.changed:
		case <-deadline:
			t.Fatalf("waited for %d notified/%d resolved, have %d/%d", notified, resolved, n, r)
		}
	}
}

func start(t *testing.T) (*approval.Gate, *fakeWriter) {
	t.Helper()
	gate, err := approval.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := newFakeWriter()
	n := New(w, func(id string) (string, domain.ProjectID) { return "Checkout flow", "proj-1" }, nil)
	gate.Observe(n.Observe)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go n.Run(ctx)
	return gate, w
}

func request(gate *approval.Gate, ctx context.Context, timeout time.Duration) <-chan approval.Outcome {
	out := make(chan approval.Outcome, 1)
	go func() {
		o, _ := gate.Request(ctx, approval.Request{
			Kind: approval.KindBrowserAction, SessionID: "sess-1", Site: "https://shop.example",
			Action: `Click "Place order" on shop.example`, Category: "payment", Timeout: timeout,
		})
		out <- o
	}()
	return out
}

func pendingID(t *testing.T, gate *approval.Gate) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := gate.Pending(); len(p) == 1 {
			return p[0].ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending approval")
	return ""
}

func TestApprovalCreatesBrowserApprovalNotification(t *testing.T) {
	gate, w := start(t)
	request(gate, context.Background(), time.Minute)
	id := pendingID(t, gate)
	w.waitFor(t, 1, 0)
	got := w.notified[0]
	if got.Type != domain.NotificationBrowserApproval || got.SessionID != "sess-1" || got.ProjectID != "proj-1" ||
		got.SourceKey != SourceKey(id) || got.SessionDisplayName != "Checkout flow" ||
		got.ApprovalAction != `Click "Place order" on shop.example` || got.ApprovalSite != "https://shop.example" {
		t.Fatalf("intent = %+v", got)
	}
}

func TestDecidedApprovalResolvesNotification(t *testing.T) {
	gate, w := start(t)
	done := request(gate, context.Background(), time.Minute)
	id := pendingID(t, gate)
	if err := gate.Decide(id, approval.DecisionDeny); err != nil {
		t.Fatal(err)
	}
	<-done
	w.waitFor(t, 1, 1)
	if res := w.resolved[0]; res.SourceKey != SourceKey(id) || res.Type != domain.NotificationBrowserApproval || res.SessionID != "sess-1" {
		t.Fatalf("resolution = %+v", res)
	}
}

func TestTimedOutApprovalResolvesNotification(t *testing.T) {
	gate, w := start(t)
	if o := <-request(gate, context.Background(), 20*time.Millisecond); o.Decision != approval.DecisionTimeout {
		t.Fatalf("outcome = %+v", o)
	}
	w.waitFor(t, 1, 1)
	if w.resolved[0].SourceKey != w.notified[0].SourceKey {
		t.Fatalf("resolved %+v, notified %+v", w.resolved[0], w.notified[0])
	}
}

func TestCancelledApprovalResolvesNotification(t *testing.T) {
	gate, w := start(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := request(gate, ctx, time.Minute)
	pendingID(t, gate)
	cancel()
	<-done
	w.waitFor(t, 1, 1)
}

// The writer is slow (a database); the gate must not wait for it, and no event
// may be lost or reordered: every approval's resolve follows its notify.
func TestNotifierNeverBlocksTheGateAndKeepsOrder(t *testing.T) {
	gate, err := approval.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	w := &blockingWriter{fakeWriter: newFakeWriter(), release: release}
	n := New(w, nil, nil)
	gate.Observe(n.Observe)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)

	const count = 50
	for i := 0; i < count; i++ {
		done := request(gate, context.Background(), time.Minute)
		id := pendingID(t, gate)
		if err := gate.Decide(id, approval.DecisionAllowOnce); err != nil {
			t.Fatal(err)
		}
		<-done
	}
	close(release)
	w.waitFor(t, count, count)
	order := map[string]int{}
	for _, step := range w.steps {
		if step.resolve && order[step.key] != 1 {
			t.Fatalf("resolve of %s came before its notify", step.key)
		}
		order[step.key]++
	}
}

type step struct {
	key     string
	resolve bool
}

type blockingWriter struct {
	*fakeWriter
	release chan struct{}
	steps   []step
}

func (b *blockingWriter) Notify(ctx context.Context, in ports.NotificationIntent) error {
	<-b.release
	b.mu.Lock()
	b.steps = append(b.steps, step{key: in.SourceKey})
	b.mu.Unlock()
	return b.fakeWriter.Notify(ctx, in)
}

func (b *blockingWriter) Resolve(ctx context.Context, res ports.NotificationResolution) error {
	<-b.release
	b.mu.Lock()
	b.steps = append(b.steps, step{key: res.SourceKey, resolve: true})
	b.mu.Unlock()
	return b.fakeWriter.Resolve(ctx, res)
}
