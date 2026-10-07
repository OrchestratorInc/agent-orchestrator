package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type fixedBrowserCapability string

func (f fixedBrowserCapability) Issue(_ domain.SessionID) (string, string, error) {
	return string(f), "verifier-1", nil
}

type browserCapabilityIssue struct {
	token    string
	verifier string
	err      error
}

type scriptedBrowserCapabilities struct {
	issues  []browserCapabilityIssue
	calls   int
	onIssue func(call int, id domain.SessionID)
}

func (s *scriptedBrowserCapabilities) Issue(id domain.SessionID) (string, string, error) {
	call := s.calls
	s.calls++
	if s.onIssue != nil {
		s.onIssue(call, id)
	}
	if call >= len(s.issues) {
		return "", "", errors.New("unexpected browser capability issuance")
	}
	issue := s.issues[call]
	return issue.token, issue.verifier, issue.err
}

func TestSpawnEnvProjectVarsCannotOverrideInternal(t *testing.T) {
	env := spawnEnv("mer-1", "mer", "issue-9", "/data", map[string]string{
		"FOO":        "bar",
		EnvSessionID: "hacked", // a project must not override AO-internal vars
		EnvProjectID: "hacked",
	})
	if env["FOO"] != "bar" {
		t.Fatalf("FOO = %q, want bar", env["FOO"])
	}
	if env[EnvSessionID] != "mer-1" {
		t.Fatalf("AO_SESSION_ID = %q, want mer-1 (internal wins)", env[EnvSessionID])
	}
	if env[EnvProjectID] != "mer" {
		t.Fatalf("AO_PROJECT_ID = %q, want mer (internal wins)", env[EnvProjectID])
	}
}

func TestSpawnEnvWindowsRemovesCaseVariantsOfProtectedVariables(t *testing.T) {
	env := spawnEnvForOS("mer-1", "mer", "issue-9", `C:\ao`, map[string]string{
		"ao_session_id": "hacked",
		"buildMode":     "production",
	}, true)
	if _, ok := env["ao_session_id"]; ok {
		t.Fatal("case variant of protected AO_SESSION_ID survived")
	}
	if env[EnvSessionID] != "mer-1" || env["buildMode"] != "production" {
		t.Fatalf("environment = %v, want protected ID and untouched project variable spelling", env)
	}
}

func TestRuntimeEnvInjectsBrowserCapability(t *testing.T) {
	manager := &Manager{
		dataDir:             "/data",
		browserCapabilities: fixedBrowserCapability("capability-1"),
		executable:          func() (string, error) { return filepath.Join("/opt", "aod", "ao"), nil },
		logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env, verifier, err := manager.launchRuntimeEnv("mer-1", "mer", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if env[EnvBrowserCapability] != "capability-1" {
		t.Fatalf("%s = %q", EnvBrowserCapability, env[EnvBrowserCapability])
	}
	if verifier != "verifier-1" {
		t.Fatalf("verifier = %q", verifier)
	}
}

func TestRuntimeEnvClearsDaemonBrowserRuntimeSecrets(t *testing.T) {
	manager := &Manager{
		dataDir:    "/data",
		executable: func() (string, error) { return filepath.Join("/opt", "aod", "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env := manager.runtimeEnv("mer-1", "mer", "", map[string]string{
		EnvBrowserRuntimeToken:      "runtime-secret",
		EnvBrowserRuntimeTokenStdin: "1",
	})
	if env[EnvBrowserRuntimeToken] != "" || env[EnvBrowserRuntimeTokenStdin] != "" {
		t.Fatalf("daemon browser runtime credentials leaked to worker: token=%q stdin=%q", env[EnvBrowserRuntimeToken], env[EnvBrowserRuntimeTokenStdin])
	}
}

func TestRuntimeEnvWindowsRemovesCaseVariantsOfProtectedVariables(t *testing.T) {
	daemonRunFile := filepath.Join(t.TempDir(), "daemon-running.json")
	previous := envKeysCaseInsensitive
	envKeysCaseInsensitive = true
	t.Cleanup(func() { envKeysCaseInsensitive = previous })

	manager := &Manager{
		dataDir:     `C:\ao`,
		runFilePath: daemonRunFile,
		executable:  func() (string, error) { return filepath.Join(t.TempDir(), "ao"), nil },
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env := manager.runtimeEnv("mer-1", "mer", "issue-9", map[string]string{
		"Path":                           `C:\project\bin`,
		"ao_session_id":                  "hacked",
		"Ao_Project_Id":                  "hacked",
		"aO_Issue_ID":                    "hacked",
		"Ao_Data_Dir":                    "hacked",
		"ao_run_file":                    "hacked",
		"ao_browser_runtime_token":       "runtime-secret",
		"ao_browser_runtime_token_stdin": "1",
		"buildMode":                      "production",
	})

	for _, key := range []string{
		"Path",
		"ao_session_id",
		"Ao_Project_Id",
		"aO_Issue_ID",
		"Ao_Data_Dir",
		"ao_run_file",
		"ao_browser_runtime_token",
		"ao_browser_runtime_token_stdin",
	} {
		if _, ok := env[key]; ok {
			t.Fatalf("case variant %s survived in runtime env: %v", key, env)
		}
	}
	if env["PATH"] == "" {
		t.Fatalf("PATH was not pinned: %v", env)
	}
	if env[EnvSessionID] != "mer-1" || env[EnvProjectID] != "mer" || env[EnvIssueID] != "issue-9" || env[EnvDataDir] != `C:\ao` {
		t.Fatalf("protected AO env = %v", env)
	}
	if env[EnvRunFile] != daemonRunFile || env[EnvBrowserRuntimeToken] != "" || env[EnvBrowserRuntimeTokenStdin] != "" {
		t.Fatalf("runtime protected env = %v", env)
	}
	if env["buildMode"] != "production" {
		t.Fatalf("project env spelling was not preserved: %v", env)
	}
}

func TestRuntimeEnvPinsHooksToDaemonRunFile(t *testing.T) {
	daemonRunFile := filepath.Join(t.TempDir(), "daemon-running.json")
	t.Setenv("AO_RUN_FILE", filepath.Join(t.TempDir(), "inherited-wrong-daemon.json"))
	manager := &Manager{
		dataDir:     "/data",
		runFilePath: daemonRunFile,
		executable:  func() (string, error) { return filepath.Join("/opt", "aod", "ao"), nil },
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env := manager.runtimeEnv("mer-1", "mer", "", map[string]string{
		"AO_RUN_FILE": "/project/cannot-redirect-hooks.json",
	})
	if got, want := env["AO_RUN_FILE"], daemonRunFile; got != want {
		t.Fatalf("AO_RUN_FILE = %q, want daemon run-file %q", got, want)
	}
}

func TestHookPATH(t *testing.T) {
	sep := string(os.PathListSeparator)
	daemonExe := filepath.Join("/opt", "aod", "ao")
	daemonDir := filepath.Dir(daemonExe)
	exeOK := func() (string, error) { return daemonExe, nil }

	cases := []struct {
		name       string
		executable func() (string, error)
		daemonPATH string
		projectEnv map[string]string
		want       string
		wantErr    bool
	}{
		{
			name:       "prepends daemon dir to inherited PATH",
			executable: exeOK,
			daemonPATH: "/usr/bin" + sep + "/bin",
			want:       daemonDir + sep + "/usr/bin" + sep + "/bin",
		},
		{
			name:       "project PATH override is the base",
			executable: exeOK,
			daemonPATH: "/usr/bin",
			projectEnv: map[string]string{"PATH": "/proj/bin"},
			want:       daemonDir + sep + "/proj/bin",
		},
		{
			name:       "empty base PATH yields the daemon dir alone",
			executable: exeOK,
			want:       daemonDir,
		},
		{
			name:       "unresolvable executable fails",
			executable: func() (string, error) { return "", errors.New("no exe") },
			daemonPATH: "/usr/bin",
			wantErr:    true,
		},
		{
			// A daemon binary not named "ao" cannot anchor `ao` resolution by
			// having its directory prepended, so the pin must be refused.
			name:       "executable not named ao fails",
			executable: func() (string, error) { return filepath.Join("/opt", "aod", "ao-daemon"), nil },
			daemonPATH: "/usr/bin",
			wantErr:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key == "PATH" {
					return tc.daemonPATH
				}
				return ""
			}
			got, err := HookPATH(tc.executable, getenv, tc.projectEnv, "/data")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("HookPATH = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("HookPATH: %v", err)
			}
			if got != tc.want {
				t.Fatalf("HookPATH = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveHarnessAndAgentConfig(t *testing.T) {
	cfg := domain.ProjectConfig{
		AgentConfig:  domain.AgentConfig{Model: "base", Effort: "medium", Mode: "low", Permissions: domain.PermissionModeAuto},
		Worker:       domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "worker", Effort: "high", Mode: "high"}},
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}

	// Explicit harness always wins.
	if h := effectiveHarness(domain.HarnessAider, domain.KindWorker, cfg); h != domain.HarnessAider {
		t.Fatalf("explicit harness = %q, want aider", h)
	}
	// Empty harness falls back to the role override per kind.
	if h := effectiveHarness("", domain.KindWorker, cfg); h != domain.HarnessCodex {
		t.Fatalf("worker harness = %q, want codex", h)
	}
	if h := effectiveHarness("", domain.KindOrchestrator, cfg); h != domain.HarnessClaudeCode {
		t.Fatalf("orchestrator harness = %q, want claude-code", h)
	}

	// Role override merges over the base agent config (set fields win; unset keep base).
	got := effectiveAgentConfig(domain.HarnessCodex, domain.KindWorker, cfg)
	if got.Model != "worker" || got.Effort != "high" || got.Mode != "high" || got.Permissions != domain.PermissionModeAuto {
		t.Fatalf("merged worker config = %#v, want model=worker mode=high permissions=auto", got)
	}
	// Orchestrator has no agent-config override, so the base config is used as-is.
	if got := effectiveAgentConfig(domain.HarnessClaudeCode, domain.KindOrchestrator, cfg); got.Model != "base" {
		t.Fatalf("orchestrator config = %#v, want base", got)
	}
	// A launch harness that differs from the role's configured harness drops the
	// role's model/mode — they were tuned for the other agent — but keeps the
	// harness-neutral permissions.
	if got := effectiveAgentConfig(domain.HarnessAider, domain.KindWorker, cfg); got.Model != "base" || got.Mode != "low" || got.Permissions != domain.PermissionModeAuto {
		t.Fatalf("mismatched-harness worker config = %#v, want model=base mode=low permissions=auto", got)
	}
	// A role override with no harness pinned is deliberately treated as
	// "applies to any harness", so its model/mode are inherited whichever
	// harness the session launches with. This is a behavior decision, not a
	// side effect: assert it across two unrelated harnesses so it cannot
	// silently flip back to the old drop-on-every-switch behavior.
	unpinned := domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Model: "base", Mode: "low"},
		Worker:      domain.RoleOverride{AgentConfig: domain.AgentConfig{Model: "worker", Mode: "high"}},
	}
	for _, harness := range []domain.AgentHarness{domain.HarnessAider, domain.HarnessCodex} {
		got := effectiveAgentConfig(harness, domain.KindWorker, unpinned)
		if got.Model != "worker" || got.Mode != "high" {
			t.Fatalf("unpinned worker config for %q = %#v, want model=worker mode=high", harness, got)
		}
	}
}

type tuningCatalog struct {
	catalog ports.AgentModelCatalog
	err     error
	calls   *int
}

func (c tuningCatalog) Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	if c.calls != nil {
		*c.calls++
	}
	return c.catalog, c.err
}

func TestResolveChatAgentConfigValidatesAndResetsDependentTuning(t *testing.T) {
	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "old", Efforts: []string{"high"}},
		{ID: "new", Efforts: []string{"low"}},
	}}}}
	project := domain.ProjectConfig{Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{Model: "old", Effort: "high"}}}
	resolved, err := m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "new"},
	}, project)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "new" || resolved.Effort != "" {
		t.Fatalf("resolved = %#v, want new model with provider defaults", resolved)
	}
	resolved, err = m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "old"}, EffortOverride: true,
	}, project)
	if err != nil || resolved.Effort != "" {
		t.Fatalf("explicit provider defaults did not clear role tuning: %#v, %v", resolved, err)
	}

	_, err = m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "new", Effort: "high"},
	}, project)
	if !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("error = %v, want ErrUnsupportedEffort", err)
	}

	resolved, err = m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "custom"},
	}, project)
	if err != nil || resolved.Model != "custom" || resolved.Effort != "" {
		t.Fatalf("custom model with provider defaults = %#v, %v", resolved, err)
	}

	m.modelCatalog = tuningCatalog{err: errors.New("discovery failed")}
	resolved, err = m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "new"},
	}, domain.ProjectConfig{})
	if err != nil || resolved.Model != "new" {
		t.Fatalf("provider defaults should survive discovery failure: %#v, %v", resolved, err)
	}
	_, err = m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		AgentConfig: ports.AgentConfig{Model: "new", Effort: "high"},
	}, domain.ProjectConfig{})
	if !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("error = %v, want ErrModelCapabilitiesUnavailable", err)
	}
}

func TestResolveChatAgentConfigValidatesClaudeEffort(t *testing.T) {
	catalogCalls := 0
	m := &Manager{modelCatalog: tuningCatalog{calls: &catalogCalls, catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "sonnet", IsDefault: true, Efforts: []string{"high"}},
	}}}}
	resolved, err := m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "sonnet", Effort: "high"}, EffortOverride: true,
	}, domain.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Effort != "high" {
		t.Fatalf("Claude Code effort = %q, want high", resolved.Effort)
	}
	if catalogCalls != 1 {
		t.Fatalf("Claude Code model catalog calls = %d, want 1", catalogCalls)
	}

	resolved, err = m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "sonnet"}, EffortOverride: true,
	}, domain.ProjectConfig{Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{Effort: "high"}}})
	if err != nil || resolved.Effort != "" {
		t.Fatalf("explicit Claude provider default did not clear role tuning: %#v, %v", resolved, err)
	}
	if catalogCalls != 2 {
		t.Fatalf("provider default did not validate the selected model: %d catalog calls", catalogCalls)
	}
}

func TestResolveClaudeTUIAgentConfigRejectsUnsupportedEffort(t *testing.T) {
	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "sonnet", IsDefault: true, Efforts: []string{"low", "high"}},
	}}}}

	_, err := m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode:  domain.SessionModeTUI,
		AgentConfig:    ports.AgentConfig{Model: "sonnet", Effort: "max"},
		EffortOverride: true,
	}, domain.ProjectConfig{})
	if !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("error = %v, want ErrUnsupportedEffort", err)
	}
}

func TestResolveClaudeModelWithoutEffortStillRequiresCatalog(t *testing.T) {
	m := &Manager{modelCatalog: tuningCatalog{err: errors.New("gateway does not list models")}}

	_, err := m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "provider/model-vNext"},
	}, domain.ProjectConfig{Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{
		Model: "sonnet", Effort: "high",
	}}})
	if !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("error = %v, want ErrModelCapabilitiesUnavailable", err)
	}
}

func TestResolveClaudeModelWithoutEffortRejectsUnknownAndStaleCatalogs(t *testing.T) {
	cfg := ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeTUI, AgentConfig: ports.AgentConfig{Model: "provider/old"},
	}
	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{
		CustomModelEntry: ports.CustomModelEntryDirect,
		Models:           []ports.AgentModelInfo{{ID: "provider/current"}},
	}}}
	if _, err := m.resolveAgentConfig(context.Background(), cfg, domain.ProjectConfig{}); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("unknown model error = %v, want ErrUnsupportedModel", err)
	}

	m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{
		CustomModelEntry: ports.CustomModelEntryDirect,
		Models:           []ports.AgentModelInfo{{ID: "provider/old"}},
		Stale:            true,
	}}
	if _, err := m.resolveAgentConfig(context.Background(), cfg, domain.ProjectConfig{}); !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("stale catalog error = %v, want ErrModelCapabilitiesUnavailable", err)
	}
}

func TestApplySymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation requires a host privilege outside this unit test")
	}
	project := t.TempDir()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("X=1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A present source is linked; a missing source is skipped, not an error.
	if err := applySymlinks(project, workspace, []string{".env", "missing.txt"}); err != nil {
		t.Fatalf("applySymlinks: %v", err)
	}
	target := filepath.Join(workspace, ".env")
	if data, err := os.ReadFile(target); err != nil || string(data) != "X=1" {
		t.Fatalf("symlinked .env = %q err=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, "missing.txt")); !os.IsNotExist(err) {
		t.Fatal("missing source should not have been linked")
	}
}

func TestApplySymlinksRejectsParentTraversal(t *testing.T) {
	project := t.TempDir()
	workspace := t.TempDir()
	// A "..", "/" or "../" segment escapes the project tree and must be refused
	// before any stat/link runs, so a project config cannot link in arbitrary
	// host files.
	for _, bad := range []string{"../escape", "/etc/passwd", "a/../../b", ".."} {
		if err := applySymlinks(project, workspace, []string{bad}); err == nil {
			t.Fatalf("applySymlinks(%q) accepted an unsafe path", bad)
		}
	}
}

func TestRunPostCreate(t *testing.T) {
	workspace := t.TempDir()
	if err := runPostCreate(context.Background(), workspace, []string{"echo hi > out.txt"}, nil, nil); err != nil {
		t.Fatalf("runPostCreate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "out.txt")); err != nil {
		t.Fatalf("post-create command did not run in workspace: %v", err)
	}
	// A failing command surfaces an error.
	if err := runPostCreate(context.Background(), workspace, []string{"exit 3"}, nil, nil); err == nil {
		t.Fatal("expected error from failing post-create command")
	}
}

func TestRunPostCreateReceivesAndRedactsProjectEnv(t *testing.T) {
	command := `echo "$PROJECT_TOKEN" && exit 3`
	if runtime.GOOS == "windows" {
		command = `echo %PROJECT_TOKEN% && exit /b 3`
	}
	secret := "project-secret-123"
	err := runPostCreate(context.Background(), t.TempDir(), []string{command}, map[string]string{"PROJECT_TOKEN": secret}, nil)
	if err == nil || !strings.Contains(err.Error(), "[REDACTED]") || strings.Contains(err.Error(), secret) {
		t.Fatalf("postCreate error did not redact project value: %v", err)
	}
}

// The actual postCreate shell invokes this test binary to read the live store.
// No expected session identity or prebuilt receipt is passed to the child.
func TestPostCreateStoreProbe(t *testing.T) {
	if os.Getenv("POSTCREATE_STORE_PROBE") != "1" {
		return
	}
	dataDir := os.Getenv(EnvDataDir)
	if !filepath.IsAbs(dataDir) {
		t.Fatalf("postCreate received non-absolute daemon data dir: %q", dataDir)
	}
	st, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec, ok, err := st.GetSession(context.Background(), domain.SessionID(os.Getenv(EnvSessionID)))
	if err != nil || !ok {
		t.Fatalf("owning session lookup: found=%v error=%v", ok, err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	actualWorkspace, resolveErr := filepath.EvalSymlinks(rec.Metadata.WorkspacePath)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if rec.ProjectID != domain.ProjectID(os.Getenv(EnvProjectID)) || rec.IssueID != domain.IssueID(os.Getenv(EnvIssueID)) ||
		rec.Metadata.WorkspacePath != os.Getenv("AO_WORKSPACE_PATH") || actualWorkspace != cwd {
		t.Fatalf("postCreate identity disagrees with live store: %+v cwd=%q", rec, cwd)
	}
	if rec.IsTerminated || rec.Metadata.RuntimeHandleID != "" || rec.Metadata.RuntimeLaunchID != "" ||
		rec.Metadata.ProviderConversationID != "" || rec.Metadata.ControllerGeneration != "" || rec.Metadata.AgentSessionID != "" ||
		rec.Activity.State != domain.ActivityIdle {
		t.Fatalf("postCreate observed a live or altered controller: %+v", rec)
	}
	for _, step := range rec.ProvisionSteps {
		if step.ID == domain.SessionProvisionStepSetup && step.Status == domain.SessionProvisionStepDone {
			t.Fatal("setup was marked done while postCreate was still running")
		}
	}
	body, err := json.Marshal(struct {
		Branch  string
		Repo    string
		RunFile string
		Token   string
	}{rec.Metadata.Branch, rec.Metadata.WorkspaceRepoPath, os.Getenv(EnvRunFile), os.Getenv("PROJECT_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("postcreate-context.json", body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSpawnPostCreateSessionContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell probe")
	}
	for _, tc := range []struct {
		name      string
		mode      domain.SessionMode
		kind      domain.SessionKind
		async     bool
		prepared  bool
		noRunFile bool
		project   domain.ProjectKind
	}{
		{name: "tui", mode: domain.SessionModeTUI, kind: domain.KindWorker},
		{name: "no manager runfile", mode: domain.SessionModeTUI, kind: domain.KindWorker, noRunFile: true},
		{name: "chat orchestrator", mode: domain.SessionModeChat, kind: domain.KindOrchestrator},
		{name: "async chat", mode: domain.SessionModeChat, kind: domain.KindWorker, async: true},
		{name: "prepared suffix", mode: domain.SessionModeTUI, kind: domain.KindWorker, prepared: true},
		{name: "async prepared suffix", mode: domain.SessionModeChat, kind: domain.KindWorker, async: true, prepared: true},
		{name: "workspace project", mode: domain.SessionModeTUI, kind: domain.KindWorker, project: domain.ProjectKindWorkspace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir, workspace := t.TempDir(), t.TempDir()
			st, err := sqlite.Open(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			runFile := filepath.Join(dataDir, "daemon.json")
			if tc.noRunFile {
				runFile = ""
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
			project := domain.ProjectRecord{ID: "mer", Path: t.TempDir(), Kind: tc.project, Config: testRoleAgents()}
			project.Config.Env = map[string]string{"PROJECT_TOKEN": "kept"}
			for _, key := range []string{EnvSessionID, EnvProjectID, EnvIssueID, EnvDataDir, EnvRunFile, "AO_WORKSPACE_PATH"} {
				t.Setenv(key, "inherited-spoof")
				project.Config.Env[key] = "project-spoof"
			}
			project.Config.PostCreate = []string{"POSTCREATE_STORE_PROBE=1 " + quote(executable) + " -test.run '^TestPostCreateStoreProbe$'"}
			if err := st.UpsertProject(context.Background(), project); err != nil {
				t.Fatal(err)
			}
			ws := &fakeWorkspace{path: workspace, createRepoPath: project.Path, createBranch: "actual-returned-branch-2"}
			if tc.project == domain.ProjectKindWorkspace {
				ws.projectCreateInfo = ports.WorkspaceProjectInfo{
					Root:      ports.WorkspaceInfo{Path: workspace, RepoPath: project.Path, Branch: ws.createBranch, SessionID: "mer-1", ProjectID: "mer"},
					Worktrees: []ports.WorkspaceRepoInfo{{RepoName: domain.RootWorkspaceRepoName, Path: workspace, RepoPath: project.Path, Branch: ws.createBranch, SessionID: "mer-1", ProjectID: "mer"}},
				}
			}
			m := New(Deps{Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: ws, Store: st,
				Messenger: &fakeMessenger{}, Lifecycle: lifecycle.New(st, nil), Chat: &recordingLauncher{}, DataDir: dataDir,
				RunFilePath: runFile, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				LookPath: func(string) (string, error) { return "/bin/true", nil }})
			deferred := deferredBackground(m)
			cfg := ports.SpawnConfig{ProjectID: "mer", IssueID: "issue-9", Harness: domain.HarnessCodex,
				Kind: tc.kind, RequestedMode: tc.mode, Async: tc.async}
			if tc.prepared {
				token, err := m.PrepareTaskWorkspace(context.Background(), project)
				if err != nil {
					t.Fatal(err)
				}
				(*deferred)[0]()
				cfg.TaskPreparation = token
			}
			// Launching a controller is outside this test. Stop there only after
			// the real shell has proved its persisted ownership and environment.
			m.runtime.(*fakeRuntime).createErr = errors.New("probe ends before controller")
			m.chat.(*recordingLauncher).startErr = errors.New("probe ends before controller")
			_, _, _, spawnErr := m.Spawn(context.Background(), cfg)
			if tc.async && spawnErr == nil {
				(*deferred)[len(*deferred)-1]()
			}
			body, err := os.ReadFile(filepath.Join(workspace, "postcreate-context.json"))
			if err != nil {
				t.Fatalf("actual postCreate did not prove ownership: %v (spawn: %v)", err, spawnErr)
			}
			var got struct {
				Branch  string
				Repo    string
				RunFile string
				Token   string
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Branch != ws.createBranch || got.Repo != project.Path ||
				got.RunFile != runFile || got.Token != "kept" {
				t.Fatalf("postCreate context = %+v", got)
			}
		})
	}
}

type refusedPostCreatePublishStore struct {
	*fakeStore
	err    error
	cancel context.CancelFunc
}

func (s *refusedPostCreatePublishStore) SetSessionProvisionedWorkspace(ctx context.Context, id domain.SessionID, branch, path, repo string, now time.Time) (bool, error) {
	if s.cancel != nil {
		updated, err := s.fakeStore.SetSessionProvisionedWorkspace(ctx, id, branch, path, repo, now)
		s.cancel()
		return updated, err
	}
	return false, s.err
}

func TestSpawnPostCreatePublicationFailurePreventsShell(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, failure := range []string{"refused", "error", "cancelled"} {
			t.Run(fmt.Sprintf("async=%v/%s", async, failure), func(t *testing.T) {
				launcher := &recordingLauncher{}
				m, st, rt := newChatManager(launcher)
				m.dataDir = t.TempDir()
				workspace := t.TempDir()
				m.workspace.(*fakeWorkspace).path = workspace
				project := st.projects["mer"]
				project.Config.PostCreate = []string{"echo ran > shell-ran"}
				st.projects["mer"] = project
				refusal := &refusedPostCreatePublishStore{fakeStore: st}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if failure == "error" {
					refusal.err = errors.New("publication failed")
				}
				if failure == "cancelled" {
					refusal.cancel = cancel
					m.backgroundContext = ctx
				}
				m.store = refusal
				deferred := deferredBackground(m)
				mode := domain.SessionModeTUI
				if async {
					mode = domain.SessionModeChat
				}
				_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: mode, Async: async})
				if async && err == nil {
					(*deferred)[0]()
				} else if err == nil {
					t.Fatal("spawn accepted failed publication")
				}
				if _, err := os.Stat(filepath.Join(workspace, "shell-ran")); !os.IsNotExist(err) {
					t.Fatalf("postCreate ran after failed publication: %v", err)
				}
				if rt.created != 0 || len(launcher.started) != 0 {
					t.Fatal("controller started after failed publication")
				}
			})
		}
	}
}

type failedSetupCheckpointStore struct{ *fakeStore }

func (s *failedSetupCheckpointStore) SetSessionProvisionSteps(context.Context, domain.SessionID, []domain.SessionProvisionStep, time.Time) error {
	return errors.New("setup checkpoint unavailable")
}

func TestSpawnPostCreateRequiresDurableSetupCheckpoint(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprint(async), func(t *testing.T) {
			m, st, _ := newChatManager(&recordingLauncher{})
			m.dataDir = t.TempDir()
			m.store = &failedSetupCheckpointStore{st}
			deferred := deferredBackground(m)
			workspace := t.TempDir()
			m.workspace.(*fakeWorkspace).path = workspace
			project := st.projects["mer"]
			project.Config.PostCreate = []string{"echo ran > shell-ran"}
			st.projects["mer"] = project
			mode := domain.SessionModeTUI
			if async {
				mode = domain.SessionModeChat
			}
			_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: mode, Async: async})
			if async && err == nil {
				(*deferred)[0]()
			}
			if _, err := os.Stat(filepath.Join(workspace, "shell-ran")); !os.IsNotExist(err) {
				t.Fatalf("hook ran without durable in-progress setup checkpoint: %v", err)
			}
		})
	}
}

func TestInterruptedPostCreateCannotLaunchOnRecovery(t *testing.T) {
	m, st, rt, ws := newManager()
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Metadata:       domain.SessionMetadata{Branch: "returned-branch", WorkspacePath: t.TempDir()},
		ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepSetup, Status: domain.SessionProvisionStepRunning}}}
	st.sessions[rec.ID] = rec
	ws.destroyErr = ports.ErrWorkspaceDirty
	if err := m.reconcileLive(context.Background(), rec); err == nil {
		t.Fatal("recovery accepted unfinished setup")
	}
	if err := m.checkSessionHealth(context.Background(), rec); err == nil {
		t.Fatal("startup accepted unfinished setup")
	}
	if _, err := m.relaunchRestoredSession(context.Background(), rec, st.projects["mer"], workspaceInfo(rec)); err == nil {
		t.Fatal("restore accepted unfinished setup")
	}
	if rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatal("recovery launched or restored unfinished setup")
	}
	if got := st.sessions[rec.ID]; got.Metadata.WorkspacePath != rec.Metadata.WorkspacePath || got.IsTerminated {
		t.Fatalf("recovery lost cleanup identity: %+v", got)
	}
}

func TestEarlyPublishedLaunchPreservesUncertainWorkspace(t *testing.T) {
	for _, setup := range []domain.SessionProvisionStepStatus{domain.SessionProvisionStepRunning, domain.SessionProvisionStepDone, ""} {
		for _, mode := range []domain.SessionMode{domain.SessionModeTUI, domain.SessionModeChat} {
			t.Run(fmt.Sprintf("setup=%s/mode=%s", setup, mode), func(t *testing.T) {
				m, st, rt, ws := newManager()
				rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
					Harness: domain.HarnessCodex, ClientRequestID: "interrupted-request", Mode: mode,
					Metadata: domain.SessionMetadata{Branch: "returned-branch", WorkspacePath: t.TempDir()}}
				if setup != "" {
					rec.ProvisionSteps = []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepSetup, Status: setup}}
				}
				st.sessions[rec.ID] = rec
				for _, recover := range []func() error{
					func() error { return m.checkSessionHealth(context.Background(), rec) },
					func() error { return m.reconcileLive(context.Background(), rec) },
					func() error { _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); return err },
					func() error {
						_, err := m.relaunchRestoredSession(context.Background(), rec, st.projects["mer"], workspaceInfo(rec))
						return err
					},
					func() error { _, err := m.Kill(context.Background(), rec.ID); return err },
					func() error { return m.saveAndTeardownOne(context.Background(), rec) },
					func() error { return m.RetireForReplacement(context.Background(), rec.ID) },
				} {
					if err := recover(); err == nil || !strings.Contains(err.Error(), "writer stop is unproven") {
						t.Fatalf("uncertain launch was not refused with missing evidence: %v", err)
					}
				}
				got, exists := st.sessions[rec.ID]
				if !exists || got.Metadata != rec.Metadata || got.IsTerminated || got.ClientRequestCommitted {
					t.Fatalf("uncertain launch lost ownership: %+v exists=%v", got, exists)
				}
				if _, err := replayClientRequest(got, ""); !errors.Is(err, ErrClientRequestIncomplete) {
					t.Fatalf("incomplete request replay = %v", err)
				}
				// Enter the public restore path with a terminated uncertainty row.
				rec.IsTerminated = true
				st.sessions[rec.ID] = rec
				if _, err := m.RestoreWithMode(context.Background(), rec.ID); err == nil || !strings.Contains(err.Error(), "writer stop is unproven") {
					t.Fatalf("restore did not refuse before workspace I/O: %v", err)
				}
				cleanup, err := m.Cleanup(context.Background(), rec.ProjectID)
				if err != nil || len(cleanup.Skipped) != 1 || !strings.Contains(cleanup.Skipped[0].Reason, "writer stop is unproven") {
					t.Fatalf("cleanup did not preserve terminated uncertainty: %+v error=%v", cleanup, err)
				}
				if rt.created != 0 || ws.destroyed != 0 || len(ws.restoreConfigs) != 0 {
					t.Fatalf("uncertain launch performed controller/workspace I/O: controllers=%d destroys=%d restores=%d", rt.created, ws.destroyed, len(ws.restoreConfigs))
				}
			})
		}
	}
}

func TestInterruptedAsyncPublishedWorkspaceRefusesRecovery(t *testing.T) {
	for _, setup := range []domain.SessionProvisionStepStatus{domain.SessionProvisionStepRunning, domain.SessionProvisionStepDone, ""} {
		t.Run(string(setup), func(t *testing.T) {
			m, st, rt := newChatManager(&recordingLauncher{})
			rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
				Mode: domain.SessionModeChat, ProvisionState: domain.SessionProvisionProvisioning,
				Metadata:       domain.SessionMetadata{WorkspacePath: t.TempDir(), Branch: "returned-branch", Prompt: "durably queued task"},
				ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepAgent, Status: domain.SessionProvisionStepPending}}}
			if setup != "" {
				rec.ProvisionSteps = append(rec.ProvisionSteps, domain.SessionProvisionStep{ID: domain.SessionProvisionStepSetup, Status: setup})
			}
			st.sessions[rec.ID] = rec
			if err := m.FailInterruptedProvisioning(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil || !strings.Contains(err.Error(), "writer stop is unproven") {
				t.Fatalf("restarted setup writer was replayed: %v", err)
			}
			if _, err := m.Kill(context.Background(), rec.ID); err == nil || !strings.Contains(err.Error(), "writer stop is unproven") {
				t.Fatalf("restarted setup writer could be removed: %v", err)
			}
			if len(m.chat.(*recordingLauncher).started) != 0 || rt.created != 0 || m.workspace.(*fakeWorkspace).destroyed != 0 {
				t.Fatal("uncertain asynchronous launch performed controller/workspace I/O")
			}
		})
	}
}

func TestSpawnIncompleteAgentCheckpointWithoutClientRequest(t *testing.T) {
	for _, setup := range []bool{false, true} {
		t.Run(fmt.Sprint(setup), func(t *testing.T) {
			m, st, rt, ws := newManager()
			m.dataDir, ws.path = t.TempDir(), t.TempDir()
			project := st.projects["mer"]
			if setup {
				project.Config.PostCreate = []string{"true"}
				st.projects["mer"] = project
			}
			observed := false
			m.browserCapabilities = &scriptedBrowserCapabilities{
				issues: []browserCapabilityIssue{{token: "test-token", verifier: "test-verifier"}},
				onIssue: func(_ int, id domain.SessionID) {
					observed = true
					rec := st.sessions[id]
					if rec.ClientRequestID != "" || !uncertainWorkspaceLaunch(rec) || unfinishedWorkspaceSetup(rec) {
						t.Fatalf("done/no-setup checkpoint did not identify incomplete launch: %+v", rec)
					}
					if err := m.checkSessionHealth(context.Background(), rec); !errors.Is(err, ErrWorkspaceWriterStopUnproven) {
						t.Fatalf("health accepted pre-controller publication: %v", err)
					}
				},
			}
			rt.createErr = errors.New("known launch failure after checkpoint probe")
			_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeTUI})
			if !observed || err == nil {
				t.Fatalf("actual Spawn did not enter incomplete launch boundary: observed=%v error=%v", observed, err)
			}
		})
	}
}

func TestBackgroundHealthOverlapsRealPostCreate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell hook")
	}
	dataDir, workspace, signals := t.TempDir(), t.TempDir(), t.TempDir()
	st, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	marker, release := filepath.Join(signals, "running"), filepath.Join(signals, "release")
	project := domain.ProjectRecord{ID: "mer", Path: t.TempDir(), Config: testRoleAgents()}
	project.Config.PostCreate = []string{fmt.Sprintf("printf ready > %q; while [ ! -e %q ]; do sleep 0.01; done; exit 1", marker, release)}
	if err := st.UpsertProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	ws, rt := &fakeWorkspace{path: workspace, createRepoPath: project.Path}, &fakeRuntime{}
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lifecycle.New(st, nil), DataDir: dataDir,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), LookPath: func(string) (string, error) { return "/bin/true", nil }})
	if err := m.ReconcileStartupSafety(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeTUI})
		done <- err
	}()
	defer func() {
		_ = os.WriteFile(release, nil, 0o600)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("test-owned hook did not finish")
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real hook did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.ReconcileBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := st.GetSession(context.Background(), "mer-1")
	if err != nil || !ok || rec.Metadata.WorkspacePath != workspace || ws.destroyed != 0 || rt.created != 0 {
		t.Fatalf("background health destroyed or launched the blocked real hook: found=%v row=%+v destroys=%d controllers=%d error=%v", ok, rec, ws.destroyed, rt.created, err)
	}
	if _, err := m.Kill(context.Background(), rec.ID); err == nil {
		t.Fatal("cleanup accepted a still-running setup writer")
	}
	if ws.destroyed != 0 {
		t.Fatal("cleanup did not wait for setup completion")
	}
	// The deferred release lets the real command fail; only its owning Spawn
	// may then perform the existing in-process rollback.
}

func TestSpawnPermissionPrecedence(t *testing.T) {
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		for _, tc := range []struct {
			name                    string
			base, role, spawn, want domain.PermissionMode
		}{
			{"unset", "", "", "", domain.PermissionModeAuto},
			{"project", domain.PermissionModeDefault, "", "", domain.PermissionModeDefault},
			{"role", domain.PermissionModeAuto, domain.PermissionModeAcceptEdits, "", domain.PermissionModeAcceptEdits},
			{"spawn", domain.PermissionModeAuto, domain.PermissionModeAcceptEdits, domain.PermissionModeDefault, domain.PermissionModeDefault},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				cfg := domain.ProjectConfig{AgentConfig: domain.AgentConfig{Permissions: tc.base}, Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{Permissions: tc.role}}, Orchestrator: domain.RoleOverride{AgentConfig: domain.AgentConfig{Permissions: tc.role}}}
				got := applySpawnAgentConfig(effectiveAgentConfig(domain.HarnessCodex, kind, cfg), domain.AgentConfig{Permissions: tc.spawn})
				if got.Permissions != tc.want {
					t.Fatalf("got %q want %q", got.Permissions, tc.want)
				}
			})
		}
	}
	if got := effectiveAgentConfig(domain.HarnessCodex, domain.KindWorker, domain.ProjectConfig{}); got.Permissions != "" {
		t.Fatalf("non-spawn resolution changed: %q", got.Permissions)
	}
}
