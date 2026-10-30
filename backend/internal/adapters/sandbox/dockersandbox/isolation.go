package dockersandbox

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// isolationImage runs the network-isolation helper. A separate image is used
// because the user's image may lack `ip`, and the agent's own container must
// never hold NET_ADMIN: the helper gets it only while it shares the netns.
const isolationImage = "alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8"

// sandboxNetwork is the AO-owned user-defined network every sandbox joins.
// Its embedded DNS (127.0.0.11) lets the helper blackhole Docker Desktop's
// host-service subnet, which on the default bridge also serves DNS.
const (
	sandboxNetwork      = "ao-sandbox"
	labelSandboxNetwork = "ao.sandbox.network"
	iccOption           = "com.docker.network.bridge.enable_icc"
)

// isolationScript blackholes the host aliases and probes the daemon port;
// exit 3 means the port is still reachable, 4 that no host alias resolved.
//
//go:embed scripts/isolate-network
var isolationScript string

// ensureNetwork creates the sandbox network unless it exists. A concurrent
// create that wins the race is success. Inter-container traffic is disabled
// so sandboxes of different sessions and projects cannot reach each other.
func (s *Sandbox) ensureNetwork(ctx context.Context, docker string) error {
	out, err := s.output(ctx, probeTimeout, docker, "network", "inspect", "--format", `{{index .Options "`+iccOption+`"}}`, sandboxNetwork)
	if err == nil {
		if strings.TrimSpace(string(out)) != "false" {
			return fmt.Errorf("%w: sandbox network %s allows traffic between containers; remove it with `docker network rm %s` so it is recreated isolated", ports.ErrSandboxMisconfigured, sandboxNetwork, sandboxNetwork)
		}
		return nil
	}
	if errors.Is(err, ports.ErrSandboxUnavailable) {
		return err
	}
	if msg := strings.ToLower(err.Error()); !strings.Contains(msg, "not found") && !strings.Contains(msg, "no such network") {
		return fmt.Errorf("%w: inspect sandbox network %s: %w", ports.ErrSandboxMisconfigured, sandboxNetwork, err)
	}
	_, err = s.output(ctx, probeTimeout, docker, "network", "create", "--label", labelSandboxNetwork+"=1", "-o", iccOption+"=false", sandboxNetwork)
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return nil
	}
	if errors.Is(err, ports.ErrSandboxUnavailable) {
		return err
	}
	return fmt.Errorf("%w: create sandbox network %s: %w", ports.ErrSandboxMisconfigured, sandboxNetwork, err)
}

// requireHostAlias reports whether the engine always defines a host alias
// (Docker Desktop, OrbStack), so an alias that fails to resolve is an error
// rather than a native Linux engine without one. Cached once known.
func (s *Sandbox) requireHostAlias(ctx context.Context, docker string) (bool, error) {
	s.mu.Lock()
	known, require := s.engineKnown, s.engineRequiresAlias
	s.mu.Unlock()
	if known {
		return require, nil
	}
	out, err := s.output(ctx, probeTimeout, docker, "info", "--format", "{{.OperatingSystem}}")
	if err != nil {
		if errors.Is(err, ports.ErrSandboxUnavailable) {
			return false, err
		}
		return false, fmt.Errorf("%w: identify the Docker engine: %w", ports.ErrSandboxMisconfigured, err)
	}
	opsys := string(out)
	require = strings.Contains(opsys, "Docker Desktop") || strings.Contains(opsys, "OrbStack")
	s.mu.Lock()
	s.engineKnown, s.engineRequiresAlias = true, require
	s.mu.Unlock()
	return require, nil
}

// isolateHostNetwork blocks the sandbox's routes to services on the host,
// such as AO's loopback API reached through host.docker.internal or Docker
// Desktop's proxy, and proves the daemon port is unreachable. A container
// restarted outside AO has no routes until the next Ensure; no agent process
// survives that restart, and restore always goes through Ensure, which runs
// this on every call.
func (s *Sandbox) isolateHostNetwork(ctx context.Context, docker, name string) error {
	require, err := s.requireHostAlias(ctx, docker)
	if err != nil {
		return err
	}
	requireArg := "0"
	if require {
		requireArg = "1"
	}
	out, err := s.output(ctx, createTimeout, docker, "run", "--rm", "--network", "container:"+name, "--cap-add", "NET_ADMIN",
		isolationImage, "/bin/sh", "-c", isolationScript, "sh", strconv.Itoa(s.cfg.daemonPort()), requireArg)
	if err == nil {
		return nil
	}
	if errors.Is(err, ports.ErrSandboxUnavailable) {
		return err
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 3:
			addr := "an address on your computer"
			for _, line := range strings.Split(string(out), "\n") {
				if a, ok := strings.CutPrefix(strings.TrimSpace(line), "REACHABLE "); ok && a != "" {
					addr = a
				}
			}
			return fmt.Errorf("%w: the sandbox can still reach Orchestrator's local API at %s; refusing to start", ports.ErrSandboxMisconfigured, addr)
		case 4:
			return fmt.Errorf("%w: could not resolve the Docker host address to block it; refusing to start", ports.ErrSandboxMisconfigured)
		}
	}
	if looksLikePullFailure(err) {
		return fmt.Errorf("%w: isolate sandbox network: %w (needs image %s)", ports.ErrSandboxMisconfigured, err, isolationImage)
	}
	// err carries the script's trimmed stderr (see execRunner).
	return fmt.Errorf("%w: isolate sandbox network: %w", ports.ErrSandboxMisconfigured, err)
}

// looksLikePullFailure reports a missing or unpullable helper image.
func looksLikePullFailure(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"unable to find image", "pull access denied", "manifest unknown", "error pulling image",
		"dial tcp", "tls handshake", "i/o timeout", "no such host", "registry-1.docker.io"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
