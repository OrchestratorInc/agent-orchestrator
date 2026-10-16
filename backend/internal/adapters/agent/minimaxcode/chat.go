package minimaxcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// PrepareChatProfile snapshots the user's configuration into a separate,
// durable ACP profile. TUI hooks and history belong to the terminal controller;
// adopting them here would mix controller generations and native identities.
// Credentials and custom resources stay private, and the source is read only.
func (p *Plugin) PrepareChatProfile(ctx context.Context, dataDir, sessionID string, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := profilePath(dataDir, sessionID); err != nil {
		return "", err
	}
	target := filepath.Join(dataDir, "agents", adapterID, "chat", sessionID)
	marker := filepath.Join(target, ".ao-managed")
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("MiniMax Chat profile must be an AO-owned directory")
		}
		body, err := os.ReadFile(marker)
		if err != nil || string(body) != chatProfileMarker {
			return "", errors.New("refusing to adopt a non-AO MiniMax Chat profile")
		}
		source, err := sourceProfile(env)
		if err != nil {
			return "", err
		}
		if err := refreshChatCredentials(ctx, target, source); err != nil {
			return "", err
		}
		return target, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), "."+sessionID+"-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	source, err := sourceProfile(env)
	if err != nil {
		return "", err
	}
	config, err := readPrivateConfig(filepath.Join(source, "config.yaml"))
	if err != nil {
		return "", err
	}
	for _, resource := range []string{"auth", "agents", "skills", "plugins", "memory", "harnesses", "subagents", "integrations", "AGENTS.md", "permission.json"} {
		if err := copyProfileTree(ctx, filepath.Join(source, resource), filepath.Join(staging, resource)); err != nil {
			return "", err
		}
	}
	if _, err := os.Lstat(filepath.Join(staging, "plugins", "ao-activity")); err == nil {
		return "", errors.New("MiniMax Chat source profile contains AO terminal hooks")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(staging, "config.yaml"), config, 0o600); err != nil {
		return "", err
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(staging, ".ao-managed"), []byte(chatProfileMarker), 0o600); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(staging, "tmp"), 0o700); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", fmt.Errorf("publish MiniMax Chat profile: %w", err)
	}
	return target, nil
}

const chatProfileMarker = "agent-orchestrator: private minimax-code ACP profile v1\n"

// Refresh only source-owned credentials/configuration at each new native launch.
// Provider history and AO context files are never replaced. Stage auth first so
// malformed or symlinked source resources cannot destroy the previous snapshot.
func refreshChatCredentials(ctx context.Context, target, source string) error {
	config, err := readPrivateConfig(filepath.Join(source, "config.yaml"))
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp(target, ".ao-auth-refresh-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := copyProfileTree(ctx, filepath.Join(source, "auth"), filepath.Join(staging, "auth")); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	auth := filepath.Join(target, "auth")
	backup := filepath.Join(staging, "previous")
	if err := os.Rename(auth, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(filepath.Join(staging, "auth"), auth); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Rename(backup, auth)
		return err
	}
	return hookutil.AtomicWriteFile(filepath.Join(target, "config.yaml"), config, 0o600)
}
