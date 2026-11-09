package controllers

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// providerProfiles 只读宿主目录，不接受路由、路径、环境变量名或任何写配置请求。
func (c *WorkflowsController) providerProfiles(w http.ResponseWriter, r *http.Request) {
	s, ok := c.Svc.(interface {
		ListProviderProfiles(context.Context) ([]ports.ProviderProfileChoice, error)
	})
	if !ok {
		envelope.WriteError(w, r, apierr.NotImplemented("PROVIDER_PROFILES_UNAVAILABLE", "供应商目录未装配"))
		return
	}
	profiles, err := s.ListProviderProfiles(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowProviderProfilesResponse{Profiles: profiles})
}
