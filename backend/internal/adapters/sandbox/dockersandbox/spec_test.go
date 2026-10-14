package dockersandbox

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestContainerName(t *testing.T) {
	per := ports.SandboxRequest{SessionID: "proj-12", ProjectID: "My Proj", Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent}}
	if got := containerName(per, "abcdef0123456789"); got != "ao-sbx-proj-12" {
		t.Fatalf("per-agent name = %q", got)
	}
	shared := per
	shared.Config.Mode = domain.SandboxModeShared
	sum := sha256.Sum256([]byte("My Proj"))
	want := "ao-sbx-my-proj-" + hex.EncodeToString(sum[:])[:8] + "-abcdef01"
	if got := containerName(shared, "abcdef0123456789"); got != want {
		t.Fatalf("shared name = %q, want %q", got, want)
	}
}

func TestContainerNameDisambiguatesLossyIDs(t *testing.T) {
	base := ports.SandboxRequest{ProjectID: "p", Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent}}
	upper, lower := base, base
	upper.SessionID = "Proj-12"
	lower.SessionID = "proj-12"
	if containerName(upper, "") == containerName(lower, "") {
		t.Fatalf("session IDs differing only by case collide: %q", containerName(lower, ""))
	}
}

func TestContainerNameFromUnsafeIDIsValidDockerName(t *testing.T) {
	nameRule := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)
	req := ports.SandboxRequest{SessionID: "!!!", ProjectID: "!!!", Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent}}
	name := containerName(req, "")
	if !strings.HasPrefix(name, "ao-sbx-") || !nameRule.MatchString(strings.TrimPrefix(name, "ao-sbx-")) {
		t.Fatalf("per-agent name %q is not a valid docker name after ao-sbx-", name)
	}
	req.Config.Mode = domain.SandboxModeShared
	name = containerName(req, "abcdef0123456789")
	if !strings.HasPrefix(name, "ao-sbx-") || !nameRule.MatchString(strings.TrimPrefix(name, "ao-sbx-")) {
		t.Fatalf("shared name %q is not a valid docker name after ao-sbx-", name)
	}
}

func TestConfigHashChangesWithImageAndMounts(t *testing.T) {
	cfg := domain.SandboxConfig{Mode: domain.SandboxModeShared, Image: "node:22"}
	m := []mount{{Path: "/a"}}
	h := configHash(cfg, m)
	if h != configHash(cfg, m) {
		t.Fatal("hash is not deterministic")
	}
	cfg2 := cfg
	cfg2.Image = "node:24"
	if h == configHash(cfg2, m) || h == configHash(cfg, []mount{{Path: "/b"}}) {
		t.Fatal("hash ignores image or mounts")
	}
}

func TestRunArgs(t *testing.T) {
	cfg := domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "node:22", CPUs: "2", Memory: "4g", PidsLimit: 256}
	got := runArgs("ao-sbx-s1", cfg, map[string]string{"ao.session": "s1", "ao.sandbox": "per-agent"},
		[]mount{{Path: "/w"}, {Path: "/p", ReadOnly: true}}, "501:20")
	want := []string{"run", "-d", "--init", "--name", "ao-sbx-s1", "--security-opt", "no-new-privileges",
		"--label", "ao.sandbox=per-agent", "--label", "ao.session=s1",
		"--user", "501:20",
		"--mount", "type=bind,src=/w,dst=/w", "--mount", "type=bind,src=/p,dst=/p,readonly",
		"--cpus", "2", "--memory", "4g", "--pids-limit", "256",
		"--entrypoint", "/bin/sh", "node:22", "-c", keepAliveScript}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runArgs =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderEnvFileRoundTripsThroughShell(t *testing.T) {
	env := map[string]string{
		"AO_SESSION_ID": "s1",
		"TRICKY":        "it's $HOME `x` \"q\"\nline2",
		"PATH":          "/host/only",
		"DYLD_X":        "drop",
	}
	data, err := renderEnvFile(env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/host/only") || strings.Contains(string(data), "DYLD_X") {
		t.Fatalf("host-only vars leaked:\n%s", data)
	}
	f := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", "-c", `set -a; . "$1"; printf %s "$TRICKY"`, "sh", f).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != env["TRICKY"] {
		t.Fatalf("round trip = %q, want %q", out, env["TRICKY"])
	}
}

func TestRenderEnvFileRejectsBadKeysAndNUL(t *testing.T) {
	if _, err := renderEnvFile(map[string]string{"BAD-KEY": "x"}); err == nil {
		t.Fatal("want error for invalid key")
	}
	if _, err := renderEnvFile(map[string]string{"K": "a\x00b"}); err == nil {
		t.Fatal("want error for NUL value")
	}
}

func TestExecArgv(t *testing.T) {
	target := ports.SandboxTarget{Container: "ao-sbx-s1", BinDir: "/d/sandbox/bin", SessionDir: "/d/sandbox/projects/p/sessions/s1"}
	got := execArgv("/usr/local/bin/docker", target, "/w/s1", []string{"claude", "--session-id", "x"})
	want := []string{"/usr/local/bin/docker", "exec", "-i", "-t", "-w", "/w/s1", "ao-sbx-s1",
		"/bin/sh", "/d/sandbox/bin/ao-enter", "/d/sandbox/projects/p/sessions/s1/env",
		"claude", "--session-id", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("execArgv = %q", got)
	}
}

func TestRunArgsTmpfsMount(t *testing.T) {
	cfg := domain.SandboxConfig{Mode: domain.SandboxModeShared, Image: "node:22"}
	got := runArgs("c", cfg, nil, []mount{{Path: "/w"}, {Path: "/w/orchestrator", Tmpfs: true},
		{Path: "/g/logs", Tmpfs: true, Writable: true}, {Path: "/g/config", ReadOnly: true}}, "")
	want := []string{"run", "-d", "--init", "--name", "c", "--security-opt", "no-new-privileges",
		"--mount", "type=bind,src=/w,dst=/w", "--mount", "type=tmpfs,destination=/w/orchestrator,readonly",
		"--mount", "type=tmpfs,destination=/g/logs,tmpfs-mode=1777",
		"--mount", "type=bind,src=/g/config,dst=/g/config,readonly",
		"--entrypoint", "/bin/sh", "node:22", "-c", keepAliveScript}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runArgs =\n%q\nwant\n%q", got, want)
	}
	if configHash(cfg, []mount{{Path: "/w/orchestrator", Tmpfs: true}}) == configHash(cfg, []mount{{Path: "/w/orchestrator"}}) {
		t.Fatal("hash ignores the tmpfs flag")
	}
	if configHash(cfg, []mount{{Path: "/g/logs", Tmpfs: true}}) == configHash(cfg, []mount{{Path: "/g/logs", Tmpfs: true, Writable: true}}) {
		t.Fatal("hash ignores the writable tmpfs flag")
	}
}

// Docker parses --mount as one CSV record, so a path holding a comma or a
// quote must be a quoted field or it would split into extra options.
func TestRunArgsQuotesMountPathsAsCSV(t *testing.T) {
	cfg := domain.SandboxConfig{Mode: domain.SandboxModeShared, Image: "node:22"}
	odd := `/w/a,readonly=false,dst=/etc"x`
	got := runArgs("c", cfg, nil, []mount{{Path: odd, ReadOnly: true}, {Path: odd + "/orch", Tmpfs: true}}, "")
	var specs []string
	for i, a := range got {
		if a == "--mount" {
			specs = append(specs, got[i+1])
		}
	}
	want := [][]string{
		{"type=bind", "src=" + odd, "dst=" + odd, "readonly"},
		{"type=tmpfs", "destination=" + odd + "/orch", "readonly"},
	}
	if len(specs) != len(want) {
		t.Fatalf("mount specs = %q", specs)
	}
	for i, spec := range specs {
		fields, err := csv.NewReader(strings.NewReader(spec)).Read()
		if err != nil || !reflect.DeepEqual(fields, want[i]) {
			t.Fatalf("mount %q parses as %q (%v), want %q", spec, fields, err, want[i])
		}
	}
}
