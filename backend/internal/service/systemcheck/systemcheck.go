package systemcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/tmuxbin"
)

type Requirement struct {
	ID        string `json:"id" enum:"git,tmux,harness,gh,github-auth" description:"Stable requirement identifier."`
	Label     string `json:"label" description:"Human-readable requirement name."`
	Satisfied bool   `json:"satisfied" description:"Whether this requirement is currently met."`
	Required  bool   `json:"required" description:"Whether this requirement blocks the overall Ready state."`
	Detail    string `json:"detail,omitempty" description:"Extra context: the resolved path when satisfied, or why it is not."`
}

type Report struct {
	Ready        bool          `json:"ready" description:"True iff every requirement with Required=true is satisfied. Requirements with Required=false (e.g. gh) are advisory and never block readiness."`
	Requirements []Requirement `json:"requirements" description:"Individual checks in stable order for the selected probe."`
}

type HarnessCatalog interface {
	RefreshFresh(ctx context.Context) (agentsvc.Inventory, error)
	FindInstalledBinary(ctx context.Context) (agentsvc.Info, bool)
}

type GitHubAuthTerminalOpener interface {
	OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error)
}

type Service struct {
	harnesses   HarnessCatalog
	executables ports.ExecutableFinder
	commands    ports.CommandRunner
	terminals   GitHubAuthTerminalOpener
}

func (s *Service) SetGitHubAuthTerminalOpener(terminals GitHubAuthTerminalOpener) {
	s.terminals = terminals
}

func New(harnesses HarnessCatalog, executables ports.ExecutableFinder) *Service {
	return &Service{harnesses: harnesses, executables: executables}
}

func NewWithCommandRunner(harnesses HarnessCatalog, executables ports.ExecutableFinder, commands ports.CommandRunner) *Service {
	return &Service{harnesses: harnesses, executables: executables, commands: commands}
}

type executableFinderFunc func(string) (string, error)

func (f executableFinderFunc) LookPath(file string) (string, error) { return f(file) }

func NewWithLookPath(harnesses HarnessCatalog, lookPath func(string) (string, error)) *Service {
	return New(harnesses, executableFinderFunc(lookPath))
}

func (s *Service) CheckStartup(ctx context.Context) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return reportFor([]Requirement{
		s.checkGit(),
		s.checkTmux(),
		s.checkStartupHarness(ctx),
		s.checkGH(),
	}), nil
}

func (s *Service) CheckGitHubAuth(ctx context.Context) (Requirement, error) {
	if err := ctx.Err(); err != nil {
		return Requirement{}, err
	}
	return s.checkGitHubAuth(ctx), nil
}

func (s *Service) OpenGitHubAuthTerminal(ctx context.Context) (shellterm.ShellTerminal, error) {
	if err := ctx.Err(); err != nil {
		return shellterm.ShellTerminal{}, err
	}
	path, err := s.executables.LookPath("gh")
	if err != nil || path == "" {
		return shellterm.ShellTerminal{}, apierr.Invalid("GITHUB_CLI_UNAVAILABLE", "GitHub CLI was not found on PATH.", nil)
	}
	if s.terminals == nil {
		return shellterm.ShellTerminal{}, apierr.Internal("GITHUB_AUTH_TERMINAL_UNAVAILABLE", "GitHub authentication terminal service is unavailable.")
	}
	return s.terminals.OpenCommandTerminal(ctx, shellterm.OpenCommandTerminalInput{
		Owner: "github-auth",
		Argv:  []string{path, "auth", "login"},
		Title: "Connect GitHub",
	})
}

func (s *Service) Check(ctx context.Context) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	requirements := []Requirement{
		s.checkGit(),
		s.checkTmux(),
		s.checkHarness(ctx),
		s.checkGH(),
		s.checkGitHubAuth(ctx),
	}

	return reportFor(requirements), nil
}

func reportFor(requirements []Requirement) Report {
	ready := true
	for _, req := range requirements {
		if req.Required && !req.Satisfied {
			ready = false
			break
		}
	}
	return Report{Ready: ready, Requirements: requirements}
}

func (s *Service) checkGit() Requirement {
	path, err := s.executables.LookPath("git")
	if err != nil || path == "" {
		return Requirement{ID: "git", Label: "git", Required: true, Detail: "git was not found on PATH."}
	}
	return Requirement{ID: "git", Label: "git", Satisfied: true, Required: true, Detail: path}
}

func (s *Service) checkTmux() Requirement {
	if runtime.GOOS == "windows" {
		return Requirement{
			ID: "tmux", Label: "tmux", Satisfied: true, Required: true,
			Detail: "Not required on Windows — Orchestrator uses the built-in ConPTY terminal runtime instead of tmux.",
		}
	}
	configured := strings.TrimSpace(os.Getenv("AO_TMUX_BINARY"))
	resolution, err := tmuxbin.ResolveWith(configured, os.Executable, s.executables.LookPath)
	if err != nil || resolution.Path == "" {
		detail := "tmux was not found on PATH; it is required on macOS/Linux to start sessions."
		if configured != "" {
			detail = "Orchestrator's bundled tmux is missing or not executable: " + configured
		}
		return Requirement{
			ID: "tmux", Label: "tmux", Required: true,
			Detail: detail,
		}
	}
	return Requirement{ID: "tmux", Label: "tmux", Satisfied: true, Required: true, Detail: resolution.Path}
}

func (s *Service) checkHarness(ctx context.Context) Requirement {
	const label = "agent harness"
	inv, err := s.harnesses.RefreshFresh(ctx)
	if err != nil {
		return Requirement{ID: "harness", Label: label, Required: true, Detail: err.Error()}
	}
	if len(inv.Installed) == 0 {
		return Requirement{
			ID: "harness", Label: label, Required: true,
			Detail: "No agent CLI (Claude Code, Codex, etc.) was found on PATH.",
		}
	}
	labels := make([]string, 0, len(inv.Installed))
	for _, info := range inv.Installed {
		labels = append(labels, info.Label)
	}
	return Requirement{ID: "harness", Label: label, Satisfied: true, Required: true, Detail: strings.Join(labels, ", ")}
}

func (s *Service) checkStartupHarness(ctx context.Context) Requirement {
	const label = "agent harness"
	info, ok := s.harnesses.FindInstalledBinary(ctx)
	if !ok {
		return Requirement{
			ID: "harness", Label: label, Required: true,
			Detail: "No agent CLI (Claude Code, Codex, etc.) was found on PATH or in a supported install location.",
		}
	}
	return Requirement{ID: "harness", Label: label, Satisfied: true, Required: true, Detail: info.Label}
}

func (s *Service) checkGH() Requirement {
	path, err := s.executables.LookPath("gh")
	if err != nil || path == "" {
		return Requirement{
			ID: "gh", Label: "gh",
			Detail: "gh was not found on PATH. It lets agent sessions open pull requests and read issues, but Orchestrator runs fine without it.",
		}
	}
	return Requirement{ID: "gh", Label: "gh", Satisfied: true, Detail: path}
}

func (s *Service) checkGitHubAuth(ctx context.Context) Requirement {
	const detail = "Sign in with `gh auth login` so agent sessions can open pull requests and read issues."
	path, err := s.executables.LookPath("gh")
	if err != nil || path == "" {
		return Requirement{ID: "github-auth", Label: "GitHub access", Detail: detail}
	}
	if s.commands == nil {
		return Requirement{ID: "github-auth", Label: "GitHub access", Detail: detail}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	err = s.commands.Run(probeCtx, []string{path, "auth", "status", "--active", "--json", "hosts"}, &stdout, io.Discard)
	var status struct {
		Hosts map[string][]struct {
			Active bool   `json:"active"`
			State  string `json:"state"`
		} `json:"hosts"`
	}
	if err == nil && json.Unmarshal(stdout.Bytes(), &status) == nil {
		if len(status.Hosts) == 0 {
			return Requirement{ID: "github-auth", Label: "GitHub access", Detail: detail}
		}
		for _, accounts := range status.Hosts {
			for _, account := range accounts {
				if account.Active && account.State == "success" {
					return Requirement{ID: "github-auth", Label: "GitHub access", Satisfied: true, Detail: "GitHub CLI is signed in."}
				}
			}
		}
	}
	if s.hasGitHubAuth(ctx, path) {
		return Requirement{ID: "github-auth", Label: "GitHub access", Satisfied: true, Detail: "GitHub CLI is signed in."}
	}
	return Requirement{ID: "github-auth", Label: "GitHub access", Detail: detail}
}

func (s *Service) hasGitHubAuth(ctx context.Context, path string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.commands.Run(probeCtx, []string{path, "auth", "token"}, io.Discard, io.Discard) == nil
}
