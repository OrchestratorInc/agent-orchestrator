package claudeacp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/claudesession"
)

var _ ports.ChatWorkspaceForker = (*checkpointDriver)(nil)

type nativeConfigDirResolver interface {
	NativeSessionConfigDir(context.Context, map[string]string) (string, error)
}

// ForkIntoWorkspace copies a whole Claude session so it opens from
// cfg.WorkspacePath. The SDK writes the copy beside the source, keyed by the
// source folder, so it is then moved to the new folder's key and its cwd is
// rewritten. The source session file is never modified.
func (d *checkpointDriver) ForkIntoWorkspace(ctx context.Context, sourceID, sourceWorkspace, upToMessageID string, cfg ports.ChatStartConfig) (string, error) {
	resolver, ok := d.plugin.(nativeConfigDirResolver)
	if !ok {
		return "", ports.ErrChatCapabilityUnavailable
	}
	configDir, err := resolver.NativeSessionConfigDir(ctx, cfg.Env)
	if err != nil {
		return "", err
	}
	src, err := filepath.EvalSymlinks(sourceWorkspace)
	if err != nil {
		return "", fmt.Errorf("claude fork: source workspace: %w", err)
	}
	dst, err := filepath.EvalSymlinks(cfg.WorkspacePath)
	if err != nil {
		return "", fmt.Errorf("claude fork: fork workspace: %w", err)
	}
	cut := ""
	if upToMessageID != "" {
		sourceFile := filepath.Join(configDir, "projects", claudesession.ProjectKey(src), sourceID+".jsonl")
		if cut, err = claudesession.ResolveForkPoint(ctx, sourceFile, upToMessageID); err != nil {
			return "", fmt.Errorf("claude fork: %w", err)
		}
	}
	env := append(os.Environ(), envList(cfg.Env)...)
	env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	childID, err := runForkScript(ctx, env, sourceID, src, cut)
	if err != nil {
		return "", fmt.Errorf("claude fork: %w", err)
	}
	if childID == "" || childID == sourceID || strings.ContainsAny(childID, `/\`) {
		return "", fmt.Errorf("claude fork: invalid session id %q", childID)
	}
	from := filepath.Join(configDir, "projects", claudesession.ProjectKey(src), childID+".jsonl")
	to := filepath.Join(configDir, "projects", claudesession.ProjectKey(dst), childID+".jsonl")
	// The transcript may name the worktree as given or as resolved.
	if err := claudesession.MoveSessionFile(ctx, from, to, []string{src, filepath.Clean(sourceWorkspace)}, dst); err != nil {
		_ = os.Remove(from)
		return "", fmt.Errorf("claude fork: move session: %w", err)
	}
	return childID, nil
}

// DeleteWorkspaceFork removes a session made by ForkIntoWorkspace. The copy
// is found by its session id, which is the fork's own, in whichever project
// folder holds it: the folder can't be derived again from a workspace that is
// already gone or was reached through a symlink.
func (d *checkpointDriver) DeleteWorkspaceFork(ctx context.Context, providerConversationID, _ string, env map[string]string) error {
	resolver, ok := d.plugin.(nativeConfigDirResolver)
	// A path separator or a pattern character would reach other files.
	if !ok || providerConversationID == "" || strings.ContainsAny(providerConversationID, `/\*?[`) {
		return nil
	}
	configDir, err := resolver.NativeSessionConfigDir(ctx, env)
	if err != nil {
		return err
	}
	copies, err := filepath.Glob(filepath.Join(configDir, "projects", "*", providerConversationID+".jsonl"))
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range copies {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// runForkScript runs claudesession.RunFork with the bundled Node and Agent SDK.
var runForkScript = func(ctx context.Context, env []string, sourceID, dir, upTo string) (string, error) {
	runtimeDir := strings.TrimSpace(os.Getenv("AO_ACP_RUNTIME_DIR"))
	if runtimeDir == "" {
		runtimeDir = runtimeDirectoryBesideExecutable()
	}
	if runtimeDir == "" {
		return "", errors.New("AO ACP runtime is not installed")
	}
	node := filepath.Join(runtimeDir, "node", "bin", "node")
	sdk := filepath.Join(runtimeDir, "node_modules", "@anthropic-ai", "claude-agent-sdk", "sdk.mjs")
	if err := requireFile(node, "packaged Node runtime"); err != nil {
		return "", err
	}
	if err := requireFile(sdk, "Claude Agent SDK"); err != nil {
		return "", err
	}
	node, err := jitCapableNode(ctx, node)
	if err != nil {
		return "", err
	}
	return claudesession.RunFork(ctx, node, sdk, env, sourceID, dir, upTo)
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
