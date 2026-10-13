package claudecode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	sandboxConfigDirName = "claude"
	sandboxConfigName    = ".claude.json"
	// sandboxConfigMaxBytes bounds reading a container-written config.
	sandboxConfigMaxBytes = 16 << 20
)

// SandboxBinary is the Claude Code executable expected on the image PATH.
func (p *Plugin) SandboxBinary() string { return "claude" }

// PrepareSandbox points Claude at a per-project config dir that is mounted at
// the same path in the container, so AO's transcript lookup on the host
// (NativeConversationExists honours env CLAUDE_CONFIG_DIR) sees what the
// sandboxed Claude writes. The host keychain is unreachable from a container,
// so the credential must be an explicit env key or AO's captured setup-token.
//
// The container controls everything in the config dir and, in shared mode,
// projectDir's children. Every access goes through an os.Root so a planted
// symlink cannot make the host read or write outside them.
func (p *Plugin) PrepareSandbox(env map[string]string, dataDir, projectDir, workspacePath string) error {
	configDir := filepath.Join(projectDir, sandboxConfigDirName)
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		return fmt.Errorf("claude-code: sandbox project dir: %w", err)
	}
	env["CLAUDE_CONFIG_DIR"] = configDir
	if env["ANTHROPIC_API_KEY"] == "" && env["CLAUDE_CODE_OAUTH_TOKEN"] == "" {
		token := readLocalOAuthToken(dataDir)
		if token == "" {
			return fmt.Errorf("%w: Claude Code in a sandbox cannot use your keychain login; set ANTHROPIC_API_KEY, or CLAUDE_CODE_OAUTH_TOKEN (create one with \"claude setup-token\"), in the project env", ports.ErrSandboxMisconfigured)
		}
		env["CLAUDE_CODE_OAUTH_TOKEN"] = token
	}
	root, err := openSandboxConfigRoot(projectDir)
	if err != nil {
		return fmt.Errorf("claude-code: sandbox config dir: %w", err)
	}
	defer func() { _ = root.Close() }()
	return seedSandboxConfig(root, configDir, workspacePath)
}

// openSandboxConfigRoot opens projectDir/claude as a root, replacing anything
// that is not a real directory (a planted symlink, file or FIFO) first.
// Opening it through the project root refuses a symlink that escapes it.
func openSandboxConfigRoot(projectDir string) (*os.Root, error) {
	project, err := os.OpenRoot(projectDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = project.Close() }()
	if err := ensureRootDir(project, sandboxConfigDirName); err != nil {
		return nil, err
	}
	return project.OpenRoot(sandboxConfigDirName)
}

// ensureRootDir makes name a real directory inside root. A non-directory is
// removed (the link itself, never its target) and recreated.
func ensureRootDir(root *os.Root, name string) error {
	fi, err := root.Lstat(name)
	switch {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		if err := root.Remove(name); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// seedSandboxConfig marks first-run onboarding done and the workspace trusted
// in the sandbox's .claude.json, so neither wizard blocks a spawn. Existing
// keys are kept. A non-regular file in its place was planted by the
// container and is replaced; a corrupt regular file is refused unchanged.
func seedSandboxConfig(root *os.Root, configDir, workspacePath string) (err error) {
	claudeTrustMu.Lock()
	defer claudeTrustMu.Unlock()
	display := filepath.Join(configDir, sandboxConfigName)
	cfg, err := readSandboxConfig(root, display)
	if err != nil {
		return err
	}
	changed := false
	if done, _ := cfg["hasCompletedOnboarding"].(bool); !done {
		cfg["hasCompletedOnboarding"] = true
		changed = true
	}
	if markWorkspaceTrusted(cfg, workspacePath) {
		changed = true
	}
	if !changed {
		return nil
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := randomName(sandboxConfigName + "-")
	if err != nil {
		return err
	}
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("claude-code: create temp config: %w", err)
	}
	defer func() {
		if err != nil {
			_ = root.Remove(tmp)
		}
	}()
	_, werr := f.Write(raw)
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("claude-code: write %s: %w", display, werr)
	}
	if cerr != nil {
		return fmt.Errorf("claude-code: close temp config: %w", cerr)
	}
	if err = root.Rename(tmp, sandboxConfigName); err != nil {
		return fmt.Errorf("claude-code: replace %s: %w", display, err)
	}
	return nil
}

// readSandboxConfig returns the parsed config, or an empty one when it is
// absent or was not a regular file (which it then removes).
func readSandboxConfig(root *os.Root, display string) (map[string]any, error) {
	cfg := map[string]any{}
	fi, err := root.Lstat(sandboxConfigName)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claude-code: stat %s: %w", display, err)
	}
	if !fi.Mode().IsRegular() {
		if err := removeRootEntry(root, sandboxConfigName, fi); err != nil {
			return nil, fmt.Errorf("claude-code: remove planted %s: %w", display, err)
		}
		return cfg, nil
	}
	// Non-blocking so a FIFO swapped in after the Lstat cannot hang the open.
	f, err := root.OpenFile(sandboxConfigName, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("claude-code: read %s: %w", display, err)
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("claude-code: %s is not a regular file", display)
	}
	raw, err := io.ReadAll(io.LimitReader(f, sandboxConfigMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("claude-code: read %s: %w", display, err)
	}
	if len(raw) > sandboxConfigMaxBytes {
		return nil, fmt.Errorf("claude-code: %s is too large", display)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("claude-code: refusing to rewrite corrupt %s: %w", display, err)
	}
	if cfg == nil { // a literal `null` config
		cfg = map[string]any{}
	}
	return cfg, nil
}

// removeRootEntry removes name without following it: a symlink or special
// file is unlinked, a real directory is removed with its contents.
func removeRootEntry(root *os.Root, name string, fi fs.FileInfo) error {
	if fi.IsDir() {
		return root.RemoveAll(name)
	}
	return root.Remove(name)
}

func randomName(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

// launchBinary returns the bare in-image name for a sandboxed launch, which
// never needs Claude on the host, and the resolved host binary otherwise.
func (p *Plugin) launchBinary(ctx context.Context, sandboxed bool) (string, error) {
	if sandboxed {
		return p.SandboxBinary(), nil
	}
	return p.claudeBinary(ctx)
}
