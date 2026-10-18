package workerexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func readClaudeDeny(t *testing.T, configDir string) []string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions struct {
			DefaultMode string   `json:"defaultMode"`
			Deny        []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatal(err)
	}
	return settings.Permissions.Deny
}

func TestApplySecretFileProtectionMergesWithExistingRules(t *testing.T) {
	t.Parallel()
	settings := map[string]any{"permissions": map[string]any{
		"defaultMode": "auto",
		"deny":        []any{"Bash(rm -rf *)", "Read(.env.*)"},
	}}
	if err := applySecretFileProtection(settings, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := applySecretFileProtection(settings, true, nil); err != nil {
		t.Fatal(err)
	}
	permissions := settings["permissions"].(map[string]any)
	deny := permissions["deny"].([]any)
	if deny[0] != "Bash(rm -rf *)" || len(deny) != 1+len(secretFileDenyRules) || permissions["defaultMode"] != "auto" {
		t.Fatalf("deny = %v", deny)
	}
	// The negations must follow the rules they carve out of.
	for index, rule := range secretFileDenyRules {
		if deny[1+index] != rule {
			t.Fatalf("deny[%d] = %v, want %s", 1+index, deny[1+index], rule)
		}
	}
	if err := applySecretFileProtection(settings, false, nil); err != nil {
		t.Fatal(err)
	}
	if deny := permissions["deny"].([]any); len(deny) != 1 || deny[0] != "Bash(rm -rf *)" {
		t.Fatalf("after opting out, deny = %v", deny)
	}

	untouched := map[string]any{}
	if err := applySecretFileProtection(untouched, false, nil); err != nil || len(untouched) != 0 {
		t.Fatalf("an unprotected session gained settings: %v, %v", untouched, err)
	}
	if err := applySecretFileProtection(map[string]any{"permissions": map[string]any{"deny": "Read(x)"}}, true, nil); err == nil {
		t.Fatal("a malformed deny list was overwritten")
	}
}

// Both the terminal agent and Chat load the protection from the settings in
// Claude's config directory; sessions without it keep their settings as
// before.
func TestClaudeSecretFileProtectionReachesTerminalAndChat(t *testing.T) {
	launch := worker.LaunchContext{SessionID: "session-1", Kind: "worker", Harness: "claude-code", Mode: "trusted"}
	credential := worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "secret"}

	protected := HarnessBuilder{DataDir: t.TempDir(), Launch: launch, ProtectSecretFiles: true}
	terminal, err := protected.BuildInteractive(launch, credential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if deny := readClaudeDeny(t, terminal.Env["CLAUDE_CONFIG_DIR"]); !slices.Equal(deny, secretFileDenyRules) {
		t.Fatalf("terminal deny = %v", deny)
	}

	chatOnly := HarnessBuilder{DataDir: t.TempDir(), Launch: launch, ProtectSecretFiles: true}
	chat, err := chatOnly.Build(context.Background(), worker.Turn{
		Harness: "claude-code", Mode: "trusted", Prompt: "hi",
	}, credential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if deny := readClaudeDeny(t, chat.Env["CLAUDE_CONFIG_DIR"]); !slices.Equal(deny, secretFileDenyRules) {
		t.Fatalf("chat deny = %v", deny)
	}

	plain := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}
	unprotected, err := plain.BuildInteractive(launch, credential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if deny := readClaudeDeny(t, unprotected.Env["CLAUDE_CONFIG_DIR"]); len(deny) != 0 {
		t.Fatalf("unprotected deny = %v", deny)
	}
	plainChat := HarnessBuilder{DataDir: t.TempDir(), Launch: launch}
	chat, err = plainChat.Build(context.Background(), worker.Turn{Harness: "claude-code", Mode: "trusted", Prompt: "hi"}, credential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(chat.Env["CLAUDE_CONFIG_DIR"], "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("an unprotected Chat turn wrote Claude settings: %v", err)
	}
}

// The control plane cannot know where a Coder template's checkout lives; the
// worker names its real workspace in the project context.
func TestSystemPromptNamesTheResolvedWorkspace(t *testing.T) {
	launch := worker.LaunchContext{
		SessionID: "session-1", Kind: "worker", Harness: "claude-code",
		SystemPrompt: "## Project Context\n\n- Workspace: " + worker.WorkspacePathPlaceholder,
	}
	prompt := HarnessBuilder{DataDir: t.TempDir()}.systemPrompt(launch, "/home/ubuntu/work/alice")
	if !slices.Contains(strings.Split(prompt, "\n"), "- Workspace: /home/ubuntu/work/alice") {
		t.Fatalf("prompt does not name the workspace:\n%s", prompt)
	}
}

// A deny rule of the checkout shape that the user wrote is theirs: AO only
// removes the checkout rules it recorded adding.
func TestApplySecretFileProtectionKeepsTheUsersCheckoutShapedRules(t *testing.T) {
	user := "Read(//home/dev/private/**/.env.*)"
	settings := map[string]any{"permissions": map[string]any{"deny": []any{user}}}
	if err := applySecretFileProtection(settings, true, []string{"/home/dev/work/voice"}); err != nil {
		t.Fatal(err)
	}
	if err := applySecretFileProtection(settings, true, []string{"/home/dev/work/julian"}); err != nil {
		t.Fatal(err)
	}
	deny := settings["permissions"].(map[string]any)["deny"].([]any)
	has := func(rule string) bool { return slices.Contains(deny, any(rule)) }
	if !has(user) {
		t.Fatalf("the user's rule was removed: %v", deny)
	}
	if has("Read(//home/dev/work/voice/**/.env.*)") || !has("Read(//home/dev/work/julian/**/.env.*)") {
		t.Fatalf("checkout rules were not replaced: %v", deny)
	}
	if err := applySecretFileProtection(settings, false, nil); err != nil {
		t.Fatal(err)
	}
	deny = settings["permissions"].(map[string]any)["deny"].([]any)
	if len(deny) != 1 || deny[0] != user {
		t.Fatalf("turning protection off left %v, want only the user's rule", deny)
	}
}
