package codexappserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestLiveCodexNativeUserAgent 用真实 Codex 向本地假模型端点发请求，验证 AO 的完整启动路径，不消耗模型额度。
func TestLiveCodexNativeUserAgent(t *testing.T) {
	bin := os.Getenv("AO_CODEX_UA_BIN")
	if bin == "" {
		t.Skip("设置 AO_CODEX_UA_BIN 后执行仅访问本地端点的 Codex HTTP 验证")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workdir := t.TempDir()
	t.Setenv("AO_DATA_DIR", workdir)
	t.Setenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE", "")
	if err := os.Unsetenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE"); err != nil {
		t.Fatal(err)
	}
	userAgents := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/responses") {
			select {
			case userAgents <- r.UserAgent():
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"local UA probe complete","type":"invalid_request_error"}}`))
	}))
	defer server.Close()
	config := fmt.Sprintf(`model_provider = "ua_probe"
model = "gpt-5"
[model_providers.ua_probe]
name = "Local UA probe"
base_url = %q
env_key = "AO_UA_PROBE_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
request_max_retries = 0
stream_max_retries = 0
[analytics]
enabled = false
`, server.URL)
	if err := os.WriteFile(filepath.Join(workdir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := New(fakePlugin{bin: bin}, nil)
	driver.persistent = false
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		WorkspacePath: workdir,
		Env:           map[string]string{"CODEX_HOME": workdir, "AO_UA_PROBE_KEY": "local-test-key"},
		Model:         "gpt-5",
		Ephemeral:     true,
		DisableTools:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conversation.Close()
	if _, err := conversation.SendTurn(ctx, ports.ChatUserMessage{Text: "UA probe"}); err != nil {
		t.Fatal(err)
	}
	select {
	case userAgent := <-userAgents:
		t.Logf("HTTP User-Agent: %s", userAgent)
		if !strings.HasPrefix(userAgent, "codex_cli_rs/") || strings.Contains(strings.ToLower(userAgent), "agent-orchestrator") {
			t.Fatalf("Codex HTTP User-Agent 不符合预期: %q", userAgent)
		}
	case <-ctx.Done():
		t.Fatal("Codex 未向本地模型端点发出请求")
	}
}
