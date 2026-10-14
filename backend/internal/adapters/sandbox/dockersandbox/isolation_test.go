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

// exitErr stands in for *exec.ExitError, which exposes the same ExitCode.
type exitErr int

func (e exitErr) Error() string { return "exit status " + strconv.Itoa(int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

func isolationCall(container, port string) string {
	return "/usr/bin/docker run --rm --network container:" + container + " --cap-add NET_ADMIN " + isolationImage +
		" /bin/sh -c " + isolationScript + " sh " + port
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
			requireIsolatedBeforeProbe(t, fr, isolationCall("ao-sbx-s1", "3001"))
		})
	}
}

func TestEnsureIsolationUnknownPortPassesZero(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	if _, err := s.Ensure(context.Background(), perAgentReq(wsOf(data))); err != nil {
		t.Fatal(err)
	}
	requireIsolatedBeforeProbe(t, fr, isolationCall("ao-sbx-s1", "0"))
}

func TestEnsureHostReachableIsMisconfigured(t *testing.T) {
	reachable := fakeCall{out: "REACHABLE 192.168.65.254\n", err: exitErr(3)}
	requireReachable := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "192.168.65.254") ||
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
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "rm -f ao-sbx-s1"},
	})
	fr.isolate = fakeCall{err: errors.New("exit status 125: Unable to find image: pull access denied")}
	_, err := s.Ensure(context.Background(), perAgentReq(wsOf(data)))
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "isolate sandbox network") ||
		!strings.Contains(err.Error(), "needs image "+isolationImage) {
		t.Fatalf("err = %v", err)
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

func TestRunArgsSetNoNewPrivileges(t *testing.T) {
	args := strings.Join(runArgs("c", domain.SandboxConfig{Image: "img"}, nil, nil, ""), " ")
	if !strings.Contains(args, "--security-opt no-new-privileges ") {
		t.Fatalf("run args lack no-new-privileges: %s", args)
	}
}

// Containers created before the hardening flags must not match the new hash,
// so a stopped one is recreated with them.
func TestConfigHashCoversContainerLayout(t *testing.T) {
	cfg := domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "img"}
	ms := []mount{{Path: "/w"}}
	if containerLayout < 2 {
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

// runIsolationScript runs the script with getent, ip and nc stand-ins that
// log their argv, and returns its output, the logged calls and exit code.
func runIsolationScript(t *testing.T, port string, env ...string) (string, string, int) {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "log")
	tools := map[string]string{
		"getent": `case "$2" in
host.docker.internal) printf '192.168.65.254  STREAM host.docker.internal\n192.168.65.254  DGRAM\nfdc4::254  STREAM host.docker.internal\n' ;;
gateway.docker.internal) printf '192.168.65.1  STREAM gateway.docker.internal\n' ;;
host.lima.internal) printf '192.168.65.254  STREAM host.lima.internal\n' ;;
*) exit 2 ;;
esac`,
		"ip": `echo "ip $*" >> "$LOG"
case "$*" in "route show default") printf 'default via 172.17.0.1 dev eth0\n172.17.0.0/16 dev eth0 scope link\n'; exit 0 ;; esac
[ -n "$FAIL_IP" ] && case "$*" in *"$FAIL_IP"*) echo "RTNETLINK answers: Operation not permitted" >&2; exit 2 ;; esac
exit 0`,
		"nc": `echo "nc $*" >> "$LOG"
[ "$4" = "$REACH" ] && exit 0
exit 1`,
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
	cmd := exec.Command("/bin/sh", script, port)
	cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "LOG=" + log}, env...)
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
	out, calls, code := runIsolationScript(t, "3001")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	want := strings.Join([]string{
		"ip route replace blackhole 192.168.65.254/32",
		"ip -6 route replace blackhole fdc4::254/128",
		"ip route replace blackhole 192.168.65.1/32",
		"ip route show default",
		"nc -z -w 2 192.168.65.254 3001",
		"nc -z -w 2 fdc4::254 3001",
		"nc -z -w 2 192.168.65.1 3001",
		"nc -z -w 2 172.17.0.1 3001",
	}, "\n") + "\n"
	if calls != want {
		t.Fatalf("calls =\n%s\nwant\n%s", calls, want)
	}
}

func TestIsolationScriptReachableExits3(t *testing.T) {
	for _, addr := range []string{"192.168.65.254", "172.17.0.1"} {
		out, calls, code := runIsolationScript(t, "3001", "REACH="+addr)
		if code != 3 || strings.TrimSpace(out) != "REACHABLE "+addr {
			t.Fatalf("%s: exit %d, out %q", addr, code, out)
		}
		if strings.Contains(calls, "blackhole 172.17.0.1") {
			t.Fatalf("default gateway was blackholed:\n%s", calls)
		}
	}
}

func TestIsolationScriptPortZeroSkipsProbe(t *testing.T) {
	out, calls, code := runIsolationScript(t, "0", "REACH=192.168.65.254")
	if code != 0 || strings.Contains(calls, "nc ") || !strings.Contains(calls, "blackhole 192.168.65.254/32") {
		t.Fatalf("exit %d out %q calls:\n%s", code, out, calls)
	}
}

func TestIsolationScriptIPFailureIsFatal(t *testing.T) {
	for _, fail := range []string{"192.168.65.1/32", "fdc4::254/128"} {
		out, calls, code := runIsolationScript(t, "3001", "FAIL_IP="+fail)
		if code == 0 || code == 3 || !strings.Contains(out, fail) || strings.Contains(calls, "nc ") {
			t.Fatalf("%s: exit %d out %q calls:\n%s", fail, code, out, calls)
		}
	}
}
