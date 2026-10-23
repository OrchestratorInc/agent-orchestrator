package workerexec

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestSystemPromptArtifactSections(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.ToSlash(worker.ArtifactsDir(dataDir, "session-1"))
	cases := []struct {
		name        string
		launch      worker.LaunchContext
		wantSection bool
		wantDeliver bool
	}{
		{"artifacts off", worker.LaunchContext{SessionID: "session-1", Kind: "worker", RequestedOutput: domain.RequestedOutputArtifact}, false, false},
		{"worker", worker.LaunchContext{SessionID: "session-1", Kind: "worker", Artifacts: true}, true, false},
		{"artifact worker", worker.LaunchContext{SessionID: "session-1", Kind: "worker", Artifacts: true, RequestedOutput: domain.RequestedOutputArtifact}, true, true},
		{"orchestrator", worker.LaunchContext{SessionID: "session-1", Kind: "orchestrator", Artifacts: true, RequestedOutput: domain.RequestedOutputArtifact}, true, false},
		{"reviewer", worker.LaunchContext{SessionID: "session-1", Kind: "reviewer", Artifacts: true}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := HarnessBuilder{DataDir: dataDir}.systemPrompt(tc.launch, "/work")
			if got := strings.Contains(prompt, "## Session Artifacts") && strings.Contains(prompt, dir); got != tc.wantSection {
				t.Fatalf("artifacts section present = %v, want %v:\n%s", got, tc.wantSection, prompt)
			}
			if got := strings.Contains(prompt, "## Artifact Output"); got != tc.wantDeliver {
				t.Fatalf("artifact output section present = %v, want %v", got, tc.wantDeliver)
			}
		})
	}
}

func TestAgentDirectoriesIncludeTheArtifactDirectory(t *testing.T) {
	dataDir := t.TempDir()
	builder := HarnessBuilder{DataDir: dataDir}
	want := worker.ArtifactsDir(dataDir, "session-1")
	launch := worker.LaunchContext{SessionID: "session-1", Kind: "worker", Artifacts: true}
	if dirs := builder.agentDirectories(launch, "/work"); len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("agentDirectories = %v, want [%s]", dirs, want)
	}
	if dirs := builder.agentDirectories(worker.LaunchContext{SessionID: "session-1", Kind: "worker"}, "/work"); len(dirs) != 0 {
		t.Fatalf("agentDirectories with artifacts off = %v, want none", dirs)
	}
	reviewer := worker.LaunchContext{SessionID: "session-1", Kind: "reviewer", Artifacts: true}
	if dirs := builder.agentDirectories(reviewer, "/work"); len(dirs) != 0 {
		t.Fatalf("agentDirectories for a reviewer = %v, want none", dirs)
	}
	if args := codexWritableRootsArgs(builder.agentDirectories(launch, "/work")); len(args) != 2 || !strings.Contains(args[1], strconv.Quote(want)) {
		t.Fatalf("codex writable roots = %v, want the artifact directory", args)
	}
}

// The artifact directory reaches the commands the harnesses run: Claude's
// --add-dir and settings, Codex's writable roots in the terminal and in Chat,
// also when the agent starts in the parent folder. It does not turn on the
// loading of CLAUDE.md files from extra directories.
func TestArtifactDirectoryReachesHarnessCommands(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dataDir := t.TempDir()
	workspace := t.TempDir()
	launch := worker.LaunchContext{
		SessionID: "session-1", Kind: "worker", Harness: "claude-code", Mode: "trusted",
		RepositoryURL: "https://github.com/acme/alice", Artifacts: true,
	}
	dir := worker.ArtifactsDir(dataDir, launch.SessionID)
	claude := worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "secret"}
	terminal, err := HarnessBuilder{DataDir: dataDir, Launch: launch}.BuildInteractive(launch, claude, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(terminal.Args, "--add-dir="+dir) && !slices.Contains(terminal.Args, dir) {
		t.Fatalf("claude terminal args lack the artifact directory: %v", terminal.Args)
	}
	settings := readJSON(t, filepath.Join(terminal.Env["CLAUDE_CONFIG_DIR"], "settings.json"))
	if got := stringsOf(settings["permissions"].(map[string]any)["additionalDirectories"]); !slices.Contains(got, dir) {
		t.Fatalf("claude settings additionalDirectories = %v", got)
	}
	if terminal.Env[claudeAdditionalDirectoriesInstructionsEnv] != "" {
		t.Fatal("the artifact directory turned on CLAUDE.md loading")
	}

	codexLaunch := launch
	codexLaunch.Harness = "codex"
	codex := worker.CredentialResponse{Provider: "codex", CredentialType: "auth_json", Secret: "{}"}
	codexTerminal, err := HarnessBuilder{DataDir: dataDir, Launch: codexLaunch}.BuildInteractive(codexLaunch, codex, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if want := `sandbox_workspace_write.writable_roots=["` + dir + `"]`; !slices.Contains(codexTerminal.Args, want) {
		t.Fatalf("codex terminal args lack %s: %v", want, codexTerminal.Args)
	}
	chat, err := HarnessBuilder{DataDir: dataDir, Launch: codexLaunch}.Build(context.Background(),
		worker.Turn{Harness: "codex", Mode: "trusted", Prompt: "hi"}, codex, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(chat.WritableRoots, dir) {
		t.Fatalf("codex chat writable roots = %v", chat.WritableRoots)
	}

	parentWorkspace, parentLaunch := multiRepoWorkspace(t)
	parentLaunch.AgentStartsIn = domain.ProjectAgentStartsInParent
	parentLaunch.Artifacts = true
	parent, err := HarnessBuilder{DataDir: dataDir, Launch: parentLaunch}.BuildInteractive(parentLaunch, claude, parentWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	parentSettings := readJSON(t, filepath.Join(parent.Env["CLAUDE_CONFIG_DIR"], "settings.json"))
	if got := stringsOf(parentSettings["permissions"].(map[string]any)["additionalDirectories"]); !slices.Contains(got, dir) {
		t.Fatalf("parent-start additionalDirectories = %v", got)
	}
}

func TestOrchestratorPromptPointsToWorkerArtifacts(t *testing.T) {
	prompt := orchestratorSystemPrompt("/skills")
	for _, want := range []string{"`artifactFiles`", "cannot read", "ao list --json"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("orchestrator prompt does not mention %q", want)
		}
	}
}
