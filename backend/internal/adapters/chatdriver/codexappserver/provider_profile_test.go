package codexappserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/providerprofile"
)

// driverProfile 使用真实登记表与本地鉴权服务，测试不访问真实账号或供应商。
func driverProfile(t *testing.T, effort string) (*providerprofile.Registry, string) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer driver-test-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), providerprofile.Filename)
	raw, err := json.Marshal([]ports.ProviderProfile{{ID: "test", Name: "本地测试", Harness: domain.HarnessCodex, BaseURL: server.URL, Model: "gpt-test", Effort: effort, ContextWindow: 64000, CredentialEnv: "AO_WORKFLOW_CREDENTIAL_TEST"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	r := &providerprofile.Registry{Path: path, Transport: server.Client().Transport, LookupEnv: func(string) (string, bool) { return "driver-test-secret", true }}
	choices, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r, choices[0].Reference
}

// TestProfileStartResumePreserveRoutingAndSandbox 删除协议映射或恢复透传会改用默认路由，删除权限隔离会解除 Planner 只读。
func TestProfileStartResumePreserveRoutingAndSandbox(t *testing.T) {
	for _, mode := range []string{"start", "resume", "missing"} {
		t.Run(mode, func(t *testing.T) {
			registry, ref := driverProfile(t, "high")
			d, srv := newTestDriver(t)
			d.plugin = fakePlugin{bin: "codex", authStatus: ports.AgentAuthStatusUnauthorized}
			d.SetProviderProfiles(registry)
			srv.respondTo("thread/start", `{"thread":{"id":"thread-1"},"model":"gpt-test","reasoningEffort":"high"}`)
			srv.respondTo("thread/resume", `{"model":"gpt-test","reasoningEffort":"high"}`)
			var processEnv []string
			spawn := d.spawn
			d.spawn = func(ctx context.Context, bin, dir string, env []string) (*process, error) {
				processEnv = append([]string(nil), env...)
				return spawn(ctx, bin, dir, env)
			}
			base := map[string]string{"PRESERVED_ENV": "present"}
			var conv ports.ChatConversation
			var err error
			method := "thread/start"
			if mode == "start" {
				conv, err = d.Start(context.Background(), ports.ChatStartConfig{SessionID: "profile-test", WorkspacePath: t.TempDir(), Profile: ref, Model: "gpt-test", Effort: "high", ReadOnly: true, Permissions: ports.PermissionModeAuto, Env: base})
			} else {
				method = "thread/resume"
				if mode == "missing" {
					srv.replyError("thread/resume", -32600, "thread not found")
					method = "thread/start"
				}
				conv, err = d.Resume(context.Background(), ports.ChatResumeConfig{SessionID: "profile-test", WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", Profile: ref, Model: "gpt-test", Effort: "high", ReadOnly: true, Permissions: ports.PermissionModeAuto, Env: base, FreshIfMissing: mode == "missing"})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conv.Close()
			frame := srv.awaitFrame(func(f frame) bool { return f.Method == method })
			var params map[string]any
			if err := json.Unmarshal(frame.Params, &params); err != nil {
				t.Fatal(err)
			}
			if params["sandbox"] != "read-only" || params["approvalPolicy"] != "never" {
				t.Fatalf("Planner policy changed: %v %v", params["sandbox"], params["approvalPolicy"])
			}
			config := params["config"].(map[string]any)
			provider := params["modelProvider"].(string)
			route := config["model_providers"].(map[string]any)[provider].(map[string]any)
			if config["model_provider"] != provider || config["model_reasoning_effort"] != "high" || config["model_context_window"] != float64(64000) || route["wire_api"] != "responses" || route["env_key"] != ports.ProviderProfileKeyEnv || route["requires_openai_auth"] != false {
				t.Fatal("provider routing was not preserved")
			}
			p, err := registry.Resolve(context.Background(), ref, domain.HarnessCodex, "gpt-test", "high")
			if err != nil || route["base_url"] != p.BaseURL {
				t.Fatal("driver selected another provider")
			}
			secretCount := 0
			for _, entry := range processEnv {
				if entry == ports.ProviderProfileKeyEnv+"=driver-test-secret" {
					secretCount++
				}
			}
			if secretCount != 1 || base[ports.ProviderProfileKeyEnv] != "" || strings.Contains(string(frame.Params), "driver-test-secret") {
				t.Fatal("secret escaped launch environment or was missing")
			}
			if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "change model", Settings: ports.ChatTurnSettings{Model: "other"}}); err == nil {
				t.Fatal("fixed model changed at turn time")
			}
		})
	}
}

// TestProfileRejectsUnconfirmedModelAndDrift 删除响应一致性或最后解析会把静默降级当作成功。
func TestProfileRejectsUnconfirmedModelAndDrift(t *testing.T) {
	registry, ref := driverProfile(t, "high")
	d, _ := newTestDriver(t)
	d.SetProviderProfiles(registry)
	if _, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir(), Profile: ref, Model: "gpt-test", Effort: "high"}); err == nil {
		t.Fatal("provider omitted requested effort but launch succeeded")
	}
	registry.LookupEnv = func(string) (string, bool) { return "", false }
	d, srv := newTestDriver(t)
	d.SetProviderProfiles(registry)
	if _, err := d.Resume(context.Background(), ports.ChatResumeConfig{WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", Profile: ref, Model: "gpt-test", Effort: "high"}); err == nil {
		t.Fatal("missing credential resumed native account")
	}
	if srv.sentMethod("initialize") || srv.sentMethod("thread/resume") {
		t.Fatal("invalid profile spawned a provider")
	}
}

// TestProfileDefaultEffortAcceptsProviderChoice 删除空 effort 的默认策略分支，会在三种真实 Driver 入口误拒合法响应。
func TestProfileDefaultEffortAcceptsProviderChoice(t *testing.T) {
	for _, mode := range []string{"start", "resume", "missing"} {
		t.Run(mode, func(t *testing.T) {
			registry, ref := driverProfile(t, "")
			d, srv := newTestDriver(t)
			d.SetProviderProfiles(registry)
			srv.reply("thread/start", `{"thread":{"id":"thread-1"},"model":"gpt-test","reasoningEffort":"medium"}`)
			srv.reply("thread/resume", `{"model":"gpt-test","reasoningEffort":"high"}`)
			var conv ports.ChatConversation
			var err error
			method := "thread/start"
			if mode == "start" {
				conv, err = d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir(), Profile: ref, Model: "gpt-test"})
			} else {
				method = "thread/resume"
				if mode == "missing" {
					srv.replyError(method, -32600, "thread not found")
					method = "thread/start"
				}
				conv, err = d.Resume(t.Context(), ports.ChatResumeConfig{WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", Profile: ref, Model: "gpt-test", FreshIfMissing: mode == "missing"})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conv.Close()
			request := srv.awaitFrame(func(f frame) bool { return f.Method == method })
			var params struct {
				Config map[string]any `json:"config"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			if _, exists := params.Config["model_reasoning_effort"]; exists {
				t.Fatal("default policy became an explicit override")
			}
			if _, err := conv.SendTurn(t.Context(), ports.ChatUserMessage{Text: "change effort", Settings: ports.ChatTurnSettings{Effort: "low"}}); err == nil {
				t.Fatal("default profile allowed a different effort")
			}
			if _, err := conv.SendTurn(t.Context(), ports.ChatUserMessage{Text: "keep default"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
