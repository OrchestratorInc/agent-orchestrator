# Docker Sandbox (Core) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a project run its local worker/orchestrator agent processes inside a Docker container, either one container per session (`per-agent`) or one long-lived container per project (`shared`), starting from a user-supplied image.

**Architecture:** The daemon, tmux/ConPTY runtime, worktrees and the `ao agent-process supervise` wrapper all stay on the host. A new `ports.Sandbox` adapter (`adapters/sandbox/dockersandbox`) ensures the container exists. The session manager then rewrites the supervised launch to `ao agent-process supervise --sandbox-relay <dir> -- docker exec -it <ctr> ao-enter <envfile> claude …`. Paths are bind-mounted at identical absolute paths, so transcripts, diffs and hook payloads mean the same thing on both sides. In-container `ao` calls (Claude hooks, `ao report`, …) reach the daemon through a file-spool relay that the host-side supervisor serves. That means **no new network listener** and no Linux `ao` build.

**Tech Stack:** Go 1.27 backend (`docker` CLI via `aoprocess.CommandContext`, `go:embed` POSIX sh scripts), Cobra CLI, generated OpenAPI/TS types.

**Spec:** GitHub issue [#6663](https://github.com/OrchestratorInc/agent-orchestrator/issues/6663) "Run local agents in a Docker sandbox (shared or per-agent, with prebuilt images)". This plan implements scope items 1 and 2 of the issue: per-agent and shared modes, an image reference, Claude Code only, on macOS and Linux. The other scope items get their own plans and PRs (see "Follow-up plans").

## Global Constraints

- One issue per PR, branched from `main`, conventional commits (`feat:`, `test:`, `docs:`). This plan is one PR referencing #6663.
- All AO state stays under `~/.ao` (`cfg.DataDir`). Sandbox state lives at `<dataDir>/sandbox/…` only.
- No new network listener and no change to the loopback daemon bind (AGENTS.md hard rule). The relay is file-based.
- The CLI stays a thin client. Only the hidden `agent-process supervise` command gains relay behavior, and it already runs on the host per session.
- No SQLite migration: sandbox config lives in the existing `projects.config` JSON blob. Container identity comes from deterministic names and labels, not new columns.
- API contract: edit `domain` + `specgen/build.go`, then run `npm run api`, and commit `openapi.yaml` + `frontend/src/api/schema.ts` together.
- Tests use fakes (`commandRunner` seam like `dockerreap`). No test may require Docker or network. Shell-script tests run `/bin/sh` locally only.
- Never fall back silently to running on the host when the sandbox is enabled but unusable. Fail the spawn with a clear error.
- Failed or ambiguous Docker probes are never proof that a session is dead (AGENTS.md / architecture.md L826).
- `mode` default is `off`; projects without a `sandbox` block behave exactly as today.

## Review Focus

1. **Docker missing or Docker Desktop not running.** Spawn must fail with `SANDBOX_UNAVAILABLE` and a human-readable message, and the agent must not start on the host. Test: Task 5 `TestEnsureDockerUnavailable`, Task 8 `TestSpawnSandboxUnavailableDoesNotLaunchOnHost`.
2. **The image lacks `claude` or `/bin/sh`.** Spawn must fail with `SANDBOX_MISCONFIGURED` naming the image and the binary. Test: Task 5 `TestEnsureMissingHarnessBinary`.
3. **A sandboxed agent tries to escape through the relay** (`ao launch`, `ao agent-process supervise -- sh`, `ao project set-config` to switch the sandbox off). The relay must refuse it, and every root command must be explicitly classified. Test: Task 6 `TestSandboxRelayPolicyCoversEveryRootCommand`, `TestSandboxRelayRefusesDeniedCommand`.
4. **Secrets and odd env values.** `CLAUDE_CODE_OAUTH_TOKEN` must never appear in the `docker exec` argv (visible in `ps`). The env file must be 0600, and values containing `'`, spaces, `$` or newlines must round-trip exactly through `/bin/sh`. Test: Task 3 `TestRenderEnvFileRoundTripsThroughShell`, Task 5 `TestExecDoesNotPutEnvInArgv`.
5. **Shared container lifetime.** Killing one of two live sandboxed sessions in a shared project must leave the container running. Killing the last one removes it. A failed session listing must keep it. Test: Task 8 `TestKillReleasesSharedSandboxOnlyWhenUnused`.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/domain/sandbox.go` (new) | `SandboxMode`, `SandboxConfig`, `Validate`, `EffectiveSandbox` |
| `backend/internal/domain/projectconfig.go` | `ProjectConfig.Sandbox`, `RoleOverride.Sandbox`, validation hook-up |
| `backend/internal/ports/sandbox.go` (new) | `Sandbox` port, `SandboxRequest/Target`, `AgentSandboxSupport`, error sentinels |
| `backend/internal/adapters/sandbox/dockersandbox/spec.go` (new) | Pure helpers: names, config hash, mounts, `docker run` args, env-file rendering, exec argv |
| `backend/internal/adapters/sandbox/dockersandbox/scripts/{ao,ao-enter}` (new) | POSIX sh relay client and launch entry point, embedded |
| `backend/internal/adapters/sandbox/dockersandbox/sandbox.go` (new) | `Sandbox` adapter: `Ensure`, `Exec`, `Release`, docker path resolution |
| `backend/internal/adapters/container/dockerreap/reap.go` | Export `IsDockerUnavailable` for reuse |
| `backend/internal/cli/sandbox_relay.go` (new) | Host-side relay server and command policy |
| `backend/internal/cli/agent_process.go` | `--sandbox-relay` flag starts the relay next to the child |
| `backend/internal/cli/project.go` | CLI mirror `sandboxConfig` |
| `backend/internal/adapters/agent/claudecode/sandbox.go` (new) | `SandboxBinary`, `PrepareSandbox` (token, config dir, trust, onboarding) |
| `backend/internal/session_manager/sandbox.go` (new) | `prepareSandboxLaunch`, `releaseSharedSandbox` |
| `backend/internal/session_manager/manager.go`, `agent_switching.go` | Call sites: spawn, restore, wrap, kill, chat/switch rejection |
| `backend/internal/service/session/service.go` | Map new sentinels to API errors |
| `backend/internal/daemon/lifecycle_wiring.go` | Construct the adapter and pass `Deps.Sandbox` |
| `backend/internal/httpd/apispec/specgen/build.go` | Schema rename entry |
| `frontend/src/docs/content/configuration/sandbox.mdx` (new), `docs/STATUS.md` | User docs + status line |

On-disk layout (host path == container path):

```
<dataDir>/sandbox/bin/{ao,ao-enter}                       # read-only mount
<dataDir>/sandbox/projects/<projectID>/home/              # $HOME inside container
<dataDir>/sandbox/projects/<projectID>/claude/            # CLAUDE_CONFIG_DIR (transcripts)
<dataDir>/sandbox/projects/<projectID>/sessions/<sid>/env # 0600, sourced by ao-enter
<dataDir>/sandbox/projects/<projectID>/sessions/<sid>/relay/{req,work,resp}/
```

Container mounts: the worktree (`per-agent`) or `<dataDir>/worktrees/<projectID>` (`shared`), the git common dir, `<dataDir>/prompts` (ro), `<dataDir>/sandbox/projects/<projectID>`, and `<dataDir>/sandbox/bin` (ro).

---

### Task 1: Sandbox config in the domain and API schema

**Files:**
- Create: `backend/internal/domain/sandbox.go`
- Modify: `backend/internal/domain/projectconfig.go:70` (field), `:160` (RoleOverride), `:239-246` (Validate role loop)
- Modify: `backend/internal/httpd/apispec/specgen/build.go:262-267`
- Test: `backend/internal/domain/sandbox_test.go`
- Regenerate: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**
- Produces: `domain.SandboxMode` (`SandboxModeOff|SandboxModeShared|SandboxModePerAgent`), `domain.SandboxConfig{Mode, Image, CPUs, Memory, PidsLimit}`, `(SandboxConfig) Enabled() bool`, `(SandboxConfig) Validate() error`, `domain.EffectiveSandbox(base SandboxConfig, role RoleOverride) SandboxConfig`, `ProjectConfig.Sandbox SandboxConfig`, `RoleOverride.Sandbox *SandboxConfig`.

- [ ] **Step 1: Write the failing test**

```go
package domain

import (
	"strings"
	"testing"
)

func TestSandboxConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     SandboxConfig
		wantErr string
	}{
		{name: "zero is off", cfg: SandboxConfig{}},
		{name: "off keeps image", cfg: SandboxConfig{Mode: SandboxModeOff, Image: "ubuntu:24.04"}},
		{name: "per-agent", cfg: SandboxConfig{Mode: SandboxModePerAgent, Image: "ghcr.io/acme/dev@sha256:abc", CPUs: "1.5", Memory: "4g", PidsLimit: 512}},
		{name: "shared", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "node:22"}},
		{name: "unknown mode", cfg: SandboxConfig{Mode: "vm", Image: "x"}, wantErr: `sandbox.mode: unknown mode "vm"`},
		{name: "enabled needs image", cfg: SandboxConfig{Mode: SandboxModeShared}, wantErr: "sandbox.image"},
		{name: "flag injection", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "--privileged"}, wantErr: "sandbox.image"},
		{name: "bad cpus", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "x", CPUs: "two"}, wantErr: "sandbox.cpus"},
		{name: "bad memory", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "x", Memory: "4 GB"}, wantErr: "sandbox.memory"},
		{name: "negative pids", cfg: SandboxConfig{Mode: SandboxModeShared, Image: "x", PidsLimit: -1}, wantErr: "sandbox.pidsLimit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestEffectiveSandboxRoleOverrideReplacesWholeBlock(t *testing.T) {
	base := SandboxConfig{Mode: SandboxModeShared, Image: "node:22", Memory: "4g"}
	if got := EffectiveSandbox(base, RoleOverride{}); got != base {
		t.Fatalf("no override = %#v, want base", got)
	}
	off := SandboxConfig{Mode: SandboxModeOff}
	if got := EffectiveSandbox(base, RoleOverride{Sandbox: &off}); got != off {
		t.Fatalf("override = %#v, want %#v", got, off)
	}
}

func TestProjectConfigValidateChecksSandbox(t *testing.T) {
	cfg := DefaultProjectConfig()
	cfg.Sandbox = SandboxConfig{Mode: SandboxModePerAgent}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.image") {
		t.Fatalf("project sandbox error = %v", err)
	}
	cfg = DefaultProjectConfig()
	cfg.Worker.Sandbox = &SandboxConfig{Mode: "bogus"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "worker.sandbox.mode") {
		t.Fatalf("worker sandbox error = %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/domain/ -run 'Sandbox' -v`
Expected: FAIL to compile with `undefined: SandboxConfig`.

- [ ] **Step 3: Write minimal implementation**

`backend/internal/domain/sandbox.go`:

```go
package domain

import (
	"fmt"
	"regexp"
)

// SandboxMode selects where a session's agent process runs (#6663).
type SandboxMode string

// Sandbox modes. The empty mode means off.
const (
	SandboxModeOff      SandboxMode = "off"
	SandboxModeShared   SandboxMode = "shared"
	SandboxModePerAgent SandboxMode = "per-agent"
)

// SandboxConfig runs a project's agent processes inside a local Docker
// container: one per session (per-agent) or one per project (shared).
type SandboxConfig struct {
	Mode      SandboxMode `json:"mode,omitempty"`
	Image     string      `json:"image,omitempty"`
	CPUs      string      `json:"cpus,omitempty"`
	Memory    string      `json:"memory,omitempty"`
	PidsLimit int         `json:"pidsLimit,omitempty"`
}

var (
	// sandboxImagePattern also rejects a leading '-' so an image can never be
	// read as a docker flag.
	sandboxImagePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)
	sandboxCPUsPattern   = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	sandboxMemoryPattern = regexp.MustCompile(`^[0-9]+[bkmgBKMG]?$`)
)

// Enabled reports whether agents should run inside a container.
func (c SandboxConfig) Enabled() bool {
	return c.Mode == SandboxModeShared || c.Mode == SandboxModePerAgent
}

// Validate checks the mode and the values passed to `docker run`.
func (c SandboxConfig) Validate() error {
	switch c.Mode {
	case "", SandboxModeOff:
		return nil
	case SandboxModeShared, SandboxModePerAgent:
	default:
		return fmt.Errorf("sandbox.mode: unknown mode %q", c.Mode)
	}
	if !sandboxImagePattern.MatchString(c.Image) {
		return fmt.Errorf("sandbox.image: %q is not a valid image reference", c.Image)
	}
	if c.CPUs != "" && !sandboxCPUsPattern.MatchString(c.CPUs) {
		return fmt.Errorf("sandbox.cpus: %q must be a number such as 2 or 1.5", c.CPUs)
	}
	if c.Memory != "" && !sandboxMemoryPattern.MatchString(c.Memory) {
		return fmt.Errorf("sandbox.memory: %q must look like 512m or 4g", c.Memory)
	}
	if c.PidsLimit < 0 {
		return fmt.Errorf("sandbox.pidsLimit: must not be negative")
	}
	return nil
}

// EffectiveSandbox returns the sandbox for a role. A role override replaces
// the project block wholesale, so "orchestrator on host, workers sandboxed"
// is expressed as an orchestrator override with mode off.
func EffectiveSandbox(base SandboxConfig, role RoleOverride) SandboxConfig {
	if role.Sandbox != nil {
		return *role.Sandbox
	}
	return base
}
```

In `projectconfig.go`, add the project field directly after `ContainerReap` (line 70):

```go
	// Sandbox runs this project's agents inside a local Docker container
	// (#6663). The zero value keeps agents on the host.
	Sandbox SandboxConfig `json:"sandbox,omitempty"`
```

Extend `RoleOverride` (line 160):

```go
type RoleOverride struct {
	Harness     AgentHarness   `json:"agent,omitempty"`
	AgentConfig AgentConfig    `json:"agentConfig,omitempty"`
	Sandbox     *SandboxConfig `json:"sandbox,omitempty"`
}
```

In `ProjectConfig.Validate`, inside the role loop after the `AgentConfig.Validate()` check:

```go
		if ro.Sandbox != nil {
			if err := ro.Sandbox.Validate(); err != nil {
				return fmt.Errorf("%s.%w", role, err)
			}
		}
```

and directly after the loop:

```go
	if err := c.Sandbox.Validate(); err != nil {
		return err
	}
```

In `specgen/build.go` add, next to `"DomainContainerReapConfig": "ContainerReapConfig",`:

```go
	"DomainSandboxConfig":             "SandboxConfig",
```

- [ ] **Step 4: Run tests and regenerate the API**

Run: `cd backend && go test ./internal/domain/ -run 'Sandbox' -v`
Expected: PASS.

Run: `npm run api && cd backend && go test ./internal/httpd/...`
Expected: `openapi.yaml` and `schema.ts` show the new `SandboxConfig` schema; the httpd spec drift tests PASS. If the generator emits `SandboxMode` as its own named type, add `"DomainSandboxMode": "SandboxMode"` to the same map and rerun.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/domain/sandbox.go backend/internal/domain/sandbox_test.go backend/internal/domain/projectconfig.go backend/internal/httpd/apispec frontend/src/api/schema.ts
git commit -m "feat(domain): add per-project and per-role Docker sandbox config (#6663)"
```

---

### Task 2: CLI mirror keeps `sandbox` across `set-config`

**Files:**
- Modify: `backend/internal/cli/project.go:84-115` (mirror types)
- Test: `backend/internal/cli/project_test.go`

**Interfaces:**
- Consumes: JSON shape from Task 1 (`sandbox.mode`, `image`, `cpus`, `memory`, `pidsLimit`).
- Produces: `cli.sandboxConfig`, `projectConfig.Sandbox *sandboxConfig`, `roleOverride.Sandbox *sandboxConfig`.

- [ ] **Step 1: Write the failing test**

```go
func TestProjectSetConfig_SandboxJSONIsForwarded(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--config-json",
		`{"sandbox":{"mode":"shared","image":"node:22","memory":"4g"},"orchestrator":{"sandbox":{"mode":"off"}}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got setConfigRequest
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
	}
	if got.Config.Sandbox == nil || got.Config.Sandbox.Mode != "shared" || got.Config.Sandbox.Image != "node:22" || got.Config.Sandbox.Memory != "4g" {
		t.Fatalf("sandbox = %#v", got.Config.Sandbox)
	}
	if got.Config.Orchestrator.Sandbox == nil || got.Config.Orchestrator.Sandbox.Mode != "off" {
		t.Fatalf("orchestrator sandbox = %#v", got.Config.Orchestrator.Sandbox)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/cli/ -run TestProjectSetConfig_SandboxJSONIsForwarded -v`
Expected: FAIL to compile with `got.Config.Sandbox undefined`.

- [ ] **Step 3: Write minimal implementation**

In `project.go` next to `containerReapConfig`:

```go
// sandboxConfig mirrors domain.SandboxConfig.
type sandboxConfig struct {
	Mode      string `json:"mode,omitempty"`
	Image     string `json:"image,omitempty"`
	CPUs      string `json:"cpus,omitempty"`
	Memory    string `json:"memory,omitempty"`
	PidsLimit int    `json:"pidsLimit,omitempty"`
}
```

Add `Sandbox *sandboxConfig `json:"sandbox,omitempty"`` to `roleOverride` and, after `ContainerReap`, to `projectConfig`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/cli/ -run 'TestProjectSetConfig' -v`
Expected: PASS (new and existing set-config tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/cli/project.go backend/internal/cli/project_test.go
git commit -m "feat(cli): carry sandbox config through project set-config (#6663)"
```

---

### Task 3: Sandbox port and pure Docker helpers

**Files:**
- Create: `backend/internal/ports/sandbox.go`
- Create: `backend/internal/adapters/sandbox/dockersandbox/spec.go`
- Test: `backend/internal/adapters/sandbox/dockersandbox/spec_test.go`

**Interfaces:**
- Consumes: `domain.SandboxConfig` (Task 1).
- Produces (ports): `ErrSandboxUnavailable`, `ErrSandboxMisconfigured`, `SandboxRequest`, `SandboxTarget`, `Sandbox`, `AgentSandboxSupport` (exact code below).
- Produces (package-internal): `containerName(req) string`, `configHash(cfg, mounts) string`, `mount{Path string; ReadOnly bool}`, `runArgs(name, image string, cfg domain.SandboxConfig, labels map[string]string, mounts []mount, user string) []string`, `renderEnvFile(env map[string]string) ([]byte, error)`, `execArgv(docker string, t ports.SandboxTarget, workspacePath string, argv []string) []string`.

- [ ] **Step 1: Write the port** (`ports/sandbox.go`, no test needed on its own; Task 5 exercises it)

```go
package ports

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrSandboxUnavailable reports that Docker is missing or not running. A
// sandboxed spawn fails instead of falling back to the host.
var ErrSandboxUnavailable = errors.New("sandbox unavailable")

// ErrSandboxMisconfigured reports a sandbox that Docker can run but AO cannot
// use: the image lacks the harness, the workspace is outside the mounted root,
// or the session shape is not supported yet.
var ErrSandboxMisconfigured = errors.New("sandbox misconfigured")

// SandboxRequest asks for a running container that can host one session.
type SandboxRequest struct {
	SessionID       domain.SessionID
	ProjectID       domain.ProjectID
	Config          domain.SandboxConfig
	WorkspacePath   string
	RequireBinaries []string
}

// SandboxTarget is a ready container plus the host directories mounted into
// it at identical paths.
type SandboxTarget struct {
	Container  string
	Docker     string // absolute path of the docker CLI on the host
	BinDir     string // relay `ao` and `ao-enter`
	ProjectDir string // per-project sandbox state (home, harness config)
	SessionDir string // per-session env file and relay spool
	RelayDir   string
}

// Sandbox runs agent processes inside local containers (#6663).
type Sandbox interface {
	Ensure(ctx context.Context, req SandboxRequest) (SandboxTarget, error)
	// Exec writes the session env file and returns the argv that runs argv
	// inside the container. Secrets never appear in the returned argv.
	Exec(target SandboxTarget, workspacePath string, env map[string]string, argv []string) ([]string, error)
	// Release removes the project's shared containers when inUse is false.
	Release(ctx context.Context, project domain.ProjectID, inUse bool) error
}

// AgentSandboxSupport is implemented by harnesses that can run sandboxed.
type AgentSandboxSupport interface {
	// SandboxBinary is the harness executable name on the image's PATH.
	SandboxBinary() string
	// PrepareSandbox seeds harness state under projectDir and sets the env the
	// harness needs inside the container (credentials, config dir).
	PrepareSandbox(env map[string]string, dataDir, projectDir, workspacePath string) error
}
```

- [ ] **Step 2: Write the failing helper tests**

```go
package dockersandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	if got := containerName(shared, "abcdef0123456789"); got != "ao-sbx-my-proj-abcdef01" {
		t.Fatalf("shared name = %q", got)
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
	want := []string{"run", "-d", "--init", "--name", "ao-sbx-s1",
		"--label", "ao.sandbox=per-agent", "--label", "ao.session=s1",
		"--user", "501:20",
		"-v", "/w:/w", "-v", "/p:/p:ro",
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd backend && go test ./internal/adapters/sandbox/dockersandbox/ -v`
Expected: FAIL to compile with `undefined: containerName`.

- [ ] **Step 4: Write minimal implementation** (`spec.go`)

```go
// Package dockersandbox runs AO agent processes inside local Docker
// containers (#6663). The daemon, runtime and worktrees stay on the host; this
// package only creates containers and rewrites a launch into `docker exec`.
// Every host path is mounted at the same absolute path inside the container.
package dockersandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Labels on every AO sandbox container. Per-agent containers also carry
// dockerreap.SessionLabel so session termination removes them.
const (
	labelSandbox = "ao.sandbox"
	labelProject = "ao.project"
	labelHash    = "ao.sandbox.hash"
	labelGitDir  = "ao.sandbox.gitdir"
)

// keepAliveScript keeps the container alive without depending on the image's
// entrypoint and exits promptly on `docker stop`.
const keepAliveScript = `trap "exit 0" TERM INT; while :; do sleep 3600 & wait $!; done`

type mount struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"ro,omitempty"`
}

var (
	envKeyPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	nameUnsafeChar = regexp.MustCompile(`[^a-z0-9_.-]+`)
)

// hostOnlyEnv never crosses into the container: these describe the host
// process environment, and the image supplies its own.
var hostOnlyEnv = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "SHELL": true, "PWD": true,
	"OLDPWD": true, "USER": true, "LOGNAME": true, "TMUX": true, "TMUX_PANE": true,
}

var hostOnlyEnvPrefixes = []string{"DYLD_", "__CF", "TERM_PROGRAM", "XPC_"}

func sanitizeName(s string) string {
	return strings.Trim(nameUnsafeChar.ReplaceAllString(strings.ToLower(s), "-"), "-.")
}

func containerName(req ports.SandboxRequest, hash string) string {
	if req.Config.Mode == domain.SandboxModeShared {
		return "ao-sbx-" + sanitizeName(string(req.ProjectID)) + "-" + hash[:8]
	}
	return "ao-sbx-" + sanitizeName(string(req.SessionID))
}

func configHash(cfg domain.SandboxConfig, mounts []mount) string {
	data, _ := json.Marshal(struct {
		Cfg    domain.SandboxConfig `json:"cfg"`
		Mounts []mount              `json:"mounts"`
	}{cfg, mounts})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

func runArgs(name string, cfg domain.SandboxConfig, labels map[string]string, mounts []mount, user string) []string {
	args := []string{"run", "-d", "--init", "--name", name}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}
	if user != "" {
		args = append(args, "--user", user)
	}
	for _, m := range mounts {
		spec := m.Path + ":" + m.Path
		if m.ReadOnly {
			spec += ":ro"
		}
		args = append(args, "-v", spec)
	}
	if cfg.CPUs != "" {
		args = append(args, "--cpus", cfg.CPUs)
	}
	if cfg.Memory != "" {
		args = append(args, "--memory", cfg.Memory)
	}
	if cfg.PidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprint(cfg.PidsLimit))
	}
	return append(args, "--entrypoint", "/bin/sh", cfg.Image, "-c", keepAliveScript)
}

// renderEnvFile renders env as POSIX sh assignments for `set -a; . file`.
// Values are single-quoted so the shell never expands them.
func renderEnvFile(env map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if hostOnlyEnv[k] || hasHostOnlyPrefix(k) {
			continue
		}
		if !envKeyPattern.MatchString(k) {
			return nil, fmt.Errorf("sandbox env: invalid variable name %q", k)
		}
		v := env[k]
		if strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("sandbox env: %s contains a NUL byte", k)
		}
		b.WriteString(k + "='" + strings.ReplaceAll(v, "'", `'\''`) + "'\n")
	}
	return []byte(b.String()), nil
}

func hasHostOnlyPrefix(k string) bool {
	for _, p := range hostOnlyEnvPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func execArgv(docker string, t ports.SandboxTarget, workspacePath string, argv []string) []string {
	out := []string{docker, "exec", "-i", "-t", "-w", workspacePath, t.Container,
		"/bin/sh", t.BinDir + "/ao-enter", t.SessionDir + "/env"}
	return append(out, argv...)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/adapters/sandbox/dockersandbox/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/ports/sandbox.go backend/internal/adapters/sandbox/dockersandbox/spec.go backend/internal/adapters/sandbox/dockersandbox/spec_test.go
git commit -m "feat(sandbox): add sandbox port and docker launch helpers (#6663)"
```

---

### Task 4: In-container scripts (`ao` relay client, `ao-enter`)

**Files:**
- Create: `backend/internal/adapters/sandbox/dockersandbox/scripts/ao`
- Create: `backend/internal/adapters/sandbox/dockersandbox/scripts/ao-enter`
- Create: `backend/internal/adapters/sandbox/dockersandbox/scripts.go`
- Test: `backend/internal/adapters/sandbox/dockersandbox/scripts_test.go`

**Interfaces:**
- Produces: `InstallBin(dir string) error` writes both scripts 0755. Relay wire format, used by Task 6:
  - The client creates `req/<id>.tmp/`, writes `argv` (NUL-terminated args) and `stdin`, then renames it to `req/<id>`.
  - The host renames `req/<id>` to `work/<id>`, then writes `resp/<id>.tmp/{stdout,stderr,exit}` and renames it to `resp/<id>`.
  - The client reads the response and removes `resp/<id>`.
  - `<id>` matches `^[0-9]+-[0-9]+-[0-9]+$`.

- [ ] **Step 1: Write the failing test.** It simulates the host side with a goroutine, so it needs no Docker.

```go
package dockersandbox

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelayClientScriptRoundTrip(t *testing.T) {
	bin := t.TempDir()
	if err := InstallBin(bin); err != nil {
		t.Fatal(err)
	}
	relay := t.TempDir()
	for _, d := range []string{"req", "resp"} {
		if err := os.MkdirAll(filepath.Join(relay, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan []string, 1)
	go func() { // fake host: answer the first request
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			entries, _ := os.ReadDir(filepath.Join(relay, "req"))
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".tmp") {
					continue
				}
				req := filepath.Join(relay, "req", e.Name())
				raw, _ := os.ReadFile(filepath.Join(req, "argv"))
				stdin, _ := os.ReadFile(filepath.Join(req, "stdin"))
				args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
				tmp := filepath.Join(relay, "resp", e.Name()+".tmp")
				_ = os.MkdirAll(tmp, 0o700)
				_ = os.WriteFile(filepath.Join(tmp, "stdout"), append([]byte("out:"), stdin...), 0o600)
				_ = os.WriteFile(filepath.Join(tmp, "stderr"), []byte("err"), 0o600)
				_ = os.WriteFile(filepath.Join(tmp, "exit"), []byte("3"), 0o600)
				_ = os.Rename(tmp, filepath.Join(relay, "resp", e.Name()))
				done <- args
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		done <- nil
	}()
	cmd := exec.Command("/bin/sh", filepath.Join(bin, "ao"), "hooks", "claude-code", "arg with space")
	cmd.Env = append(os.Environ(), "AO_SANDBOX_RELAY="+relay)
	cmd.Stdin = strings.NewReader(`{"hook":"x"}`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err == nil || !errorsAs(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("exit = %v, want code 3", err)
	}
	if got := <-done; strings.Join(got, "|") != "hooks|claude-code|arg with space" {
		t.Fatalf("argv = %q", got)
	}
	if stdout.String() != `out:{"hook":"x"}` || stderr.String() != "err" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(relay, "resp")); len(entries) != 0 {
		t.Fatalf("client left response behind: %v", entries)
	}
}

func TestAoEnterSourcesEnvAndPrependsBin(t *testing.T) {
	bin := t.TempDir()
	if err := InstallBin(bin); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(envFile, []byte("GREETING='hi there'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", filepath.Join(bin, "ao-enter"), envFile,
		"/bin/sh", "-c", `printf '%s|%s' "$GREETING" "${PATH%%:*}"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hi there|"+bin {
		t.Fatalf("ao-enter output = %q", out)
	}
}

func errorsAs(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	*target = e
	return ok
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/sandbox/dockersandbox/ -run 'Relay|AoEnter' -v`
Expected: FAIL to compile with `undefined: InstallBin`.

- [ ] **Step 3: Write the scripts and installer**

`scripts/ao`:

```sh
#!/bin/sh
# AO sandbox relay client (#6663). Inside a sandbox container this replaces the
# `ao` CLI: it hands argv and stdin to the AO supervisor on the host through a
# mounted spool directory and replays the host command's output and exit code.
set -eu
relay="${AO_SANDBOX_RELAY:?AO_SANDBOX_RELAY is not set}"
id="$(date +%s)-$$-$(od -An -N4 -tu4 /dev/urandom | tr -d ' ')"
tmp="$relay/req/$id.tmp"
mkdir -p "$tmp"
for arg in "$@"; do printf '%s\0' "$arg"; done > "$tmp/argv"
if [ -t 0 ]; then : > "$tmp/stdin"; else cat > "$tmp/stdin"; fi
mv "$tmp" "$relay/req/$id"
resp="$relay/resp/$id"
i=0
while [ ! -d "$resp" ]; do
	i=$((i + 1))
	if [ "$i" -gt 1300 ]; then
		echo "ao: sandbox relay timed out" >&2
		exit 124
	fi
	sleep 0.05 2>/dev/null || sleep 1
done
cat "$resp/stdout"
cat "$resp/stderr" >&2
code="$(cat "$resp/exit")"
rm -rf "$resp"
exit "$code"
```

`scripts/ao-enter`:

```sh
#!/bin/sh
# Entry point for sandboxed agent launches (#6663): load the session env the
# host wrote, put the relay `ao` first on PATH, then exec the harness.
set -eu
envfile="$1"
shift
set -a
. "$envfile"
set +a
PATH="$(cd "$(dirname "$0")" && pwd):$PATH"
export PATH
exec "$@"
```

`scripts.go`:

```go
package dockersandbox

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed scripts/ao scripts/ao-enter
var scripts embed.FS

// InstallBin writes the in-container helper scripts into dir. The daemon
// rewrites them on every Ensure so an upgraded AO never runs stale scripts.
func InstallBin(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sandbox bin dir: %w", err)
	}
	for _, name := range []string{"ao", "ao-enter"} {
		data, err := scripts.ReadFile("scripts/" + name)
		if err != nil {
			return err
		}
		tmp := filepath.Join(dir, "."+name+".tmp")
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return fmt.Errorf("write sandbox script %s: %w", name, err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("install sandbox script %s: %w", name, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/adapters/sandbox/dockersandbox/ -run 'Relay|AoEnter' -v -count=3`
Expected: PASS, three times in a row. The repeat checks for timing flakiness.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/sandbox/dockersandbox/scripts backend/internal/adapters/sandbox/dockersandbox/scripts.go backend/internal/adapters/sandbox/dockersandbox/scripts_test.go
git commit -m "feat(sandbox): add in-container ao relay client and launch entry (#6663)"
```

---

### Task 5: Docker sandbox adapter (`Ensure`, `Exec`, `Release`)

**Files:**
- Create: `backend/internal/adapters/sandbox/dockersandbox/sandbox.go`
- Modify: `backend/internal/adapters/container/dockerreap/reap.go:142` (rename `isDockerUnavailable` to exported `IsDockerUnavailable`, update its callers in that file)
- Test: `backend/internal/adapters/sandbox/dockersandbox/sandbox_test.go`

**Interfaces:**
- Consumes: Task 3 helpers, Task 4 `InstallBin`, `dockerreap.SessionLabel`, `dockerreap.IsDockerUnavailable(err) bool`.
- Produces: `dockersandbox.Config{DataDir, ManagedRoot string}`, `dockersandbox.New(cfg Config) *Sandbox` implementing `ports.Sandbox`; test seam `newWithRunner(cfg Config, r commandRunner, docker string) *Sandbox`.

- [ ] **Step 1: Write the failing tests** (scripted fake runner in the `dockerreap` style)

```go
package dockersandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeCall struct {
	match string // substring the joined argv must contain
	out   string
	err   error
}

type fakeRunner struct {
	t      *testing.T
	script []fakeCall
	calls  []string
}

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if len(f.script) == 0 {
		f.t.Fatalf("unexpected call: %s", line)
	}
	c := f.script[0]
	f.script = f.script[1:]
	if !strings.Contains(line, c.match) {
		f.t.Fatalf("call %q does not contain %q", line, c.match)
	}
	return []byte(c.out), c.err
}

func testSandbox(t *testing.T, script []fakeCall) (*Sandbox, *fakeRunner, string) {
	t.Helper()
	data := t.TempDir()
	fr := &fakeRunner{t: t, script: script}
	return newWithRunner(Config{DataDir: data, ManagedRoot: filepath.Join(data, "worktrees")}, fr, "/usr/bin/docker"), fr, data
}

func perAgentReq(ws string) ports.SandboxRequest {
	return ports.SandboxRequest{SessionID: "s1", ProjectID: "p", WorkspacePath: ws, RequireBinaries: []string{"claude"},
		Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "node:22"}}
}

func TestEnsureCreatesPerAgentContainer(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "rev-parse --path-format=absolute --git-common-dir", out: "/repo/.git\n"},
		{match: "inspect", err: errors.New("Error: No such object: ao-sbx-s1")},
		{match: "run -d --init --name ao-sbx-s1"},
		{match: "exec ao-sbx-s1 /bin/sh -c command -v 'claude'"},
	})
	ws := filepath.Join(data, "worktrees", "p", "s1")
	target, err := s.Ensure(context.Background(), perAgentReq(ws))
	if err != nil {
		t.Fatal(err)
	}
	run := fr.calls[2]
	for _, want := range []string{"--label ao.session=s1", "-v " + ws + ":" + ws, "-v /repo/.git:/repo/.git",
		filepath.Join(data, "prompts") + ":ro", filepath.Join(data, "sandbox", "bin") + ":ro"} {
		if !strings.Contains(run, want) {
			t.Fatalf("docker run missing %q:\n%s", want, run)
		}
	}
	if target.Container != "ao-sbx-s1" || target.SessionDir != filepath.Join(data, "sandbox", "projects", "p", "sessions", "s1") {
		t.Fatalf("target = %#v", target)
	}
	if _, err := os.Stat(filepath.Join(target.BinDir, "ao-enter")); err != nil {
		t.Fatalf("scripts not installed: %v", err)
	}
}

func TestEnsureReusesRunningContainerWithSameHash(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	ws := filepath.Join(data, "worktrees", "p", "s1")
	hash := s.hashFor(perAgentReq(ws), "/repo/.git")
	s.run.(*fakeRunner).script = []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", out: "running\t" + hash + "\n"},
		{match: "command -v 'claude'"},
	}
	if _, err := s.Ensure(context.Background(), perAgentReq(ws)); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureStartsStoppedContainer(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	ws := filepath.Join(data, "worktrees", "p", "s1")
	hash := s.hashFor(perAgentReq(ws), "/repo/.git")
	s.run.(*fakeRunner).script = []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", out: "exited\t" + hash + "\n"},
		{match: "start ao-sbx-s1"},
		{match: "command -v 'claude'"},
	}
	if _, err := s.Ensure(context.Background(), perAgentReq(ws)); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDockerUnavailable(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")},
	})
	_, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1")))
	if !errors.Is(err, ports.ErrSandboxUnavailable) {
		t.Fatalf("err = %v, want ErrSandboxUnavailable", err)
	}
}

func TestEnsureMissingHarnessBinary(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v 'claude'", err: errors.New("exit status 1")},
	})
	_, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1")))
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), `node:22`) || !strings.Contains(err.Error(), `"claude"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureSharedRejectsWorkspaceOutsideManagedRoot(t *testing.T) {
	s, _, _ := testSandbox(t, []fakeCall{{match: "rev-parse", out: "/repo/.git\n"}})
	req := perAgentReq("/elsewhere/s1")
	req.Config.Mode = domain.SandboxModeShared
	if _, err := s.Ensure(context.Background(), req); !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured", err)
	}
}

func TestEnsureSharedHasNoSessionLabel(t *testing.T) {
	s, fr, data := testSandbox(t, nil)
	ws := filepath.Join(data, "worktrees", "p", "s1")
	req := perAgentReq(ws)
	req.Config.Mode = domain.SandboxModeShared
	fr.script = []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	}
	if _, err := s.Ensure(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	run := fr.calls[2]
	if strings.Contains(run, "ao.session=") || !strings.Contains(run, "-v "+filepath.Join(data, "worktrees", "p")+":") {
		t.Fatalf("shared run args wrong:\n%s", run)
	}
}

func TestExecDoesNotPutEnvInArgv(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	target := s.targetFor("ao-sbx-s1", "p", "s1")
	if err := os.MkdirAll(target.SessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	argv, err := s.Exec(target, filepath.Join(data, "w"), map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "secret"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "secret") {
		t.Fatalf("secret in argv: %q", argv)
	}
	info, err := os.Stat(filepath.Join(target.SessionDir, "env"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("env file = %v, %v; want 0600", info, err)
	}
	body, _ := os.ReadFile(filepath.Join(target.SessionDir, "env"))
	for _, want := range []string{"HOME='" + filepath.Join(target.ProjectDir, "home") + "'", "AO_SANDBOX_RELAY='" + target.RelayDir + "'"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("env file missing %q:\n%s", want, body)
		}
	}
}

func TestReleaseRemovesSharedContainersOnlyWhenUnused(t *testing.T) {
	s, fr, _ := testSandbox(t, nil)
	if err := s.Release(context.Background(), "p", true); err != nil || len(fr.calls) != 0 {
		t.Fatalf("in-use release = %v, calls %v", err, fr.calls)
	}
	fr.script = []fakeCall{
		{match: "ps -a --filter label=ao.project=p --filter label=ao.sandbox=shared", out: "c1\nc2\n"},
		{match: "rm -f c1"},
		{match: "rm -f c2"},
	}
	if err := s.Release(context.Background(), "p", false); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/adapters/sandbox/dockersandbox/ -run 'Ensure|Exec|Release' -v`
Expected: FAIL to compile with `undefined: newWithRunner`.

- [ ] **Step 3: Write minimal implementation** (`sandbox.go`)

```go
package dockersandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/container/dockerreap"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	probeTimeout  = 30 * time.Second
	createTimeout = 10 * time.Minute // `docker run` may pull the image first
)

// dockerFallbacks covers GUI launches whose PATH lacks the Docker CLI.
var dockerFallbacks = []string{
	"/usr/local/bin/docker",
	"/opt/homebrew/bin/docker",
	"/Applications/Docker.app/Contents/Resources/bin/docker",
}

type commandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && stderr.Len() > 0 {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

// Config locates AO state. Both paths must be under the AO data dir.
type Config struct {
	DataDir     string
	ManagedRoot string // workspace adapter's managed worktree root
}

// Sandbox implements ports.Sandbox with the docker CLI.
type Sandbox struct {
	cfg    Config
	run    commandRunner
	docker string // resolved lazily unless injected by tests

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

var _ ports.Sandbox = (*Sandbox)(nil)

// New returns a docker-CLI sandbox.
func New(cfg Config) *Sandbox { return newWithRunner(cfg, execRunner{}, "") }

func newWithRunner(cfg Config, r commandRunner, docker string) *Sandbox {
	return &Sandbox{cfg: cfg, run: r, docker: docker, locks: map[string]*sync.Mutex{}}
}

func (s *Sandbox) lock(name string) func() {
	s.mu.Lock()
	l, ok := s.locks[name]
	if !ok {
		l = &sync.Mutex{}
		s.locks[name] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *Sandbox) dockerPath() (string, error) {
	if s.docker != "" {
		return s.docker, nil
	}
	if p, err := exec.LookPath("docker"); err == nil {
		return p, nil
	}
	for _, p := range dockerFallbacks {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: the docker CLI was not found; install Docker Desktop or Docker Engine", ports.ErrSandboxUnavailable)
}

func (s *Sandbox) targetFor(container string, project domain.ProjectID, session domain.SessionID) ports.SandboxTarget {
	projectDir := filepath.Join(s.cfg.DataDir, "sandbox", "projects", string(project))
	sessionDir := filepath.Join(projectDir, "sessions", string(session))
	return ports.SandboxTarget{
		Container:  container,
		Docker:     s.docker,
		BinDir:     filepath.Join(s.cfg.DataDir, "sandbox", "bin"),
		ProjectDir: projectDir,
		SessionDir: sessionDir,
		RelayDir:   filepath.Join(sessionDir, "relay"),
	}
}

func (s *Sandbox) mounts(req ports.SandboxRequest, gitDir string) ([]mount, error) {
	work := req.WorkspacePath
	if req.Config.Mode == domain.SandboxModeShared {
		work = filepath.Join(s.cfg.ManagedRoot, string(req.ProjectID))
		rel, err := filepath.Rel(work, req.WorkspacePath)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("%w: shared sandbox needs an AO-managed worktree under %s, got %s",
				ports.ErrSandboxMisconfigured, work, req.WorkspacePath)
		}
	}
	return []mount{
		{Path: work},
		{Path: gitDir},
		{Path: filepath.Join(s.cfg.DataDir, "prompts"), ReadOnly: true},
		{Path: filepath.Join(s.cfg.DataDir, "sandbox", "projects", string(req.ProjectID))},
		{Path: filepath.Join(s.cfg.DataDir, "sandbox", "bin"), ReadOnly: true},
	}, nil
}

func (s *Sandbox) gitCommonDir(ctx context.Context, workspace string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := s.run.Output(ctx, "git", "-C", workspace, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%w: resolve git dir for %s: %v", ports.ErrSandboxMisconfigured, workspace, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Sandbox) hashFor(req ports.SandboxRequest, gitDir string) string {
	m, _ := s.mounts(req, gitDir)
	return configHash(req.Config, m)
}

// Ensure returns a running container for req, creating or starting it when
// needed. It is idempotent so restore after a Docker restart works.
func (s *Sandbox) Ensure(ctx context.Context, req ports.SandboxRequest) (ports.SandboxTarget, error) {
	if !req.Config.Enabled() {
		return ports.SandboxTarget{}, fmt.Errorf("%w: sandbox mode is off", ports.ErrSandboxMisconfigured)
	}
	docker, err := s.dockerPath()
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	gitDir, err := s.gitCommonDir(ctx, req.WorkspacePath)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	mounts, err := s.mounts(req, gitDir)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	hash := configHash(req.Config, mounts)
	name := containerName(req, hash)
	unlock := s.lock(name)
	defer unlock()

	target := s.targetFor(name, req.ProjectID, req.SessionID)
	target.Docker = docker
	for _, dir := range []string{filepath.Join(target.ProjectDir, "home"), filepath.Join(target.RelayDir, "req"),
		filepath.Join(target.RelayDir, "work"), filepath.Join(target.RelayDir, "resp"), filepath.Join(s.cfg.DataDir, "prompts")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return ports.SandboxTarget{}, fmt.Errorf("sandbox state dir: %w", err)
		}
	}
	if err := InstallBin(target.BinDir); err != nil {
		return ports.SandboxTarget{}, err
	}

	status, existingHash, err := s.inspect(ctx, docker, name)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	switch {
	case status != "" && existingHash != hash:
		// Only per-agent names can collide across config changes; shared
		// names embed the hash. The worktree lives on the host, so recreating
		// loses only container-local installs.
		if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", name); err != nil {
			return ports.SandboxTarget{}, err
		}
		fallthrough
	case status == "":
		labels := map[string]string{labelSandbox: string(req.Config.Mode), labelProject: string(req.ProjectID), labelHash: hash, labelGitDir: gitDir}
		if req.Config.Mode == domain.SandboxModePerAgent {
			labels[dockerreap.SessionLabel] = string(req.SessionID)
		}
		if _, err := s.output(ctx, createTimeout, docker, runArgs(name, req.Config, labels, mounts, hostUser())...); err != nil {
			return ports.SandboxTarget{}, fmt.Errorf("start sandbox from image %s: %w", req.Config.Image, err)
		}
	case status != "running":
		if _, err := s.output(ctx, probeTimeout, docker, "start", name); err != nil {
			return ports.SandboxTarget{}, err
		}
	}
	for _, bin := range req.RequireBinaries {
		if _, err := s.output(ctx, probeTimeout, docker, "exec", name, "/bin/sh", "-c", "command -v '"+bin+"'"); err != nil {
			if errors.Is(err, ports.ErrSandboxUnavailable) {
				return ports.SandboxTarget{}, err
			}
			return ports.SandboxTarget{}, fmt.Errorf("%w: image %s has no %q on PATH (and needs /bin/sh)",
				ports.ErrSandboxMisconfigured, req.Config.Image, bin)
		}
	}
	return target, nil
}

// inspect returns ("", "", nil) when the container does not exist.
func (s *Sandbox) inspect(ctx context.Context, docker, name string) (string, string, error) {
	out, err := s.output(ctx, probeTimeout, docker, "inspect", "--type", "container",
		"--format", `{{.State.Status}}	{{index .Config.Labels "`+labelHash+`"}}`, name)
	if err != nil {
		if strings.Contains(err.Error(), "No such object") || strings.Contains(err.Error(), "No such container") {
			return "", "", nil
		}
		return "", "", err
	}
	status, hash, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	return status, hash, nil
}

func (s *Sandbox) output(ctx context.Context, timeout time.Duration, docker string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := s.run.Output(ctx, docker, args...)
	if err != nil && dockerreap.IsDockerUnavailable(err) {
		return out, fmt.Errorf("%w: Docker is not running or not reachable: %v", ports.ErrSandboxUnavailable, err)
	}
	return out, err
}

// Exec writes the session env file (0600) and returns the docker exec argv.
func (s *Sandbox) Exec(target ports.SandboxTarget, workspacePath string, env map[string]string, argv []string) ([]string, error) {
	merged := make(map[string]string, len(env)+2)
	for k, v := range env {
		merged[k] = v
	}
	data, err := renderEnvFile(merged)
	if err != nil {
		return nil, err
	}
	// Set after rendering so hostOnlyEnv cannot drop them.
	data = append(data, []byte("HOME='"+filepath.Join(target.ProjectDir, "home")+"'\nAO_SANDBOX='1'\nAO_SANDBOX_RELAY='"+target.RelayDir+"'\n")...)
	path := filepath.Join(target.SessionDir, "env")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return nil, fmt.Errorf("write sandbox env: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("install sandbox env: %w", err)
	}
	docker := target.Docker
	if docker == "" {
		if docker, err = s.dockerPath(); err != nil {
			return nil, err
		}
	}
	return execArgv(docker, target, workspacePath, argv), nil
}

// Release removes the project's shared containers once nothing uses them.
// An unreachable Docker is not an error: there is nothing to release.
func (s *Sandbox) Release(ctx context.Context, project domain.ProjectID, inUse bool) error {
	if inUse {
		return nil
	}
	docker, err := s.dockerPath()
	if err != nil {
		return nil
	}
	out, err := s.output(ctx, probeTimeout, docker, "ps", "-a", "--filter", "label="+labelProject+"="+string(project),
		"--filter", "label="+labelSandbox+"=shared", "--format", "{{.ID}}")
	if err != nil {
		if errors.Is(err, ports.ErrSandboxUnavailable) {
			return nil
		}
		return err
	}
	var errs []error
	for _, id := range strings.Fields(string(out)) {
		if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// hostUser runs the container as the host user so files written to the
// worktree keep the user's ownership on Linux.
func hostUser() string {
	return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
}
```

In `dockerreap/reap.go`, rename `isDockerUnavailable` to `IsDockerUnavailable`. Add the doc comment `// IsDockerUnavailable reports errors meaning "no usable docker", which callers treat as nothing to do.` and update the existing call sites in `reap.go` and `reap_test.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/adapters/sandbox/... ./internal/adapters/container/... -v`
Expected: PASS. If a run-args assertion fails on mount ordering, fix the test expectation, not the order in `mounts()`. If `TestEnsureDockerUnavailable` fails, check whether `IsDockerUnavailable` matches "Cannot connect to the Docker daemon" case-sensitively. If it does, make the match case-insensitive (`strings.ToLower`) and add a reap test for it.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/sandbox backend/internal/adapters/container/dockerreap
git commit -m "feat(sandbox): docker sandbox adapter with per-agent and shared containers (#6663)"
```

---

### Task 6: Host-side relay in `ao agent-process supervise`

**Files:**
- Create: `backend/internal/cli/sandbox_relay.go`
- Modify: `backend/internal/cli/agent_process.go:28-98` (flag + start relay)
- Test: `backend/internal/cli/sandbox_relay_test.go`

**Interfaces:**
- Consumes: the spool format from Task 4.
- Produces: the hidden flag `--sandbox-relay <dir>` on `ao agent-process supervise`, `sandboxRelayPolicy map[string]bool`, and `serveSandboxRelay(ctx context.Context, dir string, run relayRunFunc)`, where `relayRunFunc func(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int)`.

- [ ] **Step 1: Write the failing tests**

```go
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxRelayPolicyCoversEveryRootCommand(t *testing.T) {
	root := NewRootCommand(Deps{})
	for _, c := range root.Commands() {
		name := c.Name()
		if name == "help" {
			continue
		}
		if _, ok := sandboxRelayPolicy[name]; !ok {
			t.Errorf("root command %q has no sandbox relay decision; add it to sandboxRelayPolicy", name)
		}
	}
	for _, denied := range []string{"launch", "agent-process", "pty-host", "project", "daemon"} {
		if sandboxRelayPolicy[denied] {
			t.Errorf("%q must be denied: it can execute host commands or disable the sandbox", denied)
		}
	}
}

func writeRelayRequest(t *testing.T, dir, id string, argv []string, stdin string) {
	t.Helper()
	tmp := filepath.Join(dir, "req", id+".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	var raw strings.Builder
	for _, a := range argv {
		raw.WriteString(a + "\x00")
	}
	_ = os.WriteFile(filepath.Join(tmp, "argv"), []byte(raw.String()), 0o600)
	_ = os.WriteFile(filepath.Join(tmp, "stdin"), []byte(stdin), 0o600)
	if err := os.Rename(tmp, filepath.Join(dir, "req", id)); err != nil {
		t.Fatal(err)
	}
}

func waitRelayResponse(t *testing.T, dir, id string) (string, string, string) {
	t.Helper()
	resp := filepath.Join(dir, "resp", id)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(resp); err == nil {
			read := func(n string) string { b, _ := os.ReadFile(filepath.Join(resp, n)); return string(b) }
			return read("stdout"), read("stderr"), read("exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no relay response for %s", id)
	return "", "", ""
}

func newRelayDir(t *testing.T) string {
	dir := t.TempDir()
	for _, d := range []string{"req", "work", "resp"} {
		_ = os.MkdirAll(filepath.Join(dir, d), 0o700)
	}
	return dir
}

func TestSandboxRelayRunsAllowedCommand(t *testing.T) {
	dir := newRelayDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gotArgv []string
	var gotStdin string
	go serveSandboxRelay(ctx, dir, func(_ context.Context, argv []string, stdin []byte) ([]byte, []byte, int) {
		gotArgv, gotStdin = argv, string(stdin)
		return []byte("ok"), []byte("warn"), 0
	})
	writeRelayRequest(t, dir, "1-2-3", []string{"hooks", "claude-code", "stop"}, `{"e":1}`)
	out, errOut, code := waitRelayResponse(t, dir, "1-2-3")
	if out != "ok" || errOut != "warn" || code != "0" {
		t.Fatalf("resp = %q %q %q", out, errOut, code)
	}
	if strings.Join(gotArgv, " ") != "hooks claude-code stop" || gotStdin != `{"e":1}` {
		t.Fatalf("ran %q with stdin %q", gotArgv, gotStdin)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "work")); len(entries) != 0 {
		t.Fatalf("work dir not cleaned: %v", entries)
	}
}

func TestSandboxRelayRefusesDeniedCommand(t *testing.T) {
	dir := newRelayDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ran := false
	go serveSandboxRelay(ctx, dir, func(context.Context, []string, []byte) ([]byte, []byte, int) {
		ran = true
		return nil, nil, 0
	})
	for i, argv := range [][]string{
		{"agent-process", "supervise", "--", "sh"},
		{"--data-dir", "/x", "hooks"}, // leading global flags are refused, not parsed
		{},
	} {
		id := "9-9-" + string(rune('0'+i))
		writeRelayRequest(t, dir, id, argv, "")
		_, errOut, code := waitRelayResponse(t, dir, id)
		if code != "126" || !strings.Contains(errOut, "not allowed in the sandbox") {
			t.Fatalf("argv %q: code %q stderr %q", argv, code, errOut)
		}
	}
	if ran {
		t.Fatal("denied command reached the host runner")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/cli/ -run 'SandboxRelay' -v`
Expected: FAIL to compile with `undefined: sandboxRelayPolicy`.

- [ ] **Step 3: Write minimal implementation** (`sandbox_relay.go`)

```go
package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	sandboxRelayPoll     = 50 * time.Millisecond
	sandboxRelayTimeout  = 60 * time.Second
	sandboxRelayMaxBytes = 8 << 20
)

var sandboxRelayIDPattern = regexp.MustCompile(`^[0-9]+-[0-9]+-[0-9]+$`)

// sandboxRelayPolicy decides which top-level `ao` commands a sandboxed agent
// may run on the host (#6663). Every root command must be listed. Denied
// commands either execute arbitrary host argv (launch, agent-process,
// pty-host, chat-host), manage the daemon or credentials, or could switch the
// sandbox off (project).
var sandboxRelayPolicy = map[string]bool{
	"hooks": true, "report": true, "send": true, "spawn": true, "preview": true,
	"render": true, "browser": true, "session": true, "orchestrator": true,
	"pr": true, "review": true, "automation": true, "cue": true, "status": true,
	"version": true,

	"daemon": false, "start": false, "stop": false, "remote-host": false,
	"doctor": false, "agent": false, "mcp": false, "agent-process": false,
	"chat-host": false, "unreal-provider": false, "launch": false,
	"pty-host": false, "codex-login": false, "claude-login": false,
	"import": false, "dev": false, "project": false, "completion": false,
}

type relayRunFunc func(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int)

// serveSandboxRelay answers relay requests under dir until ctx ends.
func serveSandboxRelay(ctx context.Context, dir string, run relayRunFunc) {
	ticker := time.NewTicker(sandboxRelayPoll)
	defer ticker.Stop()
	for {
		entries, _ := os.ReadDir(filepath.Join(dir, "req"))
		for _, e := range entries {
			id := e.Name()
			if !e.IsDir() || !sandboxRelayIDPattern.MatchString(id) {
				continue
			}
			work := filepath.Join(dir, "work", id)
			// Rename claims the request so it is handled exactly once.
			if err := os.Rename(filepath.Join(dir, "req", id), work); err != nil {
				continue
			}
			go handleSandboxRelay(ctx, dir, id, work, run)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func handleSandboxRelay(ctx context.Context, dir, id, work string, run relayRunFunc) {
	defer os.RemoveAll(work)
	argv := splitRelayArgv(readCapped(filepath.Join(work, "argv")))
	stdin := readCapped(filepath.Join(work, "stdin"))
	var stdout, stderr []byte
	code := 126
	if len(argv) == 0 || !sandboxRelayPolicy[argv[0]] {
		name := ""
		if len(argv) > 0 {
			name = argv[0]
		}
		stderr = []byte(fmt.Sprintf("ao: %q is not allowed in the sandbox\n", name))
	} else {
		rctx, cancel := context.WithTimeout(ctx, sandboxRelayTimeout)
		stdout, stderr, code = run(rctx, argv, stdin)
		cancel()
	}
	tmp := filepath.Join(dir, "resp", id+".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(tmp, "stdout"), stdout, 0o600)
	_ = os.WriteFile(filepath.Join(tmp, "stderr"), stderr, 0o600)
	_ = os.WriteFile(filepath.Join(tmp, "exit"), []byte(strconv.Itoa(code)), 0o600)
	_ = os.Rename(tmp, filepath.Join(dir, "resp", id))
}

func readCapped(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, sandboxRelayMaxBytes))
	return data
}

func splitRelayArgv(raw []byte) []string {
	s := strings.TrimSuffix(string(raw), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// hostAORunner runs the host `ao` binary with the supervisor's environment,
// which is the session's host env, so AO_* identity comes from the host and
// never from the container.
func hostAORunner(exe string) relayRunFunc {
	return func(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, int) {
		cmd := aoprocess.CommandContext(ctx, exe, argv...)
		cmd.Stdin = bytes.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			if cmd.ProcessState != nil {
				return out.Bytes(), errb.Bytes(), cmd.ProcessState.ExitCode()
			}
			return out.Bytes(), append(errb.Bytes(), []byte("ao: relay: "+err.Error()+"\n")...), 1
		}
		return out.Bytes(), errb.Bytes(), 0
	}
}
```

In `agent_process.go`, add `var sandboxRelay string` and the flag:

```go
	cmd.Flags().StringVar(&sandboxRelay, "sandbox-relay", "", "serve in-container ao calls from this spool dir (internal)")
```

Pass it through: `ctx.runSupervisedProcess(cmd.Context(), sessionID, activityReviewID, launchID, strings.TrimSpace(sandboxRelay), args)`. In `runSupervisedProcess`, add the parameter `relayDir string`, and after `child.Start()` succeeds:

```go
	if relayDir != "" {
		relayCtx, stopRelay := context.WithCancel(ctx)
		defer stopRelay()
		exe, err := os.Executable()
		if err != nil {
			_, _ = fmt.Fprintf(c.deps.Err, "ao: sandbox relay disabled: %v\n", err)
		} else {
			go serveSandboxRelay(relayCtx, relayDir, hostAORunner(exe))
		}
	}
```

Update the existing `runSupervisedProcess` call sites and tests to pass `""`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/cli/ -run 'SandboxRelay|AgentProcess' -v`
Expected: PASS. If `TestSandboxRelayPolicyCoversEveryRootCommand` names a command missing from the map, decide allow or deny on purpose. Default to deny unless an agent needs it during normal work, and add it.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/cli/sandbox_relay.go backend/internal/cli/sandbox_relay_test.go backend/internal/cli/agent_process.go backend/internal/cli/*_test.go
git commit -m "feat(cli): serve in-container ao calls from the agent supervisor (#6663)"
```

---

### Task 7: Claude Code sandbox support

**Files:**
- Create: `backend/internal/adapters/agent/claudecode/sandbox.go`
- Test: `backend/internal/adapters/agent/claudecode/sandbox_test.go`

**Interfaces:**
- Consumes: `ports.AgentSandboxSupport` (Task 3), the existing `readLocalOAuthToken(dataDir)`, `ensureWorkspaceTrusted(configPath, workspacePath)`, `claudeTrustMu`.
- Produces: `(*Plugin) SandboxBinary() string` returning `"claude"`, and `(*Plugin) PrepareSandbox(env, dataDir, projectDir, workspacePath) error`. That method sets `env["CLAUDE_CONFIG_DIR"] = <projectDir>/claude`. It sets `env["CLAUDE_CODE_OAUTH_TOKEN"]` from AO's captured token unless the project env already has `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`. It writes trust and `hasCompletedOnboarding` into `<projectDir>/claude/.claude.json`.

- [ ] **Step 1: Write the failing tests**

```go
package claudecode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentSandboxSupport = (*Plugin)(nil)

func TestPrepareSandboxSeedsConfigAndToken(t *testing.T) {
	data, project := t.TempDir(), t.TempDir()
	tokenPath := localOAuthTokenPath(data)
	_ = os.MkdirAll(filepath.Dir(tokenPath), 0o700)
	_ = os.WriteFile(tokenPath, []byte("tok\n"), 0o600)
	env := map[string]string{}
	p := &Plugin{}
	if err := p.PrepareSandbox(env, data, project, "/w/s1"); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(project, "claude")
	if env["CLAUDE_CONFIG_DIR"] != configDir || env["CLAUDE_CODE_OAUTH_TOKEN"] != "tok" {
		t.Fatalf("env = %#v", env)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		HasCompletedOnboarding bool `json:"hasCompletedOnboarding"`
		Projects               map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.HasCompletedOnboarding || !cfg.Projects["/w/s1"].HasTrustDialogAccepted {
		t.Fatalf("claude config = %s", raw)
	}
}

func TestPrepareSandboxKeepsProjectAPIKey(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "sk"}
	if err := (&Plugin{}).PrepareSandbox(env, t.TempDir(), t.TempDir(), "/w"); err != nil {
		t.Fatal(err)
	}
	if _, ok := env["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Fatalf("token injected over explicit API key: %#v", env)
	}
}

func TestPrepareSandboxWithoutCredentialFails(t *testing.T) {
	err := (&Plugin{}).PrepareSandbox(map[string]string{}, t.TempDir(), t.TempDir(), "/w")
	if !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/adapters/agent/claudecode/ -run PrepareSandbox -v`
Expected: FAIL to compile with `p.PrepareSandbox undefined`.

- [ ] **Step 3: Write minimal implementation** (`sandbox.go`)

```go
package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SandboxBinary is the Claude Code executable expected on the image PATH.
func (p *Plugin) SandboxBinary() string { return "claude" }

// PrepareSandbox points Claude at a per-project config dir that is mounted at
// the same path in the container, so AO's transcript lookup on the host
// (NativeConversationExists honours env CLAUDE_CONFIG_DIR) sees what the
// sandboxed Claude writes. The host keychain is unreachable from a container,
// so the credential must be an explicit env key or AO's captured setup-token.
func (p *Plugin) PrepareSandbox(env map[string]string, dataDir, projectDir, workspacePath string) error {
	configDir := filepath.Join(projectDir, "claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("claude-code: sandbox config dir: %w", err)
	}
	env["CLAUDE_CONFIG_DIR"] = configDir
	if env["ANTHROPIC_API_KEY"] == "" && env["CLAUDE_CODE_OAUTH_TOKEN"] == "" {
		token := readLocalOAuthToken(dataDir)
		if token == "" {
			return fmt.Errorf("%w: Claude Code in a sandbox needs AO's Claude login (run `ao claude-login`) or ANTHROPIC_API_KEY in the project env", ports.ErrSandboxMisconfigured)
		}
		env["CLAUDE_CODE_OAUTH_TOKEN"] = token
	}
	configPath := filepath.Join(configDir, ".claude.json")
	if err := ensureOnboardingComplete(configPath); err != nil {
		return err
	}
	return ensureWorkspaceTrusted(configPath, workspacePath)
}

// ensureOnboardingComplete marks first-run onboarding done in a fresh sandbox
// config so the interactive theme/login wizard never blocks a spawn.
func ensureOnboardingComplete(configPath string) error {
	claudeTrustMu.Lock()
	defer claudeTrustMu.Unlock()
	cfg := map[string]any{}
	if raw, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("claude-code: refusing to rewrite corrupt %s: %w", configPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("claude-code: read %s: %w", configPath, err)
	}
	if done, _ := cfg["hasCompletedOnboarding"].(bool); done {
		return nil
	}
	cfg["hasCompletedOnboarding"] = true
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("claude-code: write %s: %w", configPath, err)
	}
	return os.Rename(tmp, configPath)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/adapters/agent/claudecode/ -v`
Expected: PASS (new and existing). Confirm the `ao claude-login` wording matches the real command name (`newClaudeLoginCommand` in `cli/root.go:221`); if its `Use` differs, use that name in the message.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/agent/claudecode/sandbox.go backend/internal/adapters/agent/claudecode/sandbox_test.go
git commit -m "feat(claude-code): prepare credentials and config for sandboxed launches (#6663)"
```

---

### Task 8: Session manager integration

**Files:**
- Create: `backend/internal/session_manager/sandbox.go`
- Modify: `backend/internal/session_manager/manager.go`:
  - `Deps` (`:769-818`) and `New` (`:822`): add the `Sandbox ports.Sandbox` field.
  - Spawn: chat branch `:1292`, terminal branch `:1330-1391`.
  - Restore: `:3290-3345` in `relaunchSessionWithPolicyAndGeneration`.
  - Wrap: `superviseAgentProcess` `:6451`, `superviseAgentProcessForSwitch`, `superviseAgentProcessMode` `:6468` and `wrapAgentProcessWithLaunchID` `:6484`.
  - Kill: after `recordTermination` at `:2543` and `:2609`.
- Modify: `backend/internal/session_manager/agent_switching.go:1345-1460,1598` (reject a switch when the sandbox is on; pass `nil`)
- Modify: `backend/internal/service/session/service.go:1413-1416` (error mapping)
- Test: `backend/internal/session_manager/sandbox_test.go`, `backend/internal/service/session/service_test.go`

**Interfaces:**
- Consumes: `ports.Sandbox`, `ports.AgentSandboxSupport`, `domain.EffectiveSandbox`, the existing `roleOverride(kind, cfg)`, `m.store.ListSessions(ctx, domain.ProjectID)`, `domain.SessionRecord.IsTerminated`.
- Produces:
  - `type sandboxLaunch struct{ target ports.SandboxTarget; workspacePath string; binary string }`
  - `(*Manager) prepareSandboxLaunch(ctx, agent ports.Agent, rec domain.SessionRecord, cfg domain.ProjectConfig, workspacePath string, env map[string]string) (*sandboxLaunch, error)`, which returns `nil, nil` when the role is not sandboxed
  - `(*Manager) releaseSharedSandbox(ctx, project domain.ProjectID)`
  - `wrapAgentProcessWithLaunchID(agent, id, env, argv, launchID, force, sb *sandboxLaunch)`, plus the same trailing `sb *sandboxLaunch` parameter on `superviseAgentProcess*`

- [ ] **Step 1: Write the failing tests.** Reuse the package's existing manager test harness. Find the helper that builds a `Manager` with fake runtime, store and agents: `grep -n "func newTestManager\|func newManagerForTest" backend/internal/session_manager/*_test.go`. Pass a `fakeSandbox` through `Deps.Sandbox`.

```go
package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSandbox struct {
	ensureErr error
	ensured   []ports.SandboxRequest
	released  []bool
}

func (f *fakeSandbox) Ensure(_ context.Context, req ports.SandboxRequest) (ports.SandboxTarget, error) {
	f.ensured = append(f.ensured, req)
	if f.ensureErr != nil {
		return ports.SandboxTarget{}, f.ensureErr
	}
	return ports.SandboxTarget{Container: "ao-sbx-" + string(req.SessionID), Docker: "/usr/bin/docker",
		BinDir: "/d/sandbox/bin", ProjectDir: "/d/sandbox/projects/p", SessionDir: "/d/s", RelayDir: "/d/s/relay"}, nil
}

func (f *fakeSandbox) Exec(t ports.SandboxTarget, ws string, _ map[string]string, argv []string) ([]string, error) {
	return append([]string{t.Docker, "exec", "-i", "-t", "-w", ws, t.Container, "/bin/sh", t.BinDir + "/ao-enter", t.SessionDir + "/env"}, argv...), nil
}

func (f *fakeSandbox) Release(_ context.Context, _ domain.ProjectID, inUse bool) error {
	f.released = append(f.released, inUse)
	return nil
}

func TestWrapAgentProcessSandboxedForcesSupervisorWithRelay(t *testing.T) {
	m := &Manager{sandbox: &fakeSandbox{}, newLaunchID: func() string { return "L1" },
		executable: func() (string, error) { return "/opt/ao", nil }}
	sb := &sandboxLaunch{target: ports.SandboxTarget{Container: "c", Docker: "/usr/bin/docker", BinDir: "/b", SessionDir: "/s", RelayDir: "/s/relay"},
		workspacePath: "/w", binary: "claude"}
	env := map[string]string{}
	argv, err := m.wrapAgentProcessWithLaunchID(hookOnlyAgent{}, "s1", env, []string{"/Users/me/.local/bin/claude", "--x"}, "L1", false, sb)
	if err != nil {
		t.Fatal(err)
	}
	want := "/opt/ao agent-process supervise --session s1 --launch L1 --sandbox-relay /s/relay -- /usr/bin/docker exec -i -t -w /w c /bin/sh /b/ao-enter /s/env claude --x"
	if got := strings.Join(argv, " "); got != want {
		t.Fatalf("argv =\n%s\nwant\n%s", got, want)
	}
}

func TestSpawnSandboxUnavailableDoesNotLaunchOnHost(t *testing.T) {
	// Build a manager with the package test harness, a project whose config has
	// Sandbox{Mode: per-agent, Image: "node:22"}, a claude-like fake agent that
	// implements ports.AgentSandboxSupport, and
	// fakeSandbox{ensureErr: fmt.Errorf("%w: docker down", ports.ErrSandboxUnavailable)}.
	// Spawn a worker in terminal mode, then assert:
	//   errors.Is(err, ports.ErrSandboxUnavailable)
	//   the fake runtime recorded zero Create calls
}

func TestKillReleasesSharedSandboxOnlyWhenUnused(t *testing.T) {
	// With the harness store holding two live sessions s1 and s2 in project p
	// (shared mode): Kill(s1) then fakeSandbox.released == []bool{true}.
	// Kill(s2) then released == []bool{true, false}.
	// With a store whose ListSessions returns an error: Kill must not call
	// Release with false. Assert released has no false entry.
}
```

`hookOnlyAgent` is a minimal `ports.Agent` fake that does **not** implement `AgentExitDetector`. If the test package already has one, reuse it (`grep -n "ExitDetectionMode" backend/internal/session_manager/*_test.go`). The two harness-based tests above list their exact setup and assertions. Write them with the package's existing spawn and kill test helpers, matching how neighbouring `TestSpawn*` and `TestKill*` tests build projects and fake runtimes.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/session_manager/ -run 'Sandbox' -v`
Expected: FAIL to compile (`m.sandbox` undefined, `wrapAgentProcessWithLaunchID` arity).

- [ ] **Step 3: Implement `session_manager/sandbox.go`**

```go
package sessionmanager

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sandboxLaunch carries one sandboxed launch from preparation to the
// supervisor wrap (#6663). nil means the agent runs on the host.
type sandboxLaunch struct {
	target        ports.SandboxTarget
	workspacePath string
	binary        string
}

func sandboxFor(kind domain.SessionKind, cfg domain.ProjectConfig) domain.SandboxConfig {
	return domain.EffectiveSandbox(cfg.Sandbox, roleOverride(kind, cfg))
}

// prepareSandboxLaunch ensures the container and harness state for a
// sandboxed role. It never falls back to the host on failure.
func (m *Manager) prepareSandboxLaunch(ctx context.Context, agent ports.Agent, rec domain.SessionRecord, cfg domain.ProjectConfig, workspacePath string, env map[string]string) (*sandboxLaunch, error) {
	sb := sandboxFor(rec.Kind, cfg)
	if !sb.Enabled() {
		return nil, nil
	}
	if m.sandbox == nil {
		return nil, fmt.Errorf("%w: this daemon has no sandbox support", ports.ErrSandboxUnavailable)
	}
	support, ok := agent.(ports.AgentSandboxSupport)
	if !ok {
		return nil, fmt.Errorf("%w: harness %s cannot run in a sandbox yet; set sandbox.mode to off for this role", ports.ErrSandboxMisconfigured, rec.Harness)
	}
	target, err := m.sandbox.Ensure(ctx, ports.SandboxRequest{
		SessionID: rec.ID, ProjectID: rec.ProjectID, Config: sb,
		WorkspacePath: workspacePath, RequireBinaries: []string{support.SandboxBinary()},
	})
	if err != nil {
		return nil, err
	}
	if err := support.PrepareSandbox(env, m.dataDir, target.ProjectDir, workspacePath); err != nil {
		return nil, err
	}
	return &sandboxLaunch{target: target, workspacePath: workspacePath, binary: support.SandboxBinary()}, nil
}

// sandboxArgv swaps the host-resolved harness path for the in-image binary.
func sandboxArgv(argv []string, binary string) []string {
	out := append([]string(nil), argv...)
	if len(out) > 0 && filepath.Base(out[0]) != "env" {
		out[0] = binary
	}
	return out
}

// releaseSharedSandbox removes a project's shared container once no live
// session remains. An unreadable session list keeps the container: an
// unknown is never proof that nothing uses it.
func (m *Manager) releaseSharedSandbox(ctx context.Context, project domain.ProjectID) {
	if m.sandbox == nil {
		return
	}
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		m.logger.Warn("sandbox: keep shared container; session list failed", "projectID", project, "error", err)
		return
	}
	inUse := false
	for _, r := range recs {
		if !r.IsTerminated {
			inUse = true
			break
		}
	}
	if err := m.sandbox.Release(ctx, project, inUse); err != nil {
		m.logger.Warn("sandbox: release shared container", "projectID", project, "error", err)
	}
}
```

- [ ] **Step 4: Wire it into `manager.go`**

1. Add `Sandbox ports.Sandbox` to `Deps` and the `sandbox ports.Sandbox` field to `Manager`. In `New`, set `sandbox: d.Sandbox`.
2. In `wrapAgentProcessWithLaunchID`, add the trailing parameter `sb *sandboxLaunch`. Directly after the `AugmentRuntimeLaunchEnv` call, so the env file includes `AO_RUNTIME_LAUNCH_ID`, insert:

```go
	var relayDir string
	if sb != nil {
		// Sandboxed launches always use the supervisor: it serves the
		// in-container `ao` relay and owns the docker exec client process.
		force = true
		var err error
		argv, err = m.sandbox.Exec(sb.target, sb.workspacePath, env, sandboxArgv(argv, sb.binary))
		if err != nil {
			return nil, fmt.Errorf("sandbox exec: %w", err)
		}
		relayDir = sb.target.RelayDir
	}
```

   Then build the supervisor argv with the flag:

```go
	wrapped = append(wrapped, executable, "agent-process", "supervise", "--session", string(id), "--launch", launchID)
	if relayDir != "" {
		wrapped = append(wrapped, "--sandbox-relay", relayDir)
	}
	wrapped = append(wrapped, "--")
	wrapped = append(wrapped, argv...)
```

3. Thread `sb *sandboxLaunch` through `superviseAgentProcess`, `superviseAgentProcessForSwitch` and `superviseAgentProcessMode`. Agent-switch call sites (`agent_switching.go:1456,1598`) pass `nil`.
4. In the Spawn chat branch (`if mode == domain.SessionModeChat {` at `:1292`), add at the top:

```go
		if sandboxFor(rec.Kind, project.Config).Enabled() {
			return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: %w: chat sessions cannot run in a sandbox yet; use terminal mode or set sandbox.mode to off", id, ports.ErrSandboxMisconfigured)
		}
```

   If `rec` is not yet in scope there, use the session kind variable that branch already uses.
5. In the Spawn terminal branch, right after `pinRuntimePermissionEnv(env, adapterConfig.Permissions)`:

```go
	sb, err := m.prepareSandboxLaunch(ctx, agent, rec, project.Config, ws.Path, env)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, true, false)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: %w", id, err)
	}
```

   Guard the host-only steps with `if sb == nil { … }`: the `AgentLaunchAuthValidator` block, `validateAgentBinary(argv)` and `augmentRuntimePATHForLaunchBinary`. Then call `m.superviseAgentProcess(agent, id, env, argv, sb)`.
6. In `relaunchSessionWithPolicyAndGeneration`, right after `prepareWorkspace(...)` succeeds and before `restoreArgv`, which reads `env` to find the transcript, so `CLAUDE_CONFIG_DIR` must already be set:

```go
	sb, err := m.prepareSandboxLaunch(ctx, agent, rec, project.Config, ws.Path, env)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
```

   Use the project config this function already loaded (`grep -n loadProject` inside it). If it only has a narrower config variable, load the project once with `m.loadProject(ctx, rec.ProjectID)` at this point. Guard `validateAgentBinary` and `augmentRuntimePATHForLaunchBinary` with `if sb == nil`. Pass `sb` to `superviseAgentProcess` and `wrapAgentProcessWithLaunchID`.
7. In `agent_switching.go` `prepareTargetActivation` (`:1345`), before building the target argv, reject a switch when the sandbox is on:

```go
	if sandboxFor(rec.Kind, project.Config).Enabled() {
		return fmt.Errorf("%w: switching agents is not supported for sandboxed sessions yet", ports.ErrSandboxMisconfigured)
	}
```

   Match the function's existing return shape, and use its loaded project config.
8. In `kill()`, after each successful `recordTermination(...)` (`:2543` and `:2609`), add `m.releaseSharedSandbox(ctx, rec.ProjectID)`.

- [ ] **Step 5: Map the errors for the API** (`service/session/service.go`, next to `ErrRuntimePrerequisite`)

```go
	case errors.Is(err, ports.ErrSandboxUnavailable):
		return apierr.Invalid("SANDBOX_UNAVAILABLE", err.Error(), nil)
	case errors.Is(err, ports.ErrSandboxMisconfigured):
		return apierr.Invalid("SANDBOX_MISCONFIGURED", err.Error(), nil)
```

Add a test in `service_test.go` next to the existing `RUNTIME_PREREQUISITE_MISSING` mapping test that asserts both codes.

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd backend && go test ./internal/session_manager/ ./internal/service/session/ -v -run 'Sandbox|Spawn|Kill|Restore|Switch'`
Expected: PASS. Then run the full package suites: `cd backend && go test ./internal/session_manager/ ./internal/service/...`
Expected: PASS. The new trailing `nil` argument must not change any existing behavior.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/session_manager backend/internal/service/session
git commit -m "feat(session): launch sandboxed agents through docker exec (#6663)"
```

---

### Task 9: Daemon wiring, docs, and end-to-end verification

**Files:**
- Modify: `backend/internal/daemon/lifecycle_wiring.go:265-310` (`startSession`)
- Create: `frontend/src/docs/content/configuration/sandbox.mdx`
- Modify: the docs nav/index that lists `configuration/projects.mdx` (find it with `grep -rn "configuration/projects" frontend/src/docs`)
- Modify: `docs/STATUS.md` (one line under in-flight or shipped work, matching that file's style)

**Interfaces:**
- Consumes: `dockersandbox.New(dockersandbox.Config{DataDir, ManagedRoot})`, `sessionmanager.Deps.Sandbox`.

- [ ] **Step 1: Wire the adapter.** In `startSession`, next to the workspace router, reuse the same managed root value:

```go
	managedRoot := filepath.Join(cfg.DataDir, "worktrees")
	sandbox := dockersandbox.New(dockersandbox.Config{DataDir: cfg.DataDir, ManagedRoot: managedRoot})
```

Use `managedRoot` for the existing `ManagedRoot:` field so the two can't drift. Add `Sandbox: sandbox,` to `sessionmanager.Deps{…}`.

- [ ] **Step 2: Build and run the full backend checks**

Run: `cd backend && go build ./... && go vet ./... && go test -race ./...`
Expected: PASS.

Run (repo root): `npm run lint`
Expected: PASS (golangci-lint v2.13.2). Fix lint findings in new files only.

- [ ] **Step 3: Write the user docs** (`sandbox.mdx`), following the frontmatter and tone of `configuration/projects.mdx`. The content must cover:
  - **What it does:** agents run in Docker, while AO, worktrees and the UI stay on the host.
  - **Modes:** `off`, `per-agent` and `shared`, with the trade-offs from the issue's table.
  - **A config example** set with `ao project set-config <id> --config-json`:

    ```json
    {"sandbox":{"mode":"shared","image":"ghcr.io/acme/dev:latest","memory":"8g"},
     "orchestrator":{"sandbox":{"mode":"off"}}}
    ```

  - **Image requirements:** `/bin/sh`, `git`, and `claude` on PATH, with a minimal Dockerfile:

    ```dockerfile
    FROM node:22-bookworm
    RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*
    RUN npm install -g @anthropic-ai/claude-code
    ```

  - **Authentication:** `ao claude-login`, or `ANTHROPIC_API_KEY` in the project env. The keychain is not visible inside the container.
  - **Current limits:**
    - Claude Code only.
    - Terminal sessions only.
    - No agent switching for sandboxed sessions.
    - Shared mode needs AO-managed worktrees.
    - No Docker access inside the sandbox.
    - `ao mcp` is not available inside the sandbox.
    - Windows isn't supported yet.
  - **Troubleshooting:** what `SANDBOX_UNAVAILABLE` and `SANDBOX_MISCONFIGURED` mean.

- [ ] **Step 4: Manual end-to-end check against real Docker.** Not part of CI. Follow the AGENTS.md desktop lab, using scratch data and never the real `~/.ao`.

```bash
git worktree add /tmp/ao-lab HEAD && cd /tmp/ao-lab/backend
go build -o /tmp/ao-lab-bin/ao ./cmd/ao
docker build -t ao-sbx-test -f - . <<'EOF'
FROM node:22-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
EOF
AO_DATA_DIR=/tmp/ao-lab-data /tmp/ao-lab-bin/ao start
```

Then, with a scratch project:
1. Set `{"sandbox":{"mode":"per-agent","image":"ao-sbx-test"}}`. Spawn a worker. `docker ps --filter label=ao.sandbox` shows `ao-sbx-<sid>`. The session's activity state moves (hooks reach the daemon through the relay). A file the agent writes shows up in the host worktree with your uid.
2. Kill the worker. The container is removed by the existing reaper.
3. Switch to `shared`. Spawn two workers and confirm one container. Kill one and the container stays. Kill the other and it is removed.
4. Run `docker stop` on a per-agent container while its session is live, then restore the session from the UI. The container restarts and the Claude conversation resumes.
5. Quit Docker Desktop and spawn. The UI shows the `SANDBOX_UNAVAILABLE` message, and `ps aux | grep claude` shows no host claude process.
6. Inside the sandboxed session, run `ao project ls`. It prints `"project" is not allowed in the sandbox` with exit 126.

Record the results (commands and outputs) in the PR body. Report any step you could not run as a gap rather than as passing.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/daemon/lifecycle_wiring.go frontend/src/docs docs/STATUS.md
git commit -m "feat(daemon): enable the Docker sandbox for local sessions and document it (#6663)"
```

- [ ] **Step 6: Run the pre-PR checks** from AGENTS.md "PR hygiene":
  - `npm run lint`
  - `npm run frontend:typecheck`
  - `cd backend && go test -race ./...`
  - `npm run api`, then confirm `git diff --exit-code` shows no drift
  - Optionally `npx @redwoodjs/agent-ci run --all`

  Write the PR description with `.agents/skills/pr-description/SKILL.md`, link #6663, and list the intentional omissions (see below).

---

## Intentional omissions (state these in the PR body)

- No settings UI yet. Config goes through `ao project set-config` and the API (follow-up plan C).
- No Dockerfile builds, image caching or setup step (follow-up plan B).
- Only Claude Code is supported. Other harnesses get `SANDBOX_MISCONFIGURED` until they implement `ports.AgentSandboxSupport`.
- Not supported for sandboxed sessions: chat-mode sessions, agent switching, reviewers, multi-repo workspace projects in shared mode, `ao mcp`, and Docker-in-Docker.
- Windows is not supported.
- The relay polls every 50 ms. That adds up to about 50 ms per hook call, which is acceptable for v1. fsnotify can replace polling later.

## Follow-up plans (separate PRs, each linked to #6663)

- **B: Prebuilt sandboxes.**
  - `sandbox.dockerfile` (repo-relative) and an optional `.devcontainer/devcontainer.json`.
  - An `ao sandbox build` command through a new daemon route.
  - Images tagged with a content hash of the build inputs; a `setup` command that runs once per new container.
  - Builds stream their logs.
- **C: Settings UI.**
  - A "Sandbox" section modelled on `ProjectEnvironmentSettings.tsx` (re-GET, then PUT the whole config).
  - Per-role mode controls in `ProjectAgentRoleControls.tsx`, plus i18n for all 8 locales.
  - A "sandboxed" badge in the session view.
- **D: More harnesses and platforms.**
  - `AgentSandboxSupport` for Codex and OpenCode.
  - Windows (Docker Desktop with WSL2 paths).
  - A per-session relay for `ao mcp` via a streaming spool.
