package acp

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// exitingSpawn is an agent that exits before answering anything.
func exitingSpawn(Launch, string) (*process, error) {
	stdinR, stdinW := io.Pipe()
	go func() { _, _ = io.Copy(io.Discard, stdinR) }()
	return &process{stdin: stdinW, stdout: strings.NewReader(""), stop: func() error { return stdinW.Close() }}, nil
}

func TestStartRetriesAnAgentThatExitsDuringItsFirstHandshake(t *testing.T) {
	for _, retry := range []bool{true, false} {
		agent := &fakeAgent{}
		spawns := 0
		driver := New(Config{
			Harness:          domain.HarnessPi,
			Capabilities:     ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
			Probe:            func(context.Context) error { return nil },
			Launch:           func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
			RetryStartupExit: retry,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		driver.useTestProcess(func(launch Launch, dir string) (*process, error) {
			spawns++
			if spawns == 1 {
				return exitingSpawn(launch, dir)
			}
			return fakeSpawn(agent)(launch, dir)
		})

		conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
		if retry {
			if err != nil || spawns != 2 {
				t.Fatalf("with retry: err = %v after %d starts, want a working second start", err, spawns)
			}
			_ = conv.Close()
		} else if err == nil || spawns != 1 {
			t.Fatalf("without retry: err = %v after %d starts, want the first exit reported", err, spawns)
		}
	}
}
