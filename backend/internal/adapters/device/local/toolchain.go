package local

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// The two external tools device support is built on. expo-device-hub streams
// simulator and emulator screens; agent-device drives them for agents. They
// are pinned: the injected agent instructions describe these versions, so an
// upgrade is a deliberate change, never a registry drift.
var (
	hubTool         = tool{pkg: "expo-device-hub", version: "0.22.1", entry: []string{"dist", "server", "cli.mjs"}}
	agentDeviceTool = tool{pkg: "agent-device", version: "0.21.36", entry: []string{"bin", "agent-device.mjs"}}
)

// installedSentinel is written only after npm exits 0. npm extracts files
// before it finishes, so an entry file alone does not prove a usable tree.
const installedSentinel = ".ao-installed"

type tool struct {
	pkg     string
	version string
	entry   []string
}

func (t tool) dir(root string) string {
	return filepath.Join(root, t.pkg, t.version)
}

func (t tool) entryPath(root string) string {
	return filepath.Join(append([]string{t.dir(root), "node_modules", t.pkg}, t.entry...)...)
}

func (t tool) installed(root string) bool {
	if _, err := os.Stat(filepath.Join(t.dir(root), installedSentinel)); err != nil {
		return false
	}
	_, err := os.Stat(t.entryPath(root))
	return err == nil
}

// ensureTool installs one pinned tool under root unless it is already there.
// It stages into a temporary sibling and renames into place, so a crash or a
// failed install never leaves a half-written version that looks installed.
func ensureTool(ctx context.Context, root, npm string, t tool) error {
	if t.installed(root) {
		return nil
	}
	dir := t.dir(root)
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return fmt.Errorf("create %s tool dir: %w", t.pkg, err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(dir), ".install-*")
	if err != nil {
		return fmt.Errorf("stage %s install: %w", t.pkg, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	// Installed from a lockfile shipped with AO (npm ci), so every machine gets
	// the same dependency tree, and without install scripts: none of the
	// locked packages has one.
	if err := writeToolManifest(staging, t); err != nil {
		return err
	}
	// Run inside the staging dir: npm ci with --prefix reports the manifest
	// and lockfile out of sync.
	cmd := process.CommandContext(ctx, npm, "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund")
	cmd.Dir = staging
	cmd.Env = append(os.Environ(), "npm_config_update_notifier=false")
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return fmt.Errorf("npm ci %s@%s: %w: %s", t.pkg, t.version, runErr, lastLines(string(out), 6))
	}
	if err := os.WriteFile(filepath.Join(staging, installedSentinel), []byte(t.version+"\n"), 0o600); err != nil {
		return fmt.Errorf("mark %s installed: %w", t.pkg, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("replace %s install: %w", t.pkg, err)
	}
	if err := os.Rename(staging, dir); err != nil {
		return fmt.Errorf("publish %s install: %w", t.pkg, err)
	}
	return nil
}

// toolLocks holds the lockfile each pinned tool installs from.
//
//go:embed locks/*.package-lock.json
var toolLocks embed.FS

// toolManifestName is the package name the lockfiles were made under.
const toolManifestName = "ao-device-tool"

// writeToolManifest writes the package.json and lockfile npm ci installs one
// tool from.
func writeToolManifest(dir string, t tool) error {
	lock, err := toolLocks.ReadFile("locks/" + t.pkg + ".package-lock.json")
	if err != nil {
		return fmt.Errorf("no lockfile for %s: %w", t.pkg, err)
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"name":         toolManifestName,
		"private":      true,
		"dependencies": map[string]string{t.pkg: t.version},
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), append(manifest, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s manifest: %w", t.pkg, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), lock, 0o600); err != nil {
		return fmt.Errorf("write %s lockfile: %w", t.pkg, err)
	}
	return nil
}

// writeAgentDeviceShim writes the launcher agents call as `agent-device`. It
// pins the installed version and AO's state directory, so the agent gets the
// version its instructions describe whatever is installed globally.
func writeAgentDeviceShim(path, nodePath, entryPath, stateDir string) error {
	if strings.ContainsAny(nodePath+entryPath+stateDir, "'\n") {
		return errors.New("agent-device shim paths must not contain quotes or newlines")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create agent-device shim dir: %w", err)
	}
	script := "#!/bin/sh\n" +
		"# Written by AO: runs the pinned agent-device with AO's device state.\n" +
		"AGENT_DEVICE_STATE_DIR='" + stateDir + "'\n" +
		"AGENT_DEVICE_NO_UPDATE_NOTIFIER=1\n" +
		"export AGENT_DEVICE_STATE_DIR AGENT_DEVICE_NO_UPDATE_NOTIFIER\n" +
		"exec '" + nodePath + "' '" + entryPath + "' \"$@\"\n"
	return os.WriteFile(path, []byte(script), 0o755) //nolint:gosec // The shim must be executable by the agent.
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// writeRevokedWrapper turns a lease's command into one that only says the
// session holds no device any more.
func writeRevokedWrapper(path string) error {
	script := "#!/bin/sh\n" +
		"# Written by AO: this session's device lease has ended.\n" +
		"echo 'This session has no device lease; run `ao device open` for a new one.' >&2\n" +
		"exit 1\n"
	return os.WriteFile(path, []byte(script), 0o755) //nolint:gosec // The wrapper must stay executable so it can say why it refuses.
}

// writeLeaseWrapper writes the command one lease's agent runs: the pinned
// agent-device with the lease's platform, device and session flags appended
// after the agent's own arguments (agent-device takes flags in any position).
func writeLeaseWrapper(path, shim string, flags []string) error {
	for _, part := range append([]string{shim}, flags...) {
		if strings.ContainsAny(part, "'\n") {
			return errors.New("lease wrapper arguments must not contain quotes or newlines")
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create lease dir: %w", err)
	}
	quoted := make([]string, len(flags))
	for i, f := range flags {
		quoted[i] = "'" + f + "'"
	}
	// help and version take no device flags (agent-device rejects them there),
	// so they pass through unbound.
	script := "#!/bin/sh\n" +
		"# Written by AO: agent-device bound to one session's device.\n" +
		"case \"$1\" in help|--help|-h|version|--version) exec '" + shim + "' \"$@\" ;; esac\n" +
		"exec '" + shim + "' \"$@\" " + strings.Join(quoted, " ") + "\n"
	return os.WriteFile(path, []byte(script), 0o755) //nolint:gosec // The wrapper must be executable by the agent.
}
