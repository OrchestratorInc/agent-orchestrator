package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

type Summary struct {
	ID                domain.ProjectID        `json:"id"`
	Name              string                  `json:"name"`
	Path              string                  `json:"path"`
	Kind              domain.ProjectKind      `json:"kind" enum:"single_repo,workspace,scratch"`
	SessionPrefix     string                  `json:"sessionPrefix"`
	OrchestratorAgent domain.AgentHarness     `json:"orchestratorAgent,omitempty"`
	ResolveError      string                  `json:"resolveError,omitempty"`
	FolderMissing     bool                    `json:"folderMissing"`
	AutoSpawnPauses   []domain.AutoSpawnPause `json:"autoSpawnPauses,omitempty"`
}

type Project struct {
	ID             domain.ProjectID      `json:"id"`
	Name           string                `json:"name"`
	Kind           domain.ProjectKind    `json:"kind" enum:"single_repo,workspace,scratch"`
	Path           string                `json:"path"`
	Repo           string                `json:"repo"`
	DefaultBranch  string                `json:"defaultBranch"`
	Agent          string                `json:"agent,omitempty"`
	Config         *domain.ProjectConfig `json:"config,omitempty"`
	WorkspaceRepos []WorkspaceRepo       `json:"workspaceRepos,omitempty"`
	FolderMissing  bool                  `json:"folderMissing"`

	CanonicalRepo           string `json:"canonicalRepo,omitempty"`
	CanonicalOwnerAvatarURL string `json:"canonicalOwnerAvatarUrl,omitempty"`
}

type Degraded struct {
	ID           domain.ProjectID   `json:"id"`
	Name         string             `json:"name"`
	Kind         domain.ProjectKind `json:"kind" enum:"single_repo,workspace,scratch"`
	Path         string             `json:"path"`
	ResolveError string             `json:"resolveError"`
}

type WorkspaceRepo struct {
	Name         string `json:"name"`
	RelativePath string `json:"relativePath"`
	Repo         string `json:"repo"`
	GitStatus    string `json:"gitStatus,omitempty"`
}
