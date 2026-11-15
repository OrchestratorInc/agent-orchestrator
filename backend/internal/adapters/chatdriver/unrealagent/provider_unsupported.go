//go:build !darwin && !linux

package unrealagent

import (
	"context"
	"fmt"
	"io"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func RunProvider(context.Context, string, io.Reader, io.Writer) error {
	return fmt.Errorf("%w: Unreal Agent supports macOS and Linux", ports.ErrChatDriverUnavailable)
}
