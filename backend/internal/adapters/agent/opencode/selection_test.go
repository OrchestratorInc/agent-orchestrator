package opencode

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
)

// TestSelectedOpenCodeRetainsMajorGate 使用真实版本进程验证共享命令名仍按 Harness 独立判定。
func TestSelectedOpenCodeRetainsMajorGate(t *testing.T) {
	name, script := "opencode", "#!/bin/sh\necho 2.0.0\n"
	if runtime.GOOS == "windows" {
		name, script = "opencode.cmd", "@echo off\r\necho 2.0.0\r\n"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	binaryutil.SetSelectedBinaries(map[string]string{"opencode": path, "opencode-v2": path})
	t.Cleanup(func() { binaryutil.SetSelectedBinaries(nil) })
	if got, err := ResolveBinaryForMajor(t.Context(), 2); err != nil || got != path {
		t.Fatalf("selected OpenCode 2 = %q, %v", got, err)
	}
	if got, err := ResolveBinaryForMajor(t.Context(), 1); err == nil || got != "" {
		t.Fatalf("OpenCode 1 accepted wrong selected major or fell back: %q, %v", got, err)
	}
}
