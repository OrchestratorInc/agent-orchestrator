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

// isolationScript blackholes the host aliases and probes the daemon port;
// exit 3 means the port is still reachable.
//
//go:embed scripts/isolate-network
var isolationScript string

// isolateHostNetwork blocks the sandbox's routes to services on the host,
// such as AO's loopback API reached through host.docker.internal, and proves
// the daemon port is unreachable. A container restart drops the routes, so
// Ensure runs this on every call.
func (s *Sandbox) isolateHostNetwork(ctx context.Context, docker, name string) error {
	out, err := s.output(ctx, createTimeout, docker, "run", "--rm", "--network", "container:"+name, "--cap-add", "NET_ADMIN",
		isolationImage, "/bin/sh", "-c", isolationScript, "sh", strconv.Itoa(s.cfg.DaemonPort))
	if err == nil {
		return nil
	}
	if errors.Is(err, ports.ErrSandboxUnavailable) {
		return err
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		addr := "an address on your computer"
		for _, line := range strings.Split(string(out), "\n") {
			if a, ok := strings.CutPrefix(strings.TrimSpace(line), "REACHABLE "); ok && a != "" {
				addr = a
			}
		}
		return fmt.Errorf("%w: the sandbox can still reach Orchestrator's local API at %s; refusing to start", ports.ErrSandboxMisconfigured, addr)
	}
	return fmt.Errorf("%w: isolate sandbox network: %w (needs image %s)", ports.ErrSandboxMisconfigured, err, isolationImage)
}
