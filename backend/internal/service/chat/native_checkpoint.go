package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var nativeCheckpointSettleWindow = 3 * time.Second

// The source has stopped, but Claude writes JSONL asynchronously to its hooks.
// Retry fresh provider observations for a bounded window before launching Chat.
func verifyNativeCheckpoint(ctx context.Context, verifier ports.NativeCheckpointVerifier, request ports.NativeCheckpointRequest) (ports.NativeCheckpointBoundary, error) {
	ctx, cancel := context.WithTimeout(ctx, nativeHistoryLoadAttemptLimit)
	defer cancel()
	deadline := time.Now().Add(nativeCheckpointSettleWindow)
	for {
		boundary, err := verifier.VerifyNativeCheckpoint(ctx, request)
		if err == nil {
			return boundary, nil
		}
		if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
			return ports.NativeCheckpointBoundary{}, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ports.NativeCheckpointBoundary{}, fmt.Errorf("wait for native checkpoint: %w: %w", err, ctx.Err())
		case <-timer.C:
		}
		if !time.Now().Before(deadline) {
			return ports.NativeCheckpointBoundary{}, fmt.Errorf("wait for native checkpoint: %w: %w", err, context.DeadlineExceeded)
		}
	}
}
