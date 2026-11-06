package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cli"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/coordination"
	integrationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/integration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualityreview"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/taskcommit"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// m4Services 只保留真实服务用于故障注入；生产请求仍穿过 Router/Approval/Store。
type m4Services struct {
	artifacts    *artifact.Service
	gates        *qualitygate.Service
	commits      *taskcommit.Service
	integrations *integrationsvc.Service
}

// newM4Fixture 复用 M3 唯一 Scheduler 和真实 Manager，新增质量装配仍只使用临时数据。
func newM4Fixture(t *testing.T) *m2ffFixture {
	t.Helper()
	data := t.TempDir()
	st := sqlitetest.MustOpenAt(t, data)
	// 共享冻结时钟仍保留租约可控性；CLI 的真实 context deadline 也必须晚于本次离线测试。
	c := newM3Control(t, st, &m3Clock{now: time.Now().UTC().Add(time.Hour)}, "m4-http-boot")
	c.quality = &m4Services{}
	return newM2FFFixture(t, m2ffPlant{store: st, dataDir: data, managedDir: filepath.Join(data, "worktrees"), repoA: m2smGitRepo(t), repoB: m2smGitRepo(t), m3: c})
}

// wire 仅替代策略里的离线进程，审批仍共用 HTTP 装配的同一张表。
func (q *m4Services) wire(t *testing.T, c *m3Control, deps *httpd.APIDeps) {
	t.Helper()
	scratch := t.TempDir()
	p := m4OfflinePolicy(t)
	q.artifacts = &artifact.Service{Store: c.store, Scratch: filepath.Join(scratch, "artifacts"), Clock: c.clock.Now}
	q.gates = &qualitygate.Service{Store: c.store, Artifacts: q.artifacts, Registry: map[string]domain.GatePolicy{p.ID: p}, Scratch: filepath.Join(scratch, "gates"), Clock: c.clock.Now}
	q.commits = &taskcommit.Service{Store: c.store, Artifacts: q.artifacts, Gates: q.gates, Clock: c.clock.Now}
	q.integrations = &integrationsvc.Service{Store: c.store, Gates: q.gates, Scratch: filepath.Join(scratch, "integrations"), Clock: c.clock.Now, Approvals: deps.OperatorApprovals}
	deps.WorkflowQualityReader, deps.WorkflowArtifacts, deps.WorkflowGates = c.store, q.artifacts, q.gates
	deps.WorkflowReviews = &qualityreview.Service{Store: c.store, Artifacts: q.artifacts, Clock: c.clock.Now}
	deps.WorkflowCommits, deps.WorkflowIntegrations = q.commits, q.integrations
	deps.WorkflowCoordination = &coordination.Service{Store: c.store, Sessions: session.New(c.manager, c.store), Clock: c.clock.Now}
}

// m4OfflinePolicy 通过当前测试二进制读取冻结文件，零网络且保留真实退出码。
func m4OfflinePolicy(t *testing.T) domain.GatePolicy {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rule := domain.GateRule{ID: "G1", Level: 1, Required: true, Executable: exe, Args: []string{"-test.run=^TestM4QualityHelper$", "--", "m4-quality-helper"}, Timeout: 10 * time.Second, MaxLogBytes: 1024}
	if root, ok := os.LookupEnv("SystemRoot"); ok {
		rule.Env = map[string]string{"SystemRoot": root}
	}
	return domain.GatePolicy{ID: "offline-quality", Revision: 1, Rules: []domain.GateRule{rule}, ValidFor: time.Hour, MaxFixes: 2}
}

// m4HTTP 只负责正式审批和 DTO 解码，断言保留在各交付行为用例。
func m4HTTP[T any](t *testing.T, f *m2ffFixture, method, path string, body any) T {
	t.Helper()
	input := ""
	if body != nil {
		input = m3JSON(t, body)
	}
	r := f.asOperator(method, path, input)
	if r.Status != http.StatusOK {
		t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Raw)
	}
	var out T
	if err := json.Unmarshal([]byte(r.Raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// m4CLI 使用正式 Cobra、run-file 和 HTTP client；只将原生人工确认替换为夹具的 consent。
func m4CLI[T any](t *testing.T, f *m2ffFixture, action string, workflow domain.WorkflowID, target string, body any) T {
	t.Helper()
	mcpEnvVars(t, m2MCPEnv{})
	deps := mcpDeps(t, &m2Env{srv: f.srv})
	deps.Now = f.m3.clock.Now
	var stdout, stderr bytes.Buffer
	deps.Out, deps.Err = &stdout, &stderr
	args := []string{"workflow", "quality", action, string(workflow)}
	if target != "" {
		args = append(args, target)
	}
	if body != nil {
		path := filepath.Join(t.TempDir(), "payload.json")
		if err := os.WriteFile(path, []byte(m3JSON(t, body)), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--body-file", path)
	}
	cmd := cli.NewRootCommand(deps)
	cmd.SetArgs(args)
	ctx, cancel := context.WithTimeout(context.Background(), principal.DefaultApprovalTTL)
	defer cancel()
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("quality %s: %v stderr=%s", action, err, stderr.String())
	}
	raw := stdout.String()
	if start := strings.LastIndex(raw, "\n{"); start >= 0 {
		raw = raw[start+1:]
	}
	var out T
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		t.Fatalf("quality %s output: %v %s", action, err, raw)
	}
	return out
}
