package persistenthost

import (
	"context"
	"errors"
	"testing"
)

// A host that never came up is labelled for spawn telemetry without changing
// what the error says or which sentinels it matches.
func TestHostStartErrorLabelsWithoutChangingTheError(t *testing.T) {
	inner := errors.Join(errors.New("start chat host: connection refused"), context.Canceled)
	err := error(hostStartError{err: inner})
	var cause interface{ FailureCause() string }
	if !errors.As(err, &cause) || cause.FailureCause() != "host_failed" {
		t.Fatalf("hostStartError cause = %v, want host_failed", err)
	}
	if err.Error() != inner.Error() || !errors.Is(err, context.Canceled) {
		t.Fatalf("hostStartError changed the error: %v", err)
	}
}
