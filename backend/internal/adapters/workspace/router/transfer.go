package router

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.WorkspaceTransfer = (*Workspace)(nil)

var errNoTransfer = errors.New("workspace router: Git adapter does not support session transfer")

func (w *Workspace) gitTransfer() (ports.WorkspaceTransfer, error) {
	t, ok := w.git.(ports.WorkspaceTransfer)
	if !ok {
		return nil, errNoTransfer
	}
	return t, nil
}

// PushTransfer delegates to the Git adapter.
func (w *Workspace) PushTransfer(ctx context.Context, info ports.WorkspaceInfo, preservedRef, remotePreservedRef string) (string, error) {
	t, err := w.gitTransfer()
	if err != nil {
		return "", err
	}
	return t.PushTransfer(ctx, info, preservedRef, remotePreservedRef)
}

// FetchTransfer delegates to the Git adapter.
func (w *Workspace) FetchTransfer(ctx context.Context, repoPath, branch, remotePreservedRef string) error {
	t, err := w.gitTransfer()
	if err != nil {
		return err
	}
	return t.FetchTransfer(ctx, repoPath, branch, remotePreservedRef)
}

// AlignTransferBranch delegates to the Git adapter.
func (w *Workspace) AlignTransferBranch(ctx context.Context, repoPath, branch, head string) error {
	t, err := w.gitTransfer()
	if err != nil {
		return err
	}
	return t.AlignTransferBranch(ctx, repoPath, branch, head)
}

// RevParse delegates to the Git adapter.
func (w *Workspace) RevParse(ctx context.Context, repoPath, ref string) (string, error) {
	t, err := w.gitTransfer()
	if err != nil {
		return "", err
	}
	return t.RevParse(ctx, repoPath, ref)
}

// ReleaseTransferBranch delegates to the Git adapter.
func (w *Workspace) ReleaseTransferBranch(ctx context.Context, info ports.WorkspaceInfo) error {
	t, err := w.gitTransfer()
	if err != nil {
		return err
	}
	return t.ReleaseTransferBranch(ctx, info)
}

// UnstageTransfer delegates to the Git adapter.
func (w *Workspace) UnstageTransfer(ctx context.Context, info ports.WorkspaceInfo) error {
	t, err := w.gitTransfer()
	if err != nil {
		return err
	}
	return t.UnstageTransfer(ctx, info)
}

// DeleteTransferRef delegates to the Git adapter.
func (w *Workspace) DeleteTransferRef(ctx context.Context, repoPath, remotePreservedRef string) error {
	t, err := w.gitTransfer()
	if err != nil {
		return err
	}
	return t.DeleteTransferRef(ctx, repoPath, remotePreservedRef)
}
