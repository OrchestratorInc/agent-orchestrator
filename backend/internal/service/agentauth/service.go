// Package agentauth owns the fixed, daemon-trusted authentication plans for
// supported harnesses. Clients select an agent and a reviewed login method;
// commands stay private, and optional credentials never appear in API results.
package agentauth

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

// ExecutableFinder resolves an executable on the host PATH.
type ExecutableFinder interface {
	LookPath(string) (string, error)
}

// AgentBinaryResolver resolves an agent through the same adapter-aware search
// used by normal session startup, including managed install locations outside
// the daemon PATH.
type AgentBinaryResolver interface {
	ResolveAgentBinary(context.Context, string) (string, error)
}

type executableFinderFunc func(string) (string, error)

func (f executableFinderFunc) LookPath(name string) (string, error) { return f(name) }

// Action describes the native authentication action a plan offers.
type Action string

const (
	// ActionLogin opens an agent's native login flow.
	ActionLogin Action = "login"
	// ActionLogout opens an agent's native logout flow.
	ActionLogout Action = "logout"
	// ActionSetup opens an agent's native provider/setup flow.
	ActionSetup Action = "setup"
	// ActionInstructions points the user to agent-owned setup documentation.
	ActionInstructions Action = "instructions"
)

// LaunchMode describes how the user enters an agent-owned authentication or
// provider setup flow.
type LaunchMode string

const (
	// LaunchTerminal opens a daemon-owned terminal running a reviewed command.
	LaunchTerminal LaunchMode = "terminal"
	// LaunchDocumentation opens the agent's official setup documentation.
	LaunchDocumentation LaunchMode = "documentation"
)

// Plan is the display-safe authentication plan for one harness. Trusted
// command and terminal details remain private to this package.
type Plan struct {
	AgentID    string     `json:"agentId"`
	Action     Action     `json:"action"`
	LaunchMode LaunchMode `json:"launchMode" enum:"terminal,documentation"`
	Available  bool       `json:"available"`
	// LogoutCommand is empty when native logout is unsupported. It is display
	// text only; clients cannot supply or override the private command.
	LogoutCommand    string `json:"logoutCommand,omitempty"`
	logoutCommand    []string
	DisplayCommand   string `json:"displayCommand,omitempty"`
	Guidance         string `json:"guidance,omitempty"`
	DocumentationURL string `json:"documentationUrl"`
	Reason           string `json:"reason,omitempty"`
	// Methods lists the login methods supported by the background UI flow.
	Methods       []LoginMethod `json:"methods,omitempty"`
	command       []string
	title         string
	terminalInput string
	// initialInput and initialInputReadyStates, when set, make the daemon inject
	// the reviewed input automatically once the terminal renders a known ready
	// state, instead of waiting for the user to trigger terminalInput.
	initialInput            string
	initialInputReadyStates []shellterm.InitialInputReadyState
	// sendInitialInputOnReadyTimeout delivers initialInput even when no ready
	// state appears in time, for harnesses whose input is safe once started.
	sendInitialInputOnReadyTimeout bool
	// prepareWorkspace, when set, runs reviewed harness-specific setup against
	// the plan's stable auth workspace before the terminal launches (for
	// example pre-recording workspace trust so a first-run dialog cannot
	// swallow the login flow).
	prepareWorkspace func(context.Context, string) error
	launcher         string
	launcherArgs     []string
	hiddenTerminal   bool
}

// TerminalOpener opens the daemon-trusted terminal used for a native
// authentication command.
type TerminalOpener interface {
	OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error)
	IsShellTerminalChildAlive(context.Context, string) (bool, error)
}

// StartResult is the display-safe result of starting a native authentication
// flow. Command arguments remain private to the resolved plan.
type StartResult struct {
	AgentID       string                  `json:"agentId"`
	Action        Action                  `json:"action"`
	Guidance      string                  `json:"guidance,omitempty"`
	TerminalInput string                  `json:"terminalInput,omitempty"`
	Terminal      shellterm.ShellTerminal `json:"terminal"`
}

// Service resolves the fixed authentication registry through AO's registered
// harness adapters, with direct PATH lookup only for callers without one.
// dataDir roots the stable per-harness auth workspaces used by plans with a
// prepareWorkspace hook.
type Service struct {
	executables    ExecutableFinder
	agents         AgentBinaryResolver
	terminals      TerminalOpener
	dataDir        string
	selfExecutable func() (string, error)
	mu             sync.Mutex
	operations     map[string]*loginOperation
	terminalStarts map[string]bool
	terminalLogins map[string]string
	closed         bool
	commandContext func(context.Context, string, ...string) *exec.Cmd
	loginTimeout   time.Duration
}

// New creates an authentication-plan service.
func New(executables ExecutableFinder, terminals TerminalOpener) *Service {
	return NewWithAgentResolver(executables, nil, terminals, "")
}

// NewWithAgentResolver creates a service that uses AO's adapter-aware binary
// resolver as the authoritative validation and discovery boundary.
func NewWithAgentResolver(executables ExecutableFinder, agents AgentBinaryResolver, terminals TerminalOpener, dataDir string) *Service {
	// Restore AO-managed provider variables before adapter authentication probes
	// and model discovery run after a daemon restart.
	if keys, err := providerkeys.Load(dataDir); err == nil {
		device, _ := providerkeys.DeviceNames(dataDir)
		republish := map[string]string{}
		for name, value := range keys {
			_ = os.Setenv(name, value)
			if device[name] && runtime.GOOS != "windows" {
				republish[name] = value
			}
		}
		// launchd and systemd user environments are session-scoped. Restore
		// the keys the user published when AO starts again after a login or
		// daemon restart, in the background so a slow or missing user bus
		// never delays daemon startup.
		if len(republish) > 0 {
			go func() {
				for name, value := range republish {
					refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					_ = publishProviderKey(refreshCtx, name, value)
					cancel()
				}
			}()
		}
	}
	return &Service{executables: executables, agents: agents, terminals: terminals, dataDir: dataDir, selfExecutable: os.Executable, operations: make(map[string]*loginOperation), terminalStarts: make(map[string]bool), terminalLogins: make(map[string]string), commandContext: process.CommandContext, loginTimeout: loginLifetime}
}

// Plans returns every known harness plan in stable Harness settings order.
func (s *Service) Plans(ctx context.Context) []Plan {
	out := make([]Plan, 0, len(plans))
	for _, plan := range plans {
		out = append(out, s.resolve(ctx, plan))
	}
	return out
}

// Plan returns the resolved plan for agentID.
func (s *Service) Plan(ctx context.Context, agentID string) (Plan, error) {
	plan, ok := planByAgentID[agentID]
	if !ok {
		return Plan{}, apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", fmt.Sprintf("unknown agent authentication target %q", agentID), nil)
	}
	return s.resolve(ctx, plan), nil
}

// Start opens the reviewed native authentication flow for agentID. Callers
// choose only the registry key; command arguments come exclusively from the
// resolved private plan fields. Interactive slash commands are either returned
// as a fixed, reviewed action that the user explicitly triggers after the TUI
// starts, or — for plans with initialInput — injected by the daemon
// automatically once the terminal renders a reviewed ready state.
func (s *Service) Start(ctx context.Context, agentID string) (StartResult, error) {
	plan, ok := planByAgentID[agentID]
	if !ok {
		return StartResult{}, apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", fmt.Sprintf("unknown agent authentication target %q", agentID), nil)
	}
	return s.start(ctx, plan)
}

// Logout opens the fixed native logout flow using the binary sessions launch.
func (s *Service) Logout(ctx context.Context, agentID string) (StartResult, error) {
	plan, ok := planByAgentID[agentID]
	if !ok {
		return StartResult{}, apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", fmt.Sprintf("unknown agent authentication target %q", agentID), nil)
	}
	if len(plan.logoutCommand) == 0 {
		return StartResult{}, apierr.Invalid("AGENT_LOGOUT_UNSUPPORTED", "This harness does not have a supported native logout command.", nil)
	}
	plan.command = plan.logoutCommand
	plan.Action = ActionLogout
	plan.title = strings.Replace(plan.title, "Log in to", "Log out of", 1)
	plan.Guidance = "Complete the harness's native logout flow, then close the terminal to check authentication again."
	plan.launcher, plan.terminalInput, plan.initialInput = "", "", ""
	plan.launcherArgs, plan.initialInputReadyStates = nil, nil
	plan.prepareWorkspace = nil
	return s.start(ctx, plan)
}

// reserveLogin excludes both startup and live terminal commands. Runtime probes
// run outside the registry lock; an inconclusive probe never releases exclusion.
func (s *Service) reserveLogin(ctx context.Context, agentID string) (func(), error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "Authentication service is stopping.")
	}
	if s.terminalStarts[agentID] {
		s.mu.Unlock()
		return nil, apierr.Conflict("AGENT_AUTH_IN_PROGRESS", "Authentication is starting or stopping.", nil)
	}
	for _, op := range s.operations {
		if op.snapshot.AgentID != agentID {
			continue
		}
		if op.cleanupFailed {
			s.mu.Unlock()
			return nil, apierr.Conflict("AGENT_AUTH_CLEANUP_PENDING", "The previous native login terminal could not be stopped. Restart AO before retrying.", nil)
		}
		select {
		case <-op.done:
		default:
			s.mu.Unlock()
			return nil, apierr.Conflict("AGENT_AUTH_IN_PROGRESS", "A login is already in progress for this harness.", map[string]any{"operationId": op.snapshot.ID})
		}
	}
	// Reserve this harness's startup, but never hold the output/polling lock
	// across binary resolution, workspace preparation, or terminal startup I/O.
	s.terminalStarts[agentID] = true
	handleID := s.terminalLogins[agentID]
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		delete(s.terminalStarts, agentID)
		s.mu.Unlock()
	}
	if handleID != "" {
		alive, err := s.terminals.IsShellTerminalChildAlive(ctx, handleID)
		if err != nil {
			release()
			return nil, apierr.Unavailable("AGENT_AUTH_TERMINAL_UNKNOWN", "The terminal login could not be checked. Close it and retry.")
		}
		if alive {
			release()
			return nil, apierr.Conflict("AGENT_AUTH_IN_PROGRESS", "Close the terminal login before starting another login.", nil)
		}
		s.mu.Lock()
		delete(s.terminalLogins, agentID)
		s.mu.Unlock()
	}
	return release, nil
}

func (s *Service) start(ctx context.Context, plan Plan) (StartResult, error) {
	release, err := s.reserveLogin(ctx, plan.AgentID)
	if err != nil {
		return StartResult{}, err
	}
	defer release()
	return s.startReserved(ctx, plan)
}

// startReserved launches only after reserveLogin excludes other login flows.
func (s *Service) startReserved(ctx context.Context, plan Plan) (StartResult, error) {
	plan = s.resolve(ctx, plan)
	if !plan.Available {
		return StartResult{}, apierr.Invalid("AGENT_AUTH_UNAVAILABLE", plan.Reason, nil)
	}
	if plan.Action == ActionInstructions {
		return StartResult{}, apierr.Invalid("AGENT_AUTH_INSTRUCTIONS_ONLY", "This authentication target provides instructions only.", nil)
	}
	if plan.LaunchMode == LaunchDocumentation {
		return StartResult{}, apierr.Invalid("AGENT_AUTH_DOCUMENTATION_ONLY", "This setup target opens the agent's documentation instead of a terminal.", nil)
	}
	if s.terminals == nil {
		return StartResult{}, apierr.Internal("AGENT_AUTH_TERMINAL_UNAVAILABLE", "Authentication terminal service is unavailable.")
	}
	argv := plan.command
	if plan.launcher != "" {
		self, err := s.selfExecutable()
		if err != nil || strings.TrimSpace(self) == "" {
			return StartResult{}, apierr.Internal("AGENT_AUTH_TERMINAL_UNAVAILABLE", "Authentication login menu is unavailable.")
		}
		argv = append([]string{self, plan.launcher, "--executable", plan.command[0]}, plan.launcherArgs...)
	}
	input := shellterm.OpenCommandTerminalInput{
		Argv:                           argv,
		HideFromList:                   plan.hiddenTerminal,
		Title:                          plan.title,
		InitialInput:                   plan.initialInput,
		InitialInputReadyStates:        plan.initialInputReadyStates,
		SendInitialInputOnReadyTimeout: plan.sendInitialInputOnReadyTimeout,
	}
	if plan.AgentID == "opencode-v2" {
		dataHome, err := opencodev2.DataHome(ctx)
		if err != nil {
			return StartResult{}, apierr.Internal("AGENT_AUTH_DATA_HOME_UNAVAILABLE", err.Error())
		}
		input.Env = map[string]string{"XDG_DATA_HOME": dataHome}
	}
	if plan.prepareWorkspace != nil {
		workingDir, err := s.prepareAuthWorkspace(ctx, plan)
		if err != nil {
			return StartResult{}, err
		}
		input.WorkingDir = workingDir
	}
	terminal, err := s.terminals.OpenCommandTerminal(ctx, input)
	if err != nil {
		return StartResult{}, err
	}
	s.mu.Lock()
	s.terminalLogins[plan.AgentID] = terminal.HandleID
	s.mu.Unlock()
	return StartResult{
		AgentID:       plan.AgentID,
		Action:        plan.Action,
		Guidance:      plan.Guidance,
		TerminalInput: plan.terminalInput,
		Terminal:      terminal,
	}, nil
}

// authWorkspaceRootName mirrors shellterm's auth-workspace root so all
// daemon-owned authentication terminals live under one directory; plans with a
// prepareWorkspace hook get a stable per-harness subdirectory instead of a
// throwaway per-handle one, so harness state the hook seeds (for example
// Kimi's workspace-trust record) persists across login attempts.
const authWorkspaceRootName = "auth-workspace"

func (s *Service) prepareAuthWorkspace(ctx context.Context, plan Plan) (string, error) {
	if strings.TrimSpace(s.dataDir) == "" {
		return "", apierr.Internal("AGENT_AUTH_WORKSPACE_UNAVAILABLE", "Authentication workspace is unavailable.")
	}
	dir := filepath.Join(s.dataDir, authWorkspaceRootName, plan.AgentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create authentication workspace: %w", err)
	}
	if err := plan.prepareWorkspace(ctx, dir); err != nil {
		return "", fmt.Errorf("prepare %s authentication workspace: %w", plan.AgentID, err)
	}
	return dir, nil
}

func (s *Service) resolve(ctx context.Context, plan Plan) Plan {
	plan.Methods = loginMethods(plan.AgentID)
	plan.command = append([]string(nil), plan.command...)
	if len(plan.command) == 0 {
		plan.Available = true
		return plan
	}
	var path string
	var err error
	if s.agents != nil {
		path, err = s.agents.ResolveAgentBinary(ctx, plan.AgentID)
	} else if s.executables != nil {
		path, err = s.executables.LookPath(plan.command[0])
	}
	if err != nil || path == "" {
		plan.Reason = fmt.Sprintf("%s was not found on PATH.", plan.command[0])
		return plan
	}
	plan.command[0] = path
	plan.Available = true
	return plan
}
