package dockerreap

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// fakeRunner scripts docker CLI responses by exact argv match, in call order.
type fakeRunner struct {
	t      *testing.T
	calls  [][]string
	script []fakeCall
	i      int
}

type fakeCall struct {
	out []byte
	err error
}

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.i >= len(f.script) {
		f.t.Fatalf("unexpected call #%d: %s %s", f.i, name, strings.Join(args, " "))
	}
	c := f.script[f.i]
	f.i++
	return c.out, c.err
}

func TestReapSessionContainers_RemovesLabeledNonSpared(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{out: []byte("abc123\tfalse\ndef456\t\n")}, // list: one explicitly not-spared, one unset
		{out: nil}, // rm abc123
		{out: nil}, // rm def456
	}}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID("sess-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if len(fr.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(fr.calls))
	}
}

func TestReapSessionContainers_SkipsSpared(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{out: []byte("abc123\ttrue\ndef456\tfalse\n")}, // abc123 spared, def456 not
		{out: nil}, // rm def456 only
	}}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID("sess-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (spared container must survive)", removed)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fr.calls))
	}
	rmArgs := fr.calls[1]
	if !strings.Contains(strings.Join(rmArgs, " "), "def456") {
		t.Fatalf("expected rm call to target def456, got %v", rmArgs)
	}
}

func TestReapSessionContainers_NoneFound(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{out: []byte("")},
	}}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID("sess-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestReapSessionContainers_EmptySessionID(t *testing.T) {
	fr := &fakeRunner{t: t, script: nil}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 for empty session id", removed)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("expected no docker calls for empty session id, got %d", len(fr.calls))
	}
}

func TestReapSessionContainers_DockerNotInstalled(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{err: &exec.Error{Name: "docker", Err: errors.New("executable file not found in $PATH")}},
	}}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID("sess-1"))
	if err != nil {
		t.Fatalf("missing docker binary must not be an error, got: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestReapSessionContainers_ListFailsSparesEverything(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{err: errors.New("boom: docker daemon returned malformed output")},
	}}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID("sess-1"))
	if err == nil {
		t.Fatal("expected error to propagate on a genuine list failure")
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 (must spare on ambiguity)", removed)
	}
}

func TestReapSessionContainers_PartialRemovalReportsCountAndError(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{out: []byte("abc123\t\ndef456\t\n")},
		{out: nil}, // rm abc123 succeeds
		{err: errors.New("rm def456: container is running")}, // rm def456 fails
	}}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID("sess-1"))
	if err == nil {
		t.Fatal("expected error from failed removal")
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (the container that did succeed)", removed)
	}
}

// TestReapSessionContainers_MiddleFailureDoesNotAbortRemaining is the
// regression for the comment/code contradiction: a stuck container in the
// middle of the list must not leave later containers unreaped. A 2-container
// test with the failure last can't distinguish "kept going" from "aborted" —
// this needs 3, with the failure in the middle.
func TestReapSessionContainers_MiddleFailureDoesNotAbortRemaining(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{out: []byte("aaa\t\nbbb\t\nccc\t\n")},
		{out: nil}, // rm aaa succeeds
		{err: errors.New("rm bbb: container is running")}, // rm bbb fails
		{out: nil}, // rm ccc must still be attempted
	}}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID("sess-1"))
	if err == nil {
		t.Fatal("expected the middle failure to surface")
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2 (aaa and ccc must both succeed despite bbb failing)", removed)
	}
	if len(fr.calls) != 4 {
		t.Fatalf("calls = %d, want 4 (list + 3 rm attempts, ccc must still be attempted after bbb fails)", len(fr.calls))
	}
	lastCall := fr.calls[3]
	if !strings.Contains(strings.Join(lastCall, " "), "ccc") {
		t.Fatalf("expected the 4th call to still target ccc, got %v", lastCall)
	}
}

// TestReapSessionContainers_ContextDeadlineExceededSparesEverything is the
// non-blocking review item: a wedged docker daemon (a docker call blocking
// past reapTimeout) must resolve to "reap nothing," not a kill-blocking
// error — same bias as every other ambiguous failure mode.
func TestReapSessionContainers_ContextDeadlineExceededSparesEverything(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{err: context.DeadlineExceeded},
	}}
	r := newWithRunner(fr)

	removed, err := r.ReapSessionContainers(context.Background(), domain.SessionID("sess-1"))
	if err != nil {
		t.Fatalf("a deadline-exceeded docker call must not be an error, got: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}
