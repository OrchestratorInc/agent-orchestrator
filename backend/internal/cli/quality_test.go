package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestQualityApprovalFlow 执行正式命令，验证审批与提交绑定同一路径和原始正文，失败不会丢失请求身份。
func TestQualityApprovalFlow(t *testing.T) {
	cfg := setConfigEnv(t)
	for _, marker := range []string{"AO_SESSION_ID", "AO_ATTEMPT_ID", "AO_WORKFLOW_ID", "AO_WORKFLOW_LEASE"} {
		t.Setenv(marker, "")
	}
	payload := "{\n  \"idempotencyKey\": \"quality-1\"\n}\n"
	bodyFile := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(bodyFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	var method, path, body string
	var phase, requests atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		requests.Add(1)
		if r.Header.Get(principal.HeaderOperatorToken) != "" {
			t.Error("quality 请求携带了旧 Operator token")
		}
		digest := principal.PayloadDigest([]byte(body))
		approval := map[string]any{"approvalId": "quality-approval", "code": "ABCD1234", "state": "pending",
			"operation": map[string]string{"method": method, "path": path}, "payloadDigest": digest,
			"expiresAt": time.Now().UTC().Add(time.Minute).Format(time.RFC3339)}
		switch r.Method + " " + r.URL.RequestURI() {
		case "POST /api/v1/operator/approvals":
			var got map[string]string
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			if got["operationMethod"] != method || got["operationPath"] != path || got["payload"] != body || got["payloadDigest"] != digest || !phase.CompareAndSwap(0, 1) {
				t.Errorf("审批未绑定预期请求: %v, phase=%d", got, phase.Load())
			}
			writeTestJSON(w, http.StatusCreated, map[string]any{"approval": approval})
		case "POST /api/v1/operator/approvals/quality-approval/confirm":
			var got map[string]string
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			if got["code"] != "ABCD1234" || got["payloadDigest"] != digest || !phase.CompareAndSwap(1, 2) {
				t.Errorf("确认未绑定本次审批: %v, phase=%d", got, phase.Load())
			}
			approval["state"] = "confirmed"
			writeTestJSON(w, http.StatusOK, map[string]any{"approval": approval})
		case method + " " + path:
			raw, err := io.ReadAll(r.Body)
			if err != nil || string(raw) != body || r.Header.Get(principal.HeaderOperatorApproval) != "quality-approval" || !phase.CompareAndSwap(2, 3) {
				t.Errorf("未经确认或正文变化: body=%q, err=%v, phase=%d", raw, err, phase.Load())
			}
			if fail.Load() {
				writeTestJSON(w, http.StatusConflict, map[string]string{"error": "conflict", "code": "DELIVERY_BLOCKED", "message": "quality blocked", "requestId": "req-quality-1"})
				return
			}
			writeTestJSON(w, http.StatusOK, map[string]string{"result": "quality-ok"})
		default:
			t.Errorf("意外请求 %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	for _, tc := range []struct{ verb, method, suffix, target string }{
		{"summary", "GET", "quality-summary", ""},
		{"coordination", "GET", "coordination-summary", ""},
		{"policies", "GET", "quality-policies", ""},
		{"configure", "PUT", "quality-policy", ""},
		{"snapshot", "POST", "attempts/a1/artifacts/snapshot", "a1"},
		{"gate", "POST", "artifacts/a1/gates", "a1"},
		{"review", "POST", "artifacts/a1/reviews", "a1"},
		{"finish-review", "POST", "reviews/r1/finish", "r1"},
		{"commit", "POST", "artifacts/a1/commit", "a1"},
		{"preview", "POST", "integrations/preview", ""},
		{"plan", "GET", "integrations/p1", "p1"},
		{"apply", "POST", "integrations/p1/apply", "p1"},
		{"push", "POST", "integrations/p1/push", "p1"},
		{"operation", "GET", "integration-operations/o1", "o1"},
		{"reconcile", "POST", "integration-operations/o1/reconcile", "o1"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			method, path, body = tc.method, "/api/v1/workflows/w1/"+tc.suffix, ""
			args := []string{"workflow", "quality", tc.verb, "w1"}
			if tc.target != "" {
				args = append(args, tc.target)
			}
			if method != "GET" {
				body = payload
				args = append(args, "--body-file", bodyFile)
			}
			phase.Store(0)
			stdout, stderr, err := executeCLI(t, Deps{}, args...)
			if err != nil || phase.Load() != 3 || !strings.Contains(stdout, `"result":"quality-ok"`) {
				t.Fatalf("流程失败: err=%v, phase=%d, stdout=%s, stderr=%s", err, phase.Load(), stdout, stderr)
			}
		})
	}
	method, path, body = "POST", "/api/v1/workflows/w1/artifacts/a1/gates", payload
	phase.Store(0)
	fail.Store(true)
	_, _, err := executeCLI(t, Deps{}, "workflow", "quality", "gate", "w1", "a1", "--body-file", bodyFile)
	var apiErr apiResponseError
	if ExitCode(err) != 1 || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || apiErr.ErrorBody.Code != "DELIVERY_BLOCKED" || apiErr.ErrorBody.RequestID != "req-quality-1" || !strings.Contains(err.Error(), "[request req-quality-1]") {
		t.Fatalf("未保留 daemon 错误及请求 ID: %v", err)
	}
	before := requests.Load()
	for _, args := range [][]string{
		{"gate", "w1", "a1"},
		{"summary", "../w1"},
	} {
		_, _, err := executeCLI(t, Deps{}, append([]string{"workflow", "quality"}, args...)...)
		if ExitCode(err) != 2 {
			t.Fatalf("非法参数未返回 usage error: %v", err)
		}
	}
	t.Setenv("AO_SESSION_ID", "agent-session")
	_, _, err = executeCLI(t, Deps{}, "workflow", "quality", "gate", "w1", "a1", "--body-file", bodyFile)
	if ExitCode(err) != exitAgentSession || requests.Load() != before {
		t.Fatalf("拒绝前发生 API 调用或退出码错误: err=%v, requests=%d→%d", err, before, requests.Load())
	}
}
