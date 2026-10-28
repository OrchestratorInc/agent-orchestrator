package httpapi

import (
	"net/http"
	"slices"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/projectsnapshot"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// defaultSnapshotHarness is the harness a project's snapshot is built for
// when its config names no agent.
const defaultSnapshotHarness = "claude-code"

// ensureProjectSnapshots starts building a new Freestyle project's repository
// snapshot for the harnesses its sessions run, as soon as the repository is
// connected. Its first session then boots with the repository already cloned
// instead of cloning it. The build clones with the project's own authority
// (no session exists yet) and failures only log: the first session still
// clones and starts the build itself.
func (s *Server) ensureProjectSnapshots(r *http.Request, orgID string, project domain.Project) {
	if s.projectSnapshots == nil {
		return
	}
	provider := domain.DecodeProjectSandboxProvider(project.Config)
	if provider == "" {
		provider = s.defaultProviderForOrg(r.Context(), principalFrom(r), orgID)
	}
	if provider != sandbox.ProviderFreestyle {
		return
	}
	var harnesses []string
	if settings, err := domain.DecodeProjectSettings(project.Config); err == nil {
		for _, role := range []*domain.ProjectRoleConfig{settings.Orchestrator, settings.Worker} {
			if role != nil && role.Agent != "" && !slices.Contains(harnesses, role.Agent) {
				harnesses = append(harnesses, role.Agent)
			}
		}
	}
	if len(harnesses) == 0 {
		harnesses = []string{defaultSnapshotHarness}
	}
	for _, harness := range harnesses {
		plan, err := s.provisioning.SessionPlanForProvider(harness, provider)
		if err != nil {
			continue
		}
		base := sandbox.FreestyleSnapshot(plan)
		if request, ok := projectsnapshot.NewRequest(orgID, project, harness, base); ok && base != "" {
			s.projectSnapshots.Ensure(request)
		}
	}
}
