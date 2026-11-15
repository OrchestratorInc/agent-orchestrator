package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type ProjectStore interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

type GitWorkspace interface {
	ports.Workspace
	ports.WorkspaceDefaultBranchRefresher
}

type Deps struct {
	Git      GitWorkspace
	Scratch  ports.Workspace
	Projects ProjectStore
}

type Workspace struct {
	git      GitWorkspace
	scratch  ports.Workspace
	projects ProjectStore
}

var _ ports.Workspace = (*Workspace)(nil)
var _ ports.WorkspaceDefaultBranchRefresher = (*Workspace)(nil)
var _ ports.WorkspaceProject = (*Workspace)(nil)
var _ ports.WorkspaceObserver = (*Workspace)(nil)
var _ ports.WorkspaceReclaimer = (*Workspace)(nil)
var _ ports.WorkspacePreparationBranchCleaner = (*Workspace)(nil)

func New(deps Deps) *Workspace {
	return &Workspace{
		git:      deps.Git,
		scratch:  deps.Scratch,
		projects: deps.Projects,
	}
}

func (w *Workspace) ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error) {
	return w.git.ResolveDefaultBranch(ctx, repoPath, configuredBranch)
}

func (w *Workspace) FetchDefaultBranch(ctx context.Context, repoPath string, target ports.WorkspaceDefaultBranch) error {
	return w.git.FetchDefaultBranch(ctx, repoPath, target)
}

func (w *Workspace) RenameTaskBranch(ctx context.Context, info ports.WorkspaceProjectInfo, branch string) (ports.WorkspaceProjectInfo, error) {
	namer, ok := w.git.(ports.WorkspaceTaskNamer)
	if !ok {
		return info, errors.New("workspace router: Git adapter does not support task naming")
	}
	return namer.RenameTaskBranch(ctx, info, branch)
}

func (w *Workspace) RevertTaskBranch(ctx context.Context, renamed, original ports.WorkspaceProjectInfo) (ports.WorkspaceProjectInfo, error) {
	namer, ok := w.git.(ports.WorkspaceTaskNamer)
	if !ok {
		return renamed, errors.New("workspace router: Git adapter does not support task naming")
	}
	return namer.RevertTaskBranch(ctx, renamed, original)
}

func (w *Workspace) Create(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	adapter, err := w.adapterForProject(ctx, cfg.ProjectID)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	return adapter.Create(ctx, cfg)
}

func (w *Workspace) Restore(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	adapter, err := w.adapterForProject(ctx, cfg.ProjectID)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	return adapter.Restore(ctx, cfg)
}

func (w *Workspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	adapter, err := w.adapterForProject(ctx, info.ProjectID)
	if err != nil {
		return err
	}
	return adapter.Destroy(ctx, info)
}

func (w *Workspace) DestroyReclaim(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	adapter, err := w.adapterForProject(ctx, info.ProjectID)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, err
	}
	if reclaimer, ok := adapter.(ports.WorkspaceReclaimer); ok {
		return reclaimer.DestroyReclaim(ctx, info)
	}
	return ports.WorkspaceReclaimRemoved, adapter.Destroy(ctx, info)
}

func (w *Workspace) DeletePreparedBranch(ctx context.Context, info ports.WorkspaceInfo) error {
	adapter, err := w.adapterForProject(ctx, info.ProjectID)
	if err != nil {
		return err
	}
	if cleaner, ok := adapter.(ports.WorkspacePreparationBranchCleaner); ok {
		return cleaner.DeletePreparedBranch(ctx, info)
	}
	return nil
}

func (w *Workspace) ForceDestroy(ctx context.Context, info ports.WorkspaceInfo) error {
	adapter, err := w.adapterForProject(ctx, info.ProjectID)
	if err != nil {
		return err
	}
	return adapter.ForceDestroy(ctx, info)
}

func (w *Workspace) StashUncommitted(ctx context.Context, info ports.WorkspaceInfo) (string, error) {
	adapter, err := w.adapterForProject(ctx, info.ProjectID)
	if err != nil {
		return "", err
	}
	return adapter.StashUncommitted(ctx, info)
}

func (w *Workspace) ApplyPreserved(ctx context.Context, info ports.WorkspaceInfo, ref string) error {
	adapter, err := w.adapterForProject(ctx, info.ProjectID)
	if err != nil {
		return err
	}
	return adapter.ApplyPreserved(ctx, info, ref)
}

func (w *Workspace) AddExclude(ctx context.Context, info ports.WorkspaceInfo, patterns ...string) error {
	adapter, err := w.adapterForProject(ctx, info.ProjectID)
	if err != nil {
		return err
	}
	return adapter.AddExclude(ctx, info, patterns...)
}

func (w *Workspace) ObserveWorkspace(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceObservation, error) {
	adapter, err := w.adapterForProject(ctx, info.ProjectID)
	if err != nil {
		return ports.WorkspaceObservation{}, err
	}
	observer, ok := adapter.(ports.WorkspaceObserver)
	if !ok {
		return ports.WorkspaceObservation{}, errors.New("workspace router: selected workspace does not support observation")
	}
	return observer.ObserveWorkspace(ctx, info)
}

func (w *Workspace) CreateWorkspaceProject(ctx context.Context, cfg ports.WorkspaceProjectConfig) (ports.WorkspaceProjectInfo, error) {
	gitProject, err := w.gitWorkspaceProject()
	if err != nil {
		return ports.WorkspaceProjectInfo{}, err
	}
	return gitProject.CreateWorkspaceProject(ctx, cfg)
}

func (w *Workspace) DestroyWorkspaceProject(ctx context.Context, info ports.WorkspaceProjectInfo) error {
	gitProject, err := w.gitWorkspaceProject()
	if err != nil {
		return err
	}
	return gitProject.DestroyWorkspaceProject(ctx, info)
}

func (w *Workspace) adapterForProject(ctx context.Context, projectID domain.ProjectID) (ports.Workspace, error) {
	if w == nil {
		return nil, errors.New("workspace router: nil router")
	}
	if projectID == "" {
		if w.scratch == nil {
			return nil, errors.New("workspace router: standalone workspace is not configured")
		}
		return w.scratch, nil
	}
	if w.projects != nil && projectID != "" {
		project, ok, err := w.projects.GetProject(ctx, string(projectID))
		if err != nil {
			return nil, fmt.Errorf("workspace router: project %q: %w", projectID, err)
		}
		if ok && project.Kind.WithDefault() == domain.ProjectKindScratch {
			if w.scratch == nil {
				return nil, errors.New("workspace router: scratch workspace is not configured")
			}
			return w.scratch, nil
		}
	}
	if w.git == nil {
		return nil, errors.New("workspace router: git workspace is not configured")
	}
	return w.git, nil
}

func (w *Workspace) gitWorkspaceProject() (ports.WorkspaceProject, error) {
	if w == nil || w.git == nil {
		return nil, errors.New("workspace router: git workspace is not configured")
	}
	gitProject, ok := w.git.(ports.WorkspaceProject)
	if !ok {
		return nil, errors.New("workspace router: git workspace does not support workspace projects")
	}
	return gitProject, nil
}
