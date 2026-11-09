package providerprofile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestRegistryPinsWholeSelectionAcrossReopen 删除摘要比较会让已批准任务采用变化后的路由。
func TestRegistryPinsWholeSelectionAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), Filename)
	p := ports.ProviderProfile{ID: "offline", Name: "离线供应商", Harness: domain.HarnessCodex, BaseURL: "https://provider.invalid/v1", Model: "offline-model", Effort: "high", ContextWindow: 64000, CredentialEnv: "AO_WORKFLOW_CREDENTIAL_TEST"}
	write := func(profiles []ports.ProviderProfile) {
		t.Helper()
		raw, err := json.Marshal(profiles)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write([]ports.ProviderProfile{p})
	registry := &Registry{Path: path, LookupEnv: func(key string) (string, bool) { return "test-secret-only", key == p.CredentialEnv }}
	choices, err := registry.List(ctx)
	if err != nil || len(choices) != 1 {
		t.Fatalf("directory: %v %v", choices, err)
	}
	ref := choices[0].Reference
	public, _ := json.Marshal(choices)
	if strings.Contains(string(public), "test-secret-only") || strings.Contains(string(public), p.CredentialEnv) || strings.Contains(string(public), p.BaseURL) {
		t.Fatal("public directory exposed private routing or credentials")
	}
	reopened := &Registry{Path: path}
	if _, err := reopened.Resolve(ctx, ref, domain.HarnessCodex, p.Model, p.Effort); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessOMP, domain.HarnessDeepSeek} {
		if _, err := reopened.Resolve(ctx, ref, harness, p.Model, p.Effort); err == nil {
			t.Fatalf("cross-adapter selection accepted: %s", harness)
		}
	}
	registry.LookupEnv = func(string) (string, bool) { return "", false }
	if _, err := registry.Environment(ctx, ref, domain.HarnessCodex, p.Model, p.Effort); err == nil {
		t.Fatal("missing profile credential fell back to native account")
	}
	p.BaseURL = "https://changed.invalid/v1"
	write([]ports.ProviderProfile{p})
	if _, err := reopened.Resolve(ctx, ref, domain.HarnessCodex, p.Model, p.Effort); err == nil {
		t.Fatal("changed route accepted old reference")
	}
	if err := Validate(ctx, nil, "", domain.HarnessClaudeCode, "native", ""); err != nil {
		t.Fatal("cleared selection did not retain native path")
	}
}

// TestRegistryRejectsWholeInvalidDocument 删除严格字段或整表验证会把未知权限或合法前缀当作已批准配置。
func TestRegistryRejectsWholeInvalidDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), Filename)
	valid := `{"id":"offline","name":"离线","harness":"codex","baseUrl":"https://provider.invalid/v1","model":"m","credentialEnv":"AO_WORKFLOW_CREDENTIAL_OFFLINE"}`
	for _, raw := range []string{
		"[" + strings.Replace(valid, "AO_WORKFLOW_CREDENTIAL_OFFLINE", "OPENAI_API_KEY", 1) + "]",
		"[" + valid + "," + valid + "]",
		"[" + strings.TrimSuffix(valid, "}") + `,"sandbox":"danger-full-access"}]`,
		"[" + strings.Replace(valid, "https://provider.invalid", "https://user:password@provider.invalid", 1) + "]",
		"[" + strings.Replace(valid, "https://provider.invalid", "http://provider.invalid", 1) + "]",
		"[" + valid + "] {}",
		"[" + valid + `,{"id":"bad"}]`,
	} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		choices, err := (&Registry{Path: path}).List(context.Background())
		if err == nil || len(choices) != 0 {
			t.Fatal("invalid document was partially accepted")
		}
		if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), path) {
			t.Fatal("registry error exposed source content")
		}
	}
}
