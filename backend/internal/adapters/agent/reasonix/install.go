package reasonix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var reasonixBinarySpec = binaryutil.BinarySpec{
	Label: "reasonix", Names: []string{"reasonix"}, WinNames: []string{"reasonix.exe", "reasonix.cmd", "reasonix"},
	UnixPaths:     []string{"/opt/homebrew/bin/reasonix", "/usr/local/bin/reasonix"},
	UnixHomePaths: append([][]string{{".local", "bin", "reasonix"}}, binaryutil.NodeManagedUnixHomePaths("reasonix")...), NodeManaged: true,
	WinPaths: []binaryutil.WinPath{{Base: binaryutil.WinAppData, Parts: []string{"npm", "reasonix.cmd"}}, {Base: binaryutil.WinHome, Parts: []string{".local", "bin", "reasonix.exe"}}},
}

var versionIdentity = regexp.MustCompile(`^reasonix (?:v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?|dev)$`)

// ResolveReasonixBinary finds and qualifies the installed CLI's host contract.
func ResolveReasonixBinary(ctx context.Context) (string, error) { return New().ResolveBinary(ctx) }

func (p *Plugin) binaryPath(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if p.lookup != nil {
		return p.lookup(ctx)
	}
	path, err := binaryutil.ResolveBinary(ctx, reasonixBinarySpec)
	if err != nil {
		return "", err
	}
	return nativeBinaryPath(path, runtime.GOOS, runtime.GOARCH)
}

// The official npm wrapper only forwards to its platform package. Windows
// CreateProcess cannot execute a .cmd shim, so use that native payload directly
// for both probes and launch, without introducing another shell.
func nativeBinaryPath(path, goos, goarch string) (string, error) {
	if goos != "windows" || strings.EqualFold(filepath.Ext(path), ".exe") {
		return path, nil
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" {
		return "", errors.New("reasonix: unsupported Windows architecture")
	}
	dir := filepath.Dir(path)
	modules := filepath.Join(dir, "node_modules")
	if strings.EqualFold(filepath.Base(dir), ".bin") {
		modules = filepath.Dir(dir)
	}
	for _, root := range []string{filepath.Join(modules, "reasonix", "node_modules"), modules} {
		candidate := filepath.Join(root, "@reasonix", "cli-win32-"+arch, "bin", "reasonix.exe")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", errors.New("reasonix: npm native Windows executable is missing; reinstall the official reasonix package with optional dependencies enabled")
}

// ResolveBinaryPresence performs no subprocess work during the first UI render.
func (p *Plugin) ResolveBinaryPresence(ctx context.Context) (string, error) {
	path, err := p.binaryPath(ctx)
	if err != nil {
		return "", err
	}
	return path, ports.ErrAgentBinaryIdentityUnknown
}

// ResolveBinary checks actual capabilities instead of inventing a released
// version floor for the upstream prompt-file feature.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	path, err := p.binaryPath(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	version, err := p.runProbe(ctx, path, "--version")
	if err != nil {
		return "", probeError(ctx, "version")
	}
	if !versionIdentity.MatchString(strings.TrimSpace(string(version))) {
		return "", fmt.Errorf("reasonix: executable is not the supported Reasonix CLI: %w", ports.ErrAgentBinaryIdentityUnknown)
	}
	help, err := p.runProbe(ctx, path, "--help")
	if err != nil {
		return "", probeError(ctx, "capabilities")
	}
	for _, flag := range []string{"--append-system-prompt-file", "--permission-mode", "--resume-exact", "--dir", "--model"} {
		if !containsFlag(string(help), flag) {
			return "", errors.New("reasonix: installed CLI lacks AO host integration; install a Reasonix build supporting --append-system-prompt-file and --resume-exact (see AO's Reasonix harness documentation)")
		}
	}
	return path, nil
}

func containsFlag(help, flag string) bool {
	for _, word := range strings.Fields(help) {
		// Native usage wraps optional flags in brackets and joins aliases with
		// a pipe ([-r|--resume [QUERY]]); still require an exact flag token.
		for _, option := range strings.Split(strings.Trim(word, "[],"), "|") {
			name, _, _ := strings.Cut(option, "=")
			if name == flag {
				return true
			}
		}
	}
	return false
}

func probeError(ctx context.Context, kind string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("reasonix: %s probe: %w", kind, err)
	}
	return fmt.Errorf("reasonix: %s probe failed", kind)
}

func (p *Plugin) runProbe(ctx context.Context, path string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.probe != nil {
		return p.probe(ctx, path, args...)
	}
	cmd := process.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	var output cappedOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return output.Bytes(), err
}

type cappedOutput struct{ bytes.Buffer }

func (w *cappedOutput) Write(p []byte) (int, error) {
	const maxProbeBytes = 128 << 10
	if w.Len()+len(p) > maxProbeBytes {
		return 0, errors.New("reasonix probe output limit exceeded")
	}
	return w.Buffer.Write(p)
}
