package dockersandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// exitErr stands in for *exec.ExitError, which exposes the same ExitCode;
// stderr mimics execRunner appending the command's stderr.
type exitErr struct {
	code   int
	stderr string
}

func (e exitErr) Error() string {
	msg := "exit status " + strconv.Itoa(e.code)
	if e.stderr != "" {
		msg += ": " + e.stderr
	}
	return msg
}
func (e exitErr) ExitCode() int { return e.code }

func isolationCall(container, port, requireAlias string) string {
	return "/usr/bin/docker run --rm --network container:" + container + " --cap-add NET_ADMIN " + isolationImage +
		" /bin/sh -c " + isolationScript + " sh " + port + " " + requireAlias
}

// requireIsolatedBeforeProbe asserts exactly one helper run, after the
// container is up and before the binary probe.
func requireIsolatedBeforeProbe(t *testing.T, fr *fakeRunner, want string) {
	t.Helper()
	iso, probe, n := -1, -1, 0
	for i, c := range fr.calls {
		if strings.Contains(c, isolationMarker) {
			n++
			iso = i
			if c != want {
				t.Fatalf("helper argv =\n%s\nwant\n%s", c, want)
			}
		}
		if strings.Contains(c, "command -v") && probe < 0 {
			probe = i
		}
	}
	if n != 1 || probe < 0 || iso > probe {
		t.Fatalf("want one helper run before the binary probe, calls:\n%s", strings.Join(fr.calls, "\n"))
	}
}

func countCalls(fr *fakeRunner, sub string) int {
	n := 0
	for _, c := range fr.calls {
		if strings.Contains(c, sub) {
			n++
		}
	}
	return n
}

func TestEnsureIsolatesNetworkOnEveryPath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first []fakeCall
	}{
		{"created", []fakeCall{{match: "inspect", err: errors.New("No such object")}, {match: "run -d"}}},
		{"started", []fakeCall{{match: "inspect", out: "exited\tHASH\n"}, {match: "start ao-sbx-s1"}}},
		{"reused", []fakeCall{{match: "inspect", out: "running\tHASH\n"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fr, data := testSandbox(t, nil)
			s.cfg.DaemonPort = 3001
			fr.engine = "Docker Desktop"
			req := perAgentReq(wsOf(data))
			hash := hashFor(t, s, req)
			for _, c := range tc.first {
				c.out = strings.ReplaceAll(c.out, "HASH", hash)
				fr.script = append(fr.script, c)
			}
			fr.script = append(fr.script, fakeCall{match: "command -v"})
			if _, err := s.Ensure(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			requireIsolatedBeforeProbe(t, fr, isolationCall("ao-sbx-s1", "3001", "1"))
		})
	}
}

func TestEnsureIsolationUnknownPortPassesZero(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	fr.engine = "Docker Desktop"
	if _, err := s.Ensure(context.Background(), perAgentReq(wsOf(data))); err != nil {
		t.Fatal(err)
	}
	requireIsolatedBeforeProbe(t, fr, isolationCall("ao-sbx-s1", "0", "1"))
}

// Docker Desktop and OrbStack always define a host alias, so the helper must
// find one; a native engine may have none. The engine is asked once.
func TestEnsureRequiresHostAliasByEngine(t *testing.T) {
	for engine, want := range map[string]string{"Docker Desktop": "1", "OrbStack": "1", "Ubuntu 24.04.1 LTS": "0"} {
		t.Run(engine, func(t *testing.T) {
			s, fr, data := testSandbox(t, nil)
			fr.engine = engine
			req := perAgentReq(wsOf(data))
			running := "running\t" + hashFor(t, s, req) + "\n"
			for i := 0; i < 2; i++ {
				fr.script = []fakeCall{{match: "inspect", out: running}, {match: "command -v"}}
				fr.calls = nil
				if _, err := s.Ensure(context.Background(), req); err != nil {
					t.Fatal(err)
				}
				requireIsolatedBeforeProbe(t, fr, isolationCall("ao-sbx-s1", "0", want))
				if got := countCalls(fr, " info --format {{.OperatingSystem}}"); got != 1-i {
					t.Fatalf("Ensure %d made %d docker info calls", i+1, got)
				}
			}
		})
	}
}

func TestEnsureNoHostAliasIsMisconfigured(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "rm -f ao-sbx-s1"},
	})
	fr.isolate = fakeCall{err: exitErr{code: 4, stderr: "no Docker host alias resolved"}}
	_, err := s.Ensure(context.Background(), perAgentReq(wsOf(data)))
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "could not resolve the Docker host address") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureHostReachableIsMisconfigured(t *testing.T) {
	reachable := fakeCall{out: "REACHABLE proxy 192.168.65.7\n", err: exitErr{code: 3}}
	requireReachable := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "proxy 192.168.65.7") ||
			!strings.Contains(err.Error(), "local API") {
			t.Fatalf("err = %v", err)
		}
	}
	t.Run("created per-agent is removed", func(t *testing.T) {
		s, fr, data := testSandbox(t, []fakeCall{
			{match: "inspect", err: errors.New("No such object")},
			{match: "run -d"},
			{match: "rm -f ao-sbx-s1"},
		})
		s.cfg.DaemonPort = 3001
		fr.isolate = reachable
		_, err := s.Ensure(context.Background(), perAgentReq(wsOf(data)))
		requireReachable(t, err)
	})
	t.Run("reused per-agent is kept", func(t *testing.T) {
		s, fr, data := testSandbox(t, nil)
		req := perAgentReq(wsOf(data))
		fr.script = []fakeCall{{match: "inspect", out: "running\t" + hashFor(t, s, req) + "\n"}}
		fr.isolate = reachable
		_, err := s.Ensure(context.Background(), req)
		requireReachable(t, err)
	})
	t.Run("started per-agent is kept", func(t *testing.T) {
		s, fr, data := testSandbox(t, nil)
		req := perAgentReq(wsOf(data))
		fr.script = []fakeCall{{match: "inspect", out: "exited\t" + hashFor(t, s, req) + "\n"}, {match: "start ao-sbx-s1"}}
		fr.isolate = reachable
		_, err := s.Ensure(context.Background(), req)
		requireReachable(t, err)
	})
	t.Run("created shared is kept", func(t *testing.T) {
		s, fr, data := testSandbox(t, []fakeCall{
			{match: "inspect", err: errors.New("No such object")},
			{match: "run -d"},
		})
		fr.isolate = reachable
		req := perAgentReq(wsOf(data))
		req.Config.Mode = domain.SandboxModeShared
		_, err := s.Ensure(context.Background(), req)
		requireReachable(t, err)
	})
}

func TestEnsureIsolationHelperFailureIsMisconfigured(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		hint     bool
		contains string
	}{
		{"pull", exitErr{code: 125, stderr: "Unable to find image 'alpine:3.22' locally: pull access denied"}, true, "pull access denied"},
		{"offline", exitErr{code: 125, stderr: "Error response from daemon: Get \"https://registry-1.docker.io/v2/\": dial tcp: lookup registry-1.docker.io: no such host"}, true, "no such host"},
		{"script", exitErr{code: 1, stderr: "ip -4 route replace blackhole 192.168.65.0/24 failed"}, false, "192.168.65.0/24 failed"},
		{"container gone", exitErr{code: 125, stderr: "Error response from daemon: cannot join network of a non running container"}, false, "non running container"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fr, data := testSandbox(t, []fakeCall{
				{match: "inspect", err: errors.New("No such object")},
				{match: "run -d"},
				{match: "rm -f ao-sbx-s1"},
			})
			fr.isolate = fakeCall{err: tc.err}
			_, err := s.Ensure(context.Background(), perAgentReq(wsOf(data)))
			if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "isolate sandbox network") ||
				!strings.Contains(err.Error(), tc.contains) || strings.Contains(err.Error(), "needs image "+isolationImage) != tc.hint {
				t.Fatalf("err = %v (want hint %v)", err, tc.hint)
			}
		})
	}
}

func TestEnsureIsolationDockerUnavailable(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
	})
	fr.isolate = fakeCall{err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")}
	if _, err := s.Ensure(context.Background(), perAgentReq(wsOf(data))); !errors.Is(err, ports.ErrSandboxUnavailable) {
		t.Fatalf("err = %v, want ErrSandboxUnavailable", err)
	}
}

func TestEnsureCreatesSandboxNetwork(t *testing.T) {
	missing := fakeCall{err: errors.New("Error response from daemon: network ao-sandbox not found")}
	for _, tc := range []struct {
		name      string
		netCreate fakeCall
	}{
		{"created", fakeCall{}},
		{"lost race", fakeCall{err: errors.New("Error response from daemon: network with name ao-sandbox already exists")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fr, data := testSandbox(t, []fakeCall{
				{match: "inspect", err: errors.New("No such object")},
				{match: "run -d"},
				{match: "command -v"},
			})
			fr.netInspect, fr.netCreate = missing, tc.netCreate
			if _, err := s.Ensure(context.Background(), perAgentReq(wsOf(data))); err != nil {
				t.Fatal(err)
			}
			create, run := -1, -1
			for i, c := range fr.calls {
				if c == "/usr/bin/docker network create --label ao.sandbox.network=1 ao-sandbox" {
					create = i
				}
				if strings.Contains(c, " run -d ") {
					run = i
				}
			}
			if create < 0 || create > run || !strings.Contains(runCall(t, fr), " --network ao-sandbox ") {
				t.Fatalf("calls:\n%s", strings.Join(fr.calls, "\n"))
			}
		})
	}
}

func TestEnsureExistingSandboxNetworkIsNotRecreated(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	if _, err := s.Ensure(context.Background(), perAgentReq(wsOf(data))); err != nil {
		t.Fatal(err)
	}
	if countCalls(fr, " network inspect ") != 1 || countCalls(fr, " network create ") != 0 {
		t.Fatalf("calls:\n%s", strings.Join(fr.calls, "\n"))
	}
}

func TestEnsureSandboxNetworkFailure(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{{match: "inspect", err: errors.New("No such object")}})
	fr.netInspect = fakeCall{err: errors.New("network ao-sandbox not found")}
	fr.netCreate = fakeCall{err: errors.New("Error response from daemon: could not find an available, non-overlapping IPv4 address pool")}
	_, err := s.Ensure(context.Background(), perAgentReq(wsOf(data)))
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "create sandbox network ao-sandbox") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunArgsHardening(t *testing.T) {
	args := strings.Join(runArgs("c", domain.SandboxConfig{Image: "img"}, nil, nil, ""), " ")
	for _, want := range []string{"--security-opt no-new-privileges ", "--network ao-sandbox "} {
		if !strings.Contains(args, want) {
			t.Fatalf("run args lack %q: %s", want, args)
		}
	}
}

// Containers created before the hardening flags must not match the new hash,
// so a stopped one is recreated with them.
func TestConfigHashCoversContainerLayout(t *testing.T) {
	cfg := domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "img"}
	ms := []mount{{Path: "/w"}}
	if containerLayout < 3 {
		t.Fatalf("containerLayout = %d; bump it when run args change", containerLayout)
	}
	if configHash(cfg, ms) == unversionedHash(cfg, ms) {
		t.Fatal("configHash ignores the container layout version")
	}
}

// unversionedHash is configHash as it was before containerLayout existed.
func unversionedHash(cfg domain.SandboxConfig, mounts []mount) string {
	data, _ := json.Marshal(struct {
		Cfg    domain.SandboxConfig `json:"cfg"`
		Mounts []mount              `json:"mounts"`
	}{cfg, mounts})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// defaultAliases mirrors Docker Desktop, plus a zoned link-local address the
// script must skip.
const defaultAliases = "host.docker.internal=192.168.65.254 host.docker.internal=fdc4::254 host.docker.internal=fe80::1%eth0 " +
	"gateway.docker.internal=192.168.65.1 host.lima.internal=192.168.65.254"

// runIsolationScript runs the script with getent, ip and nc stand-ins that
// log their argv, and returns its output, the logged calls and exit code.
// Env knobs: ALIASES (name=addr list), GW, LINK (link-scope prefix), PRE_BLACKHOLE, FAIL_IP, REACH, PROXY.
func runIsolationScript(t *testing.T, args []string, env ...string) (string, string, int) {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "log")
	tools := map[string]string{
		"getent": `found=
for pair in $ALIASES; do
	[ "${pair%%=*}" = "$2" ] || continue
	a=${pair#*=}
	printf '%s  STREAM %s\n%s  DGRAM\n' "$a" "$2" "$a"
	found=1
done
[ -n "$found" ] || exit 2`,
		"ip": `echo "ip $*" >> "$LOG"
case "$*" in
"route show default") printf 'default via %s dev eth0\n172.17.0.0/16 dev eth0 scope link\n' "${GW:-172.17.0.1}"; exit 0 ;;
"-4 route show scope link")
	printf '%s dev eth0  src 172.17.0.3\n' "${LINK:-172.17.0.0/16}"
	[ -n "$PRE_BLACKHOLE" ] && printf 'blackhole %s\n' "$PRE_BLACKHOLE"
	exit 0 ;;
"route get "*) echo "RTNETLINK answers: Invalid argument" >&2; exit 2 ;;
esac
[ -n "$FAIL_IP" ] && case "$*" in *"$FAIL_IP"*) echo "RTNETLINK answers: Operation not permitted" >&2; exit 2 ;; esac
exit 0`,
		"sleep": `echo "sleep $*" >> "$LOG"`,
		"nc": `echo "nc $*" >> "$LOG"
if [ "$1" = -z ]; then
	[ "$4" = "$REACH" ] && exit 0
	exit 1
fi
cat > /dev/null
[ "$3" = "$PROXY" ] && printf 'HTTP/1.0 200 OK\r\n\r\nhello\n'
exit 0`,
	}
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // test stand-ins must be executable
			t.Fatal(err)
		}
	}
	script := filepath.Join(t.TempDir(), "isolate-network")
	if err := os.WriteFile(script, []byte(isolationScript), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", append([]string{script}, args...)...)
	cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "LOG=" + log, "ALIASES=" + defaultAliases}, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	calls, _ := os.ReadFile(log)
	return string(out), string(calls), code
}

func TestIsolationScriptBlackholesAndProbes(t *testing.T) {
	out, calls, code := runIsolationScript(t, []string{"3001", "1"})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	want := strings.Join([]string{
		"ip route show default",
		"ip -4 route show scope link",
		"ip -4 route show scope link",
		"ip -4 route replace blackhole 192.168.65.254/32",
		"ip -4 route replace blackhole 192.168.65.0/24",
		"ip -6 route replace blackhole fdc4::254/128",
		"ip -4 route replace blackhole 192.168.65.1/32",
		"nc -z -w 2 192.168.65.254 3001",
		"nc -z -w 2 fdc4::254 3001",
		"nc -z -w 2 192.168.65.1 3001",
		"nc -z -w 2 172.17.0.1 3001",
	}, "\n") + "\n"
	// The proxy probes follow; resolv.conf nameservers vary by host.
	if !strings.HasPrefix(calls, want) || strings.Contains(calls, "fe80") || strings.Contains(calls, "172.17.0.0/24") {
		t.Fatalf("calls =\n%s\nwant prefix\n%s", calls, want)
	}
	for _, p := range []string{"nc -w 2 http.docker.internal 3128\n", "nc -w 2 192.168.65.7 3128\n"} {
		if !strings.Contains(calls, p) {
			t.Fatalf("missing proxy probe %q:\n%s", p, calls)
		}
	}
}

func TestIsolationScriptKeepsOwnNetworkReachable(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"gateway in the /24", []string{"GW=192.168.65.9"}},
		{"on-link", []string{"LINK=192.168.0.0/16"}},
		{"loopback", []string{"ALIASES=host.internal=127.0.0.2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, calls, code := runIsolationScript(t, []string{"0", "1"}, tc.env...)
			if code != 0 || strings.Contains(calls, "/24") || !strings.Contains(calls, "/32") {
				t.Fatalf("exit %d out %q calls:\n%s", code, out, calls)
			}
		})
	}
}

func TestIsolationScriptReachableExits3(t *testing.T) {
	for _, tc := range []struct{ env, want string }{
		{"REACH=192.168.65.254", "REACHABLE 192.168.65.254"},
		{"REACH=172.17.0.1", "REACHABLE 172.17.0.1"},
		{"PROXY=192.168.65.7", "REACHABLE proxy 192.168.65.7"},
		{"PROXY=http.docker.internal", "REACHABLE proxy http.docker.internal"},
	} {
		out, calls, code := runIsolationScript(t, []string{"3001", "1"}, tc.env)
		if code != 3 || strings.TrimSpace(out) != tc.want {
			t.Fatalf("%s: exit %d, out %q", tc.env, code, out)
		}
		if strings.Contains(calls, "blackhole 172.17.0") {
			t.Fatalf("default gateway was blackholed:\n%s", calls)
		}
	}
}

func TestIsolationScriptPortZeroSkipsProbe(t *testing.T) {
	out, calls, code := runIsolationScript(t, []string{"0", "1"}, "REACH=192.168.65.254", "PROXY=192.168.65.7")
	if code != 0 || strings.Contains(calls, "nc ") || !strings.Contains(calls, "blackhole 192.168.65.254/32") {
		t.Fatalf("exit %d out %q calls:\n%s", code, out, calls)
	}
}

func TestIsolationScriptNoAlias(t *testing.T) {
	out, calls, code := runIsolationScript(t, []string{"3001", "1"}, "ALIASES=")
	// Three attempts, so two sleeps.
	if code != 4 || strings.Count(calls, "sleep 1\n") != 2 || strings.Contains(calls, "blackhole") || strings.Contains(calls, "nc ") {
		t.Fatalf("required: exit %d out %q calls:\n%s", code, out, calls)
	}
	// One attempt, no sleep.
	out, calls, code = runIsolationScript(t, []string{"3001", "0"}, "ALIASES=")
	if code != 0 || strings.Contains(calls, "sleep") || strings.Contains(calls, "blackhole") || !strings.Contains(calls, "nc -z -w 2 172.17.0.1 3001") {
		t.Fatalf("optional: exit %d out %q calls:\n%s", code, out, calls)
	}
}

// A /32 blackhole left from an earlier run (the container was not
// restarted) must not make the alias look on-link and drop the /24.
func TestIsolationScriptAddsSubnetOverExistingBlackhole(t *testing.T) {
	out, calls, code := runIsolationScript(t, []string{"0", "1"}, "PRE_BLACKHOLE=192.168.65.254", "ALIASES=host.docker.internal=192.168.65.254")
	if code != 0 || !strings.Contains(calls, "ip -4 route replace blackhole 192.168.65.0/24\n") || strings.Contains(calls, "route get") {
		t.Fatalf("exit %d out %q calls:\n%s", code, out, calls)
	}
}

func TestIsolationScriptOnLinkMatchesPrefix(t *testing.T) {
	for _, tc := range []struct {
		link string
		want bool // /24 added
	}{
		{"192.168.0.0/16", false}, {"192.168.65.254", false}, {"192.168.64.0/24", true}, {"10.0.0.0/8", true},
	} {
		_, calls, _ := runIsolationScript(t, []string{"0", "1"}, "LINK="+tc.link, "ALIASES=host.docker.internal=192.168.65.254")
		if got := strings.Contains(calls, "192.168.65.0/24"); got != tc.want {
			t.Fatalf("LINK=%s: /24 added = %v, want %v\n%s", tc.link, got, tc.want, calls)
		}
	}
}

func TestIsolationScriptIPFailureIsFatal(t *testing.T) {
	for _, fail := range []string{"192.168.65.0/24", "fdc4::254/128"} {
		out, calls, code := runIsolationScript(t, []string{"3001", "1"}, "FAIL_IP="+fail)
		if code == 0 || code == 3 || !strings.Contains(out, fail) || strings.Contains(calls, "nc ") {
			t.Fatalf("%s: exit %d out %q calls:\n%s", fail, code, out, calls)
		}
	}
}
