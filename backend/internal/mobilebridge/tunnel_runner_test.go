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

const registeredLine = `2026-08-26T20:07:40Z INF Registered tunnel connection connIndex=0 connection=7dc4ace9-1ce7-42f9-98a2-ecc14c6987d7 event=0 ip=2606:4700:a8::6 location=bom11 protocol=quic`

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

func exitingCloudflared(t *testing.T, stderr string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake connector is a shell script")
	}
	bin := filepath.Join(t.TempDir(), "cloudflared")
	script := "#!/bin/sh\ncat >&2 <<'EOF'\n" + stderr + "\nEOF\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { //nolint:gosec // G306: the test runs it
		t.Fatal(err)
	}
	return bin
}

func namedRunner(t *testing.T, bin string, src *FixedTunnelSource) *TunnelRunner {
	t.Helper()
	return &TunnelRunner{
		Binary: bin, LocalPort: 4321, PIDPath: filepath.Join(t.TempDir(), "tunnel.pid"),
		Runtime: &TunnelRuntime{}, Fixed: src, HostID: "host-uuid", SettleDelay: time.Hour,
	}
}

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
	if !strings.Contains(argv, "--url http://127.0.0.1:4321") ||
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

const droppedLines = `2026-10-26T06:46:58Z ERR Serve tunnel error error="control stream encountered a failure while serving" connIndex=0 event=0 ip=198.41.200.13
2026-10-26T06:46:58Z INF Retrying connection in up to 16s connIndex=0 event=0 ip=198.41.200.13`

const refusedLine = `2026-10-06T12:00:00Z ERR Register tunnel error from server side error="Unauthorized: Tunnel not found" connIndex=0 event=0 ip=198.41.200.23`

func runUntilStopped(t *testing.T, stderr string, deadAfter time.Duration) (*TunnelRunner, *fakeAOCloud, error) {
	t.Helper()
	cloud, src := newFixedSource(t)
	bin, _ := fakeCloudflared(t, stderr)
	r := namedRunner(t, bin, src)
	r.deadAfter = deadAfter
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
	if snap := r.Runtime.Snapshot(); !snap.Running || snap.LastError != "" {
		t.Fatalf("status = %+v", snap)
	}
	if _, err := r.Fixed.TunnelFor(context.Background(), r.HostID, r.repair); err != nil || !cloud.repairs[len(cloud.repairs)-1] {
		t.Fatalf("next start repairs = %v (%v)", cloud.repairs, err)
	}
}

func TestTunnelRunnerSpacesRepairsOut(t *testing.T) {
	_, src := newFixedSource(t)
	bin, _ := fakeCloudflared(t, registeredLine+"\n"+droppedLines)
	r := namedRunner(t, bin, src)
	r.deadAfter = 100 * time.Millisecond
	r.repairedAt = time.Now().Add(-time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.runOnce(ctx); err == nil || !strings.Contains(err.Error(), "no live connection") || r.repair {
		t.Fatalf("runOnce = %v, repair %v", err, r.repair)
	}
	r.repairedAt = time.Now().Add(-time.Hour)
	if err := r.runOnce(ctx); err == nil || !r.repair {
		t.Fatalf("runOnce = %v, repair %v; want a repair an hour on", err, r.repair)
	}
}

func TestTunnelRunnerRepairsARefusedTunnel(t *testing.T) {
	refusals := strings.Repeat(refusedLine+"\n", connectorRefusals)
	r, _, err := runUntilStopped(t, registeredLine+"\n"+refusals, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "refusing") || !r.repair {
		t.Fatalf("runOnce = %v, repair %v", err, r.repair)
	}
	if snap := r.Runtime.Snapshot(); !snap.Running || snap.LastError != "" {
		t.Fatalf("status after a refusal = %+v", snap)
	}
}

func TestTunnelRunnerReplacesAConnectorThatExits(t *testing.T) {
	for _, stderr := range []string{refusedLine, `2026-10-28T12:00:00Z ERR Provided Tunnel token is not valid.`} {
		_, src := newFixedSource(t)
		r := namedRunner(t, exitingCloudflared(t, stderr), src)
		if err := r.runOnce(context.Background()); err == nil || !r.repair {
			t.Fatalf("%q: runOnce = %v, repair %v", stderr, err, r.repair)
		}
	}
}

func TestTunnelRunnerServesAQuickTunnelUntilAOCloudCanMakeTheFixedOne(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusTooManyRequests} {
		cloud, src := newFixedSource(t)
		cloud.set(status)
		bin, argsFile := fakeCloudflared(t, registeredLine)
		r := namedRunner(t, bin, src)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- r.runOnce(ctx) }()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(argsFile); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d: no connector started", status)
			}
		}
		if argv, token := readArgs(t, argsFile); strings.HasSuffix(argv, " run") || token != "" {
			t.Fatalf("%d: argv %q, token %q", status, argv, token)
		}
		cancel()
		<-done
	}
}

func TestTunnelRunnerServesAQuickTunnelWhereAOCloudHasNoFixedAddresses(t *testing.T) {
	cloud, src := newFixedSource(t)
	cloud.set(http.StatusNotFound)
	bin, argsFile := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(argsFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no connector started")
		}
	}
	if argv, token := readArgs(t, argsFile); strings.HasSuffix(argv, " run") || token != "" {
		t.Fatalf("argv %q, token %q", argv, token)
	}
}

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
	if r.next == nil || r.next.RunToken != "run-token-tun-2" {
		t.Fatalf("next = %+v", r.next)
	}
	if snap := r.Runtime.Snapshot(); !snap.Running || snap.LastError != "" {
		t.Fatalf("restarting status = %+v", snap)
	}
}

func TestTunnelRunnerServesAQuickTunnelWhileARepairGoesUnheard(t *testing.T) {
	cloud, src := newFixedSource(t)
	knownTunnel(t, cloud, src)
	cloud.set(http.StatusBadGateway)
	bin, argsFile := fakeCloudflared(t, "")
	r := namedRunner(t, bin, src)
	r.repair = true
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
			t.Fatal("no connector started")
		}
	}
	if argv, token := readArgs(t, argsFile); strings.HasSuffix(argv, " run") || token != "" {
		t.Fatalf("started the failed tunnel again: argv %q, token %q", argv, token)
	}

	cloud.set(0)
	if err := <-done; !errors.Is(err, errFixedTunnelReady) {
		t.Fatalf("runOnce = %v, want the switch to the fixed address", err)
	}
	cloud.mu.Lock()
	repaired, asked := cloud.repairs[len(cloud.repairs)-1], len(cloud.repairs)
	cloud.mu.Unlock()
	if !repaired || r.repair {
		t.Fatalf("the switch back asked for a repair = %v; repair still owed = %v", repaired, r.repair)
	}
	bin, _ = fakeCloudflared(t, registeredLine)
	r.Binary = bin
	if got := advertisedHostname(t, r); got != "h-test.example.dev" {
		t.Fatalf("advertised %q", got)
	}
	if n := cloud.requests(); n != asked {
		t.Fatalf("asked AO Cloud %d more times for a tunnel it had just made", n-asked)
	}
}

func TestTunnelRunnerKeepsTheQuickTunnelUntilAReplacementIsMade(t *testing.T) {
	cloud, src := newFixedSource(t)
	knownTunnel(t, cloud, src)
	cloud.mu.Lock()
	cloud.repairStatus = http.StatusBadGateway
	cloud.mu.Unlock()
	bin, argsFile := fakeCloudflared(t, "")
	r := namedRunner(t, bin, src)
	r.repair = true
	r.upgradeEvery = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(argsFile); err == nil && len(b) > 0 && cloud.requests() >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("quick tunnel did not start after %d AO Cloud requests", cloud.requests())
		}
	}
	cancel()
	if err := <-done; errors.Is(err, errFixedTunnelReady) {
		t.Fatal("switched back to the tunnel AO Cloud could not replace")
	}
	if argv, token := readArgs(t, argsFile); strings.HasSuffix(argv, " run") || token != "" {
		t.Fatalf("not a quick tunnel: argv %q, token %q", argv, token)
	}
}

func TestTunnelRunnerBacksOffRepairsThatDidNotHelp(t *testing.T) {
	_, src := newFixedSource(t)
	r := namedRunner(t, exitingCloudflared(t, refusedLine), src)
	r.unhelped, r.repairedAt = 2, time.Now().Add(-5*time.Minute)
	if err := r.runOnce(context.Background()); err == nil || r.repair {
		t.Fatalf("runOnce = %v, repair %v; want none within 8 minutes", err, r.repair)
	}
	r.repairedAt = time.Now().Add(-9 * time.Minute)
	if err := r.runOnce(context.Background()); err == nil || !r.repair {
		t.Fatalf("runOnce = %v, repair %v; want one after 8 minutes", err, r.repair)
	}
}

func TestTunnelRunnerKeepsTheBackoffForAConnectorThatServedBriefly(t *testing.T) {
	_, src := newFixedSource(t)
	r := namedRunner(t, exitingCloudflared(t, registeredLine), src)
	r.unhelped, r.repairedAt = 2, time.Now().Add(-5*time.Minute)
	if err := r.runOnce(context.Background()); err == nil || r.repair || r.unhelped != 2 {
		t.Fatalf("runOnce = %v, repair %v, unhelped %d; want no repair within 8 minutes", err, r.repair, r.unhelped)
	}
}

func TestTunnelRunnerShowsAStandInQuickTunnelsFailure(t *testing.T) {
	cloud, src := newFixedSource(t)
	knownTunnel(t, cloud, src)
	cloud.mu.Lock()
	cloud.repairStatus = http.StatusBadGateway
	cloud.mu.Unlock()
	r := namedRunner(t, exitingCloudflared(t, "connector crashed"), src)
	r.repair = true
	if err := r.runOnce(context.Background()); err == nil || !r.repair {
		t.Fatalf("runOnce = %v, repair %v; want the repair still owed", err, r.repair)
	}
	if snap := r.Runtime.Snapshot(); snap.Running || snap.LastError == "" {
		t.Fatalf("status = %+v, want the failure shown", snap)
	}
}

func TestTunnelRunnerDoesNotRetryAnAnswerAOCloudGave(t *testing.T) {
	cloud, src := newFixedSource(t)
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.recheckEvery, r.relinkWait = time.Hour, 2*time.Second
	nudge := make(chan struct{}, 1)
	r.nudge = nudge
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); r.Runtime.UnsettledHostname() == ""; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("fixed connector never registered")
		}
	}
	cloud.set(http.StatusTooManyRequests)
	nudge <- struct{}{}
	time.Sleep(500 * time.Millisecond)
	if n := cloud.requests(); n != 2 {
		t.Fatalf("asked AO Cloud %d times; want the start and one check", n)
	}
}

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
	if _, dead := h.deadSince(); dead {
		t.Fatal("dead while connection 1 is live")
	}
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
	link(t, src, "")
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

	link(t, src, "account-digest")
	if err := <-done; !errors.Is(err, errFixedTunnelReady) {
		t.Fatalf("runOnce = %v, want the switch to the fixed address", err)
	}
	if snap := r.Runtime.Snapshot(); !snap.Running || snap.LastError != "" {
		t.Fatalf("switching status = %+v", snap)
	}

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
	if n := cloud.requests(); n != 1 {
		t.Fatalf("asked AO Cloud %d times for one switch", n)
	}
}

func TestTunnelRunnerDropsAFixedAddressAOCloudWithdrew(t *testing.T) {
	cloud, src := newFixedSource(t)
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.recheckEvery, r.relinkWait = 20*time.Millisecond, 100*time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("stopped while AO Cloud still hands out the tunnel: %v", err)
	default:
	}
	cloud.set(http.StatusUnauthorized)
	if err := <-done; !errors.Is(err, errFixedTunnelChanged) {
		t.Fatalf("runOnce = %v, want the withdrawn fixed address", err)
	}
}

func TestTunnelRunnerRechecksAFixedTunnelWhenNudged(t *testing.T) {
	cloud, src := newFixedSource(t)
	cloud.tokens = []string{"run-token-tun-1", "run-token-tun-2"}
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.recheckEvery = time.Hour
	nudge := make(chan struct{}, 1)
	r.nudge = nudge

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); r.Runtime.UnsettledHostname() == ""; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("fixed connector never registered")
		}
	}
	nudge <- struct{}{}
	select {
	case err := <-done:
		if !errors.Is(err, errFixedTunnelChanged) || r.next == nil || r.next.RunToken != "run-token-tun-2" {
			t.Fatalf("runOnce = %v, next %+v", err, r.next)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the nudge did not recheck the fixed tunnel")
	}
}

func TestTunnelRunnerWaitsForARelinkAfterANewPassword(t *testing.T) {
	for _, relinked := range []bool{true, false} {
		cloud, src := newFixedSource(t)
		bin, _ := fakeCloudflared(t, registeredLine)
		r := namedRunner(t, bin, src)
		r.recheckEvery, r.relinkWait = time.Hour, 300*time.Millisecond
		nudge := make(chan struct{}, 1)
		r.nudge = nudge

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		done := make(chan error, 1)
		go func() { done <- r.runOnce(ctx) }()
		for deadline := time.Now().Add(10 * time.Second); r.Runtime.UnsettledHostname() == ""; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("fixed connector never registered")
			}
		}
		link(t, src, "")
		nudge <- struct{}{}
		time.Sleep(100 * time.Millisecond)
		if relinked {
			link(t, src, "new-account-digest")
		}
		select {
		case err := <-done:
			if relinked || !errors.Is(err, errFixedTunnelChanged) {
				t.Fatalf("relinked %v: runOnce = %v", relinked, err)
			}
		case <-time.After(time.Second):
			if !relinked {
				t.Fatal("kept the fixed address with no link")
			}
			cloud.mu.Lock()
			got := cloud.digests[len(cloud.digests)-1]
			cloud.mu.Unlock()
			if got != "new-account-digest" {
				t.Fatalf("asked with %q", got)
			}
			cancel()
			<-done
		}
		cancel()
	}
}

func TestTunnelRunnerReportsAStartThatFailsAfterARestart(t *testing.T) {
	r := &TunnelRunner{Binary: filepath.Join(t.TempDir(), "missing"), LocalPort: 4321, Runtime: &TunnelRuntime{}}
	r.Runtime.Started()
	if err := r.runOnce(context.Background()); err == nil {
		t.Fatal("a missing binary started")
	}
	if snap := r.Runtime.Snapshot(); snap.Running || !strings.Contains(snap.LastError, "start cloudflared") {
		t.Fatalf("status = %+v", snap)
	}
}

func TestTunnelRunnerTriesANewLinkAtOnce(t *testing.T) {
	cloud, src := newFixedSource(t)
	cloud.set(http.StatusUnauthorized)
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, src)
	r.upgradeEvery = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.runOnce(ctx) }()
	time.Sleep(400 * time.Millisecond)
	if n := cloud.requests(); n < 2 || n > 8 {
		t.Fatalf("asked AO Cloud %d times while refused", n)
	}
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
	r.upgradeEvery = time.Hour
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
