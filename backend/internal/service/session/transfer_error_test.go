package session

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A receive that fails to start the session reports why, not a bare 500.
func TestTransferErrorKeepsSpawnCodes(t *testing.T) {
	err := transferError(fmt.Errorf("receive: spawn: %w: tmux required on macOS", ports.ErrRuntimePrerequisite))
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "RUNTIME_PREREQUISITE_MISSING" {
		t.Fatalf("transferError(missing tmux) = %v", err)
	}
}
