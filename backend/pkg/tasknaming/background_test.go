package tasknaming

import (
	"maps"
	"strings"
	"testing"
)

func TestSupported(t *testing.T) {
	for _, harness := range []string{"codex", "claude-code", "kimi"} {
		if !Supported(harness) {
			t.Errorf("%s should support background naming", harness)
		}
	}
	if Supported("unknown") {
		t.Fatal("unknown harness supports background naming")
	}
}

func TestRunBackgroundPreservesCallerCredentials(t *testing.T) {
	env := map[string]string{"PROVIDER_API_KEY": "secret", "AO_SESSION_ID": "worker", "AO_PERMISSION_MODE": "auto"}
	want := maps.Clone(env)
	_, err := RunBackground(t.Context(), BackgroundConfig{
		Harness: "unknown", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Env: env,
	}, SystemPrompt, "Fix login")
	if err == nil {
		t.Fatal("expected unsupported harness")
	}
	if !maps.Equal(env, want) {
		t.Fatal("background task changed caller credentials or worker context")
	}
}

func TestRunBackgroundRejectsInvalidConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name, harness, dataDir, workspacePath, prompt, want string
	}{
		{"empty data dir", "codex", "", t.TempDir(), "Fix login", "data dir must be absolute"},
		{"relative data dir", "codex", "data", t.TempDir(), "Fix login", "data dir must be absolute"},
		{"empty workspace", "codex", t.TempDir(), "", "Fix login", "workspace path must be absolute"},
		{"relative workspace", "codex", t.TempDir(), "workspace", "Fix login", "workspace path must be absolute"},
		{"unsupported harness", "unknown", t.TempDir(), t.TempDir(), "Fix login", "chat mode unsupported"},
		{"empty prompt", "codex", t.TempDir(), t.TempDir(), "  ", "prompt is empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RunBackground(t.Context(), BackgroundConfig{Harness: tt.harness, DataDir: tt.dataDir, WorkspacePath: tt.workspacePath}, SystemPrompt, tt.prompt)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
