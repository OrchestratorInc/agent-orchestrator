package cursor

import (
	"strings"
	"testing"
)

// TestMCPDisableDropsPlatformCredentials 删除最终环境过滤会让 Cursor 预检继承平台秘密。
func TestMCPDisableDropsPlatformCredentials(t *testing.T) {
	t.Setenv("AO_WORKFLOW_CREDENTIAL_TEST", "fake-platform")
	t.Setenv("ao_workflow_provider_key", "fake-forward")
	t.Setenv("OPENAI_API_KEY", "fake-native")
	cmd := mcpDisableCommand(t.Context(), "cursor", "safe", t.TempDir(), t.TempDir())
	native := false
	for _, entry := range cmd.Env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "AO_WORKFLOW_CREDENTIAL_") || strings.EqualFold(key, "AO_WORKFLOW_PROVIDER_KEY") {
			t.Fatal("platform secret inherited by MCP preflight")
		}
		native = native || key == "OPENAI_API_KEY" && value == "fake-native"
	}
	if !native {
		t.Fatal("native authentication removed")
	}
}
