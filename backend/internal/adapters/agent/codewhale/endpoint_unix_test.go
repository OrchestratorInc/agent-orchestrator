//go:build !windows

package codewhale

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestOpenAIEndpointOverrideRunsAfterShellStartup(t *testing.T) {
	t.Setenv("CODEWHALE_PROVIDER", "openai")
	binary := filepath.Join(t.TempDir(), "synthetic-codewhale")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' \"$OPENAI_BASE_URL\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{resolvedBinary: binary}
	argv, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-c", `export OPENAI_BASE_URL=https://stray.invalid/v1; exec "$@"`, "synthetic-login-shell"}, argv...)
	output, err := exec.Command("/bin/sh", args...).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "https://api.openai.com/v1" {
		t.Fatalf("post-shell provider endpoint = %q, %v", output, err)
	}
}
