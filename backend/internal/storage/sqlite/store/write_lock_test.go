package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Break caught: shutdown cancellation could lose a race with an available
// writer token and admit new enrichment work after cancellation.
func TestContextMutexRejectsPreCanceledAdmissionWithoutLosingToken(t *testing.T) {
	mutex := newContextMutex()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mutex.LockContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("LockContext error = %v, want context canceled", err)
	}
	if err := mutex.LockContext(context.Background()); err != nil {
		t.Fatalf("LockContext after cancellation: %v", err)
	}
	mutex.Unlock()
}

// Break caught: a backfill waiting behind an ordinary store writer could keep
// daemon shutdown blocked until that unrelated write released the mutex.
func TestContextMutexCancelsWhileWaiting(t *testing.T) {
	mutex := newContextMutex()
	mutex.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	admission := make(chan struct{})
	observed := &lockAdmissionContext{Context: ctx, admission: admission}
	result := make(chan error, 1)
	go func() { result <- mutex.LockContext(observed) }()
	<-admission
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("LockContext error = %v, want context canceled", err)
	}
	mutex.Unlock()
}

type lockAdmissionContext struct {
	context.Context
	admission chan struct{}
	once      sync.Once
}

func (c *lockAdmissionContext) Err() error {
	c.once.Do(func() { close(c.admission) })
	return c.Context.Err()
}

// Break caught: a writer holding the daemon-wide lock for minutes left no
// record of who held it, and a report made before release would deadlock any
// observer that writes.
func TestContextMutexReportsSlowHolderAfterRelease(t *testing.T) {
	mutex := newContextMutex()
	mutex.slowHold = 50 * time.Millisecond
	var caller string
	var held time.Duration
	observe := func(c string, h time.Duration) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := mutex.LockContext(ctx); err != nil {
			t.Errorf("observer could not take the released lock: %v", err)
			return
		}
		mutex.Unlock()
		caller, held = c, h
	}
	mutex.onSlow.Store(&observe)

	holdWriteLock(mutex, 0)
	if caller != "" {
		t.Fatalf("fast hold reported by %q", caller)
	}

	holdWriteLock(mutex, 100*time.Millisecond)
	if held < 100*time.Millisecond || !strings.HasPrefix(caller, "store.holdWriteLock:") {
		t.Fatalf("deferred hold reported caller %q held %v", caller, held)
	}

	mutex.Lock()
	release := mutex.Unlock
	time.Sleep(100 * time.Millisecond)
	release()
	if !strings.HasPrefix(caller, "store.TestContextMutexReportsSlowHolderAfterRelease:") {
		t.Fatalf("method value release reported caller %q", caller)
	}
}

func holdWriteLock(mutex *contextMutex, d time.Duration) {
	mutex.Lock()
	defer mutex.Unlock()
	time.Sleep(d)
}
