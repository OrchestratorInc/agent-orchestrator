package agentauth

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

type ExecutableFinder interface {
	LookPath(string) (string, error)
}

type AgentBinaryResolver interface {
	ResolveAgentBinary(context.Context, string) (string, error)
}

type executableFinderFunc func(string) (string, error)

func (f executableFinderFunc) LookPath(name string) (string, error) { return f(name) }

type Action string

const (
	ActionLogin        Action = "login"
	ActionLogout       Action = "logout"
	ActionSetup        Action = "setup"
	ActionInstructions Action = "instructions"
)

type LaunchMode string

const (
	LaunchTerminal      LaunchMode = "terminal"
	LaunchDocumentation LaunchMode = "documentation"
)

type Plan struct {
	AgentID                        string     `json:"agentId"`
	Action                         Action     `json:"action"`
	LaunchMode                     LaunchMode `json:"launchMode" enum:"terminal,documentation"`
	Available                      bool       `json:"available"`
	LogoutCommand                  string     `json:"logoutCommand,omitempty"`
	logoutCommand                  []string
	DisplayCommand                 string        `json:"displayCommand,omitempty"`
	Guidance                       string        `json:"guidance,omitempty"`
	DocumentationURL               string        `json:"documentationUrl"`
	Reason                         string        `json:"reason,omitempty"`
	Methods                        []LoginMethod `json:"methods,omitempty"`
	command                        []string
	title                          string
	terminalInput                  string
	initialInput                   string
	initialInputReadyStates        []shellterm.InitialInputReadyState
	sendInitialInputOnReadyTimeout bool
	prepareWorkspace               func(context.Context, string) error
	launcher                       string
	launcherArgs                   []string
	hiddenTerminal                 bool
}

type TerminalOpener interface {
	OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error)
	IsShellTerminalChildAlive(context.Context, string) (bool, error)
	CommandTerminalHandles(context.Context, string) ([]string, error)
}

type StartResult struct {
	AgentID       string                  `json:"agentId"`
	Action        Action                  `json:"action"`
	Guidance      string                  `json:"guidance,omitempty"`
	TerminalInput string                  `json:"terminalInput,omitempty"`
	Terminal      shellterm.ShellTerminal `json:"terminal"`
}

type Service struct {
	executables    ExecutableFinder
	agents         AgentBinaryResolver
	terminals      TerminalOpener
	dataDir        string
	selfExecutable func() (string, error)
	mu             sync.Mutex
	keysMu         sync.Mutex
	operations     map[string]*loginOperation
	terminalStarts map[string]bool
	closed         bool
	commandContext func(context.Context, string, ...string) *exec.Cmd
	loginTimeout   time.Duration
}

func New(executables ExecutableFinder, terminals TerminalOpener) *Service {
	return NewWithAgentResolver(executables, nil, terminals, "")
}

func NewWithAgentResolver(executables ExecutableFinder, agents AgentBinaryResolver, terminals TerminalOpener, dataDir string) *Service {
	republish := map[string]string{}
	if keys, err := providerkeys.Load(dataDir); err == nil {
		device, _ := providerkeys.DeviceNames(dataDir)
		users, _ := providerkeys.UserValueNames(dataDir)
		for name, value := range keys {
			var published []string
			if device[name] && !users[name] {
				published = []string{value}
			}
			_ = providerkeys.ExportForProbes(name, value, published...)
			if device[name] && !users[name] && deviceSupported {
				republish[name] = value
			}
		}
	}
	s := &Service{executables: executables, agents: agents, terminals: terminals, dataDir: dataDir, selfExecutable: os.Executable, operations: make(map[string]*loginOperation), terminalStarts: make(map[string]bool), commandContext: process.CommandContext, loginTimeout: loginLifetime}
	if len(republish) > 0 {
		go s.republishProviderKeys(republish)
	}
	return s
}

func (s *Service) republishProviderKeys(republish map[string]string) {
	for name, value := range republish {
		s.keysMu.Lock()
		keys, err := providerkeys.Load(s.dataDir)
		device, deviceErr := providerkeys.DeviceNames(s.dataDir)
		users, usersErr := providerkeys.UserValueNames(s.dataDir)
		if err == nil && deviceErr == nil && usersErr == nil && keys[name] == value && device[name] && !users[name] {
			refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = publishProviderKey(refreshCtx, name, value, value)
			cancel()
		}
		s.keysMu.Unlock()
	}
}

func (s *Service) Plans(ctx context.Context) []Plan {
	out := make([]Plan, 0, len(plans))
	for _, plan := range plans {
		out = append(out, s.resolve(ctx, plan))
	}
	return out
}

func (s *Service) Plan(ctx context.Context, agentID string) (Plan, error) {
	plan, ok := planByAgentID[agentID]
	if !ok {
		return Plan{}, apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", fmt.Sprintf("unknown agent authentication target %q", agentID), nil)
	}
	return s.resolve(ctx, plan), nil
}

func (s *Service) Start(ctx context.Context, agentID string) (StartResult, error) {
	plan, ok := planByAgentID[agentID]
	if !ok {
		return StartResult{}, apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", fmt.Sprintf("unknown agent authentication target %q", agentID), nil)
	}
	return s.start(ctx, plan)
}

func (s *Service) Logout(ctx context.Context, agentID string) (StartResult, error) {
	plan, ok := planByAgentID[agentID]
	if !ok {
		return StartResult{}, apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", fmt.Sprintf("unknown agent authentication target %q", agentID), nil)
	}
	if len(plan.logoutCommand) == 0 {
		return StartResult{}, apierr.Invalid("AGENT_LOGOUT_UNSUPPORTED", "This harness does not have a supported native logout command.", nil)
	}
	plan.command = nativeLogoutCommand(agentID, plan.logoutCommand)
	plan.Action = ActionLogout
	plan.title = strings.Replace(plan.title, "Log in to", "Log out of", 1)
	plan.Guidance = "Complete the harness's native logout flow, then close the terminal to check authentication again."
	plan.launcher, plan.terminalInput, plan.initialInput = "", "", ""
	plan.launcherArgs, plan.initialInputReadyStates = nil, nil
	plan.prepareWorkspace = nil
	return s.start(ctx, plan)
}

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
	s.terminalStarts[agentID] = true
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		delete(s.terminalStarts, agentID)
		s.mu.Unlock()
	}
	if s.terminals == nil {
		return release, nil
	}
	handles, err := s.terminals.CommandTerminalHandles(ctx, "agentauth:"+agentID)
	if err != nil {
		release()
		return nil, apierr.Unavailable("AGENT_AUTH_TERMINAL_UNKNOWN", "The terminal logins could not be checked. Close them and retry.")
	}
	for _, handleID := range handles {
		alive, err := s.terminals.IsShellTerminalChildAlive(ctx, handleID)
		if err != nil {
			release()
			return nil, apierr.Unavailable("AGENT_AUTH_TERMINAL_UNKNOWN", "The terminal login could not be checked. Close it and retry.")
		}
		if alive {
			release()
			return nil, apierr.Conflict("AGENT_AUTH_IN_PROGRESS", "Close the terminal login before starting another login.", nil)
		}
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
		Owner:                          "agentauth:" + plan.AgentID,
		Argv:                           argv,
		HideFromList:                   plan.hiddenTerminal,
		Title:                          plan.title,
		InitialInput:                   plan.initialInput,
		InitialInputReadyStates:        plan.initialInputReadyStates,
		SendInitialInputOnReadyTimeout: plan.sendInitialInputOnReadyTimeout,
	}
	env := providerkeys.WithheldForSignIn(plan.AgentID)
	if plan.AgentID == "claude-code" && s.dataDir != "" {
		env["AO_DATA_DIR"] = s.dataDir
	}
	if plan.AgentID == "opencode-v2" {
		dataHome, err := opencodev2.DataHome(ctx)
		if err != nil {
			return StartResult{}, apierr.Internal("AGENT_AUTH_DATA_HOME_UNAVAILABLE", err.Error())
		}
		env["XDG_DATA_HOME"] = dataHome
	}
	if len(env) > 0 {
		input.Env = env
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
	return StartResult{
		AgentID:       plan.AgentID,
		Action:        plan.Action,
		Guidance:      plan.Guidance,
		TerminalInput: plan.terminalInput,
		Terminal:      terminal,
	}, nil
}

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
