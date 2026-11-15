package agyacp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const serverPathEnvVar = "AO_AGY_ACP_SERVER_PATH"

const harnessPathEnvVar = "ANTIGRAVITY_HARNESS_PATH"

const disableWorkspaceTrustEnvVar = "AGY_ACP_DISABLE_WORKSPACE_TRUST"

const harnessFileName = "localharness_external"

var serverSpec = binaryutil.BinarySpec{
	Label:    "agy_acp_server",
	Names:    []string{"agy_acp_server", "agy_acp_server.par"},
	WinNames: []string{"agy_acp_server.exe", "agy_acp_server"},
	UnixPaths: []string{
		"/usr/local/bin/agy_acp_server", "/opt/homebrew/bin/agy_acp_server",
	},
	UnixHomePaths: [][]string{
		{".local", "bin", "agy_acp_server"},
		{".local", "bin", "agy_acp_server.par"},
	},
}

type resolver struct {
	getenv      func(string) string
	isFile      func(string) bool
	agyCLI      func(context.Context) (string, error)
	resolveSpec func(context.Context, binaryutil.BinarySpec) (string, error)
	cliVersion  func(context.Context, string) (string, error)
}

func newResolver(agyCLI func(context.Context) (string, error)) resolver {
	return resolver{
		getenv: os.Getenv, isFile: isExecutableFile,
		agyCLI: agyCLI, resolveSpec: binaryutil.ResolveBinary,
		cliVersion: cachedCLIVersion,
	}
}

type launchTarget struct {
	path string
	cli  bool
}

func (r resolver) target(ctx context.Context) (launchTarget, error) {
	if strings.TrimSpace(r.getenv(serverPathEnvVar)) == "" && r.agyCLI != nil && r.cliVersion != nil {
		if cli, err := r.agyCLI(ctx); err == nil && cli != "" {
			if raw, err := r.cliVersion(ctx, cli); err == nil {
				if installed, ok := parseVersion(raw); ok && !installed.less(mustVersion(minimumCLIACPVersion)) {
					return launchTarget{path: cli, cli: true}, nil
				}
			}
		}
	}
	server, err := r.serverPath(ctx)
	if err != nil {
		return launchTarget{}, err
	}
	return launchTarget{path: server}, nil
}

const cliVersionTimeout = 10 * time.Second

type cliVersionKey struct {
	path    string
	size    int64
	modTime time.Time
}

var cliVersions sync.Map

func cachedCLIVersion(ctx context.Context, cli string) (string, error) {
	info, err := os.Stat(cli)
	if err != nil {
		return "", err
	}
	key := cliVersionKey{path: cli, size: info.Size(), modTime: info.ModTime()}
	if cached, ok := cliVersions.Load(key); ok {
		if version, ok := cached.(string); ok {
			return version, nil
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, cliVersionTimeout)
	defer cancel()
	out, err := aoprocess.CommandContext(probeCtx, cli, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("agy --version: %w", err)
	}
	version := strings.TrimSpace(string(out))
	cliVersions.Store(key, version)
	return version, nil
}

func (r resolver) serverPath(ctx context.Context) (string, error) {
	if override := strings.TrimSpace(r.getenv(serverPathEnvVar)); override != "" {
		if !r.isFile(override) {
			return "", fmt.Errorf("%s=%q is not an executable file", serverPathEnvVar, override)
		}
		return override, nil
	}
	if r.agyCLI != nil {
		if cli, err := r.agyCLI(ctx); err == nil && cli != "" {
			if sibling := r.sibling(cli); sibling != "" {
				return sibling, nil
			}
		}
	}
	path, err := r.resolveSpec(ctx, serverSpec)
	if err != nil {
		return "", fmt.Errorf("antigravity ACP server is not installed (update agy to %s or newer, which "+
			"includes `agy acp`; or install Google's ACP registry entry \"antigravity-acp\" beside agy or "+
			"on PATH, or set %s): %w", minimumCLIACPVersion, serverPathEnvVar, err)
	}
	return path, nil
}

func (r resolver) sibling(cli string) string {
	dir := filepath.Dir(cli)
	names := serverSpec.Names
	if runtime.GOOS == "windows" {
		names = serverSpec.WinNames
	}
	for _, name := range names {
		if path := filepath.Join(dir, name); r.isFile(path) {
			return path
		}
	}
	return ""
}

func (r resolver) harnessPath(server string) string {
	target, err := filepath.EvalSymlinks(server)
	if err != nil {
		target = server
	}
	name := harnessFileName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if path := filepath.Join(filepath.Dir(target), name); r.isFile(path) {
		return path
	}
	return ""
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}
