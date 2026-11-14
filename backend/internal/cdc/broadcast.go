package cdc

import (
	"log/slog"
	"sync"
)

type Broadcaster struct {
	mu     sync.RWMutex
	nextID int
	subs   map[int]func(Event)
	logger *slog.Logger
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[int]func(Event){}, logger: slog.Default()}
}

func (b *Broadcaster) Subscribe(fn func(Event)) (unsubscribe func()) {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

func (b *Broadcaster) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func (b *Broadcaster) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, fn := range b.subs {
		b.deliver(fn, e)
	}
}

func (b *Broadcaster) deliver(fn func(Event), e Event) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("cdc broadcaster: subscriber panicked", "seq", e.Seq, "panic", r)
		}
	}()
	fn(e)
}
