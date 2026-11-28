package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// clearCallerEnv hides the environment of the agent running the test suite,
// so calling-agent detection sees only what a test sets.
func clearCallerEnv(t *testing.T) {
	t.Helper()
	t.Setenv(registry.CallerAIAgentEnv, "")
	for _, tag := range registry.CallerEnvTags {
		t.Setenv(tag.Env, "")
	}
}

// gitRepo makes a repository with one commit, optionally with a remote.
func gitRepo(t *testing.T, withRemote bool) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitFreeEnv(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	if withRemote {
		run("remote", "add", "origin", "https://github.com/example/calc.git")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// gitFreeEnv is the test environment without GIT_* variables, so a fixture
// repository never resolves to a repository the suite runs inside (a hook).
func gitFreeEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}

const (
	setupReadinessBoth = `{"agents":[
		{"id":"codex","label":"Agent B","installation":{"state":"installed"},"authentication":{"state":"authorized"},"effectiveReadiness":"ready"},
		{"id":"claude-code","label":"Agent A","installation":{"state":"installed"},"authentication":{"state":"configured"},"effectiveReadiness":"unknown"},
		{"id":"gemini","label":"Agent C","installation":{"state":"not_installed"},"authentication":{"state":"unknown"},"effectiveReadiness":"not_ready"}]}`
)

// newSetupDaemon is a ready daemon with a signed-in GitHub CLI, one agent
// ready, another with a saved sign-in, a third not installed, and no projects.
func newSetupDaemon(t *testing.T) *taskDaemon {
	t.Helper()
	clearCallerEnv(t)
	// Never run the agent CLIs installed on this machine.
	savedStatusCommands := setupAuthStatusCommands
	setupAuthStatusCommands = map[string]setupAuthStatusCommand{}
	t.Cleanup(func() { setupAuthStatusCommands = savedStatusCommands })
	d := newTaskDaemon(t)
	dataDir := os.Getenv("AO_DATA_DIR")
	health := func(status string) func() (int, string) {
		return func() (int, string) {
			return 200, fmt.Sprintf(`{"status":%q,"service":%q,"pid":%d,"workingDirectory":%q}`, status, daemonmeta.ServiceName, os.Getpid(), dataDir)
		}
	}
	d.setFunc("GET /healthz", health("ok"))
	d.setFunc("GET /readyz", health("ready"))
	d.set("POST /api/v1/agents/readiness/ensure", 200, setupReadinessBoth)
	d.set("GET /api/v1/agents/readiness", 200, setupReadinessBoth)
	d.set("POST /api/v1/agents/codex/probe", 200, `{"agent":{"id":"codex","authStatus":"authorized"},"supported":true,"installed":true}`)
	d.set("POST /api/v1/agents/gemini/probe", 200, `{"agent":{"id":"gemini","authStatus":"unknown"},"supported":true,"installed":false}`)
	d.set("GET /api/v1/system/github-auth", 200, `{"id":"github-auth","label":"GitHub access","satisfied":true}`)
	d.set("GET /api/v1/projects", 200, `{"projects":[]}`)
	d.set("GET /api/v1/agents/auth-plans", 200, `{"plans":[{"agentId":"claude-code","displayCommand":"agent-a login"},{"agentId":"codex","displayCommand":"agent-b login"}]}`)
	return d
}

// registerOnPost makes POST /projects register the project at path.
func registerOnPost(d *taskDaemon, path, repo string) {
	branch := "main"
	if repo == "" {
		branch = "auto"
	}
	project := fmt.Sprintf(`{"id":"calc","path":%q,"repo":%q,"defaultBranch":%q,"config":{"worker":{"agent":"claude-code"},"orchestrator":{"agent":"claude-code"}}}`, path, repo, branch)
	d.set("POST /api/v1/projects", 201, `{"project":`+project+`}`)
	d.set("GET /api/v1/projects/calc", 200, `{"status":"ok","project":`+project+`}`)
}

func decodeSetup(t *testing.T, out string) setupResult {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"agent", "next_actions", "project", "ready", "steps", "task"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("JSON keys = %v, want %v\n%s", keys, want, out)
	}
	var res setupResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	var steps []map[string]json.RawMessage
	if err := json.Unmarshal(raw["steps"], &steps); err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		got := make([]string, 0, len(step))
		for k := range step {
			got = append(got, k)
		}
		sort.Strings(got)
		if want := []string{"detail", "fix", "status", "step"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("step keys = %v, want %v", got, want)
		}
	}
	return res
}

func stepStatuses(res setupResult) map[string]string {
	out := map[string]string{}
	for _, s := range res.Steps {
		out[s.Step] = s.Status
	}
	return out
}

func stepNamed(t *testing.T, res setupResult, name string) setupStep {
	t.Helper()
	for _, s := range res.Steps {
		if s.Step == name {
			return s
		}
	}
	t.Fatalf("no %q step in %+v", name, res.Steps)
	return setupStep{}
}

func TestSetupReadyWithCallingAgentFromAIAgent(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("AI_AGENT", "claude-code_2-1-326_agent")
	registerOnPost(d, repo, "example/calc")

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if got := ExitCode(err); got != 0 {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	res := decodeSetup(t, out)
	if !res.Ready || len(res.NextActions) != 0 || res.Task != nil {
		t.Fatalf("result = %+v", res)
	}
	if res.Agent == nil || res.Agent.ID != "claude-code" || res.Agent.Source != "caller" || res.Agent.DetectedFrom != "AI_AGENT" {
		t.Fatalf("agent = %+v, want the calling agent from AI_AGENT", res.Agent)
	}
	wantSteps := map[string]string{"daemon": "ok", "agent": "ok", "github": "ok", "project": "fixed", "first-run": "ok", "verify": "ok"}
	if got := stepStatuses(res); !reflect.DeepEqual(got, wantSteps) {
		t.Fatalf("steps = %v, want %v", got, wantSteps)
	}
	var body addProjectRequest
	if err := json.Unmarshal([]byte(d.bodies["POST /api/v1/projects"]), &body); err != nil {
		t.Fatal(err)
	}
	if body.Path != repo || body.Config == nil || body.Config.Worker.Agent != "claude-code" || body.Config.Orchestrator.Agent != "claude-code" {
		t.Fatalf("POST /projects body = %s", d.bodies["POST /api/v1/projects"])
	}
	if d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatal("a repo with a remote must not get a default branch set")
	}
	if res.Project == nil || res.Project.ID != "calc" || res.Project.Reused {
		t.Fatalf("project = %+v", res.Project)
	}
}

func TestSetupDetectsCallerFromAgentTag(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CODEX_THREAD_ID", "thread-1")
	registerOnPost(d, repo, "example/calc")

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	if res.Agent.ID != "codex" || res.Agent.DetectedFrom != "CODEX_THREAD_ID" {
		t.Fatalf("agent = %+v", res.Agent)
	}
	if !strings.Contains(d.bodies["POST /api/v1/projects"], `"agent":"codex"`) {
		t.Fatalf("project not registered with codex: %s", d.bodies["POST /api/v1/projects"])
	}
}

func TestSetupAgentFlagBeatsCaller(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("AI_AGENT", "claude-code")
	registerOnPost(d, repo, "example/calc")

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--orchestrator-agent", "claude-code", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	if res.Agent.ID != "codex" || res.Agent.Source != "flag" || res.Agent.Worker != "codex" || res.Agent.Orchestrator != "claude-code" {
		t.Fatalf("agent = %+v", res.Agent)
	}
	var body addProjectRequest
	if err := json.Unmarshal([]byte(d.bodies["POST /api/v1/projects"]), &body); err != nil || body.Config == nil ||
		body.Config.Worker.Agent != "codex" || body.Config.Orchestrator.Agent != "claude-code" {
		t.Fatalf("POST /projects body = %s", d.bodies["POST /api/v1/projects"])
	}
}

func TestSetupFallsBackToFirstReadyAgent(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if res := decodeSetup(t, out); res.Agent.ID != "codex" || res.Agent.Source != "ready" {
		t.Fatalf("agent = %+v, want the first ready agent", res.Agent)
	}
}

func TestSetupSignedOutAgentNeedsHumanWithLoginCommand(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CLAUDECODE", "1")
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v), want 10", got, err)
	}
	res := decodeSetup(t, out)
	agent := stepNamed(t, res, "agent")
	if res.Ready || agent.Status != setupNeedsHuman {
		t.Fatalf("ready=%v agent step = %+v", res.Ready, agent)
	}
	want := "agent-a login"
	if agent.Fix != want || !reflect.DeepEqual(res.NextActions, []string{want}) {
		t.Fatalf("fix = %q, next_actions = %q; want %q", agent.Fix, res.NextActions, want)
	}
	if !strings.Contains(agent.Detail, " --yes --agent codex`)") || !strings.Contains(agent.Detail, "ao setup --path ") {
		t.Fatalf("detail = %q, want the ready alternative", agent.Detail)
	}
	if stepNamed(t, res, "verify").Status != setupNeedsHuman {
		t.Fatalf("verify = %+v", stepNamed(t, res, "verify"))
	}
}

func TestSetupMissingAgentNeedsHumanWithInstallHint(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("GET /api/v1/agents/auth-plans", 200, `{"plans":[{"agentId":"gemini","documentationUrl":"https://example.com/agent-c"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "gemini", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	agent := stepNamed(t, decodeSetup(t, out), "agent")
	if agent.Fix != setupOpenURL("https://example.com/agent-c") || !strings.Contains(agent.Detail, "--agent codex") || !strings.Contains(agent.Detail, "install its CLI") {
		t.Fatalf("step = %+v, want the docs link in the fix and the ready alternative in the detail", agent)
	}
}

func TestSetupGitHubSignedOutNeedsHuman(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("GET /api/v1/system/github-auth", 200, `{"id":"github-auth","satisfied":false}`)

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		LookPath:     func(string) (string, error) { return "/usr/bin/gh", nil },
	}, "setup", "--path", repo, "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	res := decodeSetup(t, out)
	if gh := stepNamed(t, res, "github"); gh.Status != setupNeedsHuman || gh.Fix != "gh auth login" {
		t.Fatalf("github step = %+v", gh)
	}
	if d.saw("POST /api/v1/system/github-auth/terminal") {
		t.Fatal("setup must not open a login terminal")
	}
}

func TestSetupRepoWithoutRemoteGetsDefaultBranch(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	registerOnPost(d, repo, "")
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc","defaultBranch":"main"}}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	var put setConfigRequest
	if err := json.Unmarshal([]byte(d.bodies["PUT /api/v1/projects/calc/config"]), &put); err != nil {
		t.Fatalf("no set-config call: %v", err)
	}
	if put.Config.DefaultBranch != "main" || put.Config.Worker.Agent != "claude-code" {
		t.Fatalf("set-config body = %+v; want default branch main and the saved agents kept", put.Config)
	}
	project := stepNamed(t, res, "project")
	if project.Status != setupFixed || !strings.Contains(project.Detail, "set default branch to main") || res.Project.DefaultBranch != "main" {
		t.Fatalf("project step = %+v, project = %+v", project, res.Project)
	}
}

func TestSetupFindsRepoFromCurrentDirectory(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	sub := filepath.Join(repo, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	registerOnPost(d, repo, "example/calc")

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(d.bodies["POST /api/v1/projects"], fmt.Sprintf(`"path":%q`, repo)) {
		t.Fatalf("registered %s, want the repository root %s", d.bodies["POST /api/v1/projects"], repo)
	}
}

func TestSetupReusesRegisteredProject(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("AI_AGENT", "codex")
	d.set("GET /api/v1/projects", 200, `{"projects":[{"id":"calc"}]}`)
	d.set("GET /api/v1/projects/calc", 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"claude-code"},"orchestrator":{"agent":"claude-code"}}}}`, repo))

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	if d.saw("POST /api/v1/projects") || d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatal("setup re-registered or rewrote a project it should reuse")
	}
	if !res.Project.Reused || stepNamed(t, res, "project").Status != setupOK {
		t.Fatalf("project = %+v step = %+v", res.Project, stepNamed(t, res, "project"))
	}
	// The detected caller does not override the project's saved agents.
	if res.Agent.Worker != "claude-code" || res.Agent.Orchestrator != "claude-code" {
		t.Fatalf("agent = %+v, want the project's saved agents reported", res.Agent)
	}
}

func TestSetupPathAlreadyRegisteredReusesProject(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	project := fmt.Sprintf(`{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}`, repo)
	// Listings miss it until the registration POST (a racing caller
	// registered it in between), so setup must recover from the conflict.
	d.setFunc("GET /api/v1/projects", func() (int, string) {
		if !d.saw("POST /api/v1/projects") {
			return 200, `{"projects":[]}`
		}
		return 200, `{"projects":[{"id":"calc"}]}`
	})
	d.set("GET /api/v1/projects/calc", 200, `{"status":"ok","project":`+project+`}`)
	d.set("POST /api/v1/projects", 409, `{"code":"PATH_ALREADY_REGISTERED","message":"A project at this path is already registered"}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !d.saw("POST /api/v1/projects") {
		t.Fatal("setup never tried to register, so the conflict path was not exercised")
	}
	if res := decodeSetup(t, out); !res.Project.Reused || res.Project.ID != "calc" {
		t.Fatalf("project = %+v", res.Project)
	}
}

func TestSetupExplicitAgentOnRegisteredProjectNeedsYes(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	d.set("GET /api/v1/projects", 200, `{"projects":[{"id":"calc"}]}`)
	d.set("GET /api/v1/projects/calc", 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"claude-code"},"orchestrator":{"agent":"claude-code"}}}}`, repo))
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc"}}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatal("setup replaced saved agents without --yes")
	}
	if fix := stepNamed(t, decodeSetup(t, out), "project").Fix; !strings.Contains(fix, "--yes") {
		t.Fatalf("fix = %q", fix)
	}

	out, _, err = executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--yes", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	var put setConfigRequest
	if err := json.Unmarshal([]byte(d.bodies["PUT /api/v1/projects/calc/config"]), &put); err != nil ||
		put.Config.Worker.Agent != "codex" || put.Config.Orchestrator.Agent != "codex" {
		t.Fatalf("set-config body = %s", d.bodies["PUT /api/v1/projects/calc/config"])
	}
}

func TestSetupUnbornAndNonGitRepos(t *testing.T) {
	d := newSetupDaemon(t)
	// A space in the path: the suggested commands must quote it.
	dir := filepath.Join(t.TempDir(), "my repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d.set("POST /api/v1/projects", 400, `{"code":"PROJECT_UNBORN","message":"needs a commit"}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", dir, "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if fix := stepNamed(t, decodeSetup(t, out), "project").Fix; !strings.Contains(fix, "commit --allow-empty") {
		t.Fatalf("fix = %q", fix)
	}

	d.set("POST /api/v1/projects", 400, `{"code":"NOT_A_GIT_REPO","message":"not a repo"}`)
	out, _, err = executeCLI(t, fakeClockDeps(), "setup", "--path", dir, "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if fix := stepNamed(t, decodeSetup(t, out), "project").Fix; fix != fmt.Sprintf("git -C %s init && git -C %s commit --allow-empty -m \"initial commit\"", setupShellQuote(dir), setupShellQuote(dir)) {
		t.Fatalf("fix = %q", fix)
	}
}

func TestSetupOutsideAGitRepoNeedsPath(t *testing.T) {
	newSetupDaemon(t)
	t.Chdir(t.TempDir())
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	project := stepNamed(t, decodeSetup(t, out), "project")
	// The action is a runnable command; the --path alternative is in the detail.
	if !strings.Contains(project.Fix, " init && git -C ") || !strings.Contains(project.Detail, "--path") {
		t.Fatalf("project step = %+v", project)
	}
}

func TestSetupCheckWithoutDaemonStartsNothing(t *testing.T) {
	setConfigEnv(t)
	clearCallerEnv(t)
	t.Setenv("AI_AGENT", "codex")
	started := false
	out, _, err := executeCLI(t, Deps{
		StartDetached: func(processStartConfig) (startedProcess, error) {
			started = true
			return startedProcess{}, errors.New("must not start")
		},
	}, "setup", "--check", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if started {
		t.Fatal("--check started a daemon")
	}
	res := decodeSetup(t, out)
	if res.Ready || !reflect.DeepEqual(res.NextActions, []string{"ao setup"}) {
		t.Fatalf("ready=%v next_actions=%v", res.Ready, res.NextActions)
	}
	if res.Agent == nil || res.Agent.ID != "codex" {
		t.Fatalf("agent = %+v, want the detected caller even without a daemon", res.Agent)
	}
	for _, s := range res.Steps[:5] {
		if s.Status != setupSkipped {
			t.Fatalf("step %+v, want skipped", s)
		}
	}
}

func TestSetupCheckAndDryRunChangeNothing(t *testing.T) {
	for _, mode := range []string{"--check", "--dry-run"} {
		t.Run(mode, func(t *testing.T) {
			d := newSetupDaemon(t)
			repo := gitRepo(t, false)
			registerOnPost(d, repo, "")
			out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, mode, "--json")
			wantExit := 0
			if mode == "--check" {
				wantExit = setupExitNeedsHuman
			}
			if got := ExitCode(err); got != wantExit {
				t.Fatalf("exit = %d (%v), want %d", got, err, wantExit)
			}
			for _, r := range d.requests {
				if strings.HasPrefix(r, "PUT ") || r == "POST /api/v1/projects" || strings.Contains(r, "orchestrators") || strings.Contains(r, "/terminal") {
					t.Fatalf("%s made a change: %s", mode, r)
				}
			}
			res := decodeSetup(t, out)
			project := stepNamed(t, res, "project")
			if res.Ready || project.Status != setupSkipped || !strings.Contains(project.Detail, "would register") || !strings.Contains(project.Detail, "default branch to main") {
				t.Fatalf("ready=%v project step = %+v", res.Ready, project)
			}
		})
	}
}

func TestSetupStartsDaemonWhenNoneRuns(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	cfgRunFile := os.Getenv("AO_RUN_FILE")
	info, err := runfile.Read(cfgRunFile)
	if err != nil || info == nil {
		t.Fatalf("read run file: %v", err)
	}
	if err := os.Remove(cfgRunFile); err != nil {
		t.Fatal(err)
	}
	var got processStartConfig
	deps := fakeClockDeps()
	deps.Executable = func() (string, error) { return "/opt/ao/bin/ao", nil }
	deps.StartDetached = func(c processStartConfig) (startedProcess, error) {
		got = c
		if err := runfile.Write(cfgRunFile, *info); err != nil {
			t.Fatal(err)
		}
		return startedProcess{PID: info.PID, Exited: make(chan error)}, nil
	}
	out, _, err := executeCLI(t, deps, "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if strings.Join(got.Args, " ") != "daemon" {
		t.Fatalf("started %v, want the `ao serve` daemon path", got.Args)
	}
	if daemon := stepNamed(t, decodeSetup(t, out), "daemon"); daemon.Status != setupFixed || !strings.Contains(daemon.Detail, "started the daemon") {
		t.Fatalf("daemon step = %+v", daemon)
	}
}

func TestSetupStartWorkerReportsTask(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"calc-1"}`)
	d.set("GET /api/v1/sessions/calc-1", 200, `{"session":{"id":"calc-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/calc-1/conversation", 200, `{"turns":[{"id":"t1","state":"running"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--start", "fix the calc bug", "--worker", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	if res.Task == nil || res.Task.TaskID != "calc-1" || res.Task.Status != taskStatusRunning {
		t.Fatalf("task = %+v", res.Task)
	}
	if !strings.Contains(d.bodies["POST /api/v1/orchestrators/delegate"], `"brief":"fix the calc bug"`) {
		t.Fatalf("delegate body = %s", d.bodies["POST /api/v1/orchestrators/delegate"])
	}
	if start := stepNamed(t, res, "start"); start.Status != setupFixed || !strings.Contains(start.Detail, "one worker") {
		t.Fatalf("start step = %+v", start)
	}
	if !res.Ready {
		t.Fatal("ready = false after the task started")
	}
}

func TestSetupStartWaitingOnUserExits3(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"calc-1"}`)
	d.set("GET /api/v1/sessions/calc-1", 200, `{"session":{"id":"calc-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/calc-1/conversation", 200, `{"turns":[{"id":"t1","state":"running"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--start", "fix it", "--worker", "--json")
	if got := ExitCode(err); got != taskExitNeedsInput {
		t.Fatalf("exit = %d (%v), want 3", got, err)
	}
	res := decodeSetup(t, out)
	if res.Task == nil || res.Task.Status != taskStatusNeedsInput {
		t.Fatalf("task = %+v", res.Task)
	}
	// ao task's fix names the session to answer; setup passes it on.
	if !reflect.DeepEqual(res.NextActions, []string{"ao session get calc-1"}) {
		t.Fatalf("next_actions = %q", res.NextActions)
	}
}

func TestSetupDryRunDoesNotStartTask(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	d.set("GET /api/v1/projects", 200, `{"projects":[{"id":"calc"}]}`)
	d.set("GET /api/v1/projects/calc", 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}}`, repo))
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--dry-run", "--start", "x", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if d.saw("POST /api/v1/orchestrators") || d.saw("POST /api/v1/orchestrators/delegate") {
		t.Fatal("--dry-run started a task")
	}
	if start := stepNamed(t, decodeSetup(t, out), "start"); start.Status != setupSkipped {
		t.Fatalf("start = %+v", start)
	}
}

func TestSetupUsageErrors(t *testing.T) {
	setConfigEnv(t)
	cases := [][]string{
		{"setup", "--worker"},
		{"setup", "--check", "--dry-run"},
		{"setup", "--check", "--start", "x"},
		{"setup", "--start", "  "},
		{"setup", "extra"},
	}
	for _, args := range cases {
		_, _, err := executeCLI(t, Deps{}, args...)
		if got := ExitCode(err); got != 2 {
			t.Errorf("%v: exit = %d (%v), want 2", args, got, err)
		}
	}
}

func TestSetupHumanOutput(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CLAUDECODE", "1")
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)

	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo)
	for _, want := range []string{"[needs_human] agent", "fix: agent-a login", "Not ready yet. Next steps:", "1. agent-a login"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSetupRegistered(t *testing.T) {
	root := NewRootCommand(Deps{})
	for _, cmd := range root.Commands() {
		if cmd.Name() == "setup" {
			if !shouldEmitCLIInvocation(cmd) {
				t.Fatal("ao setup should count as a user command")
			}
			return
		}
	}
	t.Fatal("setup not registered")
}

func TestSetupShellQuote(t *testing.T) {
	cases := map[string]string{"/tmp/repo": "/tmp/repo", "/tmp/my repo": "'/tmp/my repo'", "/tmp/it's": `'/tmp/it'\''s'`}
	for in, want := range cases {
		if got := setupShellQuote(in); got != want {
			t.Errorf("setupShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetupChecksAgentsAReusedProjectKeeps(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CLAUDECODE", "1")
	d.set("GET /api/v1/projects", 200, `{"projects":[{"id":"calc"}]}`)
	d.set("GET /api/v1/projects/calc", 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}}`, repo))
	d.set("POST /api/v1/agents/codex/probe", 200, `{"agent":{"id":"codex","authStatus":"unauthorized"},"supported":true,"installed":true}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	res := decodeSetup(t, out)
	agent := stepNamed(t, res, "agent")
	if agent.Status != setupNeedsHuman || !strings.Contains(agent.Detail, "codex is not signed in") || agent.Fix != "agent-b login" {
		t.Fatalf("agent step = %+v", agent)
	}
	if res.Agent.ID != "codex" || res.Agent.Source != "project" || res.Agent.Worker != "codex" || res.Agent.Orchestrator != "codex" {
		t.Fatalf("agent = %+v", res.Agent)
	}
}

// TestSetupKeepsTheChosenAgentAcrossSignIn is the three-run flow a person
// follows: setup asks for a sign-in, they rerun before and after signing in.
// The project must save the agent setup chose, not leave the roles empty for
// the daemon to fill with its own default.
func TestSetupKeepsTheChosenAgentAcrossSignIn(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("AI_AGENT", "claude-code")
	var mu sync.Mutex
	saved := ""
	d.setFunc("POST /api/v1/projects", func() (int, string) {
		mu.Lock()
		defer mu.Unlock()
		var body addProjectRequest
		_ = json.Unmarshal([]byte(d.bodies["POST /api/v1/projects"]), &body)
		worker, orch := "opencode-v2", "opencode-v2" // what the daemon fills in for empty roles
		if body.Config != nil && body.Config.Worker.Agent != "" {
			worker, orch = body.Config.Worker.Agent, body.Config.Orchestrator.Agent
		}
		saved = fmt.Sprintf(`{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":%q},"orchestrator":{"agent":%q}}}`, repo, worker, orch)
		return 201, `{"project":` + saved + `}`
	})
	d.setFunc("GET /api/v1/projects", func() (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if saved == "" {
			return 200, `{"projects":[]}`
		}
		return 200, fmt.Sprintf(`{"projects":[{"id":"calc","path":%q}]}`, repo)
	})
	d.setFunc("GET /api/v1/projects/calc", func() (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return 200, `{"status":"ok","project":` + saved + `}`
	})
	signedOut := `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`
	d.set("POST /api/v1/agents/claude-code/probe", 200, signedOut)

	for run, want := range []struct {
		exit   int
		source string
	}{{setupExitNeedsHuman, "caller"}, {setupExitNeedsHuman, "project"}, {0, "project"}} {
		if run == 2 {
			d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"authorized"},"supported":true,"installed":true}`)
		}
		out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
		if got := ExitCode(err); got != want.exit {
			t.Fatalf("run %d: exit = %d (%v), want %d\n%s", run+1, got, err, want.exit, out)
		}
		res := decodeSetup(t, out)
		if res.Agent == nil || res.Agent.ID != "claude-code" || res.Agent.Worker != "claude-code" || res.Agent.Orchestrator != "claude-code" || res.Agent.Source != want.source {
			t.Fatalf("run %d: agent = %+v, want claude-code from %s", run+1, res.Agent, want.source)
		}
		if run < 2 && !reflect.DeepEqual(res.NextActions, []string{"agent-a login"}) {
			t.Fatalf("run %d: next_actions = %q", run+1, res.NextActions)
		}
		if run == 2 && !res.Ready {
			t.Fatalf("run 3: not ready: %+v", res.Steps)
		}
	}
	if !strings.Contains(saved, `"worker":{"agent":"claude-code"}`) {
		t.Fatalf("project saved %s, want claude-code", saved)
	}
}

func TestSetupDoesNotSaveAnUnsupportedAgent(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/agents/nope/probe", 200, `{"agent":{"id":"nope"},"supported":false,"installed":false}`)
	_, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "nope", "--json")
	if got := ExitCode(err); got != setupExitFailed {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	var body addProjectRequest
	if err := json.Unmarshal([]byte(d.bodies["POST /api/v1/projects"]), &body); err == nil && body.Config != nil {
		t.Fatalf("project registered with unsupported agent config %+v", body.Config)
	}
}

func TestSetupRefusesARepositoryAtHome(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("HOME", repo)
	t.Setenv("USERPROFILE", repo) // os.UserHomeDir reads it on Windows
	sub := filepath.Join(repo, "scratch")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", sub, "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	if d.saw("POST /api/v1/projects") {
		t.Fatal("setup registered the home-folder repository")
	}
	if fix := stepNamed(t, decodeSetup(t, out), "project").Fix; !strings.Contains(fix, "--path") {
		t.Fatalf("fix = %q", fix)
	}
}

func TestSetupDaemonErrorsFailTheStep(t *testing.T) {
	cases := []struct {
		route, step string
	}{
		{"POST /api/v1/agents/readiness/ensure", "agent"},
		{"GET /api/v1/system/github-auth", "github"},
		{"GET /api/v1/projects", "project"},
	}
	for _, tc := range cases {
		t.Run(tc.step, func(t *testing.T) {
			d := newSetupDaemon(t)
			repo := gitRepo(t, true)
			registerOnPost(d, repo, "example/calc")
			d.set(tc.route, 500, `{"code":"INTERNAL","message":"boom"}`)
			out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
			if got := ExitCode(err); got != setupExitFailed {
				t.Fatalf("exit = %d (%v), want 1", got, err)
			}
			res := decodeSetup(t, out)
			if s := stepNamed(t, res, tc.step); s.Status != setupFailed || !strings.Contains(s.Detail, "boom") {
				t.Fatalf("%s step = %+v", tc.step, s)
			}
			if v := stepNamed(t, res, "verify"); v.Status != setupFailed || !strings.Contains(v.Detail, tc.step+" step failed") {
				t.Fatalf("verify = %+v", v)
			}
		})
	}
}

func TestSetupDryRunOnRegisteredProjectReportsWouldChange(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	d.set("GET /api/v1/projects", 200, `{"projects":[{"id":"calc"}]}`)
	d.set("GET /api/v1/projects/calc", 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"claude-code"},"orchestrator":{"agent":"claude-code"}}}}`, repo))
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--yes", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatal("--dry-run saved the project's agents")
	}
	project := stepNamed(t, decodeSetup(t, out), "project")
	if project.Status != setupSkipped || !strings.Contains(project.Detail, "would set worker agent codex") {
		t.Fatalf("project step = %+v", project)
	}
}

func TestSetupPrefersAReusedProjectsAgentsOverTheCaller(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CLAUDECODE", "1")
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	d.set("GET /api/v1/projects", 200, `{"projects":[{"id":"calc"}]}`)
	d.set("GET /api/v1/projects/calc", 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}}`, repo))

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	if !res.Ready || res.Agent.ID != "codex" || res.Agent.Source != "project" {
		t.Fatalf("ready=%v agent=%+v", res.Ready, res.Agent)
	}
	if d.saw("POST /api/v1/agents/claude-code/probe") {
		t.Fatal("setup checked the signed-out caller although the project does not run it")
	}
}

func TestSetupBothRoleFlagsNeedNoDefault(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/agents/readiness/ensure", 200, `{"agents":[]}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--worker-agent", "codex", "--orchestrator-agent", "claude-code", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	if res.Agent.Worker != "codex" || res.Agent.Orchestrator != "claude-code" || res.Agent.Source != "roles" {
		t.Fatalf("agent = %+v", res.Agent)
	}
	if detail := stepNamed(t, res, "agent").Detail; !strings.Contains(detail, "from --worker-agent and --orchestrator-agent") {
		t.Fatalf("agent detail = %q", detail)
	}
}

func TestSetupCheckReportsNonGitAndUnbornPaths(t *testing.T) {
	newSetupDaemon(t)
	plain := t.TempDir()
	unborn := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir, cmd.Env = unborn, gitFreeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for dir, want := range map[string]string{plain: " init && git -C ", unborn: "commit --allow-empty"} {
		out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", dir, "--check", "--json")
		if got := ExitCode(err); got != setupExitNeedsHuman {
			t.Fatalf("%s: exit = %d (%v)", dir, got, err)
		}
		if project := stepNamed(t, decodeSetup(t, out), "project"); project.Status != setupNeedsHuman || !strings.Contains(project.Fix, want) {
			t.Fatalf("%s: project step = %+v", dir, project)
		}
	}
}

func TestSetupBothRoleFlagsIgnoreAnUnusedAgentFlag(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/agents/gemini/probe", 200, `{"agent":{"id":"gemini","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "gemini", "--worker-agent", "codex", "--orchestrator-agent", "claude-code", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if d.saw("POST /api/v1/agents/gemini/probe") {
		t.Fatal("setup checked --agent although both roles override it")
	}
	var body addProjectRequest
	if err := json.Unmarshal([]byte(d.bodies["POST /api/v1/projects"]), &body); err != nil || body.Config == nil ||
		body.Config.Worker.Agent != "codex" || body.Config.Orchestrator.Agent != "claude-code" {
		t.Fatalf("POST /projects body = %s", d.bodies["POST /api/v1/projects"])
	}
}

// fakeStatusCommand points the agent's own status command at a script that
// prints out and exits with code.
func fakeStatusCommand(t *testing.T, agent, out string, code int, cmd setupAuthStatusCommand) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// The fixture is a POSIX shell script, which Windows cannot run
		// directly. The status parsing it exercises is platform-neutral.
		t.Skip("status-command fixture is a POSIX shell script")
	}
	script := filepath.Join(t.TempDir(), "status")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%s'\nexit %d\n", out, code)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd.binary = script
	setupAuthStatusCommands[agent] = cmd
}

func TestSetupAgentOwnStatusCommandDecidesUnprovenSignIn(t *testing.T) {
	cases := []struct {
		name     string
		out      string
		code     int
		cmd      setupAuthStatusCommand
		wantExit int
		want     string
	}{
		{"json signed out", `{"loggedIn":false}`, 1, setupAuthStatusCommand{loggedInField: "loggedIn"}, setupExitNeedsHuman, setupNeedsHuman},
		{"json signed out after a warning line", `Warning: renew soon\n{"loggedIn":false}`, 1, setupAuthStatusCommand{loggedInField: "loggedIn"}, setupExitNeedsHuman, setupNeedsHuman},
		{"json signed in", `{"loggedIn":true}`, 0, setupAuthStatusCommand{loggedInField: "loggedIn"}, 0, setupOK},
		{"exit code signed out", "Not logged in", 1, setupAuthStatusCommand{signedOutText: "Not logged in"}, setupExitNeedsHuman, setupNeedsHuman},
		{"exit code signed in", "Logged in using ChatGPT", 0, setupAuthStatusCommand{signedOutText: "Not logged in"}, 0, setupOK},
		{"unclear output stays unproven", "boom", 2, setupAuthStatusCommand{loggedInField: "loggedIn"}, 0, setupOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newSetupDaemon(t)
			repo := gitRepo(t, true)
			registerOnPost(d, repo, "example/calc")
			d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"configured"},"supported":true,"installed":true}`)
			fakeStatusCommand(t, "claude-code", tc.out, tc.code, tc.cmd)

			out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "claude-code", "--json")
			if got := ExitCode(err); got != tc.wantExit {
				t.Fatalf("exit = %d (%v), want %d\n%s", got, err, tc.wantExit, out)
			}
			res := decodeSetup(t, out)
			agent := stepNamed(t, res, "agent")
			if agent.Status != tc.want {
				t.Fatalf("agent step = %+v, want %s", agent, tc.want)
			}
			if tc.want == setupNeedsHuman && (agent.Fix != "agent-a login" || len(res.NextActions) == 0 || res.NextActions[0] != "agent-a login") {
				t.Fatalf("agent step = %+v, next_actions = %q; want the verbatim login command", agent, res.NextActions)
			}
		})
	}
}

func TestSetupGitHubIsAdvisoryWithoutAGitHubRemote(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	registerOnPost(d, repo, "")
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc","defaultBranch":"main"}}`)
	d.set("GET /api/v1/system/github-auth", 200, `{"id":"github-auth","satisfied":false}`)

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		LookPath:     func(string) (string, error) { return "/usr/bin/gh", nil },
	}, "setup", "--path", repo, "--json")
	if got := ExitCode(err); got != 0 {
		t.Fatalf("exit = %d (%v), want 0: GitHub is advisory without a GitHub remote", got, err)
	}
	res := decodeSetup(t, out)
	if gh := stepNamed(t, res, "github"); gh.Status != setupSkipped || gh.Fix != "gh auth login" {
		t.Fatalf("github step = %+v", gh)
	}
	if !res.Ready || !reflect.DeepEqual(res.NextActions, []string{"gh auth login"}) {
		t.Fatalf("ready = %v, next_actions = %q; want ready with the gh fix listed", res.Ready, res.NextActions)
	}
	human, _, _ := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		LookPath:     func(string) (string, error) { return "/usr/bin/gh", nil },
	}, "setup", "--path", repo)
	if !strings.Contains(human, "Orchestrator is ready.") || !strings.Contains(human, "Optional:\n  1. gh auth login") {
		t.Fatalf("human output:\n%s", human)
	}
}

func TestSetupGitHubCheckErrorIsAdvisoryWithoutAGitHubRemote(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	registerOnPost(d, repo, "")
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc","defaultBranch":"main"}}`)
	d.set("GET /api/v1/system/github-auth", 500, `{"code":"INTERNAL","message":"boom"}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if got := ExitCode(err); got != 0 {
		t.Fatalf("exit = %d (%v), want 0", got, err)
	}
	if gh := stepNamed(t, decodeSetup(t, out), "github"); gh.Status != setupSkipped || !strings.Contains(gh.Detail, "boom") {
		t.Fatalf("github step = %+v", gh)
	}
}

func TestSetupStartAuthRequiredGivesTheLoginCommand(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"calc-1"}`)
	d.set("GET /api/v1/sessions/calc-1", 200, `{"session":{"id":"calc-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"idle"}}}`)
	d.set("GET /api/v1/sessions/calc-1/conversation", 200, `{"turns":[{"id":"t1","state":"failed","failureCode":"auth_required","errorMessage":"Not logged in"}]}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "claude-code", "--start", "fix", "--worker", "--json")
	if got := ExitCode(err); got != setupExitFailed {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	res := decodeSetup(t, out)
	if start := stepNamed(t, res, "start"); start.Fix != "agent-a login" || !reflect.DeepEqual(res.NextActions, []string{"agent-a login"}) {
		t.Fatalf("start step = %+v, next_actions = %q", start, res.NextActions)
	}
}

// gitIn runs git in dir with a clean environment.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitFreeEnv(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// registerAutoBranch makes POST /projects register path with a remote and an
// "auto" default branch, as the daemon does when origin's HEAD is not cached.
func registerAutoBranch(d *taskDaemon, path string) {
	project := fmt.Sprintf(`{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"auto","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}`, path)
	d.set("POST /api/v1/projects", 201, `{"project":`+project+`}`)
	d.set("GET /api/v1/projects/calc", 200, `{"status":"ok","project":`+project+`}`)
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc","defaultBranch":"main"}}`)
}

func TestSetupRemoteWithUnresolvableHeadNeedsHuman(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	// A feature branch is checked out: it must never become the base branch.
	gitIn(t, repo, "switch", "-q", "-c", "feature/x")
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	registerAutoBranch(d, repo)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	if d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatalf("setup saved a default branch for a remote it could not resolve: %s", d.bodies["PUT /api/v1/projects/calc/config"])
	}
	res := decodeSetup(t, out)
	project := stepNamed(t, res, "project")
	wantFix := fmt.Sprintf("git -C %s fetch origin && git -C %s remote set-head origin --auto", setupShellQuote(repo), setupShellQuote(repo))
	if project.Status != setupNeedsHuman || project.Fix != wantFix || !strings.Contains(project.Detail, "origin's default branch could not be resolved") || !strings.Contains(project.Detail, "ao project set-config calc --default-branch <branch>") {
		t.Fatalf("project step = %+v", project)
	}
	if !reflect.DeepEqual(res.NextActions, []string{wantFix}) {
		t.Fatalf("next_actions = %q", res.NextActions)
	}
}

func TestSetupRemoteWithUnresolvableHeadUsesLocalOriginMain(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	gitIn(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitIn(t, repo, "switch", "-q", "-c", "feature/x")
	registerAutoBranch(d, repo)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	var put setConfigRequest
	if err := json.Unmarshal([]byte(d.bodies["PUT /api/v1/projects/calc/config"]), &put); err != nil || put.Config.DefaultBranch != "main" {
		t.Fatalf("set-config body = %s; want origin/main's branch, not the checked-out feature branch", d.bodies["PUT /api/v1/projects/calc/config"])
	}
	if project := stepNamed(t, decodeSetup(t, out), "project"); project.Status != setupFixed || !strings.Contains(project.Detail, "using origin/main") {
		t.Fatalf("project step = %+v", project)
	}
}

func TestSetupRemoteWithCachedHeadKeepsAuto(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, repo, "clone", "-q", "--bare", repo, remote)
	gitIn(t, repo, "remote", "add", "origin", remote)
	gitIn(t, repo, "fetch", "-q", "origin")
	gitIn(t, repo, "remote", "set-head", "origin", "main")
	registerAutoBranch(d, repo)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatal("setup overrode a default branch the remote resolves")
	}
}

func TestSetupUnresolvableHeadOnDetachedHeadNeedsHuman(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	gitIn(t, repo, "checkout", "-q", "--detach")
	registerAutoBranch(d, repo)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	if project := stepNamed(t, decodeSetup(t, out), "project"); project.Status != setupNeedsHuman || !strings.Contains(project.Fix, "remote set-head origin --auto") {
		t.Fatalf("project step = %+v", project)
	}
}

func TestSetupStartStillQueuedExits4(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"calc-1"}`)
	d.set("GET /api/v1/sessions/calc-1", 200, `{"session":{"id":"calc-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/calc-1/conversation", 200, `{"turns":[{"id":"t1","state":"queued"}]}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--start", "fix it", "--worker", "--json")
	if got := ExitCode(err); got != taskExitStillQueued {
		t.Fatalf("exit = %d (%v), want 4\n%s", got, err, out)
	}
	res := decodeSetup(t, out)
	if res.Task == nil || res.Task.Status != taskStatusQueued {
		t.Fatalf("task = %+v", res.Task)
	}
	start := stepNamed(t, res, "start")
	if start.Status != setupFixed || !strings.Contains(start.Detail, "do not start it again") {
		t.Fatalf("start step = %+v", start)
	}
	for _, a := range res.NextActions {
		if strings.Contains(a, "retry") {
			t.Fatalf("next_actions tells the agent to retry a queued task: %q", res.NextActions)
		}
	}
}

func TestSetupStartNeedsInputDoesNotSayRerunSetup(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"calc-1"}`)
	d.set("GET /api/v1/sessions/calc-1", 200, `{"session":{"id":"calc-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/calc-1/conversation", 200, `{"turns":[{"id":"t1","state":"running"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`)
	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--start", "fix it", "--worker", "--json")
	verify := stepNamed(t, decodeSetup(t, out), "verify")
	if strings.Contains(verify.Detail, "then run `ao setup` again") || !strings.Contains(verify.Detail, "do not run `ao setup --start` again") {
		t.Fatalf("verify = %+v", verify)
	}
}

func TestSetupCheckWithoutDaemonReportsRoleFlags(t *testing.T) {
	setConfigEnv(t)
	clearCallerEnv(t)
	out, _, _ := executeCLI(t, Deps{}, "setup", "--check", "--agent", "gemini", "--worker-agent", "codex", "--orchestrator-agent", "claude-code", "--json")
	res := decodeSetup(t, out)
	if res.Agent == nil || res.Agent.Source != "roles" || res.Agent.Worker != "codex" || res.Agent.Orchestrator != "claude-code" || res.Agent.AuthStatus != "unknown" {
		t.Fatalf("agent = %+v", res.Agent)
	}
}

func TestSetupCheckDoesNotProbeAgents(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false) // no remote: the preview makes no network call
	for _, mode := range []string{"--check", "--dry-run"} {
		out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", mode, "--json")
		for _, r := range d.requests {
			if strings.HasSuffix(r, "/probe") || r == "POST /api/v1/agents/readiness/ensure" {
				t.Fatalf("%s called %s; it can start background agent work", mode, r)
			}
		}
		if agent := stepNamed(t, decodeSetup(t, out), "agent"); agent.Status != setupOK {
			t.Fatalf("%s: agent step = %+v (codex is ready in the readiness reading)", mode, agent)
		}
	}
}

func TestSetupReconcileUsesTheReReadConfig(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	d.set("GET /api/v1/projects", 200, fmt.Sprintf(`{"projects":[{"id":"calc","path":%q}]}`, repo))
	// The first read carries no config; the re-read shows saved agents.
	reads := 0
	d.setFunc("GET /api/v1/projects/calc", func() (int, string) {
		reads++
		if reads == 1 {
			return 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main"}}`, repo)
		}
		return 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"claude-code"},"orchestrator":{"agent":"claude-code"}}}}`, repo)
	})
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc"}}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	if d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatalf("setup replaced the saved agents without --yes: %s", d.bodies["PUT /api/v1/projects/calc/config"])
	}
}

func TestSetupReadyHintNamesTheProject(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(out, `ao task start --project calc "<task>"`) {
		t.Fatalf("output:\n%s", out)
	}
}

func TestSetupFailedStartWarnsBeforeResending(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/orchestrators/delegate", 500, `{"code":"INTERNAL","message":"boom"}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--start", "fix it", "--worker", "--json")
	if got := ExitCode(err); got != setupExitFailed {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	verify := stepNamed(t, decodeSetup(t, out), "verify")
	if strings.Contains(verify.Detail, "then run `ao setup` again") || !strings.Contains(verify.Detail, "check `ao session ls` before running `ao setup --start` again") {
		t.Fatalf("verify = %+v", verify)
	}
}

func TestSetupNeedsInputUsesTheTaskFix(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"calc-1"}`)
	d.set("GET /api/v1/sessions/calc-1", 200, `{"session":{"id":"calc-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/calc-1/conversation", 200, `{"turns":[{"id":"t1","state":"running"}],"activities":[{"turnId":"t1","activityKind":"approval","status":"pending"}]}`)
	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--start", "fix it", "--worker", "--json")
	res := decodeSetup(t, out)
	start := stepNamed(t, res, "start")
	// ao task's fix names the session to answer: setup's action opens that
	// session, and the fix itself stays in the detail.
	if res.Task == nil || res.Task.Fix == "" || start.Fix != "ao session get calc-1" || !strings.Contains(start.Detail, res.Task.Fix) {
		t.Fatalf("start = %+v, task fix = %q", start, res.Task.Fix)
	}
}

// Another run registers the project with ready agents between this run's
// agent step and its project step: the caller's sign-in no longer blocks.
func TestSetupRechecksAgentsWhenTheProjectAppearsLate(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CLAUDECODE", "1")
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	project := fmt.Sprintf(`{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}`, repo)
	d.setFunc("GET /api/v1/projects", func() (int, string) {
		if !d.saw("POST /api/v1/projects") {
			return 200, `{"projects":[]}`
		}
		return 200, fmt.Sprintf(`{"projects":[{"id":"calc","path":%q}]}`, repo)
	})
	d.set("GET /api/v1/projects/calc", 200, `{"status":"ok","project":`+project+`}`)
	d.set("POST /api/v1/projects", 409, `{"code":"PATH_ALREADY_REGISTERED","message":"already registered"}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	if !res.Ready || res.Agent.ID != "codex" || res.Agent.Source != "project" {
		t.Fatalf("ready=%v agent=%+v", res.Ready, res.Agent)
	}
	if agent := stepNamed(t, res, "agent"); agent.Status != setupOK || !strings.Contains(agent.Detail, "is not used") {
		t.Fatalf("agent step = %+v", agent)
	}
}

func TestSetupCheckApplyCommandKeepsPathAndFlags(t *testing.T) {
	newSetupDaemon(t)
	repo := gitRepo(t, false)
	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--check", "--json")
	res := decodeSetup(t, out)
	want := "ao setup --path " + setupShellQuote(repo) + " --agent codex"
	if len(res.NextActions) == 0 || res.NextActions[len(res.NextActions)-1] != want {
		t.Fatalf("next_actions = %q, want the last one %q", res.NextActions, want)
	}
}

func TestSetupRemoteNotNamedOriginNeedsHuman(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	gitIn(t, repo, "remote", "add", "upstream", filepath.Join(t.TempDir(), "missing.git"))
	gitIn(t, repo, "switch", "-q", "-c", "feature/x")
	project := fmt.Sprintf(`{"id":"calc","path":%q,"repo":"","defaultBranch":"auto","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}`, repo)
	d.set("POST /api/v1/projects", 201, `{"project":`+project+`}`)
	d.set("GET /api/v1/projects/calc", 200, `{"status":"ok","project":`+project+`}`)
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc"}}`)

	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	if d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatal("setup saved the checked-out feature branch for a repository with a non-origin remote")
	}
	// The preview says the same before registering.
	newSetupDaemon(t)
	out, _, _ = executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--check", "--json")
	if project := stepNamed(t, decodeSetup(t, out), "project"); project.Status != setupNeedsHuman || !strings.Contains(project.Detail, "upstream's default branch could not be resolved") || !strings.Contains(project.Fix, "fetch upstream") {
		t.Fatalf("preview project step = %+v", project)
	}
}

func TestSetupRechecksWhenSavedRolesChangeBetweenLookups(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	d.set("GET /api/v1/projects", 200, fmt.Sprintf(`{"projects":[{"id":"calc","path":%q}]}`, repo))
	// The agent step sees one saved agent; by the project step it is another.
	reads := 0
	d.setFunc("GET /api/v1/projects/calc", func() (int, string) {
		reads++
		agent := "codex"
		if reads == 1 {
			agent = "claude-code"
		}
		return 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":%q},"orchestrator":{"agent":%q}}}}`, repo, agent, agent)
	})
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if res := decodeSetup(t, out); !res.Ready || res.Agent.ID != "codex" {
		t.Fatalf("ready=%v agent=%+v", res.Ready, res.Agent)
	}
}

func TestSetupInsideALinkedWorktreeRegistersTheMainRepo(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	wt := filepath.Join(t.TempDir(), "repo-3")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "agent/task-3", wt)
	registerOnPost(d, repo, "example/calc")
	t.Chdir(wt)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(d.bodies["POST /api/v1/projects"], fmt.Sprintf(`"path":%q`, repo)) {
		t.Fatalf("registered %s, want the main repository %s", d.bodies["POST /api/v1/projects"], repo)
	}
}

func TestSetupUsesAOProjectIDInsideASession(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	d.set("GET /api/v1/projects", 200, fmt.Sprintf(`{"projects":[{"id":"calc","path":%q}]}`, repo))
	d.set("GET /api/v1/projects/calc", 200, fmt.Sprintf(`{"status":"ok","project":{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}}`, repo))
	t.Setenv("AO_PROJECT_ID", "calc")
	t.Chdir(t.TempDir()) // a session workspace, not the project's checkout
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if d.saw("POST /api/v1/projects") {
		t.Fatal("setup registered the session's directory instead of using AO_PROJECT_ID")
	}
	if res := decodeSetup(t, out); res.Project == nil || res.Project.ID != "calc" || !res.Project.Reused {
		t.Fatalf("project = %+v", res.Project)
	}
}

func TestSetupDoesNotSaveAnUninstalledCaller(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	t.Setenv("CURSOR_AGENT", "1")
	d.set("POST /api/v1/agents/readiness/ensure", 200, `{"agents":[
		{"id":"codex","label":"Agent B","installation":{"state":"installed"},"authentication":{"state":"authorized"},"effectiveReadiness":"ready"},
		{"id":"cursor","label":"Agent D","installation":{"state":"not_installed"},"authentication":{"state":"unknown"},"effectiveReadiness":"not_ready"}]}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	res := decodeSetup(t, out)
	if res.Agent.ID != "codex" || res.Agent.Source != "ready" || !strings.Contains(stepNamed(t, res, "agent").Detail, "cursor (from CURSOR_AGENT) is not installed") {
		t.Fatalf("agent = %+v step = %+v", res.Agent, stepNamed(t, res, "agent"))
	}
	if strings.Contains(d.bodies["POST /api/v1/projects"], "cursor") {
		t.Fatalf("saved the uninstalled caller: %s", d.bodies["POST /api/v1/projects"])
	}
}

func TestSetupNeverSavesAnUninstalledAgent(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	// An agent that is not installed: the project is registered without agents.
	_, _, _ = executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "gemini", "--json")
	var body addProjectRequest
	if err := json.Unmarshal([]byte(d.bodies["POST /api/v1/projects"]), &body); err == nil && body.Config != nil {
		t.Fatalf("saved %+v for an agent that is not installed", body.Config)
	}
}

func TestSetupGitHubSSHAliasCountsAsGitHub(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	gitIn(t, repo, "remote", "add", "origin", "git@github-work:org/calc.git")
	gitIn(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	registerOnPost(d, repo, "org/calc")
	d.set("GET /api/v1/system/github-auth", 200, `{"id":"github-auth","satisfied":false}`)
	deps := fakeClockDeps()
	deps.LookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	deps.CommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ssh" && len(args) == 2 && args[0] == "-G" && args[1] == "github-work" {
			return []byte("user git\nhostname github.com\nport 22\n"), nil
		}
		return nil, errors.New("unexpected command")
	}
	out, _, err := executeCLI(t, deps, "setup", "--path", repo, "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	if gh := stepNamed(t, decodeSetup(t, out), "github"); gh.Status != setupNeedsHuman {
		t.Fatalf("github step = %+v; an SSH alias for github.com must count as GitHub", gh)
	}
}

func TestSetupUsesANonOriginRemotesDefault(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	gitIn(t, repo, "remote", "add", "upstream", filepath.Join(t.TempDir(), "missing.git"))
	gitIn(t, repo, "update-ref", "refs/remotes/upstream/trunk", "HEAD")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/upstream/HEAD", "refs/remotes/upstream/trunk")
	gitIn(t, repo, "switch", "-q", "-c", "feature/x")
	project := fmt.Sprintf(`{"id":"calc","path":%q,"repo":"","defaultBranch":"auto","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}`, repo)
	d.set("POST /api/v1/projects", 201, `{"project":`+project+`}`)
	d.set("GET /api/v1/projects/calc", 200, `{"status":"ok","project":`+project+`}`)
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc","defaultBranch":"trunk"}}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	var put setConfigRequest
	if err := json.Unmarshal([]byte(d.bodies["PUT /api/v1/projects/calc/config"]), &put); err != nil || put.Config.DefaultBranch != "trunk" {
		t.Fatalf("set-config body = %s, want upstream's default trunk", d.bodies["PUT /api/v1/projects/calc/config"])
	}
}

func TestSetupQueuedTaskListsTheWaitCommand(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/orchestrators/delegate", 202, `{"ok":true,"workerId":"calc-1"}`)
	d.set("GET /api/v1/sessions/calc-1", 200, `{"session":{"id":"calc-1","harness":"claude-code","mode":"chat","provisionState":"ready","activity":{"state":"active"}}}`)
	d.set("GET /api/v1/sessions/calc-1/conversation", 200, `{"turns":[{"id":"t1","state":"queued"}]}`)
	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--start", "fix it", "--worker", "--json")
	if res := decodeSetup(t, out); !reflect.DeepEqual(res.NextActions, []string{"ao task wait calc-1"}) {
		t.Fatalf("next_actions = %q", res.NextActions)
	}
}

func TestSetupDryRunWithoutDaemonListsTheApplyCommand(t *testing.T) {
	setConfigEnv(t)
	clearCallerEnv(t)
	repo := gitRepo(t, false)
	out, _, err := executeCLI(t, Deps{}, "setup", "--path", repo, "--dry-run", "--json")
	if got := ExitCode(err); got != 0 {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if res := decodeSetup(t, out); !reflect.DeepEqual(res.NextActions, []string{"ao setup --path " + setupShellQuote(repo)}) {
		t.Fatalf("next_actions = %q", res.NextActions)
	}
}

func TestSetupErrorSummaryDoesNotRepeatVerify(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CLAUDECODE", "1")
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	_, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err == nil || strings.Contains(err.Error(), "verify:") {
		t.Fatalf("err = %v; the summary must not repeat the verify step", err)
	}
}

func TestSetupFirstRunOKOnceTheProjectIsRegistered(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	registerAutoBranch(d, repo)
	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "codex", "--json")
	res := decodeSetup(t, out)
	if stepNamed(t, res, "project").Status != setupNeedsHuman || stepNamed(t, res, "first-run").Status != setupOK {
		t.Fatalf("project = %+v first-run = %+v", stepNamed(t, res, "project"), stepNamed(t, res, "first-run"))
	}
}

func TestSetupUnresolvedSSHAliasChecksGitHub(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	gitIn(t, repo, "remote", "add", "origin", "git@corp-git:org/calc.git")
	gitIn(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	registerOnPost(d, repo, "org/calc")
	d.set("GET /api/v1/system/github-auth", 200, `{"id":"github-auth","satisfied":false}`)
	deps := fakeClockDeps()
	deps.LookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	deps.CommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ssh" {
			return []byte("hostname corp-git\n"), nil // ssh -G echoes an unknown alias back
		}
		return nil, errors.New("unexpected command")
	}
	out, _, _ := executeCLI(t, deps, "setup", "--path", repo, "--json")
	if gh := stepNamed(t, decodeSetup(t, out), "github"); gh.Status != setupNeedsHuman {
		t.Fatalf("github step = %+v; an alias setup cannot resolve must be checked like GitHub", gh)
	}
}

func TestSetupUninstalledCallerFallsBackOnAFreshDaemon(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	t.Setenv("CURSOR_AGENT", "1")
	// A fresh daemon: an agent is installed but its sign-in is not read yet.
	d.set("POST /api/v1/agents/readiness/ensure", 200, `{"agents":[
		{"id":"claude-code","label":"Agent A","installation":{"state":"installed"},"authentication":{"state":"unknown"},"effectiveReadiness":"unknown"},
		{"id":"codex","label":"Agent B","installation":{"state":"installed"},"authentication":{"state":"unauthorized"},"effectiveReadiness":"not_ready"},
		{"id":"cursor","label":"Agent D","installation":{"state":"not_installed"},"authentication":{"state":"unknown"},"effectiveReadiness":"not_ready"}]}`)
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"authorized"},"supported":true,"installed":true}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if res := decodeSetup(t, out); res.Agent.ID != "claude-code" || res.Agent.Source != "ready" || !res.Ready {
		t.Fatalf("ready=%v agent=%+v", res.Ready, res.Agent)
	}
}

func TestSetupInterruptedStartExits4(t *testing.T) {
	task := taskResult{Status: taskStatusRunning, TaskID: "calc-1", SessionIDs: []string{"calc-1"}, waitInterrupted: true}
	res := setupResult{Task: &task, Steps: []setupStep{{Step: setupStepStart, Status: setupFixed, Fix: "ao task wait calc-1"}}, NextActions: []string{}}
	finishSetup(&res, setupOptions{start: "fix it"})
	if got := ExitCode(setupExit(res, setupOptions{start: "fix it"})); got != taskExitStillQueued {
		t.Fatalf("exit = %d, want 4 for a submitted task whose wait was interrupted", got)
	}
	if !reflect.DeepEqual(res.NextActions, []string{"ao task wait calc-1"}) {
		t.Fatalf("next_actions = %q", res.NextActions)
	}
}

func TestSetupUnconfirmedStartExits4(t *testing.T) {
	for _, status := range []string{taskStatusStarting, taskStatusRunning} {
		task := taskResult{Status: status, TaskID: "calc-1", SessionIDs: []string{"calc-1"}, waitTimedOut: true, Reason: "timed out after 2m0s", Fix: "wait longer with `ao task wait calc-1`"}
		res := setupResult{Task: &task, Steps: []setupStep{{Step: setupStepStart, Status: setupFixed, Fix: "ao task wait calc-1"}}, NextActions: []string{}}
		finishSetup(&res, setupOptions{start: "fix it"})
		err := setupExit(res, setupOptions{start: "fix it"})
		if got := ExitCode(err); got != taskExitStillQueued {
			t.Fatalf("%s: exit = %d, want 4 for a submitted task the wait did not confirm", status, got)
		}
		if !strings.Contains(err.Error(), "do not start it again") {
			t.Fatalf("%s: message = %q", status, err)
		}
		if !reflect.DeepEqual(res.NextActions, []string{"ao task wait calc-1"}) {
			t.Fatalf("%s: next_actions = %q", status, res.NextActions)
		}
	}
}

func TestSetupListsEachSignInSeparately(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	d.set("POST /api/v1/agents/codex/probe", 200, `{"agent":{"id":"codex","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--worker-agent", "claude-code", "--orchestrator-agent", "codex", "--json")
	if res := decodeSetup(t, out); !reflect.DeepEqual(res.NextActions, []string{"agent-a login", "agent-b login"}) {
		t.Fatalf("next_actions = %q, want one runnable login command each", res.NextActions)
	}
}

func TestSetupRerunInstructionNamesTheTarget(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CLAUDECODE", "1")
	registerOnPost(d, repo, "example/calc")
	d.set("POST /api/v1/agents/claude-code/probe", 200, `{"agent":{"id":"claude-code","authStatus":"unauthorized"},"supported":true,"installed":true}`)
	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if v := stepNamed(t, decodeSetup(t, out), "verify"); !strings.Contains(v.Detail, "then run `ao setup --path "+setupShellQuote(repo)+"`") {
		t.Fatalf("verify = %+v", v)
	}
}

func TestSetupDefersRegistrationForAnUninstalledExplicitAgent(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	out, _, _ := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--agent", "gemini", "--json")
	if d.saw("POST /api/v1/projects") {
		t.Fatal("registered the project although the chosen agent is not installed")
	}
	if p := stepNamed(t, decodeSetup(t, out), "project"); p.Status != setupSkipped || !strings.Contains(p.Detail, "not registered yet") {
		t.Fatalf("project step = %+v", p)
	}
}

func TestSetupChecksSavedRolesWhenTheChoiceCannotBeSaved(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	t.Setenv("CURSOR_AGENT", "1")
	d.set("POST /api/v1/agents/readiness/ensure", 200, `{"agents":[{"id":"cursor","label":"Agent D","installation":{"state":"not_installed"},"authentication":{"state":"unknown"},"effectiveReadiness":"not_ready"}]}`)
	d.set("POST /api/v1/agents/cursor/probe", 200, `{"agent":{"id":"cursor"},"supported":true,"installed":false}`)
	// The first lookup misses the project; the project step finds it with ready agents.
	project := fmt.Sprintf(`{"id":"calc","path":%q,"repo":"example/calc","defaultBranch":"main","config":{"worker":{"agent":"codex"},"orchestrator":{"agent":"codex"}}}`, repo)
	lists := 0
	d.setFunc("GET /api/v1/projects", func() (int, string) {
		lists++
		if lists == 1 {
			return 200, `{"projects":[]}`
		}
		return 200, fmt.Sprintf(`{"projects":[{"id":"calc","path":%q}]}`, repo)
	})
	d.set("GET /api/v1/projects/calc", 200, `{"status":"ok","project":`+project+`}`)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--path", repo, "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if res := decodeSetup(t, out); !res.Ready || res.Agent.ID != "codex" {
		t.Fatalf("ready=%v agent=%+v; the project's own ready agents should be used", res.Ready, res.Agent)
	}
}

func TestSetupUnreadableAOProjectIDStops(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, true)
	registerOnPost(d, repo, "example/calc")
	t.Setenv("AO_PROJECT_ID", "missing")
	t.Chdir(repo)
	out, _, err := executeCLI(t, fakeClockDeps(), "setup", "--json")
	if got := ExitCode(err); got != setupExitFailed {
		t.Fatalf("exit = %d (%v)", got, err)
	}
	if d.saw("POST /api/v1/projects") {
		t.Fatal("fell back to the current checkout although AO_PROJECT_ID could not be read")
	}
	if p := stepNamed(t, decodeSetup(t, out), "project"); !strings.Contains(p.Detail, "pass --path") {
		t.Fatalf("project step = %+v", p)
	}
}

func TestSetupRemoteLookupFailureSavesNoBranch(t *testing.T) {
	d := newSetupDaemon(t)
	repo := gitRepo(t, false)
	gitIn(t, repo, "switch", "-q", "-c", "feature/x")
	registerOnPost(d, repo, "")
	d.set("PUT /api/v1/projects/calc/config", 200, `{"project":{"id":"calc"}}`)
	deps := fakeClockDeps()
	deps.CommandOutputInDir = func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) == 1 && args[0] == "remote" {
			return nil, errors.New("git remote: exit status 128")
		}
		return commandOutputInDir(ctx, dir, name, args...)
	}
	out, _, err := executeCLI(t, deps, "setup", "--path", repo, "--agent", "codex", "--json")
	if got := ExitCode(err); got != setupExitNeedsHuman {
		t.Fatalf("exit = %d (%v)\n%s", got, err, out)
	}
	if d.saw("PUT /api/v1/projects/calc/config") {
		t.Fatal("saved the checked-out branch although the remotes could not be listed")
	}
	if p := stepNamed(t, decodeSetup(t, out), "project"); !strings.Contains(p.Fix, "remote -v") {
		t.Fatalf("project step = %+v", p)
	}
}
