package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// workspaceAssetSizeLimit bounds the bytes one copied workspace folder may
// hold. A larger folder fails the start instead of filling the disk once per
// session. Tests lower it.
var workspaceAssetSizeLimit int64 = 1 << 30

// skippedAssetLogLimit caps how many skipped paths one log line names.
const skippedAssetLogLimit = 10

var errWorkspaceAssetTooLarge = errors.New("workspace folder exceeds the copy size limit")

type assetEntryKind int

const (
	assetDir assetEntryKind = iota
	assetFile
	assetSymlink
)

type assetEntry struct {
	kind   assetEntryKind
	rel    string
	source string
	mode   os.FileMode
	target string
}

// assetPlan is one folder's copy, walked and size-checked before anything is
// written.
type assetPlan struct {
	sourceRoot string
	limit      int64
	size       int64
	entries    []assetEntry
	skipped    []string
}

// copyWorkspaceAssets copies each plain workspace folder into the session's
// root worktree. Paths that cannot be copied safely (absolute or escaping
// symlinks, sockets, FIFOs, devices) and nested dependency folders are skipped
// and logged rather than failing the start.
func (w *Workspace) copyWorkspaceAssets(ctx context.Context, sourceRoot, destinationRoot string, assets []ports.WorkspaceProjectAssetConfig) error {
	var copied []string
	for _, asset := range assets {
		rel, err := cleanRelativePath(asset.RelativePath)
		if err != nil || hasWorkspaceManagedComponent(rel) {
			return fmt.Errorf("gitworktree: workspace asset %q: %w", asset.RelativePath, ErrUnsafePath)
		}
		name := filepath.Base(filepath.FromSlash(rel))
		if strings.HasPrefix(name, ".") || domain.IsWorkspaceDependencyDir(name) {
			w.assetLogger().InfoContext(ctx, "gitworktree: not copying dependency or dot-folder into session", "folder", rel)
			continue
		}
		if _, err := os.Lstat(asset.SourcePath); errors.Is(err, os.ErrNotExist) {
			w.assetLogger().WarnContext(ctx, "gitworktree: workspace folder to copy no longer exists", "folder", rel, "source", asset.SourcePath)
			continue
		}
		source, err := physicalAbs(asset.SourcePath)
		if err != nil || !assetPathWithin(sourceRoot, source) {
			return fmt.Errorf("gitworktree: workspace asset %q source: %w", asset.RelativePath, ErrUnsafePath)
		}
		destination := filepath.Join(destinationRoot, filepath.FromSlash(rel))
		if !assetPathWithin(destinationRoot, destination) {
			return fmt.Errorf("gitworktree: workspace asset %q destination: %w", asset.RelativePath, ErrUnsafePath)
		}
		plan := &assetPlan{sourceRoot: sourceRoot, limit: workspaceAssetSizeLimit}
		if err := plan.walk(ctx, source, ""); err != nil {
			if errors.Is(err, errWorkspaceAssetTooLarge) {
				return fmt.Errorf("gitworktree: workspace folder %q is larger than %d MiB, too large to copy into every session; move it out of the workspace folder or into a child repository: %w",
					rel, plan.limit>>20, err)
			}
			return fmt.Errorf("gitworktree: copy workspace asset %q: %w", asset.RelativePath, err)
		}
		if len(plan.skipped) > 0 {
			shown := plan.skipped
			if len(shown) > skippedAssetLogLimit {
				shown = shown[:skippedAssetLogLimit]
			}
			w.assetLogger().WarnContext(ctx, "gitworktree: skipped paths while copying workspace folder",
				"folder", rel, "skipped", len(plan.skipped), "paths", strings.Join(shown, "; "))
		}
		if err := plan.apply(ctx, destination); err != nil {
			return fmt.Errorf("gitworktree: copy workspace asset %q: %w", asset.RelativePath, err)
		}
		copied = append(copied, rel)
	}
	if err := w.excludeCopiedAssets(ctx, destinationRoot, copied); err != nil {
		// Without the exclude the session only reports the copies as untracked.
		w.assetLogger().WarnContext(ctx, "gitworktree: could not exclude copied workspace folders from git status", "error", err)
	}
	return nil
}

// assetExcludesFile names the per-worktree excludes file, inside the root
// worktree's own git directory so it goes away with the worktree.
const assetExcludesFile = "ao-asset-excludes"

// excludeCopiedAssets keeps copied folders out of the root worktree's status,
// so they never make a session look dirty or get stashed into preserve refs,
// as a root Orchestrator created already does through its .gitignore. The
// patterns go to a per-worktree core.excludesFile, never the repository's
// shared info/exclude, which also applies to the user's own checkout. A
// per-worktree core.excludesFile replaces the user's global one, so the file
// starts with that file's patterns.
func (w *Workspace) excludeCopiedAssets(ctx context.Context, rootPath string, rels []string) error {
	if len(rels) == 0 {
		return nil
	}
	out, err := w.run(ctx, w.binary, "-C", rootPath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fmt.Errorf("resolve worktree git dir: %w", err)
	}
	var content strings.Builder
	if inherited := w.effectiveExcludesFile(ctx, rootPath); inherited != "" {
		if data, err := os.ReadFile(inherited); err == nil && len(data) > 0 {
			content.Write(data)
			if data[len(data)-1] != '\n' {
				content.WriteByte('\n')
			}
		}
	}
	content.WriteString("# Workspace folders Orchestrator copied into this session\n")
	for _, rel := range rels {
		content.WriteString("/" + filepath.ToSlash(rel) + "/\n")
	}
	path := filepath.Join(strings.TrimSpace(string(out)), assetExcludesFile)
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		return err
	}
	if _, err := w.run(ctx, w.binary, "-C", rootPath, "config", "--worktree", "core.excludesFile", path); err != nil {
		return fmt.Errorf("set worktree excludes file: %w", err)
	}
	return nil
}

// effectiveExcludesFile returns the excludes file git would read for the
// worktree: core.excludesFile when set, else git's XDG default.
func (w *Workspace) effectiveExcludesFile(ctx context.Context, rootPath string) string {
	if out, err := w.run(ctx, w.binary, "-C", rootPath, "config", "--type=path", "--get", "core.excludesFile"); err == nil {
		if path := strings.TrimSpace(string(out)); path != "" {
			return path
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "git", "ignore")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "git", "ignore")
}

func (w *Workspace) assetLogger() *slog.Logger {
	if w.logger != nil {
		return w.logger
	}
	return slog.Default()
}

// walk records what copying source (at rel inside the folder) writes,
// skipping what cannot be copied safely, and fails once the folder's regular
// files exceed the size limit.
func (p *assetPlan) walk(ctx context.Context, source, rel string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	display := rel
	if display == "" {
		display = "."
	}
	switch mode := info.Mode(); {
	case mode&os.ModeSymlink != 0:
		target, reason := p.symlinkTarget(source)
		if reason != "" {
			p.skipped = append(p.skipped, display+": "+reason)
			return nil
		}
		p.entries = append(p.entries, assetEntry{kind: assetSymlink, rel: rel, source: source, target: target})
	case mode.IsDir():
		if rel != "" && domain.IsWorkspaceDependencyDir(info.Name()) {
			p.skipped = append(p.skipped, display+": dependency or build folder")
			return nil
		}
		p.entries = append(p.entries, assetEntry{kind: assetDir, rel: rel, source: source, mode: mode.Perm()})
		children, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, child := range children {
			if child.Name() == ".git" || child.Name() == ".ao" {
				continue
			}
			if err := p.walk(ctx, filepath.Join(source, child.Name()), filepath.Join(rel, child.Name())); err != nil {
				return err
			}
		}
	case mode.IsRegular():
		p.size += info.Size()
		if p.size > p.limit {
			return errWorkspaceAssetTooLarge
		}
		p.entries = append(p.entries, assetEntry{kind: assetFile, rel: rel, source: source, mode: mode.Perm()})
	default:
		p.skipped = append(p.skipped, display+": special file")
	}
	return nil
}

// symlinkTarget returns the link text to recreate, or why the link is not
// copied: it must be relative and resolve inside the workspace root, outside
// Git and Orchestrator metadata.
func (p *assetPlan) symlinkTarget(source string) (string, string) {
	target, err := os.Readlink(source)
	if err != nil {
		return "", "unreadable symlink"
	}
	if filepath.IsAbs(target) {
		return "", "absolute symlink"
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(source), target))
	if !assetPathWithin(p.sourceRoot, resolved) || hasWorkspaceManagedComponent(relativeOrEmpty(p.sourceRoot, resolved)) {
		return "", "symlink outside the workspace"
	}
	physical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", "broken symlink"
	}
	if !assetPathWithin(p.sourceRoot, physical) {
		return "", "symlink outside the workspace"
	}
	return target, ""
}

// apply writes the planned entries under destination, overlaying whatever the
// root worktree already checked out there.
func (p *assetPlan) apply(ctx context.Context, destination string) error {
	for _, entry := range p.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(destination, entry.rel)
		var err error
		switch entry.kind {
		case assetDir:
			err = applyAssetDir(path, entry.mode)
		case assetSymlink:
			err = applyAssetSymlink(path, entry.target)
		case assetFile:
			err = applyAssetFile(ctx, entry.source, path, entry.mode)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func applyAssetDir(path string, mode os.FileMode) error {
	existing, err := os.Lstat(path)
	if err == nil {
		if !existing.IsDir() {
			return fmt.Errorf("cannot overlay directory on non-directory %q", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Keep owner access so the copy can fill a read-only source directory.
	return os.Mkdir(path, mode|0o700)
}

func applyAssetSymlink(path, target string) error {
	if existing, err := os.Lstat(path); err == nil {
		if existing.IsDir() {
			return fmt.Errorf("cannot overlay symlink on directory %q", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(target, path)
}

func applyAssetFile(ctx context.Context, source, path string, mode os.FileMode) error {
	existing, err := os.Lstat(path)
	switch {
	case err == nil:
		if !existing.Mode().IsRegular() {
			return fmt.Errorf("cannot overlay file on non-regular path %q", path)
		}
	case errors.Is(err, os.ErrNotExist):
		// A copy-on-write clone shares the source's blocks, so a session costs
		// no extra disk until either side changes the file.
		if cloneFile(source, path) == nil {
			return os.Chmod(path, mode)
		}
	default:
		return err
	}
	return copyAssetFile(ctx, source, path, mode)
}

func copyAssetFile(ctx context.Context, source, path string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := out.Chmod(mode); err != nil {
		_ = out.Close()
		return err
	}
	_, copyErr := io.Copy(out, contextReader{ctx: ctx, r: in})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// contextReader stops a long file copy once the spawn is cancelled or its
// deadline passes.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
