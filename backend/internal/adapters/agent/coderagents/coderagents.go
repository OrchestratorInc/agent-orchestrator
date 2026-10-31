// Package coderagents exposes Coder Agents (the agent built into a Coder
// deployment) as a Chat-only AO harness. AO reaches it through the
// deployment's REST/WebSocket API with the session the native `coder` CLI
// holds; the CLI is used for login and for reading that session.
package coderagents

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const adapterID = "coder-agents"

// probeTimeout bounds one `coder version` or `coder login token` run.
const probeTimeout = 10 * time.Second

// versionPattern is the first line `coder version` prints, e.g.
// "Coder v2.37.9+f1205079 Fri Oct 30 ...". Anchored so another tool named
// coder cannot pass as the Coder CLI.
var versionPattern = regexp.MustCompile(`\ACoder v(2)\.(\d+)\.(\d+)`)

// Plugin is the Coder Agents harness. It has no terminal interface: Coder
// Agents runs inside the deployment and is driven only through its chat API.
type Plugin struct {
	agentbase.Base

	binaryMu       sync.Mutex
	resolvedBinary string

	// resolve, runVersion, runToken and transport are seams for tests.
	resolve    func(context.Context) (string, error)
	runVersion func(ctx context.Context, binary string) ([]byte, error)
	runToken   func(ctx context.Context, binary string, env []string) (stdout, stderr []byte, err error)
	transport  http.RoundTripper
}

// New creates the Coder Agents harness adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)

// Manifest describes the harness.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID: adapterID, Name: "Coder Agents",
		Description:  "Coder Agents from your Coder deployment, with tools AO runs in the session worktree.",
		Version:      "0.1.0",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// GetConfigSpec exposes the model config and reasoning effort overrides.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelEffortConfigSpec(ctx,
		"Coder Agents model config ID from the deployment's enabled models.",
		"Reasoning effort the selected Coder model accepts.")
}

// GetLaunchCommand rejects terminal launches: Coder Agents is Chat-only.
func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("coder agents is available in chat mode only")
}

// ResolveBinary finds the official coder CLI and confirms its identity.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	resolve := p.resolve
	if resolve == nil {
		resolve = func(ctx context.Context) (string, error) {
			return binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{
				Label: "coder", Names: []string{"coder"}, WinNames: []string{"coder.exe"},
				UnixPaths:     []string{"/opt/homebrew/bin/coder", "/usr/local/bin/coder", "/home/linuxbrew/.linuxbrew/bin/coder", "/usr/bin/coder"},
				UnixHomePaths: [][]string{{".local", "bin", "coder"}},
				ValidateIdentity: func(ctx context.Context, path string) bool {
					return p.versionIdentified(ctx, path)
				},
			})
		}
	}
	binary, err := resolve(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = strings.TrimSpace(binary)
	return p.resolvedBinary, nil
}

// InvalidateBinaryResolution clears the cached coder path.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

// SupportsTerminalVersionRefresh is false: there is no terminal to refresh.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }

// versionIdentified runs `coder version` and confirms it is the Coder v2 CLI.
func (p *Plugin) versionIdentified(ctx context.Context, binary string) bool {
	run := p.runVersion
	if run == nil {
		run = func(ctx context.Context, binary string) ([]byte, error) {
			cmd := aoprocess.CommandContext(ctx, binary, "version")
			return cmd.Output()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := run(ctx, binary)
	if err != nil {
		return false
	}
	return versionPattern.Match(bytes.TrimSpace(out))
}

// Connect resolves the deployment and session for env (project overrides
// first, then the daemon environment) and returns an API client. The token
// stays inside the returned client and credentials.
func (p *Plugin) Connect(ctx context.Context, env map[string]string) (*coderapi.Client, coderapi.Credentials, error) {
	getenv := envLookup(env)
	creds, err := coderapi.ResolveCredentials(ctx, getenv, func(ctx context.Context, extra map[string]string) ([]byte, error) {
		binary, err := p.ResolveBinary(ctx)
		if err != nil {
			return nil, err
		}
		overlay := make(map[string]string, len(env)+len(extra))
		for key, value := range env {
			overlay[key] = value
		}
		for key, value := range extra {
			overlay[key] = value
		}
		ctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		stdout, stderr, err := p.tokenRunner()(ctx, binary, processenv.Merge(overlay))
		if err != nil {
			// stderr carries the CLI's reason ("no session token found"); the
			// token itself is only ever printed to stdout.
			return stderr, err
		}
		return stdout, nil
	})
	if err != nil {
		return nil, coderapi.Credentials{}, err
	}
	return coderapi.NewClient(creds, p.transport), creds, nil
}

func (p *Plugin) tokenRunner() func(context.Context, string, []string) ([]byte, []byte, error) {
	if p.runToken != nil {
		return p.runToken
	}
	return func(ctx context.Context, binary string, env []string) ([]byte, []byte, error) {
		cmd := aoprocess.CommandContext(ctx, binary, "login", "token")
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
}

// Organization returns the organization selector configured for env.
func Organization(env map[string]string) string {
	return strings.TrimSpace(envLookup(env)(coderapi.EnvOrganization))
}

func envLookup(env map[string]string) func(string) string {
	return func(name string) string {
		if value, ok := env[name]; ok {
			return value
		}
		return os.Getenv(name)
	}
}
