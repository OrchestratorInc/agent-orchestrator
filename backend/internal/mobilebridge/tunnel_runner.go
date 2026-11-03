package mobilebridge

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"sync"
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
	// device links it, and a quick tunnel until then or while AO Cloud cannot
	// make it. A machine with a cached fixed tunnel starts that instead.
	Fixed  *FixedTunnelSource
	HostID string

	// repair asks AO Cloud for a replacement tunnel on the next start, after
	// the fixed one stopped being served, until AO Cloud has made one;
	// repairedAt is when it last did, and unhelped counts replacements made
	// since a connector last served. next is a tunnel AO Cloud already
	// handed out for the next start. Run is the only goroutine using them.
	repair     bool
	repairedAt time.Time
	unhelped   int
	next       *NamedTunnel
	// nudge makes a quick tunnel ask AO Cloud for the fixed address now.
	nudge <-chan struct{}

	deadAfter    time.Duration // zero uses connectorDeadAfter
	lostGrace    time.Duration // zero uses connectionLostGrace
	upgradeEvery time.Duration // zero uses fixedUpgradeInterval
	recheckEvery time.Duration // zero uses fixedRecheckInterval
	relinkWait   time.Duration // zero uses relinkGrace
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
		if errors.Is(err, errFixedTunnelReady) || errors.Is(err, errFixedTunnelChanged) {
			t.logger().Info("mobile tunnel restarting for its new address", "reason", err)
			continue
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
	switch {
	case t.Fixed == nil:
	case t.next != nil && !t.repair:
		// Fetched by the run that stopped to start it.
		named = *t.next
	default:
		// The client's namedProvisionTimeout is well under stableTunnelRun, so
		// a failing request never counts as a stable run that resets the
		// backoff ladder.
		// This start asks AO Cloud now, so a nudge from before it says nothing
		// new; one that comes while it asks is kept.
		select {
		case <-t.nudge:
		default:
		}
		var err error
		named, err = t.Fixed.TunnelFor(ctx, t.HostID, t.repair)
		// A replacement AO Cloud did not make, unheard or too soon after the
		// tunnel's last write, is asked for again at the next start.
		switch {
		case err == nil && t.repair && named.Repaired:
			t.repair, t.repairedAt = false, time.Now()
			t.unhelped++
		case unlinked(err):
			t.repair = false
		}
		switch {
		case named.RunToken != "" && err != nil && t.repair:
			// The cached tunnel just failed to serve and AO Cloud cannot replace
			// it: a quick tunnel serves until AO Cloud can.
			t.logger().Warn("the fixed tunnel failed and AO Cloud cannot replace it; starting a quick tunnel", "error", err)
			named = NamedTunnel{}
		case named.RunToken != "" && err != nil:
			t.logger().Warn("could not reach AO Cloud; starting the cached fixed tunnel", "error", err)
		case err != nil && !unlinked(err):
			// No fixed tunnel yet and AO Cloud cannot make one: a quick tunnel
			// serves, as before fixed addresses, and the machine keeps asking.
			t.logger().Warn("no fixed address yet; starting a quick tunnel", "error", err)
		}
	}
	t.next = nil
	fixed := named.RunToken != ""

	// A start that fails before the process exists still ends the run in the
	// status: a planned restart already shows it as starting.
	notStarted := func(err error) error {
		t.Runtime.Exited(err)
		return err
	}
	// Reserved after provisioning, which can take seconds, to keep the
	// bind-and-release window short.
	metricsPort, err := freeLoopbackPort()
	if err != nil {
		return notStarted(fmt.Errorf("reserve metrics port: %w", err))
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
		return notStarted(fmt.Errorf("stderr pipe: %w", err))
	}
	cmd.Stdout = io.Discard

	if err := cmd.Start(); err != nil {
		return notStarted(fmt.Errorf("start cloudflared: %w", err))
	}
	t.Runtime.Started()
	started := time.Now()
	pidOwner := newTunnelPIDOwner(t.PIDPath)

	// Record the pid before anything can go wrong, so a daemon that dies in the
	// next instant still leaves a trail to clean up.
	if err := pidOwner.claim(cmd.Process.Pid); err != nil {
		t.logger().Warn("could not record tunnel pid; a crash may orphan the connector", "error", err)
	}

	// Hold the endpoint back until its hostname has had time to propagate: DNS
	// for a brand-new quick tunnel, or a named tunnel's just-published record,
	// lags the connector's own "registered" signal. An older named record has
	// nothing left to wait for, including one AO Cloud just repointed at a new
	// tunnel: it never answered NXDOMAIN, devices already hold the address,
	// and the edge's brief lag in following it is a 530 they retry.
	//
	// Once advertised, a fixed address stays so while its connections are
	// down: it is still this machine's address, and the connector reconnects
	// or watchConnector restarts it. Withdrawing it would flap the QR code and
	// the account listing on every edge reconnect.
	if fixed {
		t.Runtime.NamedHostname(named.Hostname)
	}
	if fixed && time.Since(named.Since) >= t.settleDelay() {
		t.Runtime.Settled()
	} else {
		delay := t.settleDelay()
		if fixed {
			delay -= time.Since(named.Since) // what is left of the record's wait
		}
		go t.awaitSettled(runCtx, delay)
	}

	var upgraded, withdrawn atomic.Bool
	// unserved holds why a fixed connector looks unable to serve its tunnel.
	var unserved atomic.Pointer[string]
	// next is the tunnel a watcher stopped this run to start.
	var next atomic.Pointer[NamedTunnel]
	markUnserved := func(reason string) {
		unserved.CompareAndSwap(nil, &reason)
		stopRun()
	}
	var health *connectorHealth
	switch {
	case fixed:
		health = newConnectorHealth(time.Now())
		go t.watchConnector(runCtx, health, markUnserved)
		go t.watchFixedTunnel(runCtx, named, health, func(n *NamedTunnel) { next.Store(n); withdrawn.Store(true); stopRun() })
	case t.Fixed != nil:
		go t.awaitFixedTunnel(runCtx, t.repair, func(n NamedTunnel) { next.Store(&n); upgraded.Store(true); stopRun() })
	}

	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		t.Runtime.Line(line)
		t.logger().Debug("cloudflared", "line", line)
		if health != nil && health.feed(line, time.Now()) {
			markUnserved("cloudflare kept refusing the fixed tunnel's connector")
		}
	}

	waitErr := cmd.Wait()
	t.next = next.Load()
	if t.next != nil && t.next.Repaired {
		t.repair, t.repairedAt = false, time.Now()
		t.unhelped++
	}
	// A tunnel that served is not owed the replacement asked for before. Only
	// one that stayed up starts the backoff over: a connector that connects
	// and soon dies would otherwise ask for a replacement every repairSpacing.
	if health != nil && health.servedOnce() {
		t.repair = false
		if time.Since(started) >= stableTunnelRun {
			t.unhelped = 0
		}
	}
	// A fixed connector that stopped serving, refused, unconnected or gone,
	// gets a replacement tunnel at the same address, at most every
	// repairSpacing, doubling up to an hour for each replacement that did not
	// help: on a network that blocks Cloudflare's edge, or for a connector
	// that keeps crashing, a replacement every restart would spend the
	// account's tunnel writes for nothing.
	replace := func() {
		t.repair = t.repairedAt.IsZero() || time.Since(t.repairedAt) >= min(repairSpacing<<min(t.unhelped, 5), time.Hour)
	}
	switch {
	case withdrawn.Load():
		// A tunnel AO Cloud handed out instead goes before a refusal of the
		// old one, which would only ask for another.
		waitErr = errFixedTunnelChanged
	case unserved.Load() != nil:
		waitErr = errors.New(*unserved.Load())
		replace()
	case upgraded.Load():
		waitErr = errFixedTunnelReady
	case fixed && ctx.Err() == nil:
		replace() // exited by itself
	}
	if ctx.Err() == nil && (errors.Is(waitErr, errFixedTunnelReady) || errors.Is(waitErr, errFixedTunnelChanged) || (fixed && t.repair)) {
		// A planned restart, or a fixed connector's onto a replacement
		// tunnel, reads as starting, not failed: the next connector follows.
		// A quick tunnel standing in until AO Cloud can make one shows its
		// failures.
		t.Runtime.Started()
	} else {
		t.Runtime.Exited(waitErr)
	}
	// Only if this runner still owns the record: a replacement started while
	// this one was unwinding will already have written its own pid there.
	_ = pidOwner.release(cmd.Process.Pid)
	if waitErr != nil && ctx.Err() == nil {
		return waitErr
	}
	return errors.New("connector exited")
}

// watchConnector calls stop once a fixed connector has had no live edge
// connection for deadAfter, from its start or from when its last one
// dropped. A tunnel deleted while connected looks like this: measured, the
// connector printed only "Connection terminated" and "Retrying connection",
// never a refusal, and never registered again. A connector that cannot reach
// Cloudflare at all looks the same; stopping it then costs a restart.
func (t *TunnelRunner) watchConnector(ctx context.Context, h *connectorHealth, stop func(string)) {
	after := cmp.Or(t.deadAfter, connectorDeadAfter)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(after / 10):
		}
		if since, dead := h.deadSince(); dead && time.Since(since) >= after {
			stop("the fixed tunnel's connector had no live connection for " + after.String())
			return
		}
	}
}

// connectorHealth reads a fixed connector's output for the signs that its
// tunnel is gone. cloudflared has no API for this; these patterns are a
// contract with its log format, like TunnelLog's.
type connectorHealth struct {
	mu      sync.Mutex
	live    map[string]bool // by connIndex
	served  bool            // a connection registered
	downAt  time.Time       // zero while a connection is live
	refused int             // refusals in a row
	// lost is signalled when the last live connection drops.
	lost chan struct{}
}

func newConnectorHealth(start time.Time) *connectorHealth {
	return &connectorHealth{live: map[string]bool{}, downAt: start, lost: make(chan struct{}, 1)}
}

var (
	connIndexRe = regexp.MustCompile(`\bconnIndex=(\d+)`)
	connDownRe  = regexp.MustCompile(`\b(?:Connection terminated|Serve tunnel error|Unregistered tunnel connection|Retrying connection)\b`)
	// Cloudflare's edge refusing the run token: the tunnel was deleted or its
	// token replaced. The wording has varied across releases.
	refusedRe       = regexp.MustCompile(`\bRegister tunnel error from server side\b`)
	refusedReasonRe = regexp.MustCompile(`error="(?:Unauthorized:[^"]*|Failed to get tunnel|Tunnel not found|Record for tunnel not found|Invalid tunnel secret)"`)
)

// feed records one output line and reports whether the edge has refused the
// connector connectorRefusals times in a row. A new tunnel's token can be
// refused briefly while it propagates, so one refusal is not enough.
func (h *connectorHealth) feed(line string, now time.Time) (refused bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if refusedRe.MatchString(line) && refusedReasonRe.MatchString(line) {
		h.refused++
		return h.refused >= connectorRefusals
	}
	m := connIndexRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	switch {
	case registeredRe.MatchString(line):
		h.live[m[1]] = true
		h.served = true
		h.refused = 0
		h.downAt = time.Time{}
	case connDownRe.MatchString(line):
		delete(h.live, m[1])
		if len(h.live) == 0 && h.downAt.IsZero() {
			h.downAt = now
			select {
			case h.lost <- struct{}{}:
			default:
			}
		}
	}
	return false
}

// servedOnce reports whether a connection ever registered.
func (h *connectorHealth) servedOnce() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.served
}

// deadSince is when the connector last had no live connection, if it has none.
func (h *connectorHealth) deadSince() (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.downAt, !h.downAt.IsZero()
}

// A fixed connector is restarted on a replacement tunnel after it exits, has
// no live connection for three minutes, or is refused four times in a row; a
// replacement is asked for at most every two minutes. A tunnel the sweep
// released is remade without one.
const (
	connectorDeadAfter = 3 * time.Minute
	connectorRefusals  = 4
	repairSpacing      = 2 * time.Minute
)

// awaitFixedTunnel calls ready once AO Cloud hands out this host's tunnel,
// while a quick tunnel serves the machine: a signed-in device linked it since
// the connector started. Waiting for a link only reads a local file; asking
// AO Cloud backs off, except that a new link (a new account token) is tried
// at once. With repair set, the quick tunnel stands in for a fixed one that
// failed, and only a replacement AO Cloud made takes over from it.
func (t *TunnelRunner) awaitFixedTunnel(ctx context.Context, repair bool, ready func(NamedTunnel)) {
	every := cmp.Or(t.upgradeEvery, fixedUpgradeInterval)
	wait, tried, triedAt := every, "", time.Time{}
	// Standing in for a failed fixed tunnel, it asks at least every 5 minutes,
	// so the address is back soon after AO Cloud is.
	maxWait := maxFixedUpgradeWait
	if repair {
		maxWait = 5 * time.Minute
	}
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
		named, err := t.Fixed.TunnelFor(ctx, t.HostID, repair)
		if err == nil && named.RunToken != "" && (!repair || named.Repaired) {
			ready(named)
			return
		}
		if digest != tried {
			wait = every
		} else {
			wait = min(2*wait, maxWait)
		}
		tried, triedAt = digest, time.Now()
	}
}

// watchFixedTunnel calls withdrawn once AO Cloud stops handing out the tunnel
// this connector serves, with the tunnel it hands out instead, if any: the
// account removed the machine, its link was revoked by a new pairing
// password, or the tunnel was remade with a new token. Removal deletes the tunnel while it is connected, and AO Cloud is
// what says so. An unreachable AO Cloud is not a withdrawal.
//
// It asks at once when nudged: a device just linked the machine, possibly to
// another account, whose tunnel is another one, or a new password revoked the
// link. A signed-in desktop links the machine again right after a new
// password, so an unlinked machine waits up to relinkGrace for that link to
// reach AO Cloud before it gives up the address.
//
// It also asks as soon as every connection has been down for lostGrace. A
// machine offline for more than AO Cloud's five minutes has had its tunnel
// released, and asking is what makes AO Cloud give it a new one, so a laptop
// that wakes is back once Cloudflare routes the address to that tunnel. An
// edge restart reconnects within seconds and asks nothing.
func (t *TunnelRunner) watchFixedTunnel(ctx context.Context, serving NamedTunnel, health *connectorHealth, withdrawn func(*NamedTunnel)) {
	every, grace := cmp.Or(t.recheckEvery, fixedRecheckInterval), cmp.Or(t.lostGrace, connectionLostGrace)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		case <-t.nudge:
		case <-health.lost:
			select {
			case <-ctx.Done():
				return
			case <-time.After(grace):
			}
			if _, dead := health.deadSince(); !dead {
				continue
			}
		}
		named, err := t.Fixed.TunnelFor(ctx, t.HostID, false)
		// A new link is written here before it lands at AO Cloud, which
		// refuses it until then, and a machine waking from sleep can ask
		// before its network is back: either is asked again for a while. An
		// answer AO Cloud gave, such as 429 or 502, waits for the next check.
		relink := cmp.Or(t.relinkWait, relinkGrace)
		var unreachable *url.Error
		for deadline := time.Now().Add(relink); (unlinked(err) || errors.As(err, &unreachable)) && time.Now().Before(deadline); {
			select {
			case <-ctx.Done():
				return
			case <-time.After(relink / 24):
			}
			named, err = t.Fixed.TunnelFor(ctx, t.HostID, false)
		}
		if unlinked(err) {
			withdrawn(nil)
			return
		}
		if err == nil && (named.RunToken != serving.RunToken || named.Hostname != serving.Hostname) {
			withdrawn(&named)
			return
		}
	}
}

// fixedRecheckInterval spaces those checks: one AO Cloud call per machine
// every 30 minutes, and one more each time its connections all stay down for
// connectionLostGrace. relinkGrace outlasts the desktop's link: a directory
// read of up to 10 seconds and a registration of up to 75.
const (
	fixedRecheckInterval = 30 * time.Minute
	connectionLostGrace  = 10 * time.Second
	relinkGrace          = 2 * time.Minute
)

// Asking for the fixed address while a quick tunnel serves: every 30s, backing
// off to 30 minutes while AO Cloud cannot answer.
const (
	fixedUpgradeInterval = 30 * time.Second
	maxFixedUpgradeWait  = 30 * time.Minute
)

// awaitSettled holds the tunnel out of the endpoint list until its freshly
// issued hostname has had time to appear in DNS.
//
// A delay, not a probe. Measured against real cloudflared: dig began resolving
// a new quick tunnel hostname about twenty seconds after the connector
// registered — and querying it any earlier cached an NXDOMAIN locally that
// outlived propagation, leaving curl on this machine still failing thirty
// seconds after dig had started succeeding. A probe would also be testing the
// daemon's resolver rather than the phone's, which is the one that matters.
func (t *TunnelRunner) awaitSettled(ctx context.Context, delay time.Duration) {
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
	case <-time.After(delay):
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
