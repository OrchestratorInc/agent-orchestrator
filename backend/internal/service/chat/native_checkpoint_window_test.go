package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type slowCheckpointVerifier struct {
	read    time.Duration
	results []error
	calls   int
}

func (v *slowCheckpointVerifier) VerifyNativeCheckpoint(ctx context.Context, _ ports.NativeCheckpointRequest) (ports.NativeCheckpointBoundary, error) {
	select {
	case <-time.After(v.read):
	case <-ctx.Done():
		return ports.NativeCheckpointBoundary{}, ctx.Err()
	}
	err := v.results[min(v.calls, len(v.results)-1)]
	v.calls++
	if err != nil {
		return ports.NativeCheckpointBoundary{}, err
	}
	return ports.NativeCheckpointBoundary{UserMessageID: "settled"}, nil
}

func withSettleWindow(t *testing.T, window time.Duration) {
	t.Helper()
	previous := nativeCheckpointSettleWindow
	nativeCheckpointSettleWindow = window
	t.Cleanup(func() { nativeCheckpointSettleWindow = previous })
}

func unsettledCheckpoint() error {
	return &ports.ChatHistoryUnsettledError{Dimensions: []ports.ChatHistoryMismatchDimension{ports.ChatHistoryMismatchUnsettledBoundary}}
}

func TestVerifyNativeCheckpointFinishesAReadLongerThanTheWindow(t *testing.T) {
	withSettleWindow(t, 20*time.Millisecond)
	verifier := &slowCheckpointVerifier{read: 80 * time.Millisecond, results: []error{nil}}

	boundary, err := verifyNativeCheckpoint(context.Background(), verifier, ports.NativeCheckpointRequest{})
	if err != nil || boundary.UserMessageID != "settled" {
		t.Fatalf("verifyNativeCheckpoint = %+v, %v; want the settled boundary", boundary, err)
	}
}

func TestVerifyNativeCheckpointReportsUnsettledAfterASlowRead(t *testing.T) {
	withSettleWindow(t, 20*time.Millisecond)
	verifier := &slowCheckpointVerifier{read: 80 * time.Millisecond, results: []error{unsettledCheckpoint()}}

	_, err := verifyNativeCheckpoint(context.Background(), verifier, ports.NativeCheckpointRequest{})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) || len(ports.ChatHistoryMismatchDimensions(err)) == 0 {
		t.Fatalf("verifyNativeCheckpoint error = %v; want the unsettled verdict with its dimensions", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("reads = %d, want 1 once the window has passed", verifier.calls)
	}
}

func TestVerifyNativeCheckpointStartsNoReadAfterTheWindow(t *testing.T) {
	withSettleWindow(t, 150*time.Millisecond)
	verifier := &slowCheckpointVerifier{read: 120 * time.Millisecond, results: []error{unsettledCheckpoint(), nil}}

	_, err := verifyNativeCheckpoint(context.Background(), verifier, ports.NativeCheckpointRequest{})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) || verifier.calls != 1 {
		t.Fatalf("verifyNativeCheckpoint error = %v after %d reads; want unsettled after 1 read", err, verifier.calls)
	}
}

func TestVerifyNativeCheckpointBoundsAStalledRead(t *testing.T) {
	previous := nativeHistoryLoadAttemptLimit
	nativeHistoryLoadAttemptLimit = 20 * time.Millisecond
	t.Cleanup(func() { nativeHistoryLoadAttemptLimit = previous })
	verifier := &slowCheckpointVerifier{read: time.Minute, results: []error{nil}}

	_, err := verifyNativeCheckpoint(context.Background(), verifier, ports.NativeCheckpointRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("verifyNativeCheckpoint error = %v; want the read cut at its limit", err)
	}
}

func TestVerifyNativeCheckpointRetriesUnsettledWithinTheWindow(t *testing.T) {
	withSettleWindow(t, time.Second)
	verifier := &slowCheckpointVerifier{results: []error{unsettledCheckpoint(), nil}}

	boundary, err := verifyNativeCheckpoint(context.Background(), verifier, ports.NativeCheckpointRequest{})
	if err != nil || boundary.UserMessageID != "settled" || verifier.calls != 2 {
		t.Fatalf("verifyNativeCheckpoint = %+v, %v after %d reads; want settled on the second read", boundary, err, verifier.calls)
	}
}

func TestVerifyNativeCheckpointSettlesAnUnansweredTailOnlyAfterTheWindow(t *testing.T) {
	unanswered := &ports.UnansweredNativeTailError{Boundary: ports.NativeCheckpointBoundary{UserMessageID: "unanswered", Interrupted: true}}
	for name, test := range map[string]struct {
		window  time.Duration
		results []error
		want    string
	}{
		"no reply arrives": {window: 20 * time.Millisecond, results: []error{unanswered}, want: "unanswered"},
		"a reply lands":    {window: time.Second, results: []error{unanswered, nil}, want: "settled"},
	} {
		t.Run(name, func(t *testing.T) {
			withSettleWindow(t, test.window)
			boundary, err := verifyNativeCheckpoint(context.Background(), &slowCheckpointVerifier{results: test.results}, ports.NativeCheckpointRequest{})
			if err != nil || boundary.UserMessageID != test.want {
				t.Fatalf("verifyNativeCheckpoint = %+v, %v; want %s", boundary, err, test.want)
			}
		})
	}
}
