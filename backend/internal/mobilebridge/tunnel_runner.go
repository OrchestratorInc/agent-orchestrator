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

type TunnelRunner struct {
	Binary      string
	LocalPort   int
	PIDPath     string
	Runtime     *TunnelRuntime
	Log         *slog.Logger
	SettleDelay time.Duration
	Fixed       *FixedTunnelSource
	HostID      string

	repair     bool
	repairedAt time.Time
	unhelped   int
	next       *NamedTunnel
	nudge      <-chan struct{}

	deadAfter    time.Duration
	lostGrace    time.Duration
	upgradeEvery time.Duration
	recheckEvery time.Duration
	relinkWait   time.Duration
}

func (t *TunnelRunner) Run(ctx context.Context) {
	attempt := 0
	for ctx.Err() == nil {
		startedAt := time.Now()
		err := t.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
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

const stableTunnelRun = 60 * time.Second

const namedProvisionTimeout = 15 * time.Second

var (
	errFixedTunnelReady   = errors.New("fixed address ready")
	errFixedTunnelChanged = errors.New("this fixed address is no longer handed out by Orchestrator Cloud")
)

func (t *TunnelRunner) runOnce(ctx context.Context) error {
	var named NamedTunnel
	switch {
	case t.Fixed == nil:
	case t.next != nil && !t.repair:
		named = *t.next
	default:
		select {
		case <-t.nudge:
		default:
		}
		var err error
		named, err = t.Fixed.TunnelFor(ctx, t.HostID, t.repair)
		switch {
		case err == nil && t.repair && named.Repaired:
			t.repair, t.repairedAt = false, time.Now()
			t.unhelped++
		case unlinked(err):
			t.repair = false
		}
		switch {
		case named.RunToken != "" && err != nil && t.repair:
			t.logger().Warn("the fixed tunnel failed and AO Cloud cannot replace it; starting a quick tunnel", "error", err)
			named = NamedTunnel{}
		case named.RunToken != "" && err != nil:
			t.logger().Warn("could not reach AO Cloud; starting the cached fixed tunnel", "error", err)
		case err != nil && !unlinked(err):
			t.logger().Warn("no fixed address yet; starting a quick tunnel", "error", err)
		}
	}
	t.next = nil
	fixed := named.RunToken != ""

	notStarted := func(err error) error {
		t.Runtime.Exited(err)
		return err
	}
	metricsPort, err := freeLoopbackPort()
	if err != nil {
		return notStarted(fmt.Errorf("reserve metrics port: %w", err))
	}

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

	if err := pidOwner.claim(cmd.Process.Pid); err != nil {
		t.logger().Warn("could not record tunnel pid; a crash may orphan the connector", "error", err)
	}

	if fixed {
		t.Runtime.NamedHostname(named.Hostname)
	}
	if fixed && time.Since(named.Since) >= t.settleDelay() {
		t.Runtime.Settled()
	} else {
		delay := t.settleDelay()
		if fixed {
			delay -= time.Since(named.Since)
		}
		go t.awaitSettled(runCtx, delay)
	}

	var upgraded, withdrawn atomic.Bool
	var unserved atomic.Pointer[string]
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
	if health != nil && health.servedOnce() {
		t.repair = false
		if time.Since(started) >= stableTunnelRun {
			t.unhelped = 0
		}
	}
	replace := func() {
		t.repair = t.repairedAt.IsZero() || time.Since(t.repairedAt) >= min(repairSpacing<<min(t.unhelped, 5), time.Hour)
	}
	switch {
	case withdrawn.Load():
		waitErr = errFixedTunnelChanged
	case unserved.Load() != nil:
		waitErr = errors.New(*unserved.Load())
		replace()
	case upgraded.Load():
		waitErr = errFixedTunnelReady
	case fixed && ctx.Err() == nil:
		replace()
	}
	if ctx.Err() == nil && (errors.Is(waitErr, errFixedTunnelReady) || errors.Is(waitErr, errFixedTunnelChanged) || (fixed && t.repair)) {
		t.Runtime.Started()
	} else {
		t.Runtime.Exited(waitErr)
	}
	_ = pidOwner.release(cmd.Process.Pid)
	if waitErr != nil && ctx.Err() == nil {
		return waitErr
	}
	return errors.New("connector exited")
}

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

type connectorHealth struct {
	mu      sync.Mutex
	live    map[string]bool
	served  bool
	downAt  time.Time
	refused int
	lost    chan struct{}
}

func newConnectorHealth(start time.Time) *connectorHealth {
	return &connectorHealth{live: map[string]bool{}, downAt: start, lost: make(chan struct{}, 1)}
}

var (
	connIndexRe     = regexp.MustCompile(`\bconnIndex=(\d+)`)
	connDownRe      = regexp.MustCompile(`\b(?:Connection terminated|Serve tunnel error|Unregistered tunnel connection|Retrying connection)\b`)
	refusedRe       = regexp.MustCompile(`\bRegister tunnel error from server side\b`)
	refusedReasonRe = regexp.MustCompile(`error="(?:Unauthorized:[^"]*|Failed to get tunnel|Tunnel not found|Record for tunnel not found|Invalid tunnel secret)"`)
)

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

func (h *connectorHealth) servedOnce() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.served
}

func (h *connectorHealth) deadSince() (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.downAt, !h.downAt.IsZero()
}

const (
	connectorDeadAfter = 3 * time.Minute
	connectorRefusals  = 4
	repairSpacing      = 2 * time.Minute
)

func (t *TunnelRunner) awaitFixedTunnel(ctx context.Context, repair bool, ready func(NamedTunnel)) {
	every := cmp.Or(t.upgradeEvery, fixedUpgradeInterval)
	wait, tried, triedAt := every, "", time.Time{}
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

const (
	fixedRecheckInterval = 30 * time.Minute
	connectionLostGrace  = 10 * time.Second
	relinkGrace          = 2 * time.Minute
)

const (
	fixedUpgradeInterval = 30 * time.Second
	maxFixedUpgradeWait  = 30 * time.Minute
)

func (t *TunnelRunner) awaitSettled(ctx context.Context, delay time.Duration) {
	for ctx.Err() == nil {
		if t.Runtime.UnsettledHostname() != "" {
			break
		}
		if t.Runtime.Endpoint() != nil {
			return
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
	DefaultTunnelSettleDelay = 12 * time.Second
	settlePollInterval       = 500 * time.Millisecond
)

func (t *TunnelRunner) logger() *slog.Logger {
	if t.Log != nil {
		return t.Log
	}
	return slog.Default()
}

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
