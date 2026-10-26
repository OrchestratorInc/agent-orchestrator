package mobilebridge

import (
	"context"
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

func namedRunner(t *testing.T, bin string, cfg NamedTunnelConfig) *TunnelRunner {
	t.Helper()
	// An hour of settling proves an advertised address did not wait for it.
	return &TunnelRunner{
		Binary: bin, LocalPort: 4321, PIDPath: filepath.Join(t.TempDir(), "tunnel.pid"),
		Runtime: &TunnelRuntime{}, Named: &cfg, HostID: "host-uuid", SettleDelay: time.Hour,
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

func TestTunnelRunnerAdvertisesAKnownNamedAddressAtOnce(t *testing.T) {
	_, cfg := newFakeCloudflare(t)
	// The record exists already, as on every start after the first.
	if _, err := NamedTunnelFor(context.Background(), cfg, "host-uuid"); err != nil {
		t.Fatal(err)
	}
	bin, argsFile := fakeCloudflared(t, registeredLine)

	if got, want := advertisedHostname(t, namedRunner(t, bin, cfg)), NamedTunnelHostname("host-uuid", "example.dev"); got != want {
		t.Fatalf("advertised %q, want %q", got, want)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	argv, token, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if !strings.Contains(argv, "--url http://127.0.0.1:4321") || !strings.Contains(argv, "--config "+os.DevNull) ||
		!strings.HasSuffix(argv, " run") || token != "run-token-tun-1" {
		t.Fatalf("argv %q, token %q", argv, token)
	}
}

func TestTunnelRunnerStartsTheCachedTunnelWhenTheAPIRefuses(t *testing.T) {
	_, cfg := newFakeCloudflare(t)
	if _, err := NamedTunnelFor(context.Background(), cfg, "host-uuid"); err != nil {
		t.Fatal(err)
	}
	cfg.APIToken = "revoked"
	bin, _ := fakeCloudflared(t, registeredLine)

	if got, want := advertisedHostname(t, namedRunner(t, bin, cfg)), NamedTunnelHostname("host-uuid", "example.dev"); got != want {
		t.Fatalf("advertised %q, want %q", got, want)
	}
}

func TestTunnelRunnerStopsAConnectorNobodyCanReach(t *testing.T) {
	_, cfg := newFakeCloudflare(t)
	bin, _ := fakeCloudflared(t, registeredLine)
	r := namedRunner(t, bin, cfg)
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
}
