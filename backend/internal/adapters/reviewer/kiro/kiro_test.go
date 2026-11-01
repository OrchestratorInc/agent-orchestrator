package kiro

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandBuildsReadOnlyInteractiveTUI(t *testing.T) {
	promptRoot := t.TempDir()
	systemPath := filepath.Join(promptRoot, "system.md")
	if err := os.WriteFile(systemPath, []byte("AO reviewer role\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "kiro-cli", nil }}

	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		WorkerSessionID:  "worker-1",
		WorkspacePath:    "/worktrees/worker-1",
		Prompt:           "Read and follow /ao/task.md",
		SystemPromptFile: systemPath,
		TaskPromptRoot:   promptRoot,
	})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if want := []string{"kiro-cli", "chat", "--agent", reviewerAgentName}; !slices.Equal(spec.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", spec.Argv, want)
	}
	joined := strings.Join(spec.Argv, " ")
	for _, forbidden := range []string{"--no-interactive", "--trust-all-tools", "--trust-tools", "--json", "--print", "Read and follow"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("interactive argv contains %q: %#v", forbidden, spec.Argv)
		}
	}
	if spec.InitialMessage != "Read and follow /ao/task.md" {
		t.Fatalf("initial message = %q", spec.InitialMessage)
	}
	if spec.WorkingDirectory == "" || spec.WorkingDirectory == "/worktrees/worker-1" || !strings.HasPrefix(spec.WorkingDirectory, promptRoot) {
		t.Fatalf("working directory = %q, want AO-owned directory", spec.WorkingDirectory)
	}

	data, err := os.ReadFile(filepath.Join(spec.WorkingDirectory, ".kiro", "agents", reviewerAgentName+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers     map[string]any `json:"mcpServers"`
		IncludeMCPJSON bool           `json:"includeMcpJson"`
		Resources      []any          `json:"resources"`
		Tools          []string       `json:"tools"`
		AllowedTools   []string       `json:"allowedTools"`
		ToolsSettings  struct {
			Shell struct {
				AllowedCommands []string `json:"allowedCommands"`
				DeniedCommands  []string `json:"deniedCommands"`
				DenyByDefault   bool     `json:"denyByDefault"`
			} `json:"shell"`
		} `json:"toolsSettings"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("agent config: %v", err)
	}
	if cfg.IncludeMCPJSON || len(cfg.MCPServers) != 0 || len(cfg.Resources) != 0 {
		t.Fatalf("project-controlled integrations enabled: %s", data)
	}
	if !slices.Equal(cfg.Tools, []string{"read", "glob", "grep", "shell"}) || slices.Contains(cfg.AllowedTools, "shell") {
		t.Fatalf("tool policy = tools %#v allowed %#v", cfg.Tools, cfg.AllowedTools)
	}
	if !cfg.ToolsSettings.Shell.DenyByDefault || len(cfg.ToolsSettings.Shell.AllowedCommands) != 6 || len(cfg.ToolsSettings.Shell.DeniedCommands) == 0 {
		t.Fatalf("shell policy = %+v", cfg.ToolsSettings.Shell)
	}
	for _, command := range cfg.ToolsSettings.Shell.AllowedCommands {
		if !strings.HasPrefix(command, "^") || !strings.HasSuffix(command, "$") {
			t.Fatalf("shell allowance is not a full-command match %q", command)
		}
		if strings.Contains(command, "git commit") || strings.Contains(command, "git push") || command == "^.*$" {
			t.Fatalf("unsafe shell allowance %q", command)
		}
	}
	systemPrompt, err := os.ReadFile(filepath.Join(spec.WorkingDirectory, "system.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"supported git shell shapes are exactly", "whitespace-free", "intentionally unsupported"} {
		if !strings.Contains(string(systemPrompt), phrase) {
			t.Fatalf("Kiro shell policy does not document %q:\n%s", phrase, systemPrompt)
		}
	}
}

func TestReviewCommandPreflightDoesNotCreateConfig(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "kiro-cli", nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if spec.WorkingDirectory != "" || spec.InitialMessage != "" {
		t.Fatalf("preflight spec = %+v", spec)
	}
}

func TestReviewMessageAndCancelUseInteractiveTUI(t *testing.T) {
	r := &Reviewer{}
	msg, err := r.ReviewMessage(context.Background(), ports.ReviewInvocation{Prompt: "next task file"})
	if err != nil || msg != "next task file" {
		t.Fatalf("ReviewMessage = %q, %v", msg, err)
	}
	cancel, err := r.ReviewCancel(context.Background())
	if err != nil || cancel.Mode != ports.ReviewCancelInput || cancel.Input != "\x1b" {
		t.Fatalf("ReviewCancel = %+v, %v", cancel, err)
	}
}

// Kiro admits a command when an allowedCommands regex matches it and no
// deniedCommands regex does. Review text reaches the submit commands through a
// quoted heredoc, so the body may hold any character, but no body line may end
// the heredoc early and nothing may follow the closing delimiter.
func TestShellPolicyAdmitsHeredocSubmitsOnly(t *testing.T) {
	inv := ports.ReviewInvocation{WorkerSessionID: "worker-1", WorkspacePath: "/worktrees/worker-1"}
	var allowed []*regexp.Regexp
	for _, pattern := range shellAllowedCommands(inv) {
		allowed = append(allowed, regexp.MustCompile(pattern))
	}
	denied := regexp.MustCompile(shellDeniedCommand)
	admits := func(command string) bool {
		if denied.MatchString(command) {
			return false
		}
		for _, re := range allowed {
			if re.MatchString(command) {
				return true
			}
		}
		return false
	}
	const body = "## Review\nThe worker's check doesn't run: a > b | c; $(d) `e` < f\n> quoted\nAO_REVIEW_EOF is mentioned here\nAO_REVIEW_EO\n"
	for _, command := range []string{
		"gh api --method POST repos/o/r/pulls/1/reviews --input - --jq '.id' <<'AO_REVIEW_EOF'\n{ \"event\": \"COMMENT\", \"body\": \"it's a > b\" }\nAO_REVIEW_EOF",
		"ao review submit --session worker-1 --run run-1 --verdict changes_requested --review-id 42 --body - <<'AO_REVIEW_EOF'\n" + body + "AO_REVIEW_EOF",
		"ao review submit --session worker-1 --run run-1 --verdict approved --body - <<'AO_REVIEW_EOF'\nok\nAO_REVIEW_EOF\n",
		"ao review submit --session worker-1 --reviews - <<'AO_REVIEW_EOF'\n{ \"reviews\": [] }\nAO_REVIEW_EOF",
		"git --no-pager -C /worktrees/worker-1 status --short",
	} {
		if !admits(command) {
			t.Errorf("submit command was denied: %q", command)
		}
	}
	for _, command := range []string{
		"ao review submit --session worker-1 --run run-1 --verdict approved --body - <<'AO_REVIEW_EOF'\nok\nAO_REVIEW_EOF\ntouch /tmp/pwned\nAO_REVIEW_EOF",
		"ao review submit --session worker-1 --run run-1 --verdict approved --body - <<'AO_REVIEW_EOF'\nok\nAO_REVIEW_EOF\ntouch /tmp/pwned",
		"ao review submit --session worker-1 --run run-1 --verdict approved --body - <<AO_REVIEW_EOF\n$(id)\nAO_REVIEW_EOF",
		"ao review submit --session worker-1 --run run-1 --verdict approved --body - > f <<'AO_REVIEW_EOF'\nok\nAO_REVIEW_EOF",
		"ao review submit --session worker-2 --run run-1 --verdict approved --body - <<'AO_REVIEW_EOF'\nok\nAO_REVIEW_EOF",
		"gh api --method PUT repos/o/r/pulls/1/merge --input - <<'AO_REVIEW_EOF'\n{}\nAO_REVIEW_EOF",
		"printf '%s' '{}' | ao review submit --session worker-1 --reviews -",
		"git --no-pager -C /worktrees/worker-1 status > f",
	} {
		if admits(command) {
			t.Errorf("command was admitted: %q", command)
		}
	}
}
