package gitworktree

import (
	"os"
	"strings"
	"testing"
)

// TestWorkspaceCommandDropsPlatformCredentials 删除 Git 通用执行边界过滤会向仓库可影响的子进程暴露凭据。
func TestWorkspaceCommandDropsPlatformCredentials(t *testing.T) {
	if os.Getenv("AO_GIT_ENV_HELPER") == "1" {
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			if strings.HasPrefix(strings.ToUpper(key), "AO_WORKFLOW_CREDENTIAL_") || strings.EqualFold(key, "AO_WORKFLOW_PROVIDER_KEY") {
				os.Exit(21)
			}
		}
		if os.Getenv("OPENAI_API_KEY") != "fake-native" {
			os.Exit(22)
		}
		os.Exit(0)
	}
	t.Setenv("AO_GIT_ENV_HELPER", "1")
	t.Setenv("ao_workflow_credential_test", "fake-platform")
	t.Setenv("AO_WORKFLOW_PROVIDER_KEY", "fake-forward")
	t.Setenv("OPENAI_API_KEY", "fake-native")
	_, err := runCommand(t.Context(), os.Args[0], "-test.run=^TestWorkspaceCommandDropsPlatformCredentials$")
	if err != nil {
		t.Fatalf("workspace child environment: %v", err)
	}
}
