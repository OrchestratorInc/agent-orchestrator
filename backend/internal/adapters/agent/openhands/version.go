package openhands

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var minVersion = [3]int{1, 12, 0}

const versionProbeTimeout = 30 * time.Second

var versionPattern = regexp.MustCompile(`OpenHands CLI (\d+)\.(\d+)\.(\d+)`)

var versionCommand = func(ctx context.Context, binary string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, binary, "--version").Output()
}

func supportedVersion(output string) bool {
	match := versionPattern.FindStringSubmatch(output)
	if match == nil {
		return false
	}
	for i, want := range minVersion {
		got, err := strconv.Atoi(match[i+1])
		if err != nil {
			return false
		}
		if got != want {
			return got > want
		}
	}
	return true
}

func checkVersion(ctx context.Context, binary string) error {
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	output, err := versionCommand(ctx, binary)
	if err != nil {
		return fmt.Errorf("openhands: check CLI version: %w", err)
	}
	if !supportedVersion(string(output)) {
		return fmt.Errorf("openhands: OpenHands CLI %d.%d.%d or newer is required", minVersion[0], minVersion[1], minVersion[2])
	}
	return nil
}
