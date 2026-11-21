package sessionmanager

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func TestRuntimeEnvAppliesCustomHarnessEndpoint(t *testing.T) {
	dir := t.TempDir()
	if err := providerkeys.Save(t.Context(), dir, "ANTHROPIC_API_KEY", "ao-saved"); err != nil {
		t.Fatal(err)
	}
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint", Model: "glm-4.6"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	claude, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if claude["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" || claude["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" || claude["ANTHROPIC_MODEL"] != "glm-4.6" {
		t.Fatalf("custom endpoint not applied: %v", claude)
	}
	if value, ok := claude["ANTHROPIC_API_KEY"]; !ok || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY = %q (set=%v), want blanked beside the endpoint token", value, ok)
	}

	project, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{"ANTHROPIC_MODEL": "project-model"}, "", "claude-code")
	if err != nil || project["ANTHROPIC_MODEL"] != "project-model" {
		t.Fatalf("project env must win: model=%q err=%v", project["ANTHROPIC_MODEL"], err)
	}

	goose, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "goose")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := goose["ANTHROPIC_BASE_URL"]; ok || goose["ANTHROPIC_API_KEY"] != "ao-saved" {
		t.Fatalf("endpoint leaked into another harness or replaced its key: %v", goose)
	}
}
