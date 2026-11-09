package agy

import (
	"os"
	"strings"
	"testing"
)

// TestReviewerProbeDropsPlatformCredentials 删除执行边界过滤会让预检子进程收到其他平台密钥。
func TestReviewerProbeDropsPlatformCredentials(t *testing.T) {
	if os.Getenv("AO_REVIEW_ENV_HELPER") == "1" {
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
	t.Setenv("AO_WORKFLOW_CREDENTIAL_TEST", "fake-platform")
	t.Setenv("OPENAI_API_KEY", "fake-native")
	_, err := New().run(t.Context(), map[string]string{"AO_REVIEW_ENV_HELPER": "1", "ao_workflow_provider_key": "fake-overlay"}, os.Args[0], "-test.run=^TestReviewerProbeDropsPlatformCredentials$")
	if err != nil {
		t.Fatalf("reviewer child environment: %v", err)
	}
}
