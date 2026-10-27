package mobilebridge

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Verbatim cloudflared stderr.
const registeredLine = `2026-08-26T20:07:40Z INF Registered tunnel connection connIndex=0 connection=7dc4ace9-1ce7-42f9-98a2-ecc14c6987d7 event=0 ip=2606:4700:a8::6 location=bom11 protocol=quic`

// fakeCloudflared is a connector that records its argv and run token, prints
// stderr, then idles like a connected cloudflared.
func fakeCloudflared(t *testing.T, stderr string) (bin, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake connector is a shell script")
	}
	dir := t.TempDir()
	bin, argsFile = filepath.Join(dir, "cloudflared"), filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" \"$TUNNEL_TOKEN\" > '" + argsFile + "'\ncat >&2 <<'EOF'\n" + stderr + "\nEOF\nexec sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { //nolint:gosec // G306: the test runs it
		t.Fatal(err)
	}
	return bin, argsFile
}

func namedRunner(t *testing.T, bin string, src *FixedTunnelSource) *TunnelRunner {
	t.Helper()
	// An hour of settling proves an advertised address did not wait for it.
	return &TunnelRunner{
		Binary: bin, LocalPort: 4321, PIDPath: filepath.Join(t.TempDir(), "tunnel.pid"),
		Runtime: &TunnelRuntime{}, Fixed: src, HostID: "host-uuid", SettleDelay: time.Hour,
	}
}

// advertisedHostname runs one connector until it is advertised, then stops it.
func advertisedHostname(t *testing.T, r *TunnelRunner) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); r.Runtime.Endpoint() == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never advertised: %+v", r.Runtime.Snapshot())
		}
	}
	return r.Runtime.Endpoint().Hostname
}

func readArgs(t *testing.T, argsFile string) (argv, token string) {
	t.Helper()
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	argv, token, _ = strings.Cut(strings.TrimSpace(string(b)), "\n")
	return argv, strings.TrimSpace(token)
}

func TestTunnelRunnerAdvertisesAKnownFixedAddressAtOnce(t *testing.T) {
	cloud, src := newFixedSource(t)
	knownTunnel(t, cloud, src)
	bin, argsFile := fakeCloudflared(t, registeredLine)

	if got := advertisedHostname(t, namedRunner(t, bin, src)); got != "h-test.example.dev" {
		t.Fatalf("advertised %q", got)
	}
	argv, token := readArgs(t, argsFile)
	if !strings.Contains(argv, "--url http://127.0.0.1:4321") || !strings.Contains(argv, "--config "+os.DevNull) ||
		!strings.HasSuffix(argv, " run") || token != "run-token-tun-1" {
		t.Fatalf("argv %q, token %q", argv, token)
	}
}

func TestTunnelRunnerStartsTheCachedTunnelWhenAOCloudIsDown(t *testing.T) {
	cloud, src := newFixedSource(t)
	knownTunnel(t, cloud, src)
	cloud.set(http.StatusBadGateway)
	bin, _ := fakeCloudflared(t, registeredLine)

	if got := advertisedHostname(t, namedRunner(t, bin, src)); got != "h-test.example.dev" {
		t.Fatalf("advertised %q", got)
	}
}

// Verbatim cloudflared stderr after its tunnel was deleted while connected
// (measured): the connection drops and retries, and nothing is refused.
const droppedLines = `2026-10-26T06:46:58Z ERR Serve tunnel error error="control stream encountered a failure while serving" connIndex=0 event=0 ip=198.41.200.13
2026-10-26T06:46:58Z INF Retrying connection in up to 16s connIndex=0 event=0 ip=198.41.200.13`

// The edge refusing a deleted tunnel's token, as cloudflared words it.
const refusedLine = `2026-10-06T12:00:00Z ERR Register tunnel error from server side error="Unauthorized: Tunnel not found" connIndex=0 event=0 ip=198.41.200.23`

// runUntilStopped runs one fixed connector and returns why the runner stopped it.
func runUntilStopped(t *testing.T, stderr string, deadAfter time.Duration) (*TunnelRunner, *fakeAOCloud, error) {
	t.Helper()
	cloud, src := newFixedSource(t)
	bin, _ := fakeCloudflared(t, stderr)
	r := namedRunner(t, bin, src)
	r.deadAfter = deadAfter
	// The connector would idle for 30s; the runner must not wait for it.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := r.runOnce(ctx)
	if ctx.Err() != nil {
		t.Fatalf("runner never stopped the connector: %v", err)
	}
	return r, cloud, err
}

func TestTunnelRunnerRepairsATunnelThatLostEveryConnection(t *testing.T) {
	r, cloud, err := runUntilStopped(t, registeredLine+"\n"+droppedLines, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no live connection") || !r.repair {
		t.Fatalf("runOnce = %v, repair %v", err, r.repair)
	}
	if last := r.Runtime.Snapshot().LastError; !strings.Contains(last, "no live connection") {
		t.Fatalf("status error = %q", last)
	}
	// The next start asks AO Cloud to remake the tunnel.
	if _, err := r.Fixed.TunnelFor(context.Background(), r.HostID, r.repair); err != nil || !cloud.repairs[len(cloud.repairs)-1] {
		t.Fatalf("next start repairs = %v (%v)", cloud.repairs, err)
	}
}

func TestTunnelRunnerRepairsARefusedTunnel(t *testing.T) {
	refusals := strings.Repeat(refusedLine+"\n", connectorRefusals)
	// Only the refusals can stop it inside the test's time.
	r, _, err := runUntilStopped(t, registeredLine+"\n"+refusals, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "refusing") || !r.repair {
		t.Fatalf("runOnce = %v, repair %v", err, r.repair)
	}
}

// A laptop that slept past AO Cloud's five minutes finds its tunnel released:
// once its connections stay down it asks AO Cloud, gets the new tunnel and
// restarts on it, without waiting for the three-minute rule.
func TestTunnelRunnerAsksForItsTunnelOnceEveryConnectionStaysDown(t *testing.T) {
	cloud, src := newFixedSource(t)
	cloud.tokens = []string{"run-token-tun-1", "run-token-tun-2"}
	bin, _ := fakeCloudflared(t, registeredLine+"\n"+droppedLines)
	r := namedRunner(t, bin, src)
	r.deadAfter, r.recheckEvery, r.lostGrace = time.Hour, time.Hour, 20*time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.runOnce(ctx); !errors.Is(err, errFixedTunnelChanged) || r.repair {
		t.Fatalf("runOnce = %v, repair %v; want a restart on the new tunnel", err, r.repair)
	}
	if n := cloud.requests(); n != 2 {
		t.Fatalf("asked AO Cloud %d times, want once at start and once after the drop", n)
	}
	// The next start serves the tunnel that answer handed out, and the
	// restart never reads as a failure.
	if r.next == nil || r.next.RunToken != "run-token-tun-2" {
		t.Fatalf("next = %+v", r.next)
	}
	if snap := r.Runtime.Snapshot(); !snap.Running || snap.LastError != "" {
		t.Fatalf("restarting status = %+v", snap)
	}
}

// A repair asked for while AO Cloud cannot be reached is asked for again at
// the next start, not dropped.
func TestTunnelRunnerKeepsARepairAOCloudNeverHeard(t *testing.T) {
	cloud, src := newFixedSource(t)
	knownTunnel(t, cloud, src)
	cloud.set(http.StatusBadGateway)
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.repair = true
	advertisedHostname(t, r) // the cached tunnel serves meanwhile
	if !r.repair {
		t.Fatal("a repair AO Cloud never heard was dropped")
	}
	cloud.set(0)
	advertisedHostname(t, r)
	if r.repair || len(cloud.repairs) != 2 || !cloud.repairs[1] {
		t.Fatalf("repair %v, requests %v", r.repair, cloud.repairs)
	}
}

// An edge restart drops a connection and registers it again within seconds.
func TestTunnelRunnerDoesNotAskWhenAConnectionComesBack(t *testing.T) {
	cloud, src := newFixedSource(t)
	knownTunnel(t, cloud, src)
	bin, _ := fakeCloudflared(t, registeredLine+"\n"+droppedLines+"\n"+registeredLine)
	r := namedRunner(t, bin, src)
	r.deadAfter, r.recheckEvery, r.lostGrace = time.Hour, time.Hour, 50*time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); r.Runtime.Endpoint() == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("never advertised")
		}
	}
	time.Sleep(300 * time.Millisecond)
	if n := cloud.requests(); n != 1 {
		t.Fatalf("asked AO Cloud %d times after a connection came back", n)
	}
}

func TestConnectorHealthIgnoresWhatAWorkingTunnelPrints(t *testing.T) {
	start := time.Now()
	h := newConnectorHealth(start)
	if since, dead := h.deadSince(); !dead || !since.Equal(start) {
		t.Fatal("a connector that has not registered yet must count as dead from its start")
	}
	second := strings.Replace(registeredLine, "connIndex=0", "connIndex=1", 1)
	for _, line := range []string{registeredLine, second, droppedLines[:strings.Index(droppedLines, "\n")]} {
		h.feed(line, time.Now())
	}
	// One connection dropping while another serves is an ordinary edge restart.
	if _, dead := h.deadSince(); dead {
		t.Fatal("dead while connection 1 is live")
	}
	// Refusals while a new token propagates are absorbed until enough come in a row.
	for i := 1; i < connectorRefusals; i++ {
		if h.feed(refusedLine, time.Now()) {
			t.Fatalf("stopped after %d refusals", i)
		}
	}
	h.feed(registeredLine, time.Now())
	if h.feed(refusedLine, time.Now()) {
		t.Fatal("a registration did not reset the refusal count")
	}
}

func TestTunnelRunnerMovesAQuickTunnelToTheFixedAddressOnceLinked(t *testing.T) {
	cloud, src := newFixedSource(t)
	link(t, src, "") // not linked yet: a quick tunnel, and no call to AO Cloud
	bin, argsFile := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.upgradeEvery = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(argsFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("quick tunnel never started")
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := cloud.requests(); n != 0 {
		t.Fatalf("an unlinked machine asked AO Cloud %d times", n)
	}
	if argv, token := readArgs(t, argsFile); strings.HasSuffix(argv, " run") || token != "" {
		t.Fatalf("not a quick tunnel: argv %q, token %q", argv, token)
	}

	link(t, src, "account-digest") // a signed-in device links the machine
	if err := <-done; !errors.Is(err, errFixedTunnelReady) {
		t.Fatalf("runOnce = %v, want the switch to the fixed address", err)
	}
	// Between the two connectors the tunnel reads as starting, so no client
	// takes the switch for a failure.
	if snap := r.Runtime.Snapshot(); !snap.Running || snap.LastError != "" {
		t.Fatalf("switching status = %+v", snap)
	}

	// The check that found the address cached it, but its record is seconds
	// old: the fixed connector still waits out the settle delay.
	go func() { done <- r.runOnce(ctx) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); r.Runtime.UnsettledHostname() != "h-test.example.dev"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("fixed connector never registered: %+v", r.Runtime.Snapshot())
		}
	}
	if ep := r.Runtime.Endpoint(); ep != nil {
		t.Fatalf("advertised %q before its new record settled", ep.Hostname)
	}
	// It started on the answer that found the address, without asking again.
	if n := cloud.requests(); n != 1 {
		t.Fatalf("asked AO Cloud %d times for one switch", n)
	}
}

func TestTunnelRunnerDropsAFixedAddressAOCloudWithdrew(t *testing.T) {
	cloud, src := newFixedSource(t)
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.recheckEvery = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	// Still handed out: the connector keeps running.
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("stopped while AO Cloud still hands out the tunnel: %v", err)
	default:
	}
	// Every account removed the machine.
	cloud.set(http.StatusUnauthorized)
	if err := <-done; !errors.Is(err, errFixedTunnelChanged) {
		t.Fatalf("runOnce = %v, want the withdrawn fixed address", err)
	}
}

func TestTunnelRunnerTriesANewLinkAtOnce(t *testing.T) {
	cloud, src := newFixedSource(t)
	cloud.set(http.StatusUnauthorized) // linked once, then removed from the account
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.upgradeEvery = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	// Refused, the quick tunnel backs off instead of asking every tick.
	time.Sleep(400 * time.Millisecond)
	if n := cloud.requests(); n < 2 || n > 8 {
		t.Fatalf("asked AO Cloud %d times while refused", n)
	}
	// Linked again with a new account token: tried at the next tick.
	cloud.set(0)
	link(t, src, "new-account-digest")
	select {
	case err := <-done:
		if !errors.Is(err, errFixedTunnelReady) {
			t.Fatalf("runOnce = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a new link waited out the backoff")
	}
}

func TestTunnelRunnerMovesToTheFixedAddressWhenNudged(t *testing.T) {
	_, src := newFixedSource(t)
	link(t, src, "")
	bin, argsFile := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.upgradeEvery = time.Hour // only the nudge can wake it
	nudge := make(chan struct{}, 1)
	r.nudge = nudge

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(argsFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("quick tunnel never started")
		}
	}
	link(t, src, "account-digest")
	nudge <- struct{}{}
	select {
	case err := <-done:
		if !errors.Is(err, errFixedTunnelReady) {
			t.Fatalf("runOnce = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the nudge did not move the tunnel")
	}
}
