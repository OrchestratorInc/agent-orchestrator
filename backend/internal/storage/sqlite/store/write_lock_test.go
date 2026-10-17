package store

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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

// Break caught: 174 store writes took the writer with Lock(), so a request
// whose caller was gone kept waiting behind a long write. Every store write
// receives a context; take the writer with LockContext(ctx) so it can give up.
func TestStoreWritersNeverWaitForTheWriterWithoutTheirContext(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || method.Sel.Name != "Lock" {
				return true
			}
			if mu, ok := method.X.(*ast.SelectorExpr); ok && mu.Sel.Name == "writeMu" {
				t.Errorf("%s: writeMu.Lock() ignores the caller's context; use writeMu.LockContext(ctx)", fset.Position(call.Pos()))
			}
			return true
		})
	}
}
