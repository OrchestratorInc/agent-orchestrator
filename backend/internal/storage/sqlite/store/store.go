// Package store contains SQLite-backed table stores built on sqlc-generated
// queries.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// Store is the SQLite-backed persistence layer. It routes writes to a single
// writer connection (qw) and reads to a reader pool (qr) — see Open. writeMu
// guards the read-modify-write write methods (e.g. CreateSession's
// next-num-then-insert) so concurrent writes can't interleave them.
//
// CDC is captured by DB triggers (migration 0001), NOT by this layer: the store
// never writes change_log, it only reads it for the CDC poller.
type Store struct {
	writeDB               *sql.DB
	readDB                *sql.DB
	qw                    *gen.Queries // bound to the single writer connection
	qr                    *gen.Queries // bound to the reader pool
	writeMu               *contextMutex
	usageTelemetryAllowed func() bool
	// The session search index lives in its own file, search.db
	// (storage/sqlite/search_db.go), and is optional: nothing outside search
	// touches it. searchDB is its single writer; searchReadDB is a separate
	// reader pool over ao.db with search.db attached, so a search.db that
	// cannot be opened fails search queries and never the app's other reads.
	searchDB     *sql.DB
	searchReadDB *sql.DB
	// searchReset empties the index in place (ResetSessionSearchIndex).
	searchReset func(context.Context) error
	// searchErr is why the index could not be opened, when it could not.
	searchErr error
}

// SetSearchIndex installs the session search database's writer, its attached
// reader pool, and the function that empties it. The store owns both pools
// from here on and closes them.
func (s *Store) SetSearchIndex(writer, readers *sql.DB, reset func(context.Context) error) {
	s.searchDB = writer
	s.searchReadDB = readers
	s.searchReset = reset
}

// ResetSessionSearchIndex empties the session search index, so the indexer
// rebuilds it from scratch. Session data is untouched: the index is derived
// from it.
func (s *Store) ResetSessionSearchIndex(ctx context.Context) error {
	if s.searchReset == nil {
		return errors.New("session search index is not open")
	}
	return s.searchReset(ctx)
}

// SetSearchUnavailable records that the session search index could not be
// opened. Everything else keeps working; the daemon leaves search off.
func (s *Store) SetSearchUnavailable(err error) {
	s.searchErr = err
}

// SessionSearchUnavailable reports why the session search index could not be
// opened, or nil when it is available.
func (s *Store) SessionSearchUnavailable() error {
	return s.searchErr
}

// searchWriter is where index writes go: the search database when one is
// installed, otherwise (a store opened over a bare connection in a test) the
// main writer.
func (s *Store) searchWriter() *sql.DB {
	if s.searchDB != nil {
		return s.searchDB
	}
	return s.writeDB
}

// searchReader is where index reads go: the reader pool with search.db
// attached when one is installed, otherwise (a store opened over a bare
// connection in a test) the main reader pool.
func (s *Store) searchReader() *sql.DB {
	if s.searchReadDB != nil {
		return s.searchReadDB
	}
	return s.readDB
}

// SetUsageTelemetryAllowed installs the live consent check before session
// services start. Usage facts are recorded only while export is permitted.
func (s *Store) SetUsageTelemetryAllowed(allowed func() bool) {
	s.usageTelemetryAllowed = allowed
}

func (s *Store) usageAllowed() bool {
	return s.usageTelemetryAllowed != nil && s.usageTelemetryAllowed()
}

// SetWriteLockObserver reports every write lock held for a second or more,
// after it is released, with the holder's call stack.
func (s *Store) SetWriteLockObserver(observe func(caller string, held time.Duration)) {
	s.writeMu.onSlow.Store(&observe)
}

type contextMutex struct {
	token    chan struct{}
	acquired time.Time
	slowHold time.Duration
	onSlow   atomic.Pointer[func(caller string, held time.Duration)]
}

func newContextMutex() *contextMutex {
	mutex := &contextMutex{token: make(chan struct{}, 1), slowHold: time.Second}
	mutex.token <- struct{}{}
	return mutex
}

func (m *contextMutex) Lock() {
	<-m.token
	m.acquired = time.Now()
}

func (m *contextMutex) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		if err := ctx.Err(); err != nil {
			m.token <- struct{}{}
			return err
		}
		m.acquired = time.Now()
		return nil
	}
}

func (m *contextMutex) Unlock() {
	held := time.Since(m.acquired)
	m.token <- struct{}{}
	if held < m.slowHold {
		return
	}
	if onSlow := m.onSlow.Load(); onSlow != nil {
		(*onSlow)(m.holder(), held)
	}
}

func (m *contextMutex) holder() string {
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(1, pcs)])
	var stack []string
	for len(stack) < 4 {
		frame, more := frames.Next()
		fn := frame.Function
		if fn != "" && !strings.Contains(fn, "contextMutex") && !strings.HasPrefix(fn, "runtime.") {
			stack = append(stack, fmt.Sprintf("%s:%d", fn[strings.LastIndex(fn, "/")+1:], frame.Line))
		}
		if !more {
			break
		}
	}
	return strings.Join(stack, " | ")
}

type conversationProjectionTxKey struct{}

// conversationWriter returns the transaction-bound query set when a provider
// event projection is in progress. Otherwise it acquires the ordinary single
// writer lock. This makes the existing focused store methods composable inside
// one archive+projection transaction without exposing SQLite transactions above
// the adapter boundary.
func (s *Store) conversationWriter(ctx context.Context) (*gen.Queries, func()) {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return q, func() {}
	}
	s.writeMu.Lock()
	return s.qw, s.writeMu.Unlock
}

func (s *Store) conversationReader(ctx context.Context) *gen.Queries {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return q
	}
	return s.qr
}

// NewStore wraps an opened writer + reader *sql.DB (see Open) as a Store.
func NewStore(writeDB, readDB *sql.DB) *Store {
	return &Store{
		writeDB: writeDB,
		readDB:  readDB,
		qw:      gen.New(writeDB),
		qr:      gen.New(readDB),
		writeMu: newContextMutex(),
	}
}

// Close closes every pool.
func (s *Store) Close() error {
	err := s.writeDB.Close()
	if e := s.readDB.Close(); e != nil && err == nil {
		err = e
	}
	for _, db := range []*sql.DB{s.searchReadDB, s.searchDB} {
		if db == nil {
			continue
		}
		if e := db.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// inTx runs fn inside a single write transaction on the writer connection,
// rolling back on error. The caller must already hold writeMu.
func (s *Store) inTx(ctx context.Context, what string, fn func(*gen.Queries) error) error {
	return s.inTxDB(ctx, what, func(q *gen.Queries, _ gen.DBTX) error { return fn(q) })
}

// inTxDB is the raw-database variant used by handwritten transactional stores
// while their sqlc artifacts are intentionally regenerated in a later slice.
func (s *Store) inTxDB(ctx context.Context, what string, fn func(*gen.Queries, gen.DBTX) error) error {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		if err := fn(q, nil); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", what, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(s.qw.WithTx(tx), tx); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return tx.Commit()
}
