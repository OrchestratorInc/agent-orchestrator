package agentbase

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeJSONSessionRequiresExactIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	for _, tc := range []struct {
		name, output string
		want         bool
	}{
		{"export", `{"info":{"id":"original"},"messages":[{"text":"papaya"}]}`, true},
		{"list", `[{"id":"other"},{"id":"original"}]`, true},
		{"different", `{"info":{"id":"other"}}`, false},
		{"missing", `[]`, false},
		{"invalid", `no JSON`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "native")
			script := "#!/bin/sh\n[ \"$AO_RESUME_TEST\" = inherited ] || exit 1\n[ \"$PWD\" = \"$AO_RESUME_CWD\" ] || exit 2\nprintf '%s' '" + tc.output + "'\n"
			if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			got, err := NativeJSONSessionExists(context.Background(), binary, []string{"export", "original"}, ports.SessionRef{WorkspacePath: dir}, "original", map[string]string{"AO_RESUME_TEST": "inherited", "AO_RESUME_CWD": canonicalPath(t, dir)})
			if got != tc.want || (tc.want && err != nil) {
				t.Fatalf("exists=%t, error=%v, want %t", got, err, tc.want)
			}
		})
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
