package ports

import (
	"context"
	"errors"
	"sync"
	"time"
)

type spawnRequestBudgetKey struct{}

type spawnRequestBudget struct {
	base   time.Duration
	extend func(time.Duration)
}

// WithSpawnRequestBudget marks ctx as a REST request that may run the
// project's Setup (a spawn, an orchestrator start, a restore). The request is
// held to base until the operation knows its project; extend then moves the
// request's deadline to that many from now, so a Setup step longer than one
// request is not cut short while a slow request body still is.
func WithSpawnRequestBudget(ctx context.Context, base time.Duration, extend func(time.Duration)) context.Context {
	return context.WithValue(ctx, spawnRequestBudgetKey{}, spawnRequestBudget{base: base, extend: extend})
}

// ExtendSpawnRequestBudget moves a WithSpawnRequestBudget request's deadline
// to base plus setup from now and returns ctx bounded by it. Outside such a
// request it returns ctx unchanged.
func ExtendSpawnRequestBudget(ctx context.Context, setup time.Duration) (context.Context, context.CancelFunc) {
	budget, ok := ctx.Value(spawnRequestBudgetKey{}).(spawnRequestBudget)
	if !ok || budget.base <= 0 || budget.extend == nil {
		return ctx, func() {}
	}
	total := budget.base + setup
	// The request's own stop comes just after the operation's deadline, so
	// the operation sees context.DeadlineExceeded and reports it.
	budget.extend(total + time.Second)
	return context.WithTimeout(ctx, total)
}

// NewSpawnRequestContext starts a Setup route's request budget. The returned
// context is held to base until the operation extends it
// (ExtendSpawnRequestBudget). When the budget runs out the context stops with
// context.DeadlineExceeded, as an expired deadline would, so the operation
// and every context derived from it report a timeout rather than a
// cancellation. timedOut reports whether the budget ran out; release frees
// the budget once the request is served.
func NewSpawnRequestContext(parent context.Context, base time.Duration) (ctx context.Context, timedOut func() bool, release func()) {
	budget := &budgetContext{parent: parent, done: make(chan struct{})}
	stopWatching := context.AfterFunc(parent, func() { budget.stop(parent.Err()) })
	timer := time.AfterFunc(base, func() { budget.stop(context.DeadlineExceeded) })
	extend := func(total time.Duration) { timer.Reset(total) }
	timedOut = func() bool { return errors.Is(budget.Err(), context.DeadlineExceeded) }
	release = func() {
		timer.Stop()
		stopWatching()
		budget.stop(context.Canceled)
	}
	return WithSpawnRequestBudget(budget, base, extend), timedOut, release
}

// budgetContext is a request context its budget can stop with
// context.DeadlineExceeded. A context.WithCancelCause would report
// context.Canceled from Err, whatever the cause, and so would every context
// derived from it. It has a done channel of its own, so contexts derived from
// it watch it rather than the request's own cancellation.
type budgetContext struct {
	parent context.Context
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	err    error
}

func (c *budgetContext) stop(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
}

func (c *budgetContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c *budgetContext) Done() <-chan struct{}       { return c.done }
func (c *budgetContext) Value(key any) any           { return c.parent.Value(key) }

func (c *budgetContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
