package workerexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// multiRepoWorkspace lays out a main checkout and two additional checkouts
// side by side, as the worker does, and returns the main checkout's path.
func multiRepoWorkspace(t *testing.T) (workspace string, launch worker.LaunchContext) {
	t.Helper()
	root := t.TempDir()
	workspace = filepath.Join(root, "alice")
	for _, dir := range []string{"alice", "voice-calling-ai", "julian"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	launch = worker.LaunchContext{
		SessionID: "session-1", Kind: "worker", Harness: "claude-code", Mode: "trusted", Branch: "ao/abc",
		RepositoryURL: "https://github.com/acme/alice",
		ExtraRepos: []worker.RepoRef{
			{URL: "https://github.com/acme/voice-calling-ai"},
			{URL: "https://github.com/acme/julian", Branch: "develop"},
			{URL: "https://github.com/acme/never-cloned"},
		},
	}
	return workspace, launch
}

func TestRepositoriesPromptNote(t *testing.T) {
	if note := repositoriesPromptNote(worker.LaunchContext{RepositoryURL: "https://github.com/acme/alice"}, "/workspace/repository"); note != "" {
		t.Fatalf("a single-repo session got a note: %q", note)
	}
	workspace, launch := multiRepoWorkspace(t)
	root := filepath.Dir(workspace)
	note := repositoriesPromptNote(launch, workspace)
	for _, want := range []string{
		"Your working directory is the main repository's checkout, " + workspace,
		"- alice (main repository): " + workspace + " (relative to your working directory: .)",
		"- voice-calling-ai: " + filepath.Join(root, "voice-calling-ai") + " (relative to your working directory: ../voice-calling-ai)",
		"- julian (cloned at develop)",
		"this session's branch, ao/abc",
		`passing "repository"`,
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q\n---\n%s", want, note)
		}
	}
	launch.AgentStartsIn = domain.ProjectAgentStartsInParent
	parent := repositoriesPromptNote(launch, workspace)
	for _, want := range []string{
		"Your working directory is " + root + ", the folder that holds every repository",
		"(relative to your working directory: alice)",
		"(relative to your working directory: voice-calling-ai)",
		"AGENTS.md",
	} {
		if !strings.Contains(parent, want) {
			t.Errorf("parent-mode note missing %q\n---\n%s", want, parent)
		}
	}
}

func TestAgentDirectoryAndAdditionalDirectories(t *testing.T) {
	workspace, launch := multiRepoWorkspace(t)
	root := filepath.Dir(workspace)
	if got := agentDirectory(launch, workspace); got != workspace {
		t.Fatalf("default agent directory = %s", got)
	}
	want := []string{filepath.Join(root, "voice-calling-ai"), filepath.Join(root, "julian")}
	if got := additionalDirectories(launch, workspace); !slices.Equal(got, want) {
		t.Fatalf("additional directories = %v, want %v (missing clones skipped)", got, want)
	}
	launch.AgentStartsIn = domain.ProjectAgentStartsInParent
	if got := agentDirectory(launch, workspace); got != root {
		t.Fatalf("parent-mode agent directory = %s", got)
	}
	if got := additionalDirectories(launch, workspace); got != nil {
		t.Fatalf("parent mode still adds directories: %v", got)
	}
	reviewer := launch
	reviewer.Kind = "reviewer"
	if agentDirectory(reviewer, workspace) != workspace || additionalDirectories(reviewer, workspace) != nil {
		t.Fatal("a reviewer must review from the main checkout")
	}
	standalone := worker.LaunchContext{Kind: "worker", AgentStartsIn: domain.ProjectAgentStartsInParent, RepositoryURL: "https://scratch.ao.local/p1"}
	if agentDirectory(standalone, "/workspace/repository") != "/workspace/repository" {
		t.Fatal("a standalone session must start in its own folder")
	}
}

func TestApplyClaudeAdditionalDirectoriesKeepsTheUsersEntries(t *testing.T) {
	settings := map[string]any{"permissions": map[string]any{"additionalDirectories": []any{"/mine"}}}
	applyClaudeAdditionalDirectories(settings, []string{"/work/a", "/work/b"})
	roundTrip(t, settings)
	applyClaudeAdditionalDirectories(settings, []string{"/work/b"})
	roundTrip(t, settings)
	got := settings["permissions"].(map[string]any)["additionalDirectories"]
	if !slices.Equal(stringsOf(got), []string{"/mine", "/work/b"}) {
		t.Fatalf("additionalDirectories = %v", got)
	}
	applyClaudeAdditionalDirectories(settings, nil)
	roundTrip(t, settings)
	if got := stringsOf(settings["permissions"].(map[string]any)["additionalDirectories"]); !slices.Equal(got, []string{"/mine"}) {
		t.Fatalf("after clearing = %v", got)
	}
	if _, ok := settings[claudeAdditionalDirectoriesKey]; ok {
		t.Fatal("the managed marker outlived its entries")
	}
}

func TestClaudeTerminalGetsAccessToEveryCheckout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	workspace, launch := multiRepoWorkspace(t)
	root := filepath.Dir(workspace)
	credential := worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "secret"}
	command, err := HarnessBuilder{DataDir: t.TempDir(), Launch: launch, ProtectSecretFiles: true}.BuildInteractive(launch, credential, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if command.Dir != workspace {
		t.Fatalf("Dir = %s", command.Dir)
	}
	settings := readJSON(t, filepath.Join(command.Env["CLAUDE_CONFIG_DIR"], "settings.json"))
	permissions := settings["permissions"].(map[string]any)
	if got := stringsOf(permissions["additionalDirectories"]); !slices.Equal(got, []string{filepath.Join(root, "voice-calling-ai"), filepath.Join(root, "julian")}) {
		t.Fatalf("additionalDirectories = %v", got)
	}
	deny := stringsOf(permissions["deny"])
	if !slices.Contains(deny, "Read(/"+filepath.Join(root, "julian")+"/**/.env.*)") {
		t.Fatalf("sibling .env.* is not denied: %v", deny)
	}
	trust := readJSON(t, filepath.Join(command.Env["CLAUDE_CONFIG_DIR"], ".claude.json"))["projects"].(map[string]any)
	if _, ok := trust[filepath.Join(root, "voice-calling-ai")]; !ok {
		t.Fatalf("sibling checkout not trusted: %v", trust)
	}

	launch.AgentStartsIn = domain.ProjectAgentStartsInParent
	parent, err := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}.BuildInteractive(launch, credential, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if parent.Dir != root {
		t.Fatalf("parent-mode Dir = %s, want %s", parent.Dir, root)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("AO's activity hooks are not where Claude starts: %v", err)
	}
	if _, ok := readJSON(t, filepath.Join(parent.Env["CLAUDE_CONFIG_DIR"], ".claude.json"))["projects"].(map[string]any)[root]; !ok {
		t.Fatal("the parent folder is not trusted")
	}
}

func TestCodexGetsWritableRootsForSiblings(t *testing.T) {
	workspace, launch := multiRepoWorkspace(t)
	root := filepath.Dir(workspace)
	launch.Harness = "codex"
	credential := worker.CredentialResponse{Provider: "codex", CredentialType: "auth_json", Secret: "{}"}
	command, err := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}.BuildInteractive(launch, credential, workspace)
	if err != nil {
		t.Fatal(err)
	}
	want := `sandbox_workspace_write.writable_roots=["` + filepath.Join(root, "voice-calling-ai") + `","` + filepath.Join(root, "julian") + `"]`
	if !slices.Contains(command.Args, want) {
		t.Fatalf("codex terminal args lack %s: %v", want, command.Args)
	}
	chat, err := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}.Build(context.Background(),
		worker.Turn{Harness: "codex", Mode: "trusted", Prompt: "hi"}, credential, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(chat.WritableRoots, []string{filepath.Join(root, "voice-calling-ai"), filepath.Join(root, "julian")}) {
		t.Fatalf("codex chat writable roots = %v", chat.WritableRoots)
	}
	if sandbox := codexTurnSandbox("workspace-write", chat.WritableRoots); !slices.Equal(stringsOf(sandbox["writableRoots"]), chat.WritableRoots) {
		t.Fatalf("turn sandbox = %v", sandbox)
	}
	launch.AgentStartsIn = domain.ProjectAgentStartsInParent
	parent, err := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}.Build(context.Background(),
		worker.Turn{Harness: "codex", Mode: "trusted", Prompt: "hi"}, credential, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if parent.Dir != root || len(parent.WritableRoots) != 0 {
		t.Fatalf("parent-mode chat Dir = %s roots = %v", parent.Dir, parent.WritableRoots)
	}
}

func TestClaudeChatGetsAccessToSiblings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	workspace, launch := multiRepoWorkspace(t)
	credential := worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "secret"}
	chat, err := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}.Build(context.Background(),
		worker.Turn{Harness: "claude-code", Mode: "trusted", Prompt: "hi"}, credential, workspace)
	if err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, filepath.Join(chat.Env["CLAUDE_CONFIG_DIR"], "settings.json"))
	if got := stringsOf(settings["permissions"].(map[string]any)["additionalDirectories"]); len(got) != 2 {
		t.Fatalf("chat additionalDirectories = %v", got)
	}
	single := worker.LaunchContext{SessionID: "s", Kind: "worker", Harness: "claude-code", Mode: "trusted", RepositoryURL: "https://github.com/acme/alice"}
	plain, err := HarnessBuilder{DataDir: t.TempDir(), Launch: single}.Build(context.Background(),
		worker.Turn{Harness: "claude-code", Mode: "trusted", Prompt: "hi"}, credential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plain.Env["CLAUDE_CONFIG_DIR"], "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("a single-repo unprotected Chat wrote settings: %v", err)
	}
}

// Claude loads a sibling repository's CLAUDE.md only for a folder opened with
// --add-dir (terminal) or ACP additionalDirectories (Chat), with
// CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD set; settings access alone
// lets it work there without reading the repository's instructions.
func TestClaudeLoadsSiblingInstructions(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	workspace, launch := multiRepoWorkspace(t)
	root := filepath.Dir(workspace)
	siblings := []string{filepath.Join(root, "voice-calling-ai"), filepath.Join(root, "julian")}
	credential := worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "secret"}
	terminal, err := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}.BuildInteractive(launch, credential, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, sibling := range siblings {
		if !slices.Contains(terminal.Args, "--add-dir="+sibling) {
			t.Fatalf("terminal args lack --add-dir=%s: %v", sibling, terminal.Args)
		}
	}
	if terminal.Env[claudeAdditionalDirectoriesInstructionsEnv] != "1" {
		t.Fatalf("terminal env lacks %s", claudeAdditionalDirectoriesInstructionsEnv)
	}
	chat, err := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}.Build(context.Background(),
		worker.Turn{Harness: "claude-code", Mode: "trusted", Prompt: "hi"}, credential, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(chat.AdditionalDirectories, siblings) || chat.Env[claudeAdditionalDirectoriesInstructionsEnv] != "1" {
		t.Fatalf("chat additional directories = %v env = %q", chat.AdditionalDirectories, chat.Env[claudeAdditionalDirectoriesInstructionsEnv])
	}
	single := worker.LaunchContext{SessionID: "s", Kind: "worker", Harness: "claude-code", Mode: "trusted", RepositoryURL: "https://github.com/acme/alice"}
	plain, err := HarnessBuilder{DataDir: t.TempDir(), Launch: single}.BuildInteractive(single, credential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range plain.Args {
		if strings.HasPrefix(arg, "--add-dir") {
			t.Fatalf("a single-repo terminal got %s", arg)
		}
	}
	if _, ok := plain.Env[claudeAdditionalDirectoriesInstructionsEnv]; ok {
		t.Fatal("a single-repo terminal set the additional-directories env")
	}
}

func roundTrip(t *testing.T, settings map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	for key := range settings {
		delete(settings, key)
	}
	if err := json.Unmarshal(encoded, &settings); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(contents, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func stringsOf(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

// The folder holding the checkouts is refused as the agent's working directory
// when granting it would hand the agent more than the repositories: the home
// directory or a folder holding AO's worker state. The agent then starts in
// the main checkout and the prompt says why.
func TestParentAgentDirectoryIsRefusedWhenUnsafe(t *testing.T) {
	t.Run("home directory", func(t *testing.T) {
		workspace, launch := multiRepoWorkspace(t)
		launch.AgentStartsIn = domain.ProjectAgentStartsInParent
		t.Setenv("HOME", filepath.Dir(workspace))
		if got := agentDirectory(launch, workspace); got != workspace {
			t.Fatalf("agent directory = %s, want the main checkout", got)
		}
		if got := additionalDirectories(launch, workspace); len(got) == 0 {
			t.Fatal("the fallback must still grant the sibling checkouts")
		}
		if note := repositoriesPromptNote(launch, workspace); !strings.Contains(note, "it is the home directory") {
			t.Fatalf("prompt does not explain the fallback:\n%s", note)
		}
	})
	t.Run("AO worker state", func(t *testing.T) {
		workspace, launch := multiRepoWorkspace(t)
		launch.AgentStartsIn = domain.ProjectAgentStartsInParent
		if err := os.MkdirAll(filepath.Join(filepath.Dir(workspace), ".ao", "worker"), 0o700); err != nil {
			t.Fatal(err)
		}
		if got := agentDirectory(launch, workspace); got != workspace {
			t.Fatalf("agent directory = %s, want the main checkout", got)
		}
	})
}

// Agents load instruction files only from the folder they start in, so a
// multi-repository session names every repository's AGENTS.md and CLAUDE.md
// and asks the agent to follow them in that repository.
func TestRepositoriesPromptNoteNamesEachRepositorysInstructions(t *testing.T) {
	workspace, launch := multiRepoWorkspace(t)
	root := filepath.Dir(workspace)
	for _, file := range []string{
		filepath.Join(root, "alice", "AGENTS.md"),
		filepath.Join(root, "voice-calling-ai", "AGENTS.md"),
		filepath.Join(root, "voice-calling-ai", "CLAUDE.md"),
	} {
		if err := os.WriteFile(file, []byte("rules\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	note := repositoriesPromptNote(launch, workspace)
	for _, want := range []string{
		"read its instruction files below and follow them there",
		"- alice: " + filepath.Join(root, "alice", "AGENTS.md"),
		"- voice-calling-ai: " + filepath.Join(root, "voice-calling-ai", "AGENTS.md") + ", " + filepath.Join(root, "voice-calling-ai", "CLAUDE.md"),
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "- julian:") {
		t.Fatalf("a repository without instruction files was listed:\n%s", note)
	}
	bare, bareLaunch := multiRepoWorkspace(t)
	if strings.Contains(repositoriesPromptNote(bareLaunch, bare), "instruction files") {
		t.Fatal("a session without instruction files got the instructions note")
	}
	single := t.TempDir()
	if err := os.WriteFile(filepath.Join(single, "AGENTS.md"), []byte("rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if note := repositoriesPromptNote(worker.LaunchContext{RepositoryURL: "https://github.com/acme/alice"}, single); note != "" {
		t.Fatalf("a single-repo session got a note: %q", note)
	}
}
