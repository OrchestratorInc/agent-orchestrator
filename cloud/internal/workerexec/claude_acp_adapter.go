package workerexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// claudeACPAdapterPackage is Chat UI's ACP bridge for Claude Code. AO's own
// Coder templates install it (cloud/coder/main.tf); a bring-your-own template
// usually does not, so the worker installs it into its own prefix on demand.
// The version matches those templates.
const (
	claudeACPAdapterBinary  = "claude-agent-acp"
	claudeACPAdapterPackage = "@agentclientprotocol/claude-agent-acp"
	claudeACPAdapterVersion = "0.70.0"
	claudeACPInstallTimeout = 4 * time.Minute
	// claudeACPRetryAfter spaces out installs after one fails, so every Chat UI
	// request does not rerun npm against a registry that is down.
	claudeACPRetryAfter = time.Minute
)

var (
	// ErrChatAdapterUnavailable: the workspace has no Claude ACP adapter and the
	// worker could not install one (no npm, or the install failed).
	ErrChatAdapterUnavailable = errors.New("claude ACP adapter unavailable")
	// ErrChatAdapterInstalling: the worker is still installing the adapter;
	// the caller's deadline ran out first. Retrying shortly succeeds.
	ErrChatAdapterInstalling = errors.New("claude ACP adapter is being installed")
)

// claudeACPAdapter resolves (and if needed installs) the adapter once per
// worker process. The install runs detached from any one request so a short
// request deadline (model discovery) never cancels a slow npm install.
type claudeACPAdapter struct {
	lookPath func(string) (string, error)
	homeDir  func() (string, error)
	install  func(ctx context.Context, npm, prefix string) error

	mu       sync.Mutex
	path     string
	err      error
	failedAt time.Time
	pending  chan struct{}
}

var defaultClaudeACPAdapter = &claudeACPAdapter{
	lookPath: exec.LookPath,
	homeDir:  os.UserHomeDir,
	install:  npmInstallClaudeACPAdapter,
}

// EnsureClaudeACPAdapter returns the adapter's path, installing it into
// ~/.ao/tools when the workspace lacks it. It waits for an install in progress
// until ctx is done.
func EnsureClaudeACPAdapter(ctx context.Context) (string, error) {
	return defaultClaudeACPAdapter.ensure(ctx)
}

// WarmClaudeACPAdapter starts the install in the background so the first Chat
// UI request does not wait for npm.
func WarmClaudeACPAdapter() {
	go func() { _, _ = defaultClaudeACPAdapter.ensure(context.Background()) }()
}

func (a *claudeACPAdapter) ensure(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.path != "" && fileExecutable(a.path) {
		path := a.path
		a.mu.Unlock()
		return path, nil
	}
	if path, err := a.lookPath(claudeACPAdapterBinary); err == nil {
		a.path = path
		a.mu.Unlock()
		return path, nil
	}
	home, err := a.homeDir()
	if err != nil || home == "" {
		a.mu.Unlock()
		return "", fmt.Errorf("%w: no home directory to install into", ErrChatAdapterUnavailable)
	}
	prefix := filepath.Join(home, ".ao", "tools")
	local := filepath.Join(prefix, "bin", claudeACPAdapterBinary)
	if fileExecutable(local) {
		a.path = local
		a.mu.Unlock()
		return local, nil
	}
	if a.pending == nil && a.err != nil && time.Since(a.failedAt) < claudeACPRetryAfter {
		err := a.err
		a.mu.Unlock()
		return "", err
	}
	if a.pending == nil {
		npm, lookErr := a.lookPath("npm")
		if lookErr != nil {
			a.err = fmt.Errorf("%w: npm is not installed on the workspace", ErrChatAdapterUnavailable)
			a.mu.Unlock()
			return "", a.err
		}
		done := make(chan struct{})
		a.pending = done
		go func() {
			installCtx, cancel := context.WithTimeout(context.Background(), claudeACPInstallTimeout)
			defer cancel()
			installErr := a.install(installCtx, npm, prefix)
			a.mu.Lock()
			switch {
			case installErr != nil:
				a.err = fmt.Errorf("%w: %v", ErrChatAdapterUnavailable, installErr)
			case !fileExecutable(local):
				a.err = fmt.Errorf("%w: npm finished but %s is missing", ErrChatAdapterUnavailable, local)
			default:
				a.path, a.err = local, nil
			}
			if a.err != nil {
				a.failedAt = time.Now()
			}
			a.pending = nil
			a.mu.Unlock()
			close(done)
		}()
	}
	pending := a.pending
	a.mu.Unlock()
	select {
	case <-pending:
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.path != "" {
			return a.path, nil
		}
		return "", a.err
	case <-ctx.Done():
		return "", ErrChatAdapterInstalling
	}
}

func npmInstallClaudeACPAdapter(ctx context.Context, npm, prefix string) error {
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		return err
	}
	// --global with --prefix installs into prefix/bin and prefix/lib without
	// root and without touching the workspace's own global packages.
	cmd := exec.CommandContext(ctx, npm, "install", "--global", "--prefix", prefix,
		"--no-fund", "--no-audit", "--loglevel=error",
		claudeACPAdapterPackage+"@"+claudeACPAdapterVersion)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm install %s: %w: %s", claudeACPAdapterPackage, err, boundedInstallOutput(output))
	}
	return nil
}

func boundedInstallOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if len(text) > 400 {
		text = text[len(text)-400:]
	}
	return text
}

func fileExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
