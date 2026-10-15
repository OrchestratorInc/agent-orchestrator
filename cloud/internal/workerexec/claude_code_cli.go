package workerexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClaudeCodeVersion is the Claude Code release cloud workers run. The model
// API refuses newer models from older clients ("version 2.1.280 or newer is
// required"), while the Chat UI lists every model the account can use, so a
// workspace image that bakes an older release would offer models that fail on
// every turn. Images bake this version, and the worker upgrades an older
// install into its own prefix at boot. Keep the Dockerfiles and the Azure bake
// script in step with it.
const ClaudeCodeVersion = "2.1.287"

const (
	claudeCodeBinary         = "claude"
	claudeCodePackage        = "@anthropic-ai/claude-code"
	claudeCodeInstallTimeout = 4 * time.Minute
	claudeCodeVersionTimeout = 15 * time.Second
	// A failed upgrade is retried after claudeCodeRetryAfter, doubling per
	// consecutive failure up to claudeCodeRetryMax.
	claudeCodeRetryAfter = time.Minute
	claudeCodeRetryMax   = 30 * time.Minute
	// claudeCodeTurnWait bounds how long a Chat turn waits for an upgrade in
	// progress before running on the older install.
	claudeCodeTurnWait = 90 * time.Second
)

// claudeCode resolves the Claude Code executable the worker launches. It uses
// the workspace's own install when that is at least ClaudeCodeVersion, and
// otherwise installs ClaudeCodeVersion into ~/.ao/tools once per worker
// process. A failed upgrade never blocks a session: the older install is
// still used.
type claudeCode struct {
	lookPath func(string) (string, error)
	homeDir  func() (string, error)
	version  func(ctx context.Context, path string) (string, error)
	install  func(ctx context.Context, npm, prefix string) error

	mu       sync.Mutex
	path     string
	failedAt time.Time
	failures int
	pending  chan struct{}
}

var defaultClaudeCode = &claudeCode{
	lookPath: exec.LookPath,
	homeDir:  os.UserHomeDir,
	version:  claudeCodeExecutableVersion,
	install:  npmInstallClaudeCode,
}

// EnsureClaudeCode returns the Claude Code executable to launch, upgrading an
// older install first. It waits for an upgrade in progress until ctx is done
// and then falls back to whatever is installed.
func EnsureClaudeCode(ctx context.Context) string {
	return defaultClaudeCode.ensure(ctx)
}

// WarmClaudeCode starts any needed upgrade in the background so a Chat turn
// does not wait for npm.
func WarmClaudeCode() {
	go func() { _ = defaultClaudeCode.ensure(context.Background()) }()
}

// current is the executable to launch right now, without waiting: the
// upgraded install once it is ready, otherwise the bare name on PATH.
func (c *claudeCode) current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path != "" && fileExecutable(c.path) {
		return c.path
	}
	return claudeCodeBinary
}

func (c *claudeCode) ensure(ctx context.Context) string {
	c.mu.Lock()
	if c.path != "" && fileExecutable(c.path) {
		path := c.path
		c.mu.Unlock()
		return path
	}
	if c.pending == nil {
		if c.failures > 0 && time.Since(c.failedAt) < claudeCodeRetryBackoff(c.failures) {
			c.mu.Unlock()
			return claudeCodeBinary
		}
		done := make(chan struct{})
		c.pending = done
		go c.resolve(done)
	}
	pending := c.pending
	retrying := c.failures > 0
	c.mu.Unlock()
	if retrying {
		// Only the first upgrade is worth waiting for. Once one has failed
		// (npm unreachable, say), turns run on the older install while the
		// retry proceeds in the background.
		return c.current()
	}
	select {
	case <-pending:
		return c.current()
	case <-ctx.Done():
		return c.current()
	}
}

// resolve runs detached from any one request, so a short request deadline
// never cancels a slow npm install.
func (c *claudeCode) resolve(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeCodeInstallTimeout)
	defer cancel()
	path, ok := c.resolvePath(ctx)
	c.mu.Lock()
	if ok {
		c.path, c.failedAt, c.failures = path, time.Time{}, 0
	} else {
		c.failedAt = time.Now()
		c.failures++
	}
	c.pending = nil
	c.mu.Unlock()
	close(done)
}

func claudeCodeRetryBackoff(failures int) time.Duration {
	backoff := claudeCodeRetryAfter
	for i := 1; i < failures && backoff < claudeCodeRetryMax; i++ {
		backoff *= 2
	}
	return min(backoff, claudeCodeRetryMax)
}

func (c *claudeCode) resolvePath(ctx context.Context) (string, bool) {
	var prefix, local string
	if home, err := c.homeDir(); err == nil && home != "" {
		prefix = filepath.Join(home, ".ao", "tools")
		local = filepath.Join(prefix, "bin", claudeCodeBinary)
	}
	// A previous worker on this durable home may already have upgraded.
	if local != "" && fileExecutable(local) && c.atLeastPinned(ctx, local) {
		return local, true
	}
	if path, err := c.lookPath(claudeCodeBinary); err == nil && c.atLeastPinned(ctx, path) {
		return path, true
	}
	if prefix == "" {
		return "", false
	}
	npm, err := c.lookPath("npm")
	if err != nil {
		return "", false
	}
	if err := c.install(ctx, npm, prefix); err != nil || !fileExecutable(local) || !c.atLeastPinned(ctx, local) {
		return "", false
	}
	return local, true
}

func (c *claudeCode) atLeastPinned(ctx context.Context, path string) bool {
	versionCtx, cancel := context.WithTimeout(ctx, claudeCodeVersionTimeout)
	defer cancel()
	installed, err := c.version(versionCtx, path)
	return err == nil && compareReleaseVersions(installed, ClaudeCodeVersion) >= 0
}

func claudeCodeExecutableVersion(ctx context.Context, path string) (string, error) {
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", err
	}
	// "2.1.287 (Claude Code)"
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "", fmt.Errorf("%s --version printed nothing", path)
	}
	return fields[0], nil
}

// compareReleaseVersions compares dotted numeric versions; a version that does
// not parse sorts lowest, so an unknown install is upgraded.
func compareReleaseVersions(left, right string) int {
	l, lok := parseReleaseVersion(left)
	r, rok := parseReleaseVersion(right)
	switch {
	case !lok && !rok:
		return 0
	case !lok:
		return -1
	case !rok:
		return 1
	}
	for index := 0; index < len(l) || index < len(r); index++ {
		var a, b int
		if index < len(l) {
			a = l[index]
		}
		if index < len(r) {
			b = r[index]
		}
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseReleaseVersion(value string) ([]int, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if value == "" {
		return nil, false
	}
	parts := strings.Split(value, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, false
		}
		numbers = append(numbers, number)
	}
	return numbers, true
}

func npmInstallClaudeCode(ctx context.Context, npm, prefix string) error {
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, npm, "install", "--global", "--prefix", prefix,
		"--no-fund", "--no-audit", "--loglevel=error",
		claudeCodePackage+"@"+ClaudeCodeVersion)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm install %s: %w: %s", claudeCodePackage, err, boundedInstallOutput(output))
	}
	return nil
}
