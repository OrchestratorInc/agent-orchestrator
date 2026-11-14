package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const defaultVerifyTimeout = 60 * time.Second

type VerifyResult struct {
	ResolvedPath string
	Output       string
}

type Verifier struct {
	agents   ports.AgentResolver
	commands ports.CommandRunner
	timeout  time.Duration
}

func NewVerifier(agents ports.AgentResolver, commands ports.CommandRunner) *Verifier {
	return &Verifier{agents: agents, commands: commands, timeout: defaultVerifyTimeout}
}

func (v *Verifier) Resolve(ctx context.Context, target Target) (string, error) {
	if !IsAgentTarget(target) {
		return "", fmt.Errorf("systeminstall: %s is not a harness", target)
	}
	if v.agents == nil {
		return "", fmt.Errorf("systeminstall: harness verifier is not configured")
	}
	agent, ok := v.agents.Agent(domain.AgentHarness(target))
	if !ok {
		return "", fmt.Errorf("systeminstall: no adapter registered for %s", target)
	}
	resolver, ok := agent.(ports.AgentBinaryResolver)
	if !ok {
		return "", fmt.Errorf("systeminstall: adapter for %s cannot resolve its binary", target)
	}
	path, err := resolver.ResolveBinary(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve installed %s binary: %w", target, err)
	}
	if path == "" {
		return "", fmt.Errorf("resolve installed %s binary: empty path", target)
	}
	return path, nil
}

func (v *Verifier) Verify(ctx context.Context, target Target) (VerifyResult, error) {
	if v.commands == nil {
		return VerifyResult{}, fmt.Errorf("systeminstall: harness verifier is not configured")
	}

	probeCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	path, err := v.Resolve(probeCtx, target)
	if err != nil {
		if ctx.Err() == nil && errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return VerifyResult{}, fmt.Errorf("%s binary identity check timed out after %s (a newly installed CLI can be slow on its first launch; choose Verify again): %w", target, v.timeout, context.DeadlineExceeded)
		}
		return VerifyResult{}, err
	}
	out := &capturedOutput{max: maxOutputBytes}
	if err := v.commands.Run(probeCtx, append([]string{path}, versionArgs(target)...), out, out); err != nil {
		result := VerifyResult{ResolvedPath: path, Output: out.String()}
		if ctx.Err() == nil && errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return result, fmt.Errorf("run %s version probe: timed out after %s (a newly installed CLI can be slow on its first launch; choose Verify again): %w", target, v.timeout, context.DeadlineExceeded)
		}
		return result, fmt.Errorf("run %s version probe: %w", target, err)
	}
	return VerifyResult{ResolvedPath: path, Output: out.String()}, nil
}

// versionArgs is the native version command; most CLIs take --version, but
// Docker Agent only has a `version` subcommand and rejects the flag.
func versionArgs(target Target) []string {
	if target == TargetDockerAgent {
		return []string{"version"}
	}
	return []string{"--version"}
}
