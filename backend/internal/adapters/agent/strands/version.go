package strands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// The draft is deliberately pinned to the CLI release whose private config,
// persistence layout and lifecycle have been inspected. Requalify upgrades.
func checkVersion(ctx context.Context, binary string) error {
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := aoprocess.CommandContext(probe, binary, "--version").Output()
	if err != nil {
		return fmt.Errorf("strands: check CLI version: %w", err)
	}
	if strings.TrimSpace(string(output)) != "0.2.0" {
		return errors.New("strands: this experimental adapter requires CLI 0.2.0")
	}
	return nil
}
