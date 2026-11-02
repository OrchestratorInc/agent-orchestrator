package opencode

import (
	"context"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeResumeRequiresExportOfExactConversation(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		status       int
		want         bool
	}{
		{"original conversation", `{"info":{"id":"ses_original"},"messages":[{"text":"code-word: papaya"}]}`, 0, true},
		{"different conversation", `{"info":{"id":"ses_other"}}`, 0, false},
		{"missing history", ``, 1, false},
		{"malformed history", `not json`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			versionBinary(t, "if [ \"$1\" = --version ]; then echo 1.2.27; exit 0; fi\n[ \"$AO_RESUME_STORAGE_TEST\" = preserved ] || exit 98\n[ \"$1\" = export ] && [ \"$2\" = ses_original ] || exit 99\nprintf '%s' '"+tc.output+"'\nexit "+fmt.Sprint(tc.status)+"\n")
			// Use the same directory/config environment as the resumed terminal.
			exists, err := New().NativeConversationExists(context.Background(), ports.SessionRef{WorkspacePath: t.TempDir()}, "ses_original", map[string]string{"AO_RESUME_STORAGE_TEST": "preserved"})
			if tc.want {
				if err != nil || !exists {
					t.Fatalf("original history rejected: %t, %v", exists, err)
				}
			} else if exists {
				t.Fatal("unproven history accepted")
			}
		})
	}
}
