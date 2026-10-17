// Package taskworkspace exposes AO's Git worktree naming and recovery adapter
// to remote workers without giving them access to daemon storage or services.
package taskworkspace

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Options binds the existing Git workspace adapter to one session.
type Options struct {
	RepositoryPath string
	ManagedRoot    string
	ProjectID      string
	SessionID      string
	Logger         *slog.Logger
}

// Info identifies a registered task worktree and its branch.
type Info struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

// Workspace binds the existing adapter to one worker's repository and identity.
type Workspace struct {
	adapter *gitworktree.Workspace
	options Options
}

// New creates a session-bound Git workspace adapter.
func New(options Options) (*Workspace, error) {
	adapter, err := gitworktree.New(gitworktree.Options{
		ManagedRoot:  options.ManagedRoot,
		RepoResolver: gitworktree.StaticRepoResolver{domain.ProjectID(options.ProjectID): options.RepositoryPath},
		Logger:       options.Logger,
	})
	if err != nil {
		return nil, err
	}
	return &Workspace{adapter: adapter, options: options}, nil
}

// Create creates a fresh registered worktree on the requested base.
func (w *Workspace) Create(ctx context.Context, branch, base string) (Info, error) {
	root, err := w.adapter.Create(ctx, ports.WorkspaceConfig{
		ProjectID: domain.ProjectID(w.options.ProjectID), SessionID: domain.SessionID(w.options.SessionID),
		Kind: domain.KindWorker, Branch: branch, BaseBranch: base, FreshBranch: true,
	})
	return Info{Path: root.Path, Branch: root.Branch}, err
}

// Restore restores an existing worktree without discarding dirty files.
func (w *Workspace) Restore(ctx context.Context, info Info, base string) (Info, error) {
	root, err := w.adapter.Restore(ctx, ports.WorkspaceConfig{
		ProjectID: domain.ProjectID(w.options.ProjectID), SessionID: domain.SessionID(w.options.SessionID),
		Kind: domain.KindWorker, Path: info.Path, Branch: info.Branch, BaseBranch: base,
	})
	return Info{Path: root.Path, Branch: root.Branch}, err
}

// Name assigns the task branch and managed directory through the Git adapter.
func (w *Workspace) Name(ctx context.Context, info Info, branch, slug string) (Info, error) {
	result, err := w.adapter.NameTaskWorkspace(ctx, w.projectInfo(info), branch, slug)
	return Info{Path: result.Root.Path, Branch: result.Root.Branch}, err
}

// Recover resumes an interrupted task workspace move.
func (w *Workspace) Recover(ctx context.Context) (Info, bool, error) {
	result, found, err := w.adapter.RecoverTaskWorkspace(ctx, domain.ProjectID(w.options.ProjectID), domain.SessionID(w.options.SessionID))
	return Info{Path: result.Root.Path, Branch: result.Root.Branch}, found, err
}

// Complete acknowledges the committed name and clears the move journal.
func (w *Workspace) Complete(ctx context.Context, info Info) error {
	return w.adapter.CompleteTaskWorkspaceNaming(ctx, w.projectInfo(info))
}

func (w *Workspace) projectInfo(info Info) ports.WorkspaceProjectInfo {
	return ports.WorkspaceProjectInfo{Root: ports.WorkspaceInfo{
		Path: info.Path, Branch: info.Branch, RepoPath: w.options.RepositoryPath,
		ProjectID: domain.ProjectID(w.options.ProjectID), SessionID: domain.SessionID(w.options.SessionID),
	}}
}
