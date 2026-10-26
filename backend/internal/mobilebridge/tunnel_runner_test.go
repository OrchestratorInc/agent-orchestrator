package mobilebridge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
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
	_, src := newFixedSource(t)
	// The record exists already, as on every start after the first.
	if _, err := src.TunnelFor(context.Background(), "host-uuid", false); err != nil {
		t.Fatal(err)
	}
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
	if _, err := src.TunnelFor(context.Background(), "host-uuid", false); err != nil {
		t.Fatal(err)
	}
	cloud.set(http.StatusBadGateway)
	bin, _ := fakeCloudflared(t, registeredLine)

	if got := advertisedHostname(t, namedRunner(t, bin, src)); got != "h-test.example.dev" {
		t.Fatalf("advertised %q", got)
	}
}

func TestTunnelRunnerStopsAConnectorNobodyCanReach(t *testing.T) {
	cloud, src := newFixedSource(t)
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	// What Cloudflare serves for a deleted tunnel, while cloudflared idles on
	// as if connected. The first answer is a 200, so only a run of 530s counts.
	var probes atomic.Int32
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if probes.Add(1) > 1 {
			w.WriteHeader(530)
		}
	}))
	defer edge.Close()
	r.probeBase, r.probeEvery = edge.URL, 20*time.Millisecond

	// The connector would idle for 30s; the runner must not wait for it.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := r.runOnce(ctx)
	if ctx.Err() != nil || err == nil || !strings.Contains(err.Error(), "530") {
		t.Fatalf("runOnce = %v (ctx %v)", err, ctx.Err())
	}
	if n := probes.Load(); n != 1+namedProbeMisses {
		t.Fatalf("stopped after %d probes, want %d", n, 1+namedProbeMisses)
	}
	if last := r.Runtime.Snapshot().LastError; !strings.Contains(last, "530") {
		t.Fatalf("status error = %q", last)
	}
	// The next start asks AO Cloud to remake the tunnel.
	if _, err := r.Fixed.TunnelFor(ctx, r.HostID, r.repair); err != nil || !cloud.repairs[len(cloud.repairs)-1] {
		t.Fatalf("next start repairs = %v (%v)", cloud.repairs, err)
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
