package daemon

import (
	"log/slog"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/coordination"
	integrationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/integration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualityreview"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/taskcommit"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// wireQualityServices 在既有数据目录下装配质量侧车，复用唯一审批表与 Session 服务，不启动 Provider。
func wireQualityServices(deps *httpd.APIDeps, store *sqlite.Store, dataDir string, approvals *principal.ApprovalStore, log *slog.Logger) *artifact.Service {
	registry, err := qualitygate.LoadRegistry(filepath.Join(dataDir, "quality-policies.json"))
	if err != nil {
		log.Error("quality policy registry unavailable; gates remain blocked", "err", err)
		registry = map[string]domain.GatePolicy{}
	}
	scratch := filepath.Join(dataDir, "quality")
	a := &artifact.Service{Store: store, Scratch: filepath.Join(scratch, "artifacts"), Clock: time.Now}
	g := &qualitygate.Service{Store: store, Artifacts: a, Registry: registry, Scratch: filepath.Join(scratch, "gates"), Clock: time.Now}
	deps.WorkflowQualityReader, deps.WorkflowArtifacts, deps.WorkflowGates = store, a, g
	deps.WorkflowReviews = &qualityreview.Service{Store: store, Artifacts: a, Clock: time.Now}
	deps.WorkflowCommits = &taskcommit.Service{Store: store, Artifacts: a, Gates: g, Clock: time.Now}
	deps.WorkflowIntegrations = &integrationsvc.Service{Store: store, Gates: g, Scratch: filepath.Join(scratch, "integrations"), Clock: time.Now, Approvals: approvals}
	deps.WorkflowCoordination = &coordination.Service{Store: store, Sessions: deps.Sessions, Clock: time.Now}
	return a
}
