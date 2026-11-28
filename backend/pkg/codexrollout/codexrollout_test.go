package codexrollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type rpcErr struct {
	code int
	msg  string
}

func (e *rpcErr) Error() string      { return fmt.Sprintf("rpc %d: %s", e.code, e.msg) }
func (e *rpcErr) RPCCode() int       { return e.code }
func (e *rpcErr) RPCMessage() string { return e.msg }

type call struct {
	method string
	params map[string]any
}

// fakeServer answers like an app-server whose thread holds turns.
type fakeServer struct {
	turns    []string
	calls    []call
	rollback error
	revert   error
	fork     string
}

func (f *fakeServer) Request(_ context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	f.calls = append(f.calls, call{method, p})
	var body any
	switch method {
	case "thread/read":
		turns := make([]Turn, len(f.turns))
		for i, id := range f.turns {
			turns[i] = Turn{ID: id, Status: "completed"}
		}
		body = map[string]any{"thread": map[string]any{"turns": turns}}
	case "thread/rollback":
		return f.rollback
	case "thread/revert":
		return f.revert
	case "thread/fork":
		body = map[string]any{"thread": map[string]any{"id": f.fork}}
	}
	if result == nil || body == nil {
		return nil
	}
	raw, _ = json.Marshal(body)
	return json.Unmarshal(raw, result)
}

func (f *fakeServer) methods() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.method
	}
	return out
}

func TestRollbackDropsTheTurnAndEverythingAfterIt(t *testing.T) {
	f := &fakeServer{turns: []string{"a", "b", "c"}}
	if err := Rollback(context.Background(), f, "thread-1", "b"); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	if last.method != "thread/rollback" || last.params["threadId"] != "thread-1" || last.params["numTurns"] != float64(2) {
		t.Fatalf("rollback call = %+v, want numTurns 2", last)
	}
}

func TestRollbackOfATurnTheThreadLacksDiscardsNothing(t *testing.T) {
	f := &fakeServer{turns: []string{"a", "b"}}
	err := Rollback(context.Background(), f, "thread-1", "zzz")
	if !errors.Is(err, ErrTurnNotInHistory) {
		t.Fatalf("Rollback = %v, want ErrTurnNotInHistory", err)
	}
	if got := f.methods(); !reflect.DeepEqual(got, []string{"thread/read"}) {
		t.Fatalf("calls = %v, want only the read", got)
	}
}

// Codex 0.162 removed thread/rollback; only that refusal falls back to
// thread/revert, anchored on the turn itself.
func TestRollbackFallsBackToRevertOnlyWhenTheMethodIsGone(t *testing.T) {
	for _, removed := range []error{
		&rpcErr{code: -32601, msg: "Method not found"},
		fmt.Errorf("wrapped: %w", &rpcErr{code: -32600, msg: "unknown variant `thread/rollback`"}),
	} {
		f := &fakeServer{turns: []string{"a", "b"}, rollback: removed}
		if err := Rollback(context.Background(), f, "thread-1", "b"); err != nil {
			t.Fatalf("Rollback with %v = %v", removed, err)
		}
		last := f.calls[len(f.calls)-1]
		if last.method != "thread/revert" || last.params["beforeTurnId"] != "b" {
			t.Fatalf("after %v, last call = %+v, want thread/revert before b", removed, last)
		}
	}
	busy := &rpcErr{code: -32600, msg: "Cannot rollback while a turn is in progress."}
	f := &fakeServer{turns: []string{"a", "b"}, rollback: busy}
	err := Rollback(context.Background(), f, "thread-1", "b")
	if !errors.Is(err, busy) {
		t.Fatalf("Rollback = %v, want the busy refusal", err)
	}
	if got := f.methods(); got[len(got)-1] != "thread/rollback" {
		t.Fatalf("a busy refusal tried another mutation: %v", got)
	}
}

func TestRollbackNeedsATurn(t *testing.T) {
	if err := Rollback(context.Background(), &fakeServer{}, "thread-1", " "); err == nil {
		t.Fatal("Rollback with a blank turn succeeded")
	}
}

func TestForkSendsOnlyTheThreadAndAnchor(t *testing.T) {
	f := &fakeServer{fork: "thread-2"}
	anchor := " b "
	id, err := Fork(context.Background(), f, "thread-1", &anchor)
	if err != nil || id != "thread-2" {
		t.Fatalf("Fork = %q, %v", id, err)
	}
	want := map[string]any{"threadId": "thread-1", "lastTurnId": "b"}
	if got := f.calls[0].params; !reflect.DeepEqual(got, want) {
		t.Fatalf("fork params = %v, want %v (no cwd, so the fork keeps the source's)", got, want)
	}
	if _, err := Fork(context.Background(), &fakeServer{fork: "thread-3"}, "thread-1", nil); err != nil {
		t.Fatalf("Fork of the whole thread = %v", err)
	}
	blank := "  "
	if _, err := Fork(context.Background(), f, "thread-1", &blank); err == nil {
		t.Fatal("Fork with a blank anchor succeeded")
	}
	if _, err := Fork(context.Background(), &fakeServer{}, "thread-1", nil); err == nil {
		t.Fatal("Fork that returned no thread id succeeded")
	}
}
