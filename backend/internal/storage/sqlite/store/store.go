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

type Store struct {
	writeDB               *sql.DB
	readDB                *sql.DB
	qw                    *gen.Queries
	qr                    *gen.Queries
	writeMu               *contextMutex
	usageTelemetryAllowed func() bool
	searchDB              *sql.DB
	searchReadDB          *sql.DB
	searchReset           func(context.Context) error
	searchErr             error
}

func (s *Store) SetSearchIndex(writer, readers *sql.DB, reset func(context.Context) error) {
	s.searchDB = writer
	s.searchReadDB = readers
	s.searchReset = reset
}

func (s *Store) ResetSessionSearchIndex(ctx context.Context) error {
	if s.searchReset == nil {
		return errors.New("session search index is not open")
	}
	return s.searchReset(ctx)
}

func (s *Store) SetSearchUnavailable(err error) {
	s.searchErr = err
}

func (s *Store) SessionSearchUnavailable() error {
	return s.searchErr
}

func (s *Store) searchWriter() *sql.DB {
	if s.searchDB != nil {
		return s.searchDB
	}
	return s.writeDB
}

func (s *Store) searchReader() *sql.DB {
	if s.searchReadDB != nil {
		return s.searchReadDB
	}
	return s.readDB
}

func (s *Store) SetUsageTelemetryAllowed(allowed func() bool) {
	s.usageTelemetryAllowed = allowed
}

func (s *Store) usageAllowed() bool {
	return s.usageTelemetryAllowed != nil && s.usageTelemetryAllowed()
}

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

func NewStore(writeDB, readDB *sql.DB) *Store {
	return &Store{
		writeDB: writeDB,
		readDB:  readDB,
		qw:      gen.New(writeDB),
		qr:      gen.New(readDB),
		writeMu: newContextMutex(),
	}
}

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

func (s *Store) inTx(ctx context.Context, what string, fn func(*gen.Queries) error) error {
	return s.inTxDB(ctx, what, func(q *gen.Queries, _ gen.DBTX) error { return fn(q) })
}

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
