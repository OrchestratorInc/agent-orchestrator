package zcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStandingInstructionsRejectTruncationBeforeNativeLaunch(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "restore"}[restore], func(t *testing.T) {
			p := &Plugin{resolvedBinary: "must-not-execute"}
			text := strings.Repeat("x", maxStandingInstructionBytes+1)
			var err error
			if restore {
				_, _, err = p.GetRestoreCommand(context.Background(), ports.RestoreConfig{SystemPrompt: text})
			} else {
				_, err = p.GetLaunchCommand(context.Background(), ports.LaunchConfig{SystemPrompt: text})
			}
			if err == nil || !strings.Contains(err.Error(), "native hook context budget") {
				t.Fatalf("oversized private instructions were not rejected before native command: %v", err)
			}
		})
	}
}
func TestStandingInstructionFileBoundAndUTF8(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private.md")
	for _, tc := range []struct {
		name, text string
		wantError  bool
	}{
		{"ascii exact bound", strings.Repeat("x", maxStandingInstructionBytes), false},
		{"unicode byte bound", strings.Repeat("界", maxStandingInstructionBytes/3), false},
		{"unicode exceeds", strings.Repeat("界", maxStandingInstructionBytes/3+1), true},
		{"JSON escaping exceeds native stdout", strings.Repeat("\x00", maxStandingInstructionBytes), true},
		{"invalid UTF8", string([]byte{0xff}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateStandingInstructions(ctx, "", path); (err != nil) != tc.wantError {
				t.Fatalf("file validation error=%v, want error=%v", err, tc.wantError)
			}
		})
	}
	if err := validateStandingInstructions(ctx, "", path+"-missing"); err == nil {
		t.Fatal("missing private input accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := validateStandingInstructions(canceled, "", path); err == nil {
		t.Fatal("cancellation ignored")
	}
}
