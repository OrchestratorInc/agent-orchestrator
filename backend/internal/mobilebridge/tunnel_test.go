package mobilebridge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cloudflaredStartup = `2026-08-26T20:07:33Z INF Thank you for trying Cloudflare Tunnel. Doing so, without a Cloudflare account, is a quick way to experiment and try it out. However, be aware that these account-less Tunnels have no uptime guarantee.
2026-08-26T20:07:33Z INF Requesting new quick Tunnel on trycloudflare.com...
2026-08-26T20:07:38Z INF +--------------------------------------------------------------------------------------------+
2026-08-26T20:07:38Z INF |  Your quick Tunnel has been created! Visit it at (it may take some time to be reachable):  |
2026-08-26T20:07:38Z INF |  https://ferrari-moderate-internet-lid.trycloudflare.com                                   |
2026-08-26T20:07:38Z INF +--------------------------------------------------------------------------------------------+
2026-08-26T20:07:38Z INF Generated Connector ID: dc21c5eb-e1a4-4636-8fbb-0d688fa3de8d
2026-08-26T20:07:40Z INF Registered tunnel connection connIndex=0 connection=7dc4ace9-1ce7-42f9-98a2-ecc14c6987d7 event=0 ip=2606:4700:a8::6 location=bom11 protocol=quic`

func feedLines(t *testing.T, text string) *TunnelLog {
	t.Helper()
	log := &TunnelLog{}
	for _, line := range splitLines(text) {
		log.Feed(line)
	}
	return log
}

func TestTunnelLogReadsHostnameAndReadiness(t *testing.T) {
	got := feedLines(t, cloudflaredStartup)

	if got.URL != "https://ferrari-moderate-internet-lid.trycloudflare.com" {
		t.Errorf("URL = %q", got.URL)
	}
	if !got.Ready() {
		t.Error("not ready after a registered connection")
	}
	if got.Location != "bom11" {
		t.Errorf("Location = %q want bom11", got.Location)
	}
	if got.Protocol != "quic" {
		t.Errorf("Protocol = %q want quic", got.Protocol)
	}
}

func TestTunnelLogNotReadyOnHostnameAlone(t *testing.T) {
	partial := `2026-08-26T20:07:38Z INF |  https://ferrari-moderate-internet-lid.trycloudflare.com  |
2026-08-26T20:07:38Z INF Generated Connector ID: dc21c5eb-e1a4-4636-8fbb-0d688fa3de8d`

	got := feedLines(t, partial)

	if got.URL == "" {
		t.Fatal("hostname not captured")
	}
	if got.Ready() {
		t.Error("reported ready before any connection was registered")
	}
}

func TestTunnelLogNotReadyWithoutHostname(t *testing.T) {
	got := feedLines(t, `2026-08-26T20:07:40Z INF Registered tunnel connection connIndex=0 location=bom11 protocol=quic`)

	if got.Ready() {
		t.Error("reported ready with no hostname")
	}
}

func TestParseCloudflaredVersion(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		want      CloudflaredVersion
		ok        bool
	}{
		{"real --version output", "cloudflared version 2026.7.2 (built 2026-07-15T11:01:07Z)", CloudflaredVersion{2026, 7, 2}, true},
		{"trailing newline", "cloudflared version 2025.11.0 (built x)\n", CloudflaredVersion{2025, 11, 0}, true},
		{"no version present", "some other binary", CloudflaredVersion{}, false},
		{"empty", "", CloudflaredVersion{}, false},
	} {
		got, ok := ParseCloudflaredVersion(tc.out)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got (%v, %v) want (%v, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCloudflaredVersionOrdersNumerically(t *testing.T) {
	for _, tc := range []struct {
		name   string
		v, min CloudflaredVersion
		want   bool
	}{
		{"equal", CloudflaredVersion{2026, 7, 2}, CloudflaredVersion{2026, 7, 2}, true},
		{"newer patch", CloudflaredVersion{2026, 7, 3}, CloudflaredVersion{2026, 7, 2}, true},
		{"older patch", CloudflaredVersion{2026, 7, 1}, CloudflaredVersion{2026, 7, 2}, false},
		{"month 10 beats month 9", CloudflaredVersion{2026, 10, 0}, CloudflaredVersion{2026, 9, 0}, true},
		{"month 9 below month 10", CloudflaredVersion{2026, 9, 0}, CloudflaredVersion{2026, 10, 0}, false},
		{"newer year, older month", CloudflaredVersion{2026, 1, 0}, CloudflaredVersion{2025, 12, 9}, true},
		{"older year, newer month", CloudflaredVersion{2025, 12, 9}, CloudflaredVersion{2026, 1, 0}, false},
	} {
		if got := tc.v.AtLeast(tc.min); got != tc.want {
			t.Errorf("%s: %v.AtLeast(%v) = %v want %v", tc.name, tc.v, tc.min, got, tc.want)
		}
	}
}

func lookupFor(env, managed, system string, versions map[string]CloudflaredVersion) CloudflaredLookup {
	present := map[string]bool{}
	for _, p := range []string{env, managed, system} {
		if p != "" {
			present[p] = true
		}
	}
	return CloudflaredLookup{
		EnvPath:     env,
		ManagedPath: managed,
		LookPath: func(string) (string, error) {
			if system == "" {
				return "", os.ErrNotExist
			}
			return system, nil
		},
		Exists: func(p string) bool { return present[p] },
		Version: func(p string) (CloudflaredVersion, bool) {
			v, ok := versions[p]
			return v, ok
		},
	}
}

func TestResolveCloudflaredPrefersExplicitOverride(t *testing.T) {
	got := ResolveCloudflared(lookupFor("/opt/cf", "/home/u/.ao/bin/cloudflared", "/usr/local/bin/cloudflared",
		map[string]CloudflaredVersion{"/opt/cf": {2019, 1, 0}}))

	if got.Path != "/opt/cf" || got.Source != CloudflaredFromEnv {
		t.Fatalf("got %+v, want /opt/cf from env", got)
	}
	if got.NeedsInstall {
		t.Error("explicit override should never ask for an install")
	}
}

func TestResolveCloudflaredPrefersOurManagedCopyOverSystem(t *testing.T) {
	got := ResolveCloudflared(lookupFor("", "/home/u/.ao/bin/cloudflared", "/usr/local/bin/cloudflared",
		map[string]CloudflaredVersion{
			"/home/u/.ao/bin/cloudflared": {2026, 7, 2},
			"/usr/local/bin/cloudflared":  {2026, 7, 2},
		}))

	if got.Path != "/home/u/.ao/bin/cloudflared" || got.Source != CloudflaredManaged {
		t.Fatalf("got %+v, want the managed copy", got)
	}
}

func TestResolveCloudflaredReplacesAManagedCopyOfAnotherRelease(t *testing.T) {
	managed := "/home/u/.ao/bin/cloudflared"
	pinned := CloudflaredVersion{2026, 10, 2}
	for name, versions := range map[string]map[string]CloudflaredVersion{
		"older pin": {managed: {2026, 10, 1}},
		"newer pin": {managed: {2026, 11, 0}},
	} {
		l := lookupFor("", managed, "", versions)
		l.ManagedVersion = pinned
		if got := ResolveCloudflared(l); !got.NeedsInstall {
			t.Errorf("%s: got %+v, want a new download", name, got)
		}
	}
	for name, versions := range map[string]map[string]CloudflaredVersion{
		"pinned":     {managed: pinned},
		"unreadable": {},
	} {
		l := lookupFor("", managed, "", versions)
		l.ManagedVersion = pinned
		if got := ResolveCloudflared(l); got.Path != managed || got.NeedsInstall {
			t.Errorf("%s: got %+v, want the managed copy", name, got)
		}
	}
}

func TestResolveCloudflaredReusesARecentSystemInstall(t *testing.T) {
	got := ResolveCloudflared(lookupFor("", "", "/opt/homebrew/bin/cloudflared",
		map[string]CloudflaredVersion{"/opt/homebrew/bin/cloudflared": {2026, 7, 2}}))

	if got.Path != "/opt/homebrew/bin/cloudflared" || got.Source != CloudflaredFromSystem {
		t.Fatalf("got %+v, want the system install reused", got)
	}
	if got.NeedsInstall {
		t.Error("a recent system install must not trigger a download")
	}
}

func TestResolveCloudflaredInstallsAlongsideAnOldSystemCopy(t *testing.T) {
	got := ResolveCloudflared(lookupFor("", "", "/usr/bin/cloudflared",
		map[string]CloudflaredVersion{"/usr/bin/cloudflared": {2021, 3, 1}}))

	if !got.NeedsInstall {
		t.Fatal("an outdated system copy should trigger a managed install")
	}
	if got.Path == "/usr/bin/cloudflared" {
		t.Error("must not run the outdated system binary")
	}
}

func TestResolveCloudflaredInstallsWhenNothingIsPresent(t *testing.T) {
	got := ResolveCloudflared(lookupFor("", "", "", nil))

	if !got.NeedsInstall {
		t.Fatal("nothing installed should trigger an install")
	}
	if got.Source != CloudflaredAbsent {
		t.Errorf("Source = %q want absent", got.Source)
	}
}

func TestResolveCloudflaredIgnoresASystemBinaryOfUnknownVersion(t *testing.T) {
	got := ResolveCloudflared(lookupFor("", "", "/usr/bin/cloudflared", nil))

	if !got.NeedsInstall {
		t.Fatal("unparseable version should trigger a managed install")
	}
}

func TestTunnelPIDRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobile", "tunnel.pid")

	if err := WriteTunnelPID(path, 4242); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, ok := ReadTunnelPID(path)
	if !ok || got != 4242 {
		t.Fatalf("got (%d, %v) want (4242, true)", got, ok)
	}
}

func TestReadTunnelPIDAbsentWhenNeverWritten(t *testing.T) {
	if _, ok := ReadTunnelPID(filepath.Join(t.TempDir(), "tunnel.pid")); ok {
		t.Fatal("reported a pid with no file")
	}
}

func TestReapStaleTunnelKillsAnOrphanedConnector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	if err := WriteTunnelPID(path, 4242); err != nil {
		t.Fatalf("write: %v", err)
	}

	killed := 0
	err := ReapStaleTunnel(path,
		func(pid int) bool { return pid == 4242 },
		func(pid int) error { killed = pid; return nil },
	)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if killed != 4242 {
		t.Fatalf("killed %d, want 4242", killed)
	}
	if _, ok := ReadTunnelPID(path); ok {
		t.Error("pid file survived the reap")
	}
}

func TestReapStaleTunnelNeverKillsAnUnconfirmedProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	if err := WriteTunnelPID(path, 4242); err != nil {
		t.Fatalf("write: %v", err)
	}

	killed := false
	err := ReapStaleTunnel(path,
		func(int) bool { return false },
		func(int) error { killed = true; return nil },
	)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if killed {
		t.Fatal("killed a process that was not confirmed to be ours")
	}
	if _, ok := ReadTunnelPID(path); ok {
		t.Error("stale pid file should still be cleared")
	}
}

func TestReapStaleTunnelIsANoOpWithoutAPIDFile(t *testing.T) {
	killed := false
	err := ReapStaleTunnel(filepath.Join(t.TempDir(), "tunnel.pid"),
		func(int) bool { return true },
		func(int) error { killed = true; return nil },
	)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if killed {
		t.Fatal("killed something with no pid file present")
	}
}

func TestTunnelRuntimeAdvertisesNothingBeforeReady(t *testing.T) {
	r := &TunnelRuntime{}
	if r.Endpoint() != nil {
		t.Fatal("advertised an endpoint before the connector started")
	}

	r.Started()
	r.Line(`2026-08-26T20:07:38Z INF |  https://abc.trycloudflare.com  |`)

	if got := r.Endpoint(); got != nil {
		t.Fatalf("advertised %+v during the pre-registration window", got)
	}
}

func TestTunnelRuntimeAdvertisesOnceRegistered(t *testing.T) {
	r := &TunnelRuntime{}
	r.Started()
	r.Line(`INF |  https://abc.trycloudflare.com  |`)
	r.Line(`INF Registered tunnel connection connIndex=0 location=pnq01 protocol=quic`)
	r.Settled()

	got := r.Endpoint()
	if got == nil {
		t.Fatal("no endpoint after the connector registered and was confirmed")
		return
	}
	if !got.Ready || got.Hostname != "abc.trycloudflare.com" {
		t.Fatalf("got %+v", got)
	}
}

func TestTunnelRuntimeStopsAdvertisingAfterExit(t *testing.T) {
	r := &TunnelRuntime{}
	r.Started()
	r.Line(`INF |  https://abc.trycloudflare.com  |`)
	r.Line(`INF Registered tunnel connection connIndex=0`)
	r.Settled()
	if r.Endpoint() == nil {
		t.Fatal("precondition: expected a live endpoint")
	}

	r.Exited(errors.New("signal: killed"))

	if got := r.Endpoint(); got != nil {
		t.Fatalf("still advertising %+v after the connector exited", got)
	}
}

func TestTunnelRuntimeForgetsTheOldHostnameOnRestart(t *testing.T) {
	r := &TunnelRuntime{}
	r.Started()
	r.Line(`INF |  https://first-name.trycloudflare.com  |`)
	r.Line(`INF Registered tunnel connection connIndex=0`)
	r.Settled()
	r.Exited(nil)

	r.Started()
	if got := r.Endpoint(); got != nil {
		t.Fatalf("restart inherited %+v from the previous run", got)
	}

	r.Line(`INF |  https://second-name.trycloudflare.com  |`)
	r.Line(`INF Registered tunnel connection connIndex=0`)
	r.Settled()

	got := r.Endpoint()
	if got == nil || got.Hostname != "second-name.trycloudflare.com" {
		t.Fatalf("got %+v want the new hostname", got)
	}
}

func TestTunnelRuntimeSnapshotReportsWhyItIsDown(t *testing.T) {
	r := &TunnelRuntime{}
	r.Started()
	r.Exited(errors.New("exit status 1"))

	s := r.Snapshot()
	if s.Running {
		t.Error("Running true after exit")
	}
	if s.LastError != "exit status 1" {
		t.Errorf("LastError = %q", s.LastError)
	}
}

func TestCloudflaredArgsTargetLoopback(t *testing.T) {
	args := CloudflaredArgs(3011, 20241)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--url http://127.0.0.1:3011") {
		t.Errorf("args do not target loopback: %v", args)
	}
	if !strings.Contains(joined, "--metrics 127.0.0.1:20241") {
		t.Errorf("metrics endpoint missing or not loopback-bound: %v", args)
	}
	if !strings.Contains(joined, "--no-autoupdate") {
		t.Errorf("autoupdate not disabled — a self-update would swap the binary "+
			"under a pinned log-format contract: %v", args)
	}
}

func TestTunnelBackoffGrowsAndCaps(t *testing.T) {
	first := TunnelBackoff(0)
	second := TunnelBackoff(1)
	if second <= first {
		t.Errorf("backoff did not grow: %v then %v", first, second)
	}

	for _, attempt := range []int{10, 50, 1000} {
		if got := TunnelBackoff(attempt); got > MaxTunnelBackoff {
			t.Errorf("attempt %d backed off %v, above the %v cap", attempt, got, MaxTunnelBackoff)
		}
	}
	if got := TunnelBackoff(1000); got != MaxTunnelBackoff {
		t.Errorf("attempt 1000 = %v, want the cap %v", got, MaxTunnelBackoff)
	}
}

func TestTunnelBackoffTreatsNegativeAttemptsAsFirst(t *testing.T) {
	if got, want := TunnelBackoff(-3), TunnelBackoff(0); got != want {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTunnelRuntimeWaitsForTheHostnameToSettle(t *testing.T) {
	r := &TunnelRuntime{}
	r.Started()
	r.Line(`INF |  https://abc.trycloudflare.com  |`)
	r.Line(`INF Registered tunnel connection connIndex=0 location=pnq01`)

	if got := r.Endpoint(); got != nil {
		t.Fatalf("advertised %+v before the hostname had time to propagate", got)
	}

	r.Settled()

	got := r.Endpoint()
	if got == nil || got.Hostname != "abc.trycloudflare.com" {
		t.Fatalf("got %+v want the settled endpoint", got)
	}
}

func TestTunnelRuntimeRequiresSettlingAgainAfterRestart(t *testing.T) {
	r := &TunnelRuntime{}
	r.Started()
	r.Line(`INF |  https://first.trycloudflare.com  |`)
	r.Line(`INF Registered tunnel connection connIndex=0`)
	r.Settled()
	r.Exited(nil)

	r.Started()
	r.Line(`INF |  https://second.trycloudflare.com  |`)
	r.Line(`INF Registered tunnel connection connIndex=0`)

	if got := r.Endpoint(); got != nil {
		t.Fatalf("carried the previous run's settling over to %+v", got)
	}
}

func TestTunnelRuntimeUnsettledHostnameOnlyAfterRegistration(t *testing.T) {
	r := &TunnelRuntime{}
	r.Started()
	r.Line(`INF |  https://abc.trycloudflare.com  |`)
	if got := r.UnsettledHostname(); got != "" {
		t.Errorf("UnsettledHostname = %q before registration", got)
	}

	r.Line(`INF Registered tunnel connection connIndex=0`)
	if got := r.UnsettledHostname(); got != "abc.trycloudflare.com" {
		t.Errorf("UnsettledHostname = %q want abc.trycloudflare.com", got)
	}

	r.Settled()
	if got := r.UnsettledHostname(); got != "" {
		t.Errorf("UnsettledHostname = %q after settling", got)
	}
}

func TestRemoveOwnTunnelPIDFileLeavesAReplacementAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	if err := WriteTunnelPID(path, 4242); err != nil {
		t.Fatalf("seed pid: %v", err)
	}

	if err := RemoveOwnTunnelPIDFile(path, 1111); err != nil {
		t.Fatalf("remove: %v", err)
	}

	pid, ok := ReadTunnelPID(path)
	if !ok || pid != 4242 {
		t.Fatalf("replacement pid = (%d, %v), want (4242, true)", pid, ok)
	}
}

func TestRemoveOwnTunnelPIDFileClearsItsOwnRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	if err := WriteTunnelPID(path, 1111); err != nil {
		t.Fatalf("seed pid: %v", err)
	}

	if err := RemoveOwnTunnelPIDFile(path, 1111); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, ok := ReadTunnelPID(path); ok {
		t.Fatal("own pid record should be gone")
	}
}

func TestRemoveOwnTunnelPIDFileToleratesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	if err := RemoveOwnTunnelPIDFile(path, 1111); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestTunnelPIDOwnersForTheSamePathAreSerializedTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	a := newTunnelPIDOwner(path)
	b := newTunnelPIDOwner(path)
	if a.mu != b.mu {
		t.Fatal("PID owners for one path do not share the cleanup lock")
	}
}
