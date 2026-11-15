package gitlab

import (
	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
)

var ErrNoToken = scmgitlab.ErrNoToken

func DefaultTokenSource() scmgitlab.TokenSource {
	return scmgitlab.FallbackTokenSource{
		&scmgitlab.EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}},
		&scmgitlab.GLabTokenSource{},
	}
}
