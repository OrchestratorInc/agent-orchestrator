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
	// Fixed, when set, serves this host's fixed address once a signed-in
	// device links it, and a quick tunnel until then or while AO Cloud
	// cannot hand out the tunnel.
	Fixed  *FixedTunnelSource
	HostID string

	// repair asks AO Cloud to remake the tunnel on the next start, after the
	// fixed address stopped being served. Run is the only goroutine using it.
	repair bool
	// nudge makes a quick tunnel ask AO Cloud for the fixed address now.
	nudge <-chan struct{}

	probeBase    string        // tests replace https://<hostname>
	probeEvery   time.Duration // zero uses namedProbeInterval
	upgradeEvery time.Duration // zero uses fixedUpgradeInterval
	recheckEvery time.Duration // zero uses fixedRecheckInterval
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
		if errors.Is(err, errFixedTunnelReady) || errors.Is(err, errFixedTunnelChanged) {
			t.logger().Info("mobile tunnel restarting for its new address", "reason", err)
			continue
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

// namedProvisionTimeout bounds one request for the fixed tunnel. Every start
// asks AO Cloud, and an unreachable AO Cloud delays a cached start by this
// much, so it is short. A first request also has AO Cloud make the tunnel,
// three to five Cloudflare calls of under a second.
const namedProvisionTimeout = 15 * time.Second

var (
	// errFixedTunnelReady stops a quick tunnel because the fixed address can
	// be served now.
	errFixedTunnelReady = errors.New("fixed address ready")
	// errFixedTunnelChanged stops a fixed tunnel AO Cloud no longer hands out.
	errFixedTunnelChanged = errors.New("AO Cloud no longer hands out this fixed address")
)

func (t *TunnelRunner) runOnce(ctx context.Context) error {
	var named NamedTunnel
	if t.Fixed != nil {
		// Bounded well under stableTunnelRun, so a failing request never
		// counts as a stable run that resets the backoff ladder.
		provisionCtx, cancel := context.WithTimeout(ctx, namedProvisionTimeout)
		var err error
		named, err = t.Fixed.TunnelFor(provisionCtx, t.HostID, t.repair)
		cancel()
		t.repair = false
		switch {
		case named.RunToken != "" && err != nil:
			t.logger().Warn("could not reach AO Cloud; starting the cached fixed tunnel", "error", err)
		case named.RunToken == "" && !errors.Is(err, errNotLinked):
			t.logger().Warn("no fixed address for this machine; starting a quick tunnel", "error", err)
		}
	}
	fixed := named.RunToken != ""

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
	if fixed {
		args = NamedCloudflaredArgs(t.LocalPort, metricsPort)
	}
	cmd := aoprocess.CommandContext(runCtx, t.Binary, args...)
	if fixed {
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
	// lags the connector's own "registered" signal. A named record this
	// machine has known for longer than that has nothing left to wait for.
	settleCtx, stopSettle := context.WithCancel(ctx)
	defer stopSettle()
	if fixed {
		t.Runtime.NamedHostname(named.Hostname)
	}
	if fixed && time.Since(named.Since) >= t.settleDelay() {
		t.Runtime.Settled()
	} else {
		go t.awaitSettled(settleCtx)
	}

	var unserved, upgraded, withdrawn atomic.Bool
	switch {
	case fixed:
		go t.watchNamedAddress(runCtx, named.Hostname, func() { unserved.Store(true); stopRun() })
		go t.watchFixedTunnel(runCtx, named, func() { withdrawn.Store(true); stopRun() })
	case t.Fixed != nil:
		go t.awaitFixedTunnel(runCtx, func() { upgraded.Store(true); stopRun() })
	}

	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		t.Runtime.Line(line)
		t.logger().Debug("cloudflared", "line", line)
	}

	waitErr := cmd.Wait()
	switch {
	case unserved.Load():
		waitErr = errors.New("cloudflare kept answering the fixed address with 530: no connector serves the tunnel")
		t.repair = true
	case upgraded.Load():
		waitErr = errFixedTunnelReady
	case withdrawn.Load():
		waitErr = errFixedTunnelChanged
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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/identity", http.NoBody)
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
		misses++
		if misses >= namedProbeMisses {
			stop()
			return
		}
	}
}

// awaitFixedTunnel calls ready once AO Cloud hands out this host's tunnel,
// while a quick tunnel serves the machine: a signed-in device linked it since
// the connector started, or AO Cloud was unreachable then. The quick tunnel
// keeps running until the fixed one can replace it, so a failing AO Cloud
// never costs a working address. Waiting for a link only reads a local file;
// asking AO Cloud backs off, except that a new link (a new account token)
// is tried at once.
func (t *TunnelRunner) awaitFixedTunnel(ctx context.Context, ready func()) {
	every := t.upgradeEvery
	if every == 0 {
		every = fixedUpgradeInterval
	}
	wait, tried, triedAt := every, "", time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		case <-t.nudge:
			tried = ""
		}
		digest := t.Fixed.digest()
		if digest == "" || (digest == tried && time.Since(triedAt) < wait) {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, namedProvisionTimeout)
		named, err := t.Fixed.TunnelFor(requestCtx, t.HostID, false)
		cancel()
		if err == nil && named.RunToken != "" {
			ready()
			return
		}
		if digest != tried {
			wait = every
		} else if wait = 2 * wait; wait > maxFixedUpgradeWait {
			wait = maxFixedUpgradeWait
		}
		tried, triedAt = digest, time.Now()
	}
}

// watchFixedTunnel calls withdrawn once AO Cloud stops handing out the tunnel
// this connector serves: every account removed the machine, its link was
// revoked by a new pairing password, or the tunnel was remade with a new
// token. Removal deletes the DNS record too, so the address stops answering
// at all rather than with 530 and watchNamedAddress cannot see it. An
// unreachable AO Cloud is not a withdrawal.
func (t *TunnelRunner) watchFixedTunnel(ctx context.Context, serving NamedTunnel, withdrawn func()) {
	every := t.recheckEvery
	if every == 0 {
		every = fixedRecheckInterval
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
		requestCtx, cancel := context.WithTimeout(ctx, namedProvisionTimeout)
		named, err := t.Fixed.TunnelFor(requestCtx, t.HostID, false)
		cancel()
		if errors.Is(err, errHostRefused) || errors.Is(err, errNotLinked) ||
			(err == nil && (named.RunToken != serving.RunToken || named.Hostname != serving.Hostname)) {
			withdrawn()
			return
		}
	}
}

// fixedRecheckInterval spaces those checks: one AO Cloud call per machine
// every 30 minutes.
const fixedRecheckInterval = 30 * time.Minute

// Asking for the fixed address while a quick tunnel serves: every 30s, backing
// off to 30 minutes while AO Cloud cannot answer.
const (
	fixedUpgradeInterval = 30 * time.Second
	maxFixedUpgradeWait  = 30 * time.Minute
)

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
