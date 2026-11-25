package pr

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// editLocks lets one title or description save at a time through to a pull
// request, so a second save compares against what the first one wrote. Saves to
// different pull requests don't wait for each other. The zero value is ready.
type editLocks struct {
	mu    sync.Mutex
	locks map[string]*editLock
}

type editLock struct {
	// held carries a token while a save holds the lock.
	held chan struct{}
	// users counts the save holding the lock and those waiting for it, so the
	// last one out can drop the entry.
	users int
}

// acquire waits for key's lock and returns its release, or ctx's error when the
// caller gives up first.
func (l *editLocks) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	lock, ok := l.locks[key]
	if !ok {
		if l.locks == nil {
			l.locks = map[string]*editLock{}
		}
		lock = &editLock{held: make(chan struct{}, 1)}
		l.locks[key] = lock
	}
	lock.users++
	l.mu.Unlock()

	select {
	case lock.held <- struct{}{}:
		return func() {
			<-lock.held
			l.leave(key, lock)
		}, nil
	case <-ctx.Done():
		l.leave(key, lock)
		return nil, ctx.Err()
	}
}

func (l *editLocks) leave(key string, lock *editLock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lock.users--
	if lock.users == 0 {
		delete(l.locks, key)
	}
}

// editLockKey names the pull request itself rather than the URL it was asked
// for by, since one pull request can be tracked under more than one.
func editLockKey(ref ports.SCMPRRef) string {
	return strings.ToLower(strings.Join([]string{ref.Repo.Provider, ref.Repo.Host, ref.Repo.Owner, ref.Repo.Name, strconv.Itoa(ref.Number)}, "\x00"))
}
