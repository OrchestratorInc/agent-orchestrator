package shellterm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
)

type ShellRuntime interface {
	ports.RuntimeChildInspector
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
	SendInput(ctx context.Context, handle ports.RuntimeHandle, input string) error
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}

type TerminalInputForgetter interface {
	ForgetTerminalInput(ctx context.Context, terminalID string) error
}

type ProjectRootLocator interface {
	ProjectRoot(ctx context.Context, id domain.ProjectID) (string, error)
	ProjectEnv(ctx context.Context, id domain.ProjectID) (map[string]string, error)
}

type SessionWorkspaceLocator interface {
	SessionWorkspace(ctx context.Context, id domain.SessionID) (workspacePath string, projectID domain.ProjectID, err error)
	CueCommandSessionTarget(ctx context.Context, id domain.SessionID) (CueCommandSessionTarget, error)
}

type Service struct {
	runtime             ShellRuntime
	store               Store
	projects            ProjectRootLocator
	sessions            SessionWorkspaceLocator
	dataDir             string
	appRunID            string
	log                 *slog.Logger
	previewCapabilities *browser.Authority
	pendingPreviewMu    sync.Mutex
	pendingPreview      map[domain.SessionID]string
	hiddenAuthMu        sync.RWMutex
	hiddenAuthTerminals map[string]struct{}

	inputForgetter TerminalInputForgetter

	now                 func() time.Time
	newHandleID         func() (string, error)
	executable          func() (string, error)
	initialInputTimeout time.Duration

	gatesMu           sync.Mutex
	gates             map[domain.SessionID]*sessionGate
	onSessionGateWait func(domain.SessionID)

	trackers []TerminalTracker
}

type TerminalTracker interface {
	TrackShellTerminal(handleID string)
	UntrackShellTerminal(handleID string)
}

func (s *Service) SetTerminalTrackers(ctx context.Context, trackers ...TerminalTracker) {
	s.trackers = trackers
	if len(trackers) == 0 {
		return
	}
	recs, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
	if err != nil {
		s.log.Warn("shell terminal tracking: list terminals failed", "err", err)
		return
	}
	for _, rec := range recs {
		if !rec.Transient {
			for _, tracker := range trackers {
				tracker.TrackShellTerminal(rec.HandleID)
			}
		}
	}
}

func (s *Service) IsShellTerminalChildAlive(ctx context.Context, handleID string) (bool, error) {
	return s.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: strings.TrimSpace(handleID)})
}

func (s *Service) CommandTerminalHandles(ctx context.Context, owner string) ([]string, error) {
	records, err := s.store.SelectShellTerminalsByAppRunID(ctx, s.appRunID)
	if err != nil {
		return nil, fmt.Errorf("list command terminals: %w", err)
	}
	var handles []string
	for _, rec := range records {
		if rec.Transient && (rec.CommandOwner == owner || rec.CommandOwner == "") {
			handles = append(handles, rec.HandleID)
		}
	}
	return handles, nil
}

type sessionGate struct {
	ch chan struct{}
}

func newSessionGate() *sessionGate {
	return &sessionGate{ch: make(chan struct{}, 1)}
}

var sessionGateWaitTimeout = 30 * time.Second

func (g *sessionGate) acquire(ctx context.Context, id domain.SessionID) (release func(), err error) {
	var once sync.Once
	releaseFn := func() { once.Do(func() { <-g.ch }) }

	select {
	case g.ch <- struct{}{}:
		return releaseFn, nil
	default:
	}

	timer := time.NewTimer(sessionGateWaitTimeout)
	defer timer.Stop()
	select {
	case g.ch <- struct{}{}:
		return releaseFn, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("shell terminal gate for session %s: %w", id, ctx.Err())
	case <-timer.C:
		return nil, apierr.Conflict("SHELL_TERMINAL_SESSION_BUSY",
			"This session's shell terminals are being torn down; try again in a moment", nil)
	}
}

func NewService(shellRuntime ShellRuntime, store Store, projects ProjectRootLocator, sessions SessionWorkspaceLocator, dataDir, appRunID string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		runtime:             shellRuntime,
		store:               store,
		projects:            projects,
		sessions:            sessions,
		dataDir:             dataDir,
		appRunID:            appRunID,
		log:                 log,
		previewCapabilities: browser.NewAuthority(),
		pendingPreview:      make(map[domain.SessionID]string),
		hiddenAuthTerminals: make(map[string]struct{}),
		now:                 time.Now,
		newHandleID:         newShellTerminalHandleID,
		executable:          agentlaunch.Executable,
		initialInputTimeout: initialInputTimeout,
		gates:               map[domain.SessionID]*sessionGate{},
	}
}

func (s *Service) SetTerminalInputForgetter(f TerminalInputForgetter) {
	s.inputForgetter = f
}

func (s *Service) forgetTerminalInput(ctx context.Context, handleID string) {
	if s.inputForgetter == nil {
		return
	}
	if err := s.inputForgetter.ForgetTerminalInput(ctx, handleID); err != nil {
		s.log.Warn("shell terminal: forget input timestamp failed", "handleId", handleID, "error", err)
	}
}

func (s *Service) pinnedEnv(projectEnv map[string]string) map[string]string {
	path, err := agentlaunch.PinnedPATH(s.executable, os.Getenv, projectEnv, s.dataDir)
	if err != nil {
		s.log.Warn("shell terminal PATH not pinned to the daemon binary; a bare `ao` may resolve to a different install", "err", err)
		return agentlaunch.MergeEnv(projectEnv, nil)
	}
	child := map[string]string{"PATH": path}
	loginEnv, err := agentlaunch.LoginShellEnv(path, os.Getenv, projectEnv, s.dataDir)
	if err != nil {
		s.log.Warn("shell terminal startup files may put another ao first on PATH", "err", err)
	}
	for key, value := range loginEnv {
		child[key] = value
	}
	return agentlaunch.MergeEnv(projectEnv, child)
}

func (s *Service) ValidPreviewCapability(ctx context.Context, sessionID domain.SessionID, token string) (bool, error) {
	if sessionID == "" || token == "" {
		return false, nil
	}
	s.pendingPreviewMu.Lock()
	pending := s.pendingPreview[sessionID]
	s.pendingPreviewMu.Unlock()
	if s.previewCapabilities.Valid(sessionID, token, pending) {
		return true, nil
	}
	recs, err := s.store.SelectShellTerminalsBySessionID(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("validate shell preview capability: %w", err)
	}
	for _, rec := range recs {
		if s.previewCapabilities.Valid(sessionID, token, rec.PreviewCapabilityVerifier) {
			alive, err := s.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: rec.HandleID})
			if err != nil {
				return false, fmt.Errorf("probe shell preview owner %s: %w", rec.HandleID, err)
			}
			return alive, nil
		}
	}
	return false, nil
}

func (s *Service) sessionGateFor(id domain.SessionID) *sessionGate {
	s.gatesMu.Lock()
	defer s.gatesMu.Unlock()
	g, ok := s.gates[id]
	if !ok {
		g = newSessionGate()
		s.gates[id] = g
	}
	return g
}

func (s *Service) acquireSessionGate(ctx context.Context, id domain.SessionID) (release func(), err error) {
	gate := s.sessionGateFor(id)
	if s.onSessionGateWait != nil {
		s.onSessionGateWait(id)
	}
	return gate.acquire(ctx, id)
}

func (s *Service) OpenShellTerminal(ctx context.Context, in OpenShellTerminalInput) (ShellTerminal, error) {
	title := strings.TrimSpace(in.Title)
	if utf8.RuneCountInString(title) > maxShellTerminalTitleLen {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_TITLE_TOO_LONG",
			fmt.Sprintf("A shell terminal title must be at most %d characters", maxShellTerminalTitleLen), nil)
	}
	titleExplicit := title != "" && !defaultShellTerminalTitle.MatchString(title)
	if in.SessionID != "" {
		if s.sessions == nil {
			return ShellTerminal{}, apierr.Internal("SHELL_TERMINAL_NO_SESSION_LOOKUP", "Session lookup is unavailable")
		}
		if _, _, err := s.sessions.SessionWorkspace(ctx, in.SessionID); err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: resolve session %s: %w", in.SessionID, err)
		}
		release, err := s.acquireSessionGate(ctx, in.SessionID)
		if err != nil {
			return ShellTerminal{}, err
		}
		defer release()
	}
	workingDir, projectID, err := s.resolveShellTerminalWorkingDir(ctx, in.ProjectID, in.SessionID)
	if err != nil {
		return ShellTerminal{}, err
	}
	var projectEnv map[string]string
	if projectID != "" {
		projectEnv, err = s.projects.ProjectEnv(ctx, projectID)
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: resolve project environment: %w", err)
		}
	}
	if title == "" {
		openTerminals, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: list existing terminals: %w", err)
		}
		title = nextShellTerminalTitle(openTerminals)
	}
	argv, usedFallback := resolveUserLoginShell(in.Shell)
	if usedFallback {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_SHELL_UNAVAILABLE",
			fmt.Sprintf("The selected shell is unavailable: %s. Choose another shell in Settings.", in.Shell), nil)
	}
	if len(argv) == 0 {
		return ShellTerminal{}, apierr.Internal("SHELL_TERMINAL_NO_SHELL",
			"Could not determine a shell to launch. Set SHELL (macOS/Linux) or ComSpec (Windows).")
	}
	env := s.pinnedEnv(projectEnv)
	if env == nil {
		env = make(map[string]string, 3)
	}
	env["AO_BROWSER_CAPABILITY"] = ""
	env["AO_PREVIEW_CAPABILITY"] = ""
	env[caller.ShellTerminalEnv] = "1"
	var verifier string
	if in.SessionID != "" {
		env["AO_SESSION_ID"] = string(in.SessionID)
		var token string
		token, verifier, err = s.previewCapabilities.Issue(in.SessionID)
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: preview capability: %w", err)
		}
		env["AO_PREVIEW_CAPABILITY"] = token
		s.pendingPreviewMu.Lock()
		s.pendingPreview[in.SessionID] = verifier
		s.pendingPreviewMu.Unlock()
		defer func() {
			s.pendingPreviewMu.Lock()
			delete(s.pendingPreview, in.SessionID)
			s.pendingPreviewMu.Unlock()
		}()
	}
	return s.openTerminal(ctx, openTerminalConfig{
		argv: argv, env: env, projectEnv: projectEnv, projectID: projectID, sessionID: in.SessionID,
		workingDir: workingDir, title: title, titleExplicit: titleExplicit,
		previewVerifier: verifier,
		startOnAttach:   in.StartOnAttach,
		cols:            in.Cols,
		rows:            in.Rows,
	})
}

const authWorkspaceDirectoryName = "auth-workspace"

func (s *Service) OpenCommandTerminal(ctx context.Context, in OpenCommandTerminalInput) (ShellTerminal, error) {
	if in.HideFromList {
		s.hiddenAuthMu.Lock()
		defer s.hiddenAuthMu.Unlock()
	}
	if err := validateOpenCommandTerminalInput(in); err != nil {
		return ShellTerminal{}, err
	}
	handleID, err := s.newHandleID()
	if err != nil {
		return ShellTerminal{}, fmt.Errorf("open command terminal: handle id: %w", err)
	}

	workingDir := in.WorkingDir
	cleanupWorkingDirOnError := false
	if workingDir == "" {
		authWorkspaceRoot := filepath.Join(s.dataDir, authWorkspaceDirectoryName)
		if err := os.MkdirAll(authWorkspaceRoot, 0o700); err != nil {
			return ShellTerminal{}, fmt.Errorf("open command terminal: create auth workspace: %w", err)
		}
		workingDir = filepath.Join(authWorkspaceRoot, handleID)
		if err := os.Mkdir(workingDir, 0o700); err != nil {
			return ShellTerminal{}, fmt.Errorf("open command terminal: create private auth workspace: %w", err)
		}
		cleanupWorkingDirOnError = true
	}
	terminal, err := s.openTerminal(ctx, openTerminalConfig{
		handleID:                 handleID,
		argv:                     in.Argv,
		env:                      in.Env,
		workingDir:               workingDir,
		title:                    in.Title,
		titleExplicit:            true,
		transient:                true,
		commandOwner:             in.Owner,
		cleanupWorkingDirOnError: cleanupWorkingDirOnError,
	})
	if err != nil {
		return ShellTerminal{}, err
	}
	if in.HideFromList {
		s.hiddenAuthTerminals[terminal.HandleID] = struct{}{}
	}
	if len(in.InitialInputReadyStates) > 0 {
		go s.sendInitialInputWhenReady(context.WithoutCancel(ctx), ports.RuntimeHandle{ID: terminal.HandleID}, in.InitialInput, in.InitialInputReadyStates, in.SendInitialInputOnReadyTimeout)
	}
	return terminal, nil
}

func (s *Service) RunCueCommand(ctx context.Context, in RunCueCommandInput) (ShellTerminal, error) {
	if err := ctx.Err(); err != nil {
		return ShellTerminal{}, err
	}
	if in.ProjectID == "" {
		return ShellTerminal{}, apierr.Invalid("CUE_COMMAND_PROJECT_REQUIRED", "A project is required to run a command Cue", nil)
	}
	if strings.TrimSpace(in.Command) == "" {
		return ShellTerminal{}, apierr.Invalid("CUE_COMMAND_REQUIRED", "A command is required to run a command Cue", nil)
	}
	workingDir, projectID, err := s.resolveCueCommandWorkingDir(ctx, in.ProjectID, in.SessionID)
	if err != nil {
		return ShellTerminal{}, err
	}
	if in.SessionID != "" {
		release, acquireErr := s.acquireSessionGate(ctx, in.SessionID)
		if acquireErr != nil {
			return ShellTerminal{}, acquireErr
		}
		defer release()
		workingDir, projectID, err = s.resolveCueCommandWorkingDir(ctx, in.ProjectID, in.SessionID)
		if err != nil {
			return ShellTerminal{}, err
		}
	}
	records, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
	if err != nil {
		return ShellTerminal{}, fmt.Errorf("run cue command: list terminals: %w", err)
	}
	argv, usedFallback := resolveUserLoginShell(in.Shell)
	if usedFallback {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_SHELL_UNAVAILABLE",
			fmt.Sprintf("The selected shell is unavailable: %s. Choose another shell in Settings.", in.Shell), nil)
	}
	if len(argv) == 0 {
		return ShellTerminal{}, apierr.Internal("SHELL_TERMINAL_NO_SHELL", "Could not determine a shell to launch. Set SHELL (macOS/Linux) or ComSpec (Windows).")
	}
	projectEnv, err := s.projects.ProjectEnv(ctx, projectID)
	if err != nil {
		return ShellTerminal{}, fmt.Errorf("run cue command: resolve project environment: %w", err)
	}
	env := s.pinnedEnv(projectEnv)
	readiness, err := prepareCueShellReadiness(s.dataDir, argv, env)
	if err != nil {
		return ShellTerminal{}, err
	}
	defer readiness.cleanup()
	readiness.env["AO_CUE_COMMAND"] = ""
	env = agentlaunch.MergeEnv(env, readiness.env)
	terminal, err := s.openTerminal(ctx, openTerminalConfig{argv: readiness.argv, env: env, projectEnv: projectEnv, projectID: projectID,
		sessionID: in.SessionID, workingDir: workingDir, title: nextShellTerminalTitle(records)})
	if err != nil {
		return ShellTerminal{}, err
	}
	if err := ctx.Err(); err != nil {
		return ShellTerminal{}, err
	}
	if err := s.waitForCueShellReady(ctx, ports.RuntimeHandle{ID: terminal.HandleID}, readiness.file); err != nil {
		return ShellTerminal{}, err
	}
	delivery, err := prepareCueCommand(s.dataDir, argv, in.Command)
	if err != nil {
		return ShellTerminal{}, err
	}
	defer delivery.cleanup()
	if err := s.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: terminal.HandleID}, delivery.input); err != nil {
		return ShellTerminal{}, fmt.Errorf("run cue command: send to terminal %s: %w", terminal.HandleID, err)
	}
	if err := waitForCueCommandConsumed(ctx, delivery.file); err != nil {
		return ShellTerminal{}, err
	}
	return terminal, nil
}

func (s *Service) resolveCueCommandWorkingDir(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID) (string, domain.ProjectID, error) {
	if sessionID == "" {
		root, err := s.resolveProjectRootOrDataDir(ctx, projectID)
		if err != nil {
			return "", "", err
		}
		if !filepath.IsAbs(root) {
			return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The project root is unavailable", nil)
		}
		info, statErr := os.Stat(root)
		if statErr != nil || !info.IsDir() {
			return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The project root is unavailable", nil)
		}
		return filepath.Clean(root), projectID, nil
	}
	if s.sessions == nil {
		return "", "", apierr.Internal("SHELL_TERMINAL_NO_SESSION_LOOKUP", "Session lookup is unavailable")
	}
	target, err := s.sessions.CueCommandSessionTarget(ctx, sessionID)
	if err != nil {
		return "", "", fmt.Errorf("run cue command: resolve session %s: %w", sessionID, err)
	}
	if target.ProjectID != projectID {
		return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The selected session belongs to another project", nil)
	}
	if target.IsTerminated || target.Activity == domain.ActivityExited || target.Activity == domain.ActivityBlocked {
		return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The selected session cannot run command Cues", nil)
	}
	workspace := strings.TrimSpace(target.WorkspacePath)
	if workspace == "" || !filepath.IsAbs(workspace) {
		return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The selected session worktree is unavailable", nil)
	}
	info, statErr := os.Stat(workspace)
	if statErr != nil || !info.IsDir() {
		return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The selected session worktree is unavailable", nil)
	}
	return filepath.Clean(workspace), target.ProjectID, nil
}

const (
	initialInputTimeout      = 10 * time.Second
	initialInputPollInterval = 50 * time.Millisecond
	initialInputOutputLines  = 100

	initialInputFallbackSendTimeout = 5 * time.Second
)

func MatchInitialInputReadyState(output string, readyStates []InitialInputReadyState) *InitialInputReadyState {
	for i := range readyStates {
		if strings.Contains(output, readyStates[i].Text) {
			return &readyStates[i]
		}
	}
	return nil
}

func (s *Service) initialInputReadyState(ctx context.Context, handle ports.RuntimeHandle, readyStates []InitialInputReadyState) *InitialInputReadyState {
	output, err := s.runtime.GetOutput(ctx, handle, initialInputOutputLines)
	if err != nil {
		return nil
	}
	ready := MatchInitialInputReadyState(output, readyStates)
	if styled, ok := s.runtime.(ports.StyledTerminalOutputReader); ok {
		if rendered, styledErr := styled.GetStyledOutput(ctx, handle, initialInputOutputLines); styledErr == nil {
			if renderedReady := MatchInitialInputReadyState(terminalui.PlainTerminalText(rendered), readyStates); renderedReady != nil {
				return renderedReady
			}
		}
	}
	return ready
}

func (s *Service) waitForInitialInputReadyState(ctx context.Context, handle ports.RuntimeHandle, readyStates []InitialInputReadyState) *InitialInputReadyState {
	ticker := time.NewTicker(initialInputPollInterval)
	defer ticker.Stop()
	for {
		if ready := s.initialInputReadyState(ctx, handle, readyStates); ready != nil {
			return ready
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Service) sendInitialInputAfterTimeout(handle ports.RuntimeHandle, input string) {
	ctx, cancel := context.WithTimeout(context.Background(), initialInputFallbackSendTimeout)
	defer cancel()
	alive, err := s.runtime.IsAlive(ctx, handle)
	if err != nil || !alive {
		s.log.Warn("authentication terminal exited before initial input", "handleId", handle.ID, "error", err)
		return
	}
	s.log.Info("authentication terminal showed no ready marker; sending initial input anyway", "handleId", handle.ID)
	if err := s.runtime.SendMessage(ctx, handle, input); err != nil {
		s.log.Warn("authentication terminal initial input failed", "handleId", handle.ID, "error", err)
	}
}

func (s *Service) sendInitialInputWhenReady(ctx context.Context, handle ports.RuntimeHandle, input string, readyStates []InitialInputReadyState, sendOnTimeout bool) {
	readyCtx, cancelReady := context.WithTimeout(ctx, s.initialInputTimeout)
	defer cancelReady()
	ready := s.waitForInitialInputReadyState(readyCtx, handle, readyStates)
	if ready == nil {
		if sendOnTimeout {
			s.sendInitialInputAfterTimeout(handle, input)
			return
		}
		s.log.Warn("authentication terminal did not become ready for initial input", "handleId", handle.ID)
		return
	}
	if ready.SkipInput {
		return
	}
	inputCtx := readyCtx
	if ready.RawPrefix != "" {
		if err := s.runtime.SendInput(inputCtx, handle, ready.RawPrefix); err != nil {
			s.log.Warn("authentication terminal initial input prefix failed", "handleId", handle.ID, "error", err)
			return
		}
	}
	if ready.SubmitReadyText != "" {
		completionCtx, cancelCompletion := context.WithTimeout(ctx, s.initialInputTimeout)
		defer cancelCompletion()
		inputCtx = completionCtx
		if s.waitForInitialInputReadyState(completionCtx, handle, []InitialInputReadyState{{Text: ready.SubmitReadyText}}) == nil {
			s.log.Warn("authentication terminal initial input completion did not appear", "handleId", handle.ID)
			return
		}
	}
	if err := s.runtime.SendMessage(inputCtx, handle, input); err != nil {
		s.log.Warn("authentication terminal initial input failed", "handleId", handle.ID, "error", err)
	}
}

type openTerminalConfig struct {
	handleID                 string
	argv                     []string
	env                      map[string]string
	projectEnv               map[string]string
	projectID                domain.ProjectID
	sessionID                domain.SessionID
	workingDir               string
	title                    string
	titleExplicit            bool
	transient                bool
	commandOwner             string
	cleanupWorkingDirOnError bool
	previewVerifier          string
	startOnAttach            bool
	cols, rows               int
}

func (s *Service) openTerminal(ctx context.Context, cfg openTerminalConfig) (ShellTerminal, error) {
	handleID := cfg.handleID
	if handleID == "" {
		var err error
		handleID, err = s.newHandleID()
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: handle id: %w", err)
		}
	}

	createdAt := s.now().UTC()
	handle, err := s.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:               domain.SessionID(handleID),
		WorkspacePath:           cfg.workingDir,
		Argv:                    cfg.argv,
		Env:                     cfg.env,
		ExitOnCommandCompletion: true,
		StartOnAttach:           cfg.startOnAttach,
		InitialCols:             clampGridDimension(cfg.cols),
		InitialRows:             clampGridDimension(cfg.rows),
		LazyStyledOutput:        true,
	})
	if err != nil {
		if cfg.cleanupWorkingDirOnError {
			s.cleanupAuthWorkspace(cfg.workingDir, handleID)
		}
		return ShellTerminal{}, fmt.Errorf("open shell terminal %s: runtime: %w", handleID, agentlaunch.RedactError(err, cfg.projectEnv))
	}

	rec := ShellTerminalRecord{
		HandleID:                  handle.ID,
		ProjectID:                 cfg.projectID,
		SessionID:                 cfg.sessionID,
		WorkingDir:                cfg.workingDir,
		Title:                     cfg.title,
		TitleExplicit:             cfg.titleExplicit,
		AppRunID:                  s.appRunID,
		Transient:                 cfg.transient,
		CommandOwner:              cfg.commandOwner,
		CreatedAt:                 createdAt,
		PreviewCapabilityVerifier: cfg.previewVerifier,
	}
	if err := s.store.InsertShellTerminal(ctx, rec); err != nil {
		stillAlive, destroyErr := s.destroyRuntimeConfirmed(context.WithoutCancel(ctx), handle)
		if stillAlive {
			destroyErr = agentlaunch.RedactError(destroyErr, cfg.projectEnv)
			s.log.Warn("shell terminal rollback failed; retaining workspace for live runtime",
				"handleId", handle.ID, "workingDir", cfg.workingDir, "error", destroyErr)
		} else if cfg.cleanupWorkingDirOnError {
			s.cleanupAuthWorkspace(cfg.workingDir, handle.ID)
		}
		return ShellTerminal{}, fmt.Errorf("open shell terminal %s: persist: %w", handle.ID, err)
	}
	s.log.Info("shell terminal opened", "handleId", handle.ID, "workingDir", cfg.workingDir)
	if !cfg.transient {
		for _, tracker := range s.trackers {
			tracker.TrackShellTerminal(handle.ID)
		}
	}
	return shellTerminalFromRecord(rec), nil
}

var defaultShellTerminalTitle = regexp.MustCompile(`^Terminal( \d+)?$`)

func nextShellTerminalTitle(terminals []ShellTerminalRecord) string {
	maxNumber := 0
	for _, terminal := range terminals {
		if terminal.Title == "Terminal" {
			if maxNumber < 1 {
				maxNumber = 1
			}
			continue
		}
		var number int
		if _, err := fmt.Sscanf(terminal.Title, "Terminal %d", &number); err == nil && number > maxNumber {
			maxNumber = number
		}
	}
	return fmt.Sprintf("Terminal %d", maxNumber+1)
}

const maxShellTerminalTitleLen = 80

func validateOpenCommandTerminalInput(in OpenCommandTerminalInput) error {
	if len(in.Argv) == 0 {
		return apierr.Invalid("SHELL_TERMINAL_COMMAND_REQUIRED", "A shell terminal command is required", nil)
	}
	if strings.TrimSpace(in.Title) == "" {
		return apierr.Invalid("SHELL_TERMINAL_TITLE_REQUIRED", "A shell terminal title is required", nil)
	}
	if utf8.RuneCountInString(in.Title) > maxShellTerminalTitleLen {
		return apierr.Invalid("SHELL_TERMINAL_TITLE_TOO_LONG",
			fmt.Sprintf("A shell terminal title must be at most %d characters", maxShellTerminalTitleLen), nil)
	}
	if in.InitialInput != "" && len(in.InitialInputReadyStates) == 0 {
		return apierr.Invalid("SHELL_TERMINAL_INITIAL_INPUT_READY_TEXT_REQUIRED", "A reviewed readiness marker is required for automatic terminal input", nil)
	}
	for _, state := range in.InitialInputReadyStates {
		if strings.TrimSpace(state.Text) == "" {
			return apierr.Invalid("SHELL_TERMINAL_INITIAL_INPUT_READY_TEXT_REQUIRED", "A reviewed readiness marker is required for automatic terminal input", nil)
		}
		if state.SubmitReadyText != "" && strings.TrimSpace(state.SubmitReadyText) == "" {
			return apierr.Invalid("SHELL_TERMINAL_INITIAL_INPUT_READY_TEXT_REQUIRED", "A reviewed submission marker must not be whitespace", nil)
		}
	}
	return nil
}

func (s *Service) RenameShellTerminal(ctx context.Context, handleID, title string) (ShellTerminal, error) {
	if handleID == "" {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_ID_REQUIRED", "A shell terminal id is required", nil)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_TITLE_REQUIRED", "A shell terminal title is required", nil)
	}
	if utf8.RuneCountInString(title) > maxShellTerminalTitleLen {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_TITLE_TOO_LONG",
			fmt.Sprintf("A shell terminal title must be at most %d characters", maxShellTerminalTitleLen), nil)
	}
	rec, found, err := s.store.UpdateShellTerminalTitle(ctx, handleID, title)
	if err != nil {
		return ShellTerminal{}, fmt.Errorf("rename shell terminal %s: %w", handleID, err)
	}
	if !found {
		return ShellTerminal{}, apierr.NotFound("SHELL_TERMINAL_NOT_FOUND", "No such shell terminal: "+handleID)
	}
	s.log.Info("shell terminal renamed", "handleId", handleID)
	return shellTerminalFromRecord(rec), nil
}

func (s *Service) CloseShellTerminal(ctx context.Context, handleID string) error {
	if handleID == "" {
		return apierr.Invalid("SHELL_TERMINAL_ID_REQUIRED", "A shell terminal id is required", nil)
	}
	rec, found, err := s.store.SelectShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return fmt.Errorf("close shell terminal %s: %w", handleID, err)
	}
	if !found {
		s.hiddenAuthMu.Lock()
		delete(s.hiddenAuthTerminals, handleID)
		s.hiddenAuthMu.Unlock()
		return apierr.NotFound("SHELL_TERMINAL_NOT_FOUND", "No such shell terminal: "+handleID)
	}

	if rec.SessionID != "" {
		release, err := s.acquireSessionGate(ctx, rec.SessionID)
		if err != nil {
			return err
		}
		defer release()
	}

	stillAlive, destroyErr := s.destroyConfirmed(ctx, rec)
	if stillAlive {
		s.log.Warn("close shell terminal: runtime still alive after destroy", "handleId", handleID, "error", destroyErr)
		return apierr.Conflict("SHELL_TERMINAL_STILL_RUNNING",
			"The shell process is still running; try closing it again in a moment", nil)
	}
	s.hiddenAuthMu.Lock()
	delete(s.hiddenAuthTerminals, handleID)
	s.hiddenAuthMu.Unlock()
	return nil
}

func (s *Service) ListShellTerminalsForCurrentAppRun(ctx context.Context) ([]ShellTerminal, error) {
	s.hiddenAuthMu.RLock()
	recs, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
	hidden := make(map[string]struct{}, len(s.hiddenAuthTerminals))
	for id := range s.hiddenAuthTerminals {
		hidden[id] = struct{}{}
	}
	s.hiddenAuthMu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("list shell terminals: %w", err)
	}
	out := make([]ShellTerminal, 0, len(recs))
	for _, rec := range recs {
		_, hide := hidden[rec.HandleID]
		if hide {
			continue
		}
		alive, err := s.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: rec.HandleID})
		if err != nil {
			s.log.Warn("shell terminal liveness probe failed; keeping row",
				"handleId", rec.HandleID, "error", err)
			out = append(out, shellTerminalFromRecord(rec))
			continue
		}
		if !alive {
			if stillAlive, destroyErr := s.destroyConfirmed(ctx, rec); stillAlive {
				s.log.Warn("pruning exited shell terminal: runtime survived cleanup", "handleId", rec.HandleID, "error", destroyErr)
				out = append(out, shellTerminalFromRecord(rec))
			}
			continue
		}
		out = append(out, shellTerminalFromRecord(rec))
	}
	return out, nil
}

func (s *Service) ListCommandTerminalsForCurrentAppRun(ctx context.Context) ([]ShellTerminal, error) {
	recs, err := s.store.SelectShellTerminalsByAppRunID(ctx, s.appRunID)
	if err != nil {
		return nil, fmt.Errorf("list command terminals: %w", err)
	}
	out := make([]ShellTerminal, 0, len(recs))
	for _, rec := range recs {
		if rec.Transient {
			out = append(out, shellTerminalFromRecord(rec))
		}
	}
	return out, nil
}

func (s *Service) ReapShellTerminalsFromPreviousAppRuns(ctx context.Context) (int64, error) {
	orphans, err := s.store.SelectShellTerminalsFromPreviousAppRuns(ctx, s.appRunID)
	if err != nil {
		return 0, fmt.Errorf("reap shell terminals: %w", err)
	}
	var cleared int64
	for _, rec := range orphans {
		if !rec.Transient && rec.SessionID != "" {
			alive, probeErr := s.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: rec.HandleID})
			if probeErr != nil {
				s.log.Warn("shell terminal liveness probe failed; keeping row", "handleId", rec.HandleID, "error", probeErr)
				continue
			}
			if alive {
				continue
			}
		}
		stillAlive, destroyErr := s.destroyConfirmed(ctx, rec)
		if stillAlive {
			s.log.Warn("reaping orphaned shell terminal: runtime still alive after destroy",
				"handleId", rec.HandleID, "appRunId", rec.AppRunID, "error", destroyErr)
			continue
		}
		cleared++
	}
	if cleared > 0 {
		s.log.Info("reaped shell terminals from previous app runs", "count", cleared)
	}
	return cleared, nil
}

func (s *Service) destroyConfirmed(ctx context.Context, rec ShellTerminalRecord) (stillAlive bool, destroyErr error) {
	handleID := rec.HandleID
	stillAlive, destroyErr = s.destroyRuntimeConfirmed(ctx, ports.RuntimeHandle{ID: handleID})
	if stillAlive {
		return true, destroyErr
	}
	for _, tracker := range s.trackers {
		tracker.UntrackShellTerminal(handleID)
	}
	s.forgetTerminalInput(ctx, handleID)
	if deleted, err := s.store.DeleteShellTerminalByHandleID(ctx, handleID); err != nil {
		s.log.Warn("shell terminal: delete row after destroy failed", "handleId", handleID, "error", err)
	} else if deleted {
		s.cleanupAuthWorkspace(rec.WorkingDir, rec.HandleID)
	}
	return false, nil
}

func (s *Service) destroyRuntimeConfirmed(ctx context.Context, handle ports.RuntimeHandle) (stillAlive bool, destroyErr error) {
	destroyErr = s.runtime.Destroy(ctx, handle)
	if destroyErr == nil {
		return false, nil
	}
	alive, aliveErr := s.runtime.IsAlive(ctx, handle)
	if aliveErr != nil || alive {
		return true, destroyErr
	}
	return false, destroyErr
}

func (s *Service) cleanupAuthWorkspace(workingDir, handleID string) {
	root := filepath.Clean(filepath.Join(s.dataDir, authWorkspaceDirectoryName))
	rel, err := filepath.Rel(root, filepath.Clean(workingDir))
	if err != nil || !strings.HasPrefix(rel, "shellterm-") || filepath.IsAbs(rel) || filepath.Dir(rel) != "." {
		return
	}
	if handleID != rel && !strings.HasSuffix(handleID, ":"+rel) {
		return
	}
	if err := os.RemoveAll(workingDir); err != nil {
		s.log.Warn("shell terminal: remove private auth workspace failed", "workingDir", workingDir, "error", err)
	}
}

func (s *Service) BeginSessionTeardown(ctx context.Context, sessionID domain.SessionID) (release func(), err error) {
	release, err = s.acquireSessionGate(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	recs, err := s.store.SelectShellTerminalsBySessionID(ctx, sessionID)
	if err != nil {
		release()
		return nil, fmt.Errorf("close shell terminals for session %s: %w", sessionID, err)
	}

	var stillAlive []error
	for _, rec := range recs {
		alive, destroyErr := s.destroyConfirmed(ctx, rec)
		if alive {
			s.log.Warn("close shell terminal for session: runtime still alive after destroy",
				"sessionID", sessionID, "handleId", rec.HandleID, "error", destroyErr)
			stillAlive = append(stillAlive, fmt.Errorf("%s: %w", rec.HandleID, destroyErr))
		}
	}

	if len(stillAlive) > 0 {
		release()
		return nil, fmt.Errorf("close shell terminals for session %s: %d still alive: %w",
			sessionID, len(stillAlive), errors.Join(stillAlive...))
	}
	return release, nil
}

func (s *Service) resolveShellTerminalWorkingDir(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID) (workingDir string, resolvedProjectID domain.ProjectID, err error) {
	if sessionID != "" {
		if s.sessions == nil {
			return "", "", apierr.Internal("SHELL_TERMINAL_NO_SESSION_LOOKUP", "Session lookup is unavailable")
		}
		workspacePath, sessionProjectID, err := s.sessions.SessionWorkspace(ctx, sessionID)
		if err != nil {
			return "", "", fmt.Errorf("open shell terminal: resolve session %s: %w", sessionID, err)
		}
		projectID = sessionProjectID
		if workspacePath != "" {
			return workspacePath, projectID, nil
		}
	}
	dir, err := s.resolveProjectRootOrDataDir(ctx, projectID)
	if err != nil {
		return "", "", err
	}
	return dir, projectID, nil
}

func (s *Service) resolveProjectRootOrDataDir(ctx context.Context, projectID domain.ProjectID) (string, error) {
	if projectID == "" {
		if s.dataDir == "" {
			return "", apierr.Internal("SHELL_TERMINAL_NO_WORKING_DIR",
				"No project selected and the daemon has no data dir to fall back to")
		}
		return s.dataDir, nil
	}
	if s.projects == nil {
		return "", apierr.Internal("SHELL_TERMINAL_NO_PROJECT_LOOKUP",
			"Project lookup is unavailable")
	}
	root, err := s.projects.ProjectRoot(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("open shell terminal: resolve project %s: %w", projectID, err)
	}
	if root == "" {
		return "", apierr.NotFound("SHELL_TERMINAL_PROJECT_NOT_FOUND",
			"No such project: "+string(projectID))
	}
	return root, nil
}

func newShellTerminalHandleID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "shellterm-" + hex.EncodeToString(buf), nil
}

func shellTerminalFromRecord(rec ShellTerminalRecord) ShellTerminal {
	terminalTitle := ""
	if !rec.TitleExplicit {
		terminalTitle = domain.MeaningfulTerminalTitle(rec.AppTitle, rec.WorkingDir)
	}
	return ShellTerminal{
		TerminalTitle: terminalTitle,
		HandleID:      rec.HandleID,
		ProjectID:     rec.ProjectID,
		SessionID:     rec.SessionID,
		WorkingDir:    rec.WorkingDir,
		Title:         rec.Title,
		CreatedAt:     rec.CreatedAt,
	}
}

func clampGridDimension(n int) uint16 {
	if n < 2 || n > 1000 {
		return 0
	}
	return uint16(n)
}
