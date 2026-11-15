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

func WithSpawnRequestBudget(ctx context.Context, base time.Duration, extend func(time.Duration)) context.Context {
	return context.WithValue(ctx, spawnRequestBudgetKey{}, spawnRequestBudget{base: base, extend: extend})
}

func ExtendSpawnRequestBudget(ctx context.Context, setup time.Duration) (context.Context, context.CancelFunc) {
	budget, ok := ctx.Value(spawnRequestBudgetKey{}).(spawnRequestBudget)
	if !ok || budget.base <= 0 || budget.extend == nil {
		return ctx, func() {}
	}
	total := budget.base + setup
	budget.extend(total + time.Second)
	return context.WithTimeout(ctx, total)
}

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
