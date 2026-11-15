package domain

import "time"

const (
	ProjectKindSingleRepo ProjectKind = "single_repo"
	ProjectKindWorkspace  ProjectKind = "workspace"
	ProjectKindScratch    ProjectKind = "scratch"
	RootWorkspaceRepoName             = "__root__"
)

type ProjectKind string

func (k ProjectKind) WithDefault() ProjectKind {
	if k == "" {
		return ProjectKindSingleRepo
	}
	return k
}

type ProjectRecord struct {
	ID            string
	Path          string
	RepoOriginURL string
	DisplayName   string
	RegisteredAt  time.Time
	ArchivedAt    time.Time
	Kind          ProjectKind
	Config        ProjectConfig
}

type GitStatus string

const (
	GitStatusReady     GitStatus = "ready"
	GitStatusNeedsInit GitStatus = "needs_init"
)

func (g GitStatus) WithDefault() GitStatus {
	if g == "" {
		return GitStatusReady
	}
	return g
}

type WorkspaceRepoRecord struct {
	ProjectID     ProjectID
	Name          string
	RelativePath  string
	RepoOriginURL string
	DefaultBranch string
	RegisteredAt  time.Time
	GitStatus     GitStatus
}

type SessionWorktreeRecord struct {
	SessionID    SessionID
	RepoName     string
	Branch       string
	BaseSHA      string
	BaseRef      string
	CreationSHA  string
	WorktreePath string
	PreservedRef string
	State        string
}
