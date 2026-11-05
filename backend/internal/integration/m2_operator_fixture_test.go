package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/cli"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// m2Consent 仅替代 OS 人工验证边界；路由、正文绑定和一次性消费均使用生产实现。
type m2Consent struct {
	mu   sync.Mutex
	err  error
	seen []principal.Approval
}

// Verify 记录原生窗口将展示的操作，并按测试指定结果确认或拒绝。
func (c *m2Consent) Verify(_ context.Context, approval principal.Approval) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, approval)
	return c.err
}

// deny 切换下一次 OS 验证结果，不直接改变审批状态。
func (c *m2Consent) deny(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// approvals 返回验证边界收到的独立快照，避免 HTTP goroutine 访问竞争。
func (c *m2Consent) approvals() []principal.Approval {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]principal.Approval(nil), c.seen...)
}

// m2ApprovedHeaders 供旧 HTTP 场景申请单次权限，不作为正式 CLI 验收证据。
func m2ApprovedHeaders(t *testing.T, base, method, path, body string) map[string]string {
	t.Helper()
	if !principal.IsOperatorOperation(method, "/api/v1"+path) {
		return nil
	}
	e := &m2Env{baseURL: base}
	request, err := json.Marshal(map[string]string{
		"operationMethod": method, "operationPath": "/api/v1" + path,
		"payloadDigest": principal.PayloadDigest([]byte(body)), "payload": body,
	})
	if err != nil {
		t.Fatal(err)
	}
	created := e.doWithHeaders(t, http.MethodPost, "/operator/approvals", string(request), nil)
	if created.Status != http.StatusCreated {
		t.Fatalf("申请审批: %d %s", created.Status, created.Raw)
	}
	id := str(dig(created.Body, "approval", "approvalId"))
	confirm := fmt.Sprintf(`{"code":%q,"payloadDigest":%q}`,
		str(dig(created.Body, "approval", "code")), principal.PayloadDigest([]byte(body)))
	confirmed := e.doWithHeaders(t, http.MethodPost, "/operator/approvals/"+id+"/confirm", confirm, nil)
	if confirmed.Status != http.StatusOK {
		t.Fatalf("原生审批: %d %s", confirmed.Status, confirmed.Raw)
	}
	return map[string]string{principal.HeaderOperatorApproval: id}
}

// m2RunOperator 执行生产 Cobra 入口与真实 HTTP 客户端，不改 transport、不注入凭据。
func m2RunOperator(t *testing.T, f *m2ffFixture, args ...string) (map[string]any, error) {
	t.Helper()
	deps := mcpDeps(t, &m2Env{srv: f.srv})
	var out, stderr bytes.Buffer
	deps.Out, deps.Err = &out, &stderr
	cmd := cli.NewRootCommand(deps)
	cmd.SetArgs(append(append([]string{"workflow", "operator"}, args...), "--json"))
	ctx, cancel := context.WithTimeout(context.Background(), principal.DefaultApprovalTTL)
	defer cancel()
	if err := cmd.ExecuteContext(ctx); err != nil {
		return nil, fmt.Errorf("%w; stderr=%s", err, stderr.String())
	}
	// 命令先打印人工审批摘要，最后打印独立 JSON 结果。
	raw := out.String()
	start := strings.LastIndex(raw, "\n{")
	if start >= 0 {
		raw = raw[start+1:]
	}
	var result map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("解析 CLI 输出: %v: %s", err, out.String())
	}
	return result, nil
}

// m2MustOperator 让失败保留生产客户端返回的错误封套。
func m2MustOperator(t *testing.T, f *m2ffFixture, args ...string) map[string]any {
	t.Helper()
	result, err := m2RunOperator(t, f, args...)
	if err != nil {
		t.Fatalf("ao workflow operator %v: %v", args, err)
	}
	return result
}
