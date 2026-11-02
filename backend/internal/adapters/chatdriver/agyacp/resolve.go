package agyacp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
)

// serverPathEnvVar points AO at an exact agy_acp_server executable, like
// AO_CLAUDE_ACP_COMMAND does for Claude's adapter.
const serverPathEnvVar = "AO_AGY_ACP_SERVER_PATH"

// harnessPathEnvVar is how the server finds its localharness_external helper
// when it is not on PATH, for example when agy_acp_server is a symlink.
const harnessPathEnvVar = "ANTIGRAVITY_HARNESS_PATH"

// disableWorkspaceTrustEnvVar tells agy_acp_server to treat all workspaces as
// trusted for executing automated hooks, bypassing the interactive prompt.
const disableWorkspaceTrustEnvVar = "AGY_ACP_DISABLE_WORKSPACE_TRUST"

const harnessFileName = "localharness_external"

// serverSpec is the user-installed Antigravity ACP server. Google ships it as a
// separate download (ACP registry entry "antigravity-acp"), not with the agy
// CLI, so AO looks for it the way it looks for any user-installed binary and
// never downloads it.
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

// resolver finds the user's Antigravity ACP server and GEMINI_HOME. Its lookups
// are injectable so tests never touch the real home directory or PATH.
type resolver struct {
	getenv      func(string) string
	isFile      func(string) bool
	agyCLI      func(context.Context) (string, error)
	resolveSpec func(context.Context, binaryutil.BinarySpec) (string, error)
}

func newResolver(agyCLI func(context.Context) (string, error)) resolver {
	return resolver{
		getenv: os.Getenv, isFile: isExecutableFile,
		agyCLI: agyCLI, resolveSpec: binaryutil.ResolveBinary,
	}
}

// serverPath resolves, in order: the explicit override, the server installed
// beside the agy CLI, then PATH and the usual install directories. An override
// that does not exist is an error rather than a silent fallback, because it
// states exactly which binary to run.
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
		return "", fmt.Errorf("antigravity ACP server is not installed (install Google's ACP registry entry "+
			"\"antigravity-acp\" beside agy or on PATH, or set %s): %w", serverPathEnvVar, err)
	}
	return path, nil
}

// sibling returns an executable server in the agy CLI's directory. Beside the
// resolved CLI is a better answer than a fresh PATH search: it cannot pick up a
// different installation.
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

// harnessPath is the localharness_external helper shipped in the same archive as
// the server, found beside the server's real location so a symlinked server
// still finds it.
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
