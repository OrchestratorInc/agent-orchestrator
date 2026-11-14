package sessionguard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type inputLeaseFunc func(domain.SessionID) (func(), bool)

func (f inputLeaseFunc) AcquireSessionInput(id domain.SessionID) (func(), bool) { return f(id) }

func TestGuard_TerminalEncoderUsesFreshRecordUnderLease(t *testing.T) {
	store := &fakeStore{rec: record(domain.ActivityIdle, false), ok: true}
	store.rec.Harness = domain.HarnessClaudeCode
	messenger := &fakeMessenger{}
	guard := New(store, messenger, nil)
	leased := false
	guard.SetInputLease(inputLeaseFunc(func(domain.SessionID) (func(), bool) {
		leased = true
		// Select the adapter from the durable read after admission, rather
		// than from a caller's earlier view of the session's harness.
		store.rec.Harness = domain.HarnessFreebuff
		return func() { leased = false }, true
	}))
	message := strings.Repeat("A complete semantic line.\n", 150) + "MESSAGE_END"
	calls := 0
	guard.SetTerminalMessageEncoder(func(rec domain.SessionRecord, body string) (string, error) {
		calls++
		if !leased || rec.Harness != domain.HarnessFreebuff || body != message {
			t.Fatal("encoder did not receive the fresh semantic message under admission")
		}
		return "\x1b[200~" + body + "\x1b[201~", nil
	})
	postWrite := false
	outcome, err := guard.DeliverWithComposerCheck(context.Background(), "s1", message, nil, func(context.Context) error {
		postWrite = true
		if !leased || message != strings.Repeat("A complete semantic line.\n", 150)+"MESSAGE_END" {
			t.Fatal("post-write callback lost admission or semantic input")
		}
		return nil
	})
	if err != nil || outcome != Sent || calls != 1 || !postWrite || leased {
		t.Fatalf("outcome=%v err=%v calls=%d postWrite=%v leased=%v", outcome, err, calls, postWrite, leased)
	}
	if len(messenger.sent) != 1 || messenger.sent[0] != "\x1b[200~"+message+"\x1b[201~" {
		t.Fatal("wire message was not encoded exactly once")
	}
}

func TestGuard_TerminalEncoderBypassesChatEmptyAndUnsetCallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       domain.SessionMode
		message    string
		setEncoder bool
	}{
		{name: "chat", mode: domain.SessionModeChat, message: "semantic message", setEncoder: true},
		{name: "Enter only", mode: domain.SessionModeTUI, setEncoder: true},
		{name: "unconfigured guard", mode: domain.SessionModeTUI, message: "semantic message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := record(domain.ActivityIdle, false)
			rec.Mode = tc.mode
			messenger := &fakeMessenger{}
			guard := New(&fakeStore{rec: rec, ok: true}, messenger, nil)
			if tc.setEncoder {
				guard.SetTerminalMessageEncoder(func(domain.SessionRecord, string) (string, error) {
					t.Fatal("encoder must be bypassed")
					return "", errors.New("unexpected encoder")
				})
			}
			outcome, err := guard.Deliver(context.Background(), "s1", tc.message)
			if err != nil || outcome != Sent || len(messenger.sent) != 1 || messenger.sent[0] != tc.message {
				t.Fatalf("outcome=%v err=%v sends=%q", outcome, err, messenger.sent)
			}
		})
	}
}

func TestGuard_TerminalEncoderFailureDoesNotWriteOrRecord(t *testing.T) {
	messenger := &fakeMessenger{}
	guard := New(&fakeStore{rec: record(domain.ActivityIdle, false), ok: true}, messenger, nil)
	encodeErr := errors.New("unsafe terminal input")
	guard.SetTerminalMessageEncoder(func(domain.SessionRecord, string) (string, error) {
		return "partial unsafe frame", encodeErr
	})
	postWrite := false
	outcome, err := guard.DeliverWithComposerCheck(context.Background(), "s1", "semantic message", nil, func(context.Context) error {
		postWrite = true
		return nil
	})
	if outcome != SuppressedUnknown || !errors.Is(err, encodeErr) || len(messenger.sent) != 0 || postWrite {
		t.Fatalf("outcome=%v err=%v writes=%d postWrite=%v", outcome, err, len(messenger.sent), postWrite)
	}
}

func TestGuard_TerminalEncodingRunsAfterSuppression(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rec     domain.SessionRecord
		missing bool
		gated   bool
		startup bool
		want    Outcome
	}{
		{name: "missing", missing: true, want: SuppressedNotFound},
		{name: "terminated", rec: record(domain.ActivityIdle, true), want: SuppressedTerminated},
		{name: "exited", rec: record(domain.ActivityExited, false), want: SuppressedExited},
		{name: "blocked", rec: record(domain.ActivityBlocked, false), want: SuppressedAwaitingUser},
		{name: "input gated", rec: record(domain.ActivityIdle, false), gated: true, want: SuppressedInputGated},
		{name: "startup pending", rec: domain.SessionRecord{ID: "s1", Activity: domain.Activity{State: domain.ActivityIdle}}, startup: true, want: SuppressedStartupPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messenger := &fakeMessenger{}
			guard := New(&fakeStore{rec: tc.rec, ok: !tc.missing}, messenger, nil)
			guard.SetInputLease(fixedInputLease(!tc.gated))
			guard.SetStartupSignalGate(func(domain.AgentHarness) bool { return tc.startup })
			guard.SetTerminalMessageEncoder(func(domain.SessionRecord, string) (string, error) {
				t.Fatal("suppressed input reached the encoder")
				return "", errors.New("invalid terminal input")
			})
			outcome, err := guard.Deliver(context.Background(), "s1", "invalid semantic message")
			if err != nil || outcome != tc.want || len(messenger.sent) != 0 {
				t.Fatalf("outcome=%v err=%v writes=%d", outcome, err, len(messenger.sent))
			}
		})
	}
}

func TestGuard_TerminalEncodingRunsAfterMutationProof(t *testing.T) {
	messenger := &fakeMessenger{}
	guard := New(&fakeStore{rec: record(domain.ActivityIdle, false), ok: true}, messenger, nil)
	proofErr := errors.New("runtime generation changed")
	guard.SetTerminalMessageEncoder(func(domain.SessionRecord, string) (string, error) {
		t.Fatal("failed pre-write proof reached the encoder")
		return "", nil
	})
	outcome, err := guard.DeliverUnderMutationChecked(context.Background(), "s1", "semantic message", func(context.Context, domain.SessionRecord) error {
		return proofErr
	})
	if outcome != SuppressedUnknown || !errors.Is(err, proofErr) || len(messenger.sent) != 0 {
		t.Fatalf("outcome=%v err=%v writes=%d", outcome, err, len(messenger.sent))
	}

	proofRan := false
	guard.SetTerminalMessageEncoder(func(_ domain.SessionRecord, message string) (string, error) {
		if !proofRan {
			t.Fatal("encoder ran before the runtime generation proof")
		}
		return "encoded:" + message, nil
	})
	outcome, err = guard.DeliverUnderMutationChecked(context.Background(), "s1", "semantic message", func(context.Context, domain.SessionRecord) error {
		proofRan = true
		return nil
	})
	if err != nil || outcome != Sent || len(messenger.sent) != 1 || messenger.sent[0] != "encoded:semantic message" {
		t.Fatalf("outcome=%v err=%v sends=%q", outcome, err, messenger.sent)
	}
}
