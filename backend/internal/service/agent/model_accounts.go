package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountModelEnvironment interface {
	CatalogLaunchEnv(context.Context, domain.AgentHarness, domain.SessionID, string) (string, map[string]string, error)
}

// SetAccountModelEnvironment aligns discovery with the account used to launch.
func (s *Service) SetAccountModelEnvironment(accounts accountModelEnvironment) {
	s.accountModels = accounts
}

// Account scopes contain identifiers only; credentials and config paths remain
// server-side. Separate keys prevent stale capabilities leaking across accounts.
type accountModelScope struct {
	ProjectID string
	AccountID string
}

const accountModelPrefix = "@account:"

func decodeAccountModelScope(key string) (accountModelScope, bool) {
	encoded, ok := strings.CutPrefix(key, accountModelPrefix)
	if !ok {
		return accountModelScope{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	var scope accountModelScope
	if err != nil || json.Unmarshal(raw, &scope) != nil || scope.AccountID == "" {
		return accountModelScope{}, false
	}
	return scope, true
}

func (s *Service) accountModelScope(ctx context.Context, agentID, projectID string, sessionID domain.SessionID, accountID string) (string, error) {
	if s.accountModels == nil {
		return projectID, nil
	}
	if _, ok := decodeAccountModelScope(projectID); ok {
		return projectID, nil
	}
	if _, ok := credentialTypeFromScope(projectID); ok {
		return projectID, nil
	}
	id, _, err := s.accountModels.CatalogLaunchEnv(ctx, domain.AgentHarness(agentID), sessionID, accountID)
	if err != nil {
		return "", err
	}
	if id == "" {
		return projectID, nil
	}
	projectID, err = s.modelCatalogScope(ctx, projectID)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(accountModelScope{ProjectID: projectID, AccountID: id})
	return accountModelPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// ModelsForSession uses the session's bound account, or the proposed target
// account during a switch, without changing any account bindings.
func (s *Service) ModelsForSession(ctx context.Context, agentID, projectID string, sessionID domain.SessionID, accountID string, refresh bool) (ports.AgentModelCatalog, error) {
	scope, err := s.accountModelScope(ctx, agentID, projectID, sessionID, accountID)
	if err != nil {
		return ports.AgentModelCatalog{}, err
	}
	catalog, err := s.modelCatalog(ctx, agentID, scope, refresh)
	if err != nil {
		return catalog, err
	}
	return s.withModelUsage(ctx, agentID, projectID, catalog), nil
}
