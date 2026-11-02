package sessionimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	cliListTimeout  = 10 * time.Second
	cliListMaxBytes = 8 << 20
)

var errCLIListTooLarge = errors.New("provider conversation list exceeded the output limit")

// readOnlyCLIList is only for provider-defined, noninteractive list commands.
// Callers supply fixed list arguments, never prompts or authentication commands.
// It inherits the existing login/config environment without changing either.
func readOnlyCLIList(parent context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, cliListTimeout)
	defer cancel()
	stdout := &cliListBuffer{limit: cliListMaxBytes, cancel: cancel}
	stderr := &cliListBuffer{limit: 64 << 10, cancel: cancel}
	cmd := process.CommandContext(ctx, binary, args...)
	process.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, errCLIListTooLarge
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("list provider conversations: %w", err)
	}
	return stdout.buffer.Bytes(), nil
}

type cliListBuffer struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (b *cliListBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		b.exceeded = true
		b.cancel()
		return remaining, errCLIListTooLarge
	}
	return b.buffer.Write(p)
}
