package mobilebridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// TunnelRunner supervises one managed cloudflared connector: spawn, stream its
// output into a TunnelRuntime, restart with backoff, and never leave the
// process behind.
//
// Deliberately thin. Every decision worth testing — which binary, when the
// tunnel may be advertised, how long to back off, whether a recorded pid is
// safe to kill — lives in tunnel.go and is unit tested there. What is left here
// is process plumbing, verified by running it against the real binary.
type TunnelRunner struct {
	Binary    string
	LocalPort int
	PIDPath   string
	Runtime   *TunnelRuntime
	Log       *slog.Logger
	// SettleDelay overrides how long after registration the hostname is
	// considered resolvable. Zero uses DefaultTunnelSettleDelay.
	SettleDelay time.Duration
	// Named, when set, runs this host's named tunnel instead of a quick tunnel.
	Named  *NamedTunnelConfig
	HostID string

	probeBase  string        // tests replace https://<hostname>
	probeEvery time.Duration // zero uses namedProbeInterval
}

// Run supervises the connector until ctx is cancelled. It blocks, so callers
// run it in a goroutine. Restarts are spaced by TunnelBackoff; a run that
// stayed up long enough to be useful resets the ladder.
func (t *TunnelRunner) Run(ctx context.Context) {
	attempt := 0
	for ctx.Err() == nil {
		startedAt := time.Now()
		err := t.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		// A connector that stayed up is not a failing one; only rapid exits
		// should escalate the wait.
		if time.Since(startedAt) >= stableTunnelRun {
			attempt = 0
		}
		wait := TunnelBackoff(attempt)
		attempt++
		t.logger().Warn("mobile tunnel exited; restarting",
			"error", err, "in", wait, "attempt", attempt)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// stableTunnelRun is how long a connector must survive before its exit is
// treated as a fresh fault rather than a continuing failure.
const stableTunnelRun = 60 * time.Second

// namedProvisionTimeout bounds all of one named tunnel provision's API calls.
// Every start provisions, and an unreachable API delays a cached start by this
// much, so it is short: a provision is three to five calls of under a second.
const namedProvisionTimeout = 15 * time.Second

func (t *TunnelRunner) runOnce(ctx context.Context) error {
	var named NamedTunnel
	if t.Named != nil {
		// Bounded well under stableTunnelRun, so a failing provision never
		// counts as a stable run that resets the backoff ladder.
		provisionCtx, cancel := context.WithTimeout(ctx, namedProvisionTimeout)
		var err error
		named, err = NamedTunnelFor(provisionCtx, *t.Named, t.HostID)
		cancel()
		if named.RunToken == "" {
			t.Runtime.Exited(err)
			return fmt.Errorf("provision named tunnel: %w", err)
		}
		if err != nil {
			t.logger().Warn("could not provision the named tunnel; starting the cached one", "error", err)
		}
	}

	// Reserved after provisioning, which can take seconds, to keep the
	// bind-and-release window short.
	metricsPort, err := freeLoopbackPort()
	if err != nil {
		return fmt.Errorf("reserve metrics port: %w", err)
	}

	// Its own context, so a connector nobody can reach any more can be stopped
	// without stopping the runner.
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	args := CloudflaredArgs(t.LocalPort, metricsPort)
	if t.Named != nil {
		args = NamedCloudflaredArgs(t.LocalPort, metricsPort)
	}
	cmd := aoprocess.CommandContext(runCtx, t.Binary, args...)
	if t.Named != nil {
		cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+named.RunToken)
	}
	// cloudflared logs to stderr; the hostname is only available there.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	cmd.Stdout = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cloudflared: %w", err)
	}
	t.Runtime.Started()
	pidOwner := newTunnelPIDOwner(t.PIDPath)

	// Record the pid before anything can go wrong, so a daemon that dies in the
	// next instant still leaves a trail to clean up.
	if err := pidOwner.claim(cmd.Process.Pid); err != nil {
		t.logger().Warn("could not record tunnel pid; a crash may orphan the connector", "error", err)
	}

	// Hold the endpoint back until its hostname has had time to propagate: DNS
	// for a brand-new quick tunnel, or a named tunnel's just-published record,
	// lags the connector's own "registered" signal. A named record published
	// on an earlier start has nothing left to wait for.
	settleCtx, stopSettle := context.WithCancel(ctx)
	defer stopSettle()
	if t.Named != nil {
		t.Runtime.NamedHostname(named.Hostname)
	}
	if t.Named != nil && !named.NewRecord {
		t.Runtime.Settled()
	} else {
		go t.awaitSettled(settleCtx)
	}

	var unserved atomic.Bool
	if t.Named != nil {
		go t.watchNamedAddress(runCtx, named.Hostname, func() { unserved.Store(true); stopRun() })
	}

	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		t.Runtime.Line(line)
		t.logger().Debug("cloudflared", "line", line)
	}

	waitErr := cmd.Wait()
	if unserved.Load() {
		waitErr = errors.New("cloudflare kept answering the named address with 530: no connector serves the tunnel")
	}
	t.Runtime.Exited(waitErr)
	// Only if this runner still owns the record: a replacement started while
	// this one was unwinding will already have written its own pid there.
	_ = pidOwner.release(cmd.Process.Pid)
	if waitErr != nil && ctx.Err() == nil {
		return waitErr
	}
	return errors.New("connector exited")
}

// watchNamedAddress calls stop once Cloudflare keeps answering this host's
// address with HTTP 530, its "no connector serves this tunnel" page. That is
// what a deleted tunnel or a replaced token looks like from outside, and
// nothing local shows it: measured with a tunnel deleted while connected,
// cloudflared kept running for minutes, printed only generic connection
// retries and still reported two ready connections. Stopping it lets the next
// start provision the tunnel again.
//
// Only 530 counts. A network error says nothing about the tunnel, and acting
// on it would restart a working connector whenever this machine is offline.
func (t *TunnelRunner) watchNamedAddress(ctx context.Context, hostname string, stop func()) {
	base, every := t.probeBase, t.probeEvery
	if base == "" {
		base = "https://" + hostname
	}
	if every == 0 {
		every = namedProbeInterval
	}
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/identity", nil)
		if err != nil {
			return
		}
		resp, err := namedProbeClient.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 530 {
			misses = 0
			continue
		}
		if misses++; misses >= namedProbeMisses {
			stop()
			return
		}
	}
}

// Probing the named address: about every 30s, and three 530s in a row.
const (
	namedProbeInterval = 30 * time.Second
	namedProbeMisses   = 3
)

var namedProbeClient = &http.Client{Timeout: 10 * time.Second}

// awaitSettled holds the tunnel out of the endpoint list until its freshly
// issued hostname has had time to appear in DNS.
//
// A delay, not a probe. Measured against real cloudflared: dig began resolving
// a new quick tunnel hostname about twenty seconds after the connector
// registered — and querying it any earlier cached an NXDOMAIN locally that
// outlived propagation, leaving curl on this machine still failing thirty
// seconds after dig had started succeeding. A probe would also be testing the
// daemon's resolver rather than the phone's, which is the one that matters.
func (t *TunnelRunner) awaitSettled(ctx context.Context) {
	for ctx.Err() == nil {
		if t.Runtime.UnsettledHostname() != "" {
			break
		}
		if t.Runtime.Endpoint() != nil {
			return // already settled
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(settlePollInterval):
		}
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(t.settleDelay()):
	}
	t.Runtime.Settled()
	t.logger().Info("mobile tunnel advertisable", "hostname", t.Runtime.Snapshot().Hostname)
}

func (t *TunnelRunner) settleDelay() time.Duration {
	if t.SettleDelay > 0 {
		return t.SettleDelay
	}
	return DefaultTunnelSettleDelay
}

const (
	// DefaultTunnelSettleDelay is how long after registration the hostname is
	// treated as resolvable.
	//
	// Measured propagation was ~20s on one network, and this was 25s to cover a
	// slower run. That margin is guesswork the daemon cannot improve on: probing
	// from the machine that owns the tunnel tests its own resolver, and probing
	// early caches an NXDOMAIN locally that outlives propagation.
	//
	// The phone now retries a tunnel candidate whose hostname does not resolve
	// yet (see the mobile probeRetry policy), which is where the resolver that
	// actually decides lives. With that absorbing the tail, this only has to
	// cover the common case, and the whole enable is quicker for it.
	DefaultTunnelSettleDelay = 12 * time.Second
	settlePollInterval       = 500 * time.Millisecond
)

func (t *TunnelRunner) logger() *slog.Logger {
	if t.Log != nil {
		return t.Log
	}
	return slog.Default()
}

// freeLoopbackPort reserves an ephemeral port by binding and releasing it.
// Inherently racy, but the metrics listener is loopback-only and a collision
// merely costs one restart.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %T", l.Addr())
	}
	return addr.Port, nil
}
