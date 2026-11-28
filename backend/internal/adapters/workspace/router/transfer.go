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

// CheckTransfer delegates to the Git adapter.
func (w *Workspace) CheckTransfer(ctx context.Context, info ports.WorkspaceInfo) error {
	t, err := w.gitTransfer()
	if err != nil {
		return err
	}
	return t.CheckTransfer(ctx, info)
}

// BundleTransfer delegates to the Git adapter.
func (w *Workspace) BundleTransfer(ctx context.Context, info ports.WorkspaceInfo, preservedRef, remotePreservedRef, bundlePath string) (string, bool, bool, error) {
	t, err := w.gitTransfer()
	if err != nil {
		return "", false, false, err
	}
	return t.BundleTransfer(ctx, info, preservedRef, remotePreservedRef, bundlePath)
}

// FetchTransfer delegates to the Git adapter.
func (w *Workspace) FetchTransfer(ctx context.Context, repoPath, branch, head, bundlePath, remotePreservedRef string) error {
	t, err := w.gitTransfer()
	if err != nil {
		return err
	}
	return t.FetchTransfer(ctx, repoPath, branch, head, bundlePath, remotePreservedRef)
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

// RestoreTransferIndex delegates to the Git adapter.
func (w *Workspace) RestoreTransferIndex(ctx context.Context, info ports.WorkspaceInfo, remotePreservedRef string) error {
	t, err := w.gitTransfer()
	if err != nil {
		return err
	}
	return t.RestoreTransferIndex(ctx, info, remotePreservedRef)
}
