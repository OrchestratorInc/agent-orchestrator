package chat

import (
	"context"
	"errors"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"testing"
)

func TestPoolAutoRejectedBeforeDriverProbe(t *testing.T) {
	// No registry or provider is necessary: unsupported policy fails before probe.
	s := &Service{}
	if err := s.PreflightChat(context.Background(), domain.HarnessPool, ports.PermissionModeAuto); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("Auto: %v", err)
	}
}
