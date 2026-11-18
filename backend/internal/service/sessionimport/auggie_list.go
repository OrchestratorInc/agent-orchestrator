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
	auggieListTimeout  = 10 * time.Second
	auggieListMaxBytes = 8 << 20
)

var errAuggieListTooLarge = errors.New("Auggie conversation list exceeded the output limit")

// readAuggieList is only for Auggie-defined, noninteractive list commands.
// Callers supply fixed list arguments, never prompts or authentication commands.
// It inherits the existing login/config environment without changing either.
func readAuggieList(parent context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, auggieListTimeout)
	defer cancel()
	stdout := &auggieListBuffer{limit: auggieListMaxBytes, cancel: cancel}
	stderr := &auggieListBuffer{limit: 64 << 10, cancel: cancel}
	cmd := process.CommandContext(ctx, binary, args...)
	process.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, errAuggieListTooLarge
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("list Auggie conversations: %w", err)
	}
	return stdout.buffer.Bytes(), nil
}

type auggieListBuffer struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (b *auggieListBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		b.exceeded = true
		b.cancel()
		return remaining, errAuggieListTooLarge
	}
	return b.buffer.Write(p)
}
