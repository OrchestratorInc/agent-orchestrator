package opencodev2

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeResumeExportUsesPrivateServerAndDataHome(t *testing.T) {
	binary := fakeBinary(t, "2.0.0")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	script := `#!/bin/sh
if [ "$1" = --version ]; then echo 2.0.0; exit; fi
[ "$1" = session ] && [ "$2" = export ] && [ "$3" = ses_original ] && [ "$4" = --standalone ] || exit 91
[ "$XDG_DATA_HOME" = "$AO_EXPECTED_DATA_HOME" ] || exit 92
printf '%s' '{"info":{"id":"ses_original"},"messages":[{"text":"papaya"}]}'
`
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	exists, err := New().NativeConversationExists(context.Background(), ports.SessionRef{WorkspacePath: t.TempDir()}, "ses_original", map[string]string{"AO_EXPECTED_DATA_HOME": filepath.Join(home, "opencode-v2-home")})
	if err != nil || !exists {
		t.Fatalf("isolated exact history export: %t, %v", exists, err)
	}
}
