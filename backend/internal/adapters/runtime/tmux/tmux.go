package tmux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/inputdelay"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/ptyexec"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/tmuxbin"
)

const (
	defaultTimeout    = 5 * time.Second
	defaultChunkBytes = 16 * 1024
	defaultEnterDelay = 300 * time.Millisecond
	defaultReapGrace  = 5 * time.Second
	reapPollInterval  = 50 * time.Millisecond
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var getenv = os.Getenv

type Options struct {
	Binary       string
	LegacyBinary string
	SocketName   string
	Shell        string
	Timeout      time.Duration
	ChunkSize    int
	EnterDelay   time.Duration
	ReapGrace    time.Duration
	EnvDir       string
}

type Runtime struct {
	binary         string
	legacyBinary   string
	socketName     string
	shell          string
	timeout        time.Duration
	chunkSize      int
	enterDelay     time.Duration
	reapGrace      time.Duration
	envDir         string
	runner         runner
	reapSessions   func(ctx context.Context, pids []int, grace time.Duration)
	socketMu       sync.RWMutex
	sessionSockets map[string]string
}

var _ ports.Runtime = (*Runtime)(nil)
var _ ports.FencedRuntimeProber = (*Runtime)(nil)
var _ ports.Attacher = (*Runtime)(nil)

type runtimeEffectFailure struct {
	err     error
	handle  ports.RuntimeHandle
	effect  ports.RuntimeEffectOutcome
	cleanup ports.RuntimeCleanupOutcome
}

func (e runtimeEffectFailure) Error() string                               { return e.err.Error() }
func (e runtimeEffectFailure) Unwrap() error                               { return e.err }
func (e runtimeEffectFailure) PossibleHandle() ports.RuntimeHandle         { return e.handle }
func (e runtimeEffectFailure) EffectOutcome() ports.RuntimeEffectOutcome   { return e.effect }
func (e runtimeEffectFailure) CleanupOutcome() ports.RuntimeCleanupOutcome { return e.cleanup }

func tmuxCreateFailure(err error) error {
	return runtimeEffectFailure{err: err, effect: ports.RuntimeEffectNone, cleanup: ports.RuntimeCleanupNotAttempted}
}

func tmuxPossibleCreateFailure(err error, handle ports.RuntimeHandle, cleanup ports.RuntimeCleanupOutcome) error {
	return runtimeEffectFailure{err: err, handle: handle, effect: ports.RuntimeEffectPossible, cleanup: cleanup}
}

type runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

func killSessionsByPID(ctx context.Context, pids []int, grace time.Duration) {
	reapPaneSessions(ctx, pids, grace, signalSessions, sessionsHaveProcesses)
}

func reapPaneSessions(
	ctx context.Context,
	pids []int,
	grace time.Duration,
	signal func(ctx context.Context, pids []int, sig string) bool,
	hasProcesses func(ctx context.Context, pids []int) bool,
) {
	if len(pids) == 0 {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace+5*time.Second)
	defer cancel()

	if !signal(cleanupCtx, pids, "-TERM") {
		return
	}
	if !hasProcesses(cleanupCtx, pids) {
		return
	}

	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	ticker := time.NewTicker(reapPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-cleanupCtx.Done():
			return
		case <-ticker.C:
			if !hasProcesses(cleanupCtx, pids) {
				return
			}
		case <-deadline.C:
			if !hasProcesses(cleanupCtx, pids) {
				return
			}
			signal(cleanupCtx, pids, "-KILL")
			return
		}
	}
}

func signalSessions(ctx context.Context, pids []int, sig string) bool {
	supported := false
	for _, pid := range pids {
		err := aoprocess.CommandContext(ctx, "pkill", sig, "-s", strconv.Itoa(pid)).Run()
		if !isUnsupportedMatcher(err) {
			supported = true
		}
	}
	return supported
}

func isUnsupportedMatcher(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() >= 2
	}
	return true
}

func sessionsHaveProcesses(ctx context.Context, pids []int) bool {
	for _, pid := range pids {
		err := aoprocess.CommandContext(ctx, "pgrep", "-s", strconv.Itoa(pid)).Run()
		if err == nil || ctx.Err() != nil {
			return true
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return true
		}
	}
	return false
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, name, args...)
	cmd.Env = append(append([]string(nil), os.Environ()...), env...)
	cmd.Dir = stableRunDir()
	return cmd.CombinedOutput()
}

func stableRunDir() string {
	candidates := []string{os.TempDir()}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, home)
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

func New(opts Options) *Runtime {
	binary := opts.Binary
	if binary == "" {
		resolution, err := tmuxbin.Resolve()
		if err == nil {
			binary = resolution.Path
		} else if configured := strings.TrimSpace(getenv("AO_TMUX_BINARY")); configured != "" {
			binary = configured
		} else {
			binary = "tmux"
		}
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	shellPath := opts.Shell
	if shellPath == "" {
		shellPath = getenv("SHELL")
	}
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	chunkSize := opts.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkBytes
	}
	enterDelay := opts.EnterDelay
	if enterDelay <= 0 {
		enterDelay = defaultEnterDelay
	}
	reapGrace := opts.ReapGrace
	if reapGrace <= 0 {
		reapGrace = defaultReapGrace
	}
	socketName := strings.TrimSpace(opts.SocketName)
	if socketName == "" {
		socketName = strings.TrimSpace(getenv("AO_TMUX_SOCKET_NAME"))
	}
	legacyBinary := opts.LegacyBinary
	if socketName == "" {
		legacyBinary = binary
	} else if legacyBinary == "" {
		if systemTmux, err := exec.LookPath("tmux"); err == nil {
			legacyBinary = systemTmux
		}
	}
	return &Runtime{
		binary:         binary,
		legacyBinary:   legacyBinary,
		socketName:     socketName,
		shell:          shellPath,
		timeout:        timeout,
		chunkSize:      chunkSize,
		enterDelay:     enterDelay,
		reapGrace:      reapGrace,
		envDir:         opts.EnvDir,
		runner:         execRunner{},
		reapSessions:   killSessionsByPID,
		sessionSockets: make(map[string]string),
	}
}

func (r *Runtime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	id, err := tmuxSessionName(cfg.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, tmuxCreateFailure(err)
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, tmuxCreateFailure(errors.New("tmux runtime: workspace path is required"))
	}
	if len(cfg.Argv) == 0 {
		return ports.RuntimeHandle{}, tmuxCreateFailure(errors.New("tmux runtime: launch command is required"))
	}
	if err := validateEnvKeys(cfg.Env); err != nil {
		return ports.RuntimeHandle{}, tmuxCreateFailure(err)
	}

	envFile, err := r.writeLaunchEnv(ctx, cfg)
	if err != nil {
		return ports.RuntimeHandle{}, tmuxCreateFailure(err)
	}
	created := false
	defer func() {
		if !created {
			_ = os.Remove(envFile)
		}
	}()
	launchCmd := buildLaunchCommand(cfg, envFile)
	args := newSessionArgs(id, cfg.WorkspacePath, r.shell, launchCmd)
	if _, err := r.run(ctx, args...); err != nil {
		return ports.RuntimeHandle{}, tmuxPossibleCreateFailure(
			fmt.Errorf("tmux runtime: create session %s: %w", id, err),
			ports.RuntimeHandle{ID: id},
			ports.RuntimeCleanupNotAttempted,
		)
	}
	r.rememberSessionSocket(id, r.socketName)
	handle := ports.RuntimeHandle{ID: id}
	if err := r.verifyPaneWorkingDirectory(ctx, id, cfg.WorkspacePath); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, err)
	}

	if _, err := r.run(ctx, setStatusOffArgs(id)...); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: set status %s: %w", id, err))
	}

	if _, err := r.run(ctx, setMouseOnArgs(id)...); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: set mouse %s: %w", id, err))
	}

	if _, err := r.run(ctx, setWindowSizeLargestArgs(id)...); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: set window-size %s: %w", id, err))
	}

	if _, err := r.run(ctx, setDetachOnDestroyOnArgs(id)...); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: set detach-on-destroy %s: %w", id, err))
	}

	alive, err := r.IsAlive(ctx, handle)
	if err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: verify session %s: %w", id, err))
	}
	if !alive {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: session %s exited before ready", id))
	}
	created = true
	return handle, nil
}

func (r *Runtime) failedCreatedRuntime(handle ports.RuntimeHandle, cause error) error {
	if cleanupErr := r.Destroy(context.Background(), handle); cleanupErr != nil {
		return tmuxPossibleCreateFailure(errors.Join(cause, cleanupErr), handle, ports.RuntimeCleanupFailed)
	}
	return tmuxPossibleCreateFailure(cause, handle, ports.RuntimeCleanupSucceeded)
}

func (r *Runtime) Restart(ctx context.Context, handle ports.RuntimeHandle, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	id, err := handleID(handle)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	expectedID, err := tmuxSessionName(cfg.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	if expectedID != id {
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: restart handle %s does not match session %s", id, cfg.SessionID)
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, errors.New("tmux runtime: workspace path is required")
	}
	if len(cfg.Argv) == 0 {
		return ports.RuntimeHandle{}, errors.New("tmux runtime: launch command is required")
	}
	if err := validateEnvKeys(cfg.Env); err != nil {
		return ports.RuntimeHandle{}, err
	}

	envFile, err := r.writeLaunchEnv(ctx, cfg)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	launchCmd := buildLaunchCommand(cfg, envFile)
	if _, err := r.runForSession(ctx, id, respawnPaneArgs(id, cfg.WorkspacePath, r.shell, launchCmd)...); err != nil {
		_ = os.Remove(envFile)
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: restart session %s: %w", id, err)
	}
	alive, err := r.IsAlive(ctx, handle)
	if err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: verify restarted session %s: %w", id, err)
	}
	if !alive {
		_ = os.Remove(envFile)
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: session %s exited during restart", id)
	}
	return handle, nil
}

const (
	paneCwdVerifyAttempts   = 5
	paneCwdVerifyRetryDelay = 50 * time.Millisecond
)

func (r *Runtime) verifyPaneWorkingDirectory(ctx context.Context, id, want string) error {
	var lastErr error
	for attempt := 0; attempt < paneCwdVerifyAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(paneCwdVerifyRetryDelay):
			}
		}
		out, err := r.runForSession(ctx, id, paneCurrentPathArgs(id)...)
		if err != nil {
			if !errors.Is(lastErr, ports.ErrRuntimeWorkspaceCwdMismatch) {
				lastErr = fmt.Errorf("tmux runtime: verify working directory %s: %w", id, err)
			}
			continue
		}
		got := strings.TrimSpace(string(out))
		if sameDirectory(got, want) {
			return nil
		}
		lastErr = fmt.Errorf(
			"%w: session %s started in %q, want %q (the worktree may be missing, or the tmux server may be pinned to a stale directory)",
			ports.ErrRuntimeWorkspaceCwdMismatch, id, got, want,
		)
	}
	return lastErr
}

func (r *Runtime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	sessionIDs := r.paneSessionIDs(ctx, id)

	if out, setErr := r.runForSession(ctx, id, setDetachOnDestroyOnArgs(id)...); setErr != nil && !confirmedAbsentOutput(setErr, string(out)) {
		return fmt.Errorf("tmux runtime: set detach-on-destroy %s: %w", id, setErr)
	}

	out, err := r.runForSession(ctx, id, killSessionArgs(id)...)
	r.reapSessions(ctx, sessionIDs, r.reapGrace)

	if err != nil {
		if confirmedAbsentOutput(err, string(out)) {
			r.forgetSessionSocket(id)
			return nil
		}
		return fmt.Errorf("tmux runtime: destroy session %s: %w", id, err)
	}
	r.forgetSessionSocket(id)
	return nil
}

func (r *Runtime) paneSessionIDs(ctx context.Context, id string) []int {
	out, err := r.runForSession(ctx, id, listPanePIDsArgs(id)...)
	if err != nil {
		return nil
	}
	var ids []int
	for _, line := range strings.Split(string(out), "\n") {
		pid, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || pid <= 1 {
			continue
		}
		ids = append(ids, pid)
	}
	return ids
}

func (r *Runtime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	id, err := handleID(handle)
	if err != nil {
		return false, err
	}
	out, err := r.runForSession(ctx, id, hasSessionArgs(id)...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if sessionMissingOutput(string(out)) {
				return false, nil
			}
			if serverNotRunningOutput(string(out)) || serverSocketAbsentOutput(string(out)) {
				return false, fmt.Errorf("tmux runtime: probe session %s: %w: %s",
					id, ports.ErrRuntimeUnavailable, strings.TrimSpace(string(out)))
			}
			if transientServerFailureOutput(string(out)) {
				return false, fmt.Errorf("tmux runtime: probe session %s: %w: %s",
					id, ports.ErrRuntimeProbeInconclusive, strings.TrimSpace(string(out)))
			}
		}
		return false, fmt.Errorf("tmux runtime: probe session %s: %w", id, err)
	}
	return true, nil
}

func (r *Runtime) ProcessRootPIDs(ctx context.Context, handle ports.RuntimeHandle) ([]int, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	out, err := r.runForSession(ctx, id, listPanePIDsArgs(id)...)
	if err != nil {
		if sessionMissingOutput(string(out)) || serverNotRunningOutput(string(out)) || serverSocketAbsentOutput(string(out)) {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux runtime: list pane pids %s: %w", id, err)
	}
	var ids []int
	for _, line := range strings.Split(string(out), "\n") {
		pid, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || pid <= 1 {
			continue
		}
		ids = append(ids, pid)
	}
	return ids, nil
}

func (r *Runtime) ServerPID(ctx context.Context) (int, bool) {
	if r.socketName == "" {
		return 0, false
	}
	out, err := r.run(ctx, "display-message", "-p", "#{pid}")
	if err != nil {
		return 0, false
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil || pid <= 1 {
		return 0, false
	}
	return pid, true
}

func (r *Runtime) IsChildAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	alive, err := r.IsAlive(ctx, handle)
	if errors.Is(err, ports.ErrRuntimeUnavailable) {
		return false, nil
	}
	if err != nil || !alive {
		return false, err
	}
	out, err := r.runForSession(ctx, handle.ID, paneDeadArgs(handle.ID)...)
	if err != nil {
		return false, fmt.Errorf("tmux runtime: probe child status for %s: %w", handle.ID, err)
	}
	states := strings.Fields(string(out))
	if len(states) == 0 {
		return false, fmt.Errorf("tmux runtime: no pane status for %s: %w", handle.ID, ports.ErrRuntimeProbeInconclusive)
	}
	childAlive := false
	for _, state := range states {
		switch state {
		case "0":
			childAlive = true
		case "1":
		default:
			return false, fmt.Errorf("tmux runtime: invalid pane status %q for %s: %w", state, handle.ID, ports.ErrRuntimeProbeInconclusive)
		}
	}
	return childAlive, nil
}

func (r *Runtime) IsUnsupervisedReviewerAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	alive, err := r.IsAlive(ctx, handle)
	if err != nil || !alive {
		return false, err
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return false, err
	}
	return containsUnsupervisedReviewerWorkload(entries, panePID), nil
}

func (r *Runtime) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	if ref.Handle.ID == "" || ref.SessionID == "" || strings.TrimSpace(ref.Generation) == "" || ref.Handle.ID != string(ref.SessionID) {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonIdentityMissing}
	}
	alive, err := r.IsAlive(ctx, ref.Handle)
	if err != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	if !alive {
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, ref.Handle)
	if err != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	descendants := descendantPIDs(entries, panePID)
	exactSupervisorFound := false
	for _, entry := range entries {
		if entry.pid == panePID || !descendants[entry.pid] || !isAnySupervisorCommand(entry.command) {
			continue
		}
		if isSupervisorCommand(entry.command, string(ref.SessionID), ref.Generation) {
			exactSupervisorFound = true
			continue
		}
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonGenerationMismatch}
	}
	if exactSupervisorFound {
		if containsExactSupervisedWorkload(entries, panePID, string(ref.SessionID), ref.Generation) {
			return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
		}
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonIdentityMissing}
}

func (r *Runtime) IsSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return false, err
	}
	return containsManagedWorkload(entries, panePID, string(ref.SessionID), ref.LaunchID), nil
}

func (r *Runtime) IsExactSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	if ref.SessionID == "" || strings.TrimSpace(ref.LaunchID) == "" {
		return false, errors.New("tmux runtime: exact supervisor session and launch are required")
	}
	alive, err := r.IsAlive(ctx, handle)
	if err != nil || !alive {
		return false, err
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return false, err
	}
	return containsExactSupervisedWorkload(entries, panePID, string(ref.SessionID), ref.LaunchID), nil
}

func (r *Runtime) HasSupervisedProcessRecord(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	alive, err := r.IsAlive(ctx, handle)
	if err != nil || !alive {
		return false, err
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return false, err
	}
	descendants := descendantPIDs(entries, panePID)
	for _, entry := range entries {
		if entry.pid != panePID && descendants[entry.pid] && isAnySupervisorCommand(entry.command) {
			return true, nil
		}
	}
	return false, nil
}

func (r *Runtime) RunningWorkloadCommands(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (int, error) {
	if ref.SessionID == "" || strings.TrimSpace(ref.LaunchID) == "" {
		return 0, errors.New("tmux runtime: exact supervisor session and launch are required")
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return 0, err
	}
	return countWorkloadCommands(entries, panePID, string(ref.SessionID), ref.LaunchID)
}

func countWorkloadCommands(entries []processEntry, panePID int, sessionID, launchID string) (int, error) {
	descendants := descendantPIDs(entries, panePID)
	supervisor := 0
	for _, entry := range entries {
		if entry.pid != panePID && descendants[entry.pid] && isSupervisorCommand(entry.command, sessionID, launchID) {
			supervisor = entry.pid
			break
		}
	}
	if supervisor == 0 {
		return 0, errors.New("tmux runtime: exact supervisor is not running")
	}
	agents := 0
	commands := 0
	for _, agent := range entries {
		if agent.ppid != supervisor {
			continue
		}
		agents++
		below := descendantPIDs(entries, agent.pid)
		for _, entry := range entries {
			if entry.pid == agent.pid || !below[entry.pid] || !isShellCommand(entry.command) {
				continue
			}
			if parent, ok := processByPID(entries, entry.ppid); ok && parent.pid != agent.pid && below[parent.pid] && isShellCommand(parent.command) {
				continue
			}
			commands += len(descendantPIDs(entries, entry.pid))
		}
	}
	if agents == 0 {
		return 0, errors.New("tmux runtime: supervised agent is not running")
	}
	return commands, nil
}

func processByPID(entries []processEntry, pid int) (processEntry, bool) {
	for _, entry := range entries {
		if entry.pid == pid {
			return entry, true
		}
	}
	return processEntry{}, false
}

func isShellCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	name := strings.TrimPrefix(filepath.Base(fields[0]), "-")
	switch name {
	case "sh", "bash", "zsh", "dash", "fish", "ksh":
		return true
	}
	return false
}

func (r *Runtime) supervisedProcessTree(ctx context.Context, handle ports.RuntimeHandle) ([]processEntry, int, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, 0, err
	}
	paneOut, err := r.runForSession(ctx, id, panePIDArgs(id)...)
	if err != nil {
		return nil, 0, fmt.Errorf("tmux runtime: inspect pane pid %s: %w", id, err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(paneOut)))
	if err != nil || panePID <= 0 {
		return nil, 0, fmt.Errorf("tmux runtime: invalid pane pid %q", strings.TrimSpace(string(paneOut)))
	}
	processOut, err := r.runCommand(ctx, "ps", "-ww", "-axo", "pid=,ppid=,args=")
	if err != nil {
		return nil, 0, fmt.Errorf("tmux runtime: inspect process tree %s: %w", id, err)
	}
	entries, err := parseProcessTable(string(processOut))
	if err != nil {
		return nil, 0, fmt.Errorf("tmux runtime: parse process tree %s: %w", id, err)
	}
	return entries, panePID, nil
}

func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	enterCtx := ctx
	if message != "" {
		delay := inputdelay.ForMessage(message, r.enterDelay)
		messageChunks := chunks(message, r.chunkSize)
		sendCtx := ctx
		var finishCancel context.CancelFunc
		for i, chunk := range messageChunks {
			if _, err := r.runForSession(sendCtx, id, sendKeysLiteralArgs(id, chunk)...); err != nil {
				if finishCancel != nil {
					finishCancel()
				}
				return fmt.Errorf("tmux runtime: send message %s: %w", id, err)
			}
			if i == 0 {
				completionBudget := sendCompletionBudget(len(messageChunks), r.timeout, delay)
				enterCtx, finishCancel = context.WithTimeout(context.WithoutCancel(ctx), completionBudget)
				sendCtx = enterCtx
			}
		}
		if finishCancel != nil {
			defer finishCancel()
		}
		if delay > 0 {
			select {
			case <-enterCtx.Done():
				return enterCtx.Err()
			case <-time.After(delay):
			}
		}
	}
	if _, err := r.runForSession(enterCtx, id, sendEnterArgs(id)...); err != nil {
		return fmt.Errorf("tmux runtime: send enter %s: %w", id, err)
	}
	return nil
}

func sendCompletionBudget(chunkCount int, commandTimeout, enterDelay time.Duration) time.Duration {
	return time.Duration(chunkCount)*commandTimeout + enterDelay
}

func (r *Runtime) Interrupt(ctx context.Context, handle ports.RuntimeHandle) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	if _, err := r.runForSession(ctx, id, sendInterruptArgs(id)...); err != nil {
		return fmt.Errorf("tmux runtime: interrupt session %s: %w", id, err)
	}
	return nil
}

func (r *Runtime) SendInput(ctx context.Context, handle ports.RuntimeHandle, input string) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	args := sendKeysLiteralArgs(id, input)
	if _, err := r.runForSession(ctx, id, args...); err != nil {
		return fmt.Errorf("tmux runtime: send input %s: %w", id, err)
	}
	return nil
}

func (r *Runtime) GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	id, err := handleID(handle)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		return "", errors.New("tmux runtime: lines must be positive")
	}
	out, err := r.runForSession(ctx, id, capturePaneArgs(id, lines)...)
	if err != nil {
		return "", fmt.Errorf("tmux runtime: capture output %s: %w", id, err)
	}
	return tailLines(trimTrailingBlankLines(string(out)), lines), nil
}

func (r *Runtime) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	id, err := handleID(handle)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		return "", errors.New("tmux runtime: lines must be positive")
	}
	out, err := r.runForSession(ctx, id, capturePaneStyledArgs(id, lines)...)
	if err != nil {
		return "", fmt.Errorf("tmux runtime: capture styled output %s: %w", id, err)
	}
	return tailLines(trimTrailingBlankLines(string(out)), lines), nil
}

func (r *Runtime) Attach(ctx context.Context, handle ports.RuntimeHandle, rows, cols uint16) (ports.Stream, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	socketName, err := r.socketForSession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("tmux runtime: attach session %s: %w", id, err)
	}
	argv := r.attachCommandForSocket(id, socketName)
	return ptyexec.Spawn(ctx, argv, attachEnv(os.Environ()), rows, cols)
}

func (r *Runtime) attachCommand(handle ports.RuntimeHandle) ([]string, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	return r.attachCommandForSocket(id, r.socketName), nil
}

func (r *Runtime) attachCommandForSocket(id, socketName string) []string {
	argv := []string{r.binaryForSocket(socketName)}
	if socketName != "" {
		argv = append(argv, "-L", socketName)
	} else if r.socketName != "" {
		argv = append(argv, "-L", "default")
	}
	return append(argv, "-u", "-T", "RGB", "attach-session", "-t", exactSessionTarget(id))
}

func attachEnv(base []string) []string {
	env := append([]string(nil), base...)
	hasTerm := false
	hasColorTerm := false
	for i, kv := range env {
		switch {
		case strings.HasPrefix(kv, "TERM="):
			env[i] = "TERM=xterm-256color"
			hasTerm = true
		case strings.HasPrefix(kv, "COLORTERM="):
			env[i] = "COLORTERM=truecolor"
			hasColorTerm = true
		}
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasColorTerm {
		env = append(env, "COLORTERM=truecolor")
	}
	return env
}

func (r *Runtime) run(ctx context.Context, args ...string) ([]byte, error) {
	return r.runOnSocket(ctx, r.socketName, args...)
}

func (r *Runtime) runOnSocket(ctx context.Context, socketName string, args ...string) ([]byte, error) {
	if socketName != "" {
		args = append([]string{"-L", socketName}, args...)
	} else if r.socketName != "" {
		args = append([]string{"-L", "default"}, args...)
	}
	return r.runCommand(ctx, r.binaryForSocket(socketName), args...)
}

func (r *Runtime) binaryForSocket(socketName string) string {
	if socketName == "" && r.socketName != "" {
		return r.legacyBinary
	}
	return r.binary
}

func (r *Runtime) runForSession(ctx context.Context, id string, args ...string) ([]byte, error) {
	socketName, err := r.socketForSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.runOnSocket(ctx, socketName, args...)
}

func (r *Runtime) socketForSession(ctx context.Context, id string) (string, error) {
	if r.socketName == "" {
		return "", nil
	}
	r.socketMu.RLock()
	socketName, ok := r.sessionSockets[id]
	r.socketMu.RUnlock()
	if ok {
		return socketName, nil
	}

	out, err := r.runOnSocket(ctx, r.socketName, hasSessionArgs(id)...)
	if err == nil {
		r.rememberSessionSocket(id, r.socketName)
		return r.socketName, nil
	}
	if !sessionMissingOutput(string(out)) &&
		!serverNotRunningOutput(string(out)) &&
		!serverSocketAbsentOutput(string(out)) {
		return r.socketName, nil
	}
	if r.legacyBinary == "" {
		return "", fmt.Errorf(
			"%w: cannot inspect legacy system-socket session %s because system tmux is unavailable",
			ports.ErrRuntimeProbeInconclusive,
			id,
		)
	}
	legacyOut, legacyErr := r.runOnSocket(ctx, "", hasSessionArgs(id)...)
	if legacyErr == nil {
		if err := r.enforceDetachOnDestroy(ctx, "", id); err != nil {
			return "", err
		}
		r.rememberSessionSocket(id, "")
		return "", nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if sessionMissingOutput(string(legacyOut)) ||
		serverNotRunningOutput(string(legacyOut)) ||
		serverSocketAbsentOutput(string(legacyOut)) {
		return r.socketName, nil
	}
	return "", fmt.Errorf(
		"%w: system tmux %q could not inspect legacy system-socket session %s: %w",
		ports.ErrRuntimeProbeInconclusive,
		r.legacyBinary,
		id,
		legacyErr,
	)
}

func (r *Runtime) rememberSessionSocket(id, socketName string) {
	r.socketMu.Lock()
	r.sessionSockets[id] = socketName
	r.socketMu.Unlock()
}

func (r *Runtime) forgetSessionSocket(id string) {
	r.socketMu.Lock()
	delete(r.sessionSockets, id)
	r.socketMu.Unlock()
}

func (r *Runtime) runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := r.runner.Run(cmdCtx, nil, name, args...)
	if cmdCtx.Err() != nil {
		return out, cmdCtx.Err()
	}
	if err != nil {
		return out, commandError{err: err, output: strings.TrimSpace(string(out))}
	}
	return out, nil
}

type processEntry struct {
	pid     int
	ppid    int
	command string
}

func parseProcessTable(out string) ([]processEntry, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	entries := make([]processEntry, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("incomplete process row %q", line)
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("invalid pid in %q", line)
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("invalid parent pid in %q", line)
		}
		entries = append(entries, processEntry{pid: pid, ppid: ppid, command: strings.Join(fields[2:], " ")})
	}
	return entries, nil
}

func descendantPIDs(entries []processEntry, rootPID int) map[int]bool {
	descendants := map[int]bool{rootPID: true}
	for changed := true; changed; {
		changed = false
		for _, entry := range entries {
			if descendants[entry.pid] || !descendants[entry.ppid] {
				continue
			}
			descendants[entry.pid] = true
			changed = true
		}
	}
	return descendants
}

func containsManagedWorkload(entries []processEntry, rootPID int, sessionID, launchID string) bool {
	descendants := descendantPIDs(entries, rootPID)
	hasChild := false
	hasSupervisor := false
	for _, entry := range entries {
		if entry.pid == rootPID || !descendants[entry.pid] {
			continue
		}
		hasChild = true
		if !isAnySupervisorCommand(entry.command) {
			continue
		}
		hasSupervisor = true
		if isSupervisorCommand(entry.command, sessionID, launchID) {
			return true
		}
	}

	return hasChild && !hasSupervisor
}

func containsUnsupervisedReviewerWorkload(entries []processEntry, rootPID int) bool {
	descendants := descendantPIDs(entries, rootPID)
	for _, entry := range entries {
		if entry.pid == rootPID || !descendants[entry.pid] || isAnySupervisorCommand(entry.command) {
			continue
		}
		if isPreservedPaneProcess(entry.command) {
			continue
		}
		return true
	}
	return false
}

func isPreservedPaneProcess(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return true
	}
	name := filepath.Base(fields[0])
	switch name {
	case "sh", "bash", "zsh", "fish", "ksh", "dash", "nu", "cat":
		return true
	default:
		return false
	}
}

func containsExactSupervisedWorkload(entries []processEntry, rootPID int, sessionID, launchID string) bool {
	descendants := descendantPIDs(entries, rootPID)
	supervisorPID := 0
	for _, entry := range entries {
		if entry.pid != rootPID && descendants[entry.pid] && isSupervisorCommand(entry.command, sessionID, launchID) {
			supervisorPID = entry.pid
			break
		}
	}
	if supervisorPID == 0 {
		return false
	}
	workloadDescendants := descendantPIDs(entries, supervisorPID)
	for _, entry := range entries {
		if entry.pid != supervisorPID && workloadDescendants[entry.pid] {
			return true
		}
	}
	return false
}

func isAnySupervisorCommand(command string) bool {
	fields := strings.Fields(command)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "agent-process" && fields[i+1] == "supervise" {
			return true
		}
	}
	return false
}

func isSupervisorCommand(command, sessionID, launchID string) bool {
	fields := strings.Fields(command)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "agent-process" || fields[i+1] != "supervise" {
			continue
		}
		gotSession, gotLaunch := "", ""
		for j := i + 2; j+1 < len(fields) && fields[j] != "--"; j++ {
			switch fields[j] {
			case "--session":
				gotSession = fields[j+1]
				j++
			case "--launch":
				gotLaunch = fields[j+1]
				j++
			}
		}
		if gotSession == sessionID && gotLaunch == launchID {
			return true
		}
	}
	return false
}

func tmuxSessionName(id domain.SessionID) (string, error) {
	raw := string(id)
	if raw == "" {
		return "", errors.New("tmux runtime: session id is required")
	}
	return SessionName(raw), nil
}

func SessionName(id string) string {
	if sessionIDPattern.MatchString(id) && len(id) <= 48 {
		return id
	}
	return sanitizedSessionName(id)
}

func sanitizedSessionName(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "session"
	}
	if len(base) > 32 {
		base = strings.TrimRight(base[:32], "-")
	}
	sum := sha256.Sum256([]byte(raw))
	return base + "-" + hex.EncodeToString(sum[:4])
}

func handleID(handle ports.RuntimeHandle) (string, error) {
	id := handle.ID
	if id == "" {
		return "", errors.New("tmux runtime: session id is required")
	}
	if !sessionIDPattern.MatchString(id) {
		return "", fmt.Errorf("tmux runtime: invalid handle id %q", id)
	}
	return id, nil
}

func sessionMissingOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "can't find session") ||
		strings.Contains(s, "session not found") ||
		strings.Contains(s, "no such session")
}

func serverNotRunningOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "no server running")
}

func serverSocketAbsentOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "error connecting") &&
		strings.Contains(s, "no such file or directory")
}

func transientServerFailureOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "error connecting") ||
		strings.Contains(s, "protocol version mismatch") ||
		strings.Contains(s, "server exited unexpectedly")
}

func confirmedAbsentOutput(err error, out string) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) &&
		(sessionMissingOutput(out) || serverNotRunningOutput(out) || serverSocketAbsentOutput(out))
}

func (r *Runtime) enforceDetachOnDestroy(ctx context.Context, socketName, id string) error {
	if out, err := r.runOnSocket(ctx, socketName, setDetachOnDestroyOnArgs(id)...); err != nil {
		if confirmedAbsentOutput(err, string(out)) {
			return nil
		}
		return fmt.Errorf("tmux runtime: set detach-on-destroy for adopted session %s: %w", id, err)
	}
	out, err := r.runOnSocket(ctx, socketName, showDetachOnDestroyArgs(id)...)
	if err != nil {
		if confirmedAbsentOutput(err, string(out)) {
			return nil
		}
		return fmt.Errorf("tmux runtime: verify detach-on-destroy for adopted session %s: %w", id, err)
	}
	if got := strings.TrimSpace(string(out)); got != "on" {
		return fmt.Errorf("tmux runtime: detach-on-destroy for adopted session %s reports %q, want \"on\"", id, got)
	}
	return nil
}

func chunks(s string, maxBytes int) []string {
	if s == "" {
		return []string{""}
	}
	if maxBytes <= 0 || len(s) <= maxBytes {
		return []string{s}
	}
	parts := []string{}
	for s != "" {
		if len(s) <= maxBytes {
			parts = append(parts, s)
			break
		}
		end := maxBytes
		for end > 0 && !utf8.ValidString(s[:end]) {
			end--
		}
		if end == 0 {
			_, size := utf8.DecodeRuneInString(s)
			end = size
		}
		parts = append(parts, s[:end])
		s = s[end:]
	}
	return parts
}

func tailLines(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "")
}

func trimTrailingBlankLines(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimRight(lines[len(lines)-1], "\r\n") == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "")
}

func validateEnvKeys(env map[string]string) error {
	for key := range env {
		if !validEnvKey(key) {
			return fmt.Errorf("tmux runtime: invalid env key %q", key)
		}
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func buildLaunchCommand(cfg ports.RuntimeConfig, envFile string) string {
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(cfg.WorkspacePath))
	b.WriteString(" || exit; ")
	if _, configured := cfg.Env["NO_COLOR"]; !configured {
		b.WriteString("unset NO_COLOR; ")
	}
	file := shellQuote(envFile)
	b.WriteString(". " + file + " || { rm -f " + file + "; exit 1; }; rm -f " + file + "; ")
	parts := make([]string, len(cfg.Argv))
	for i, a := range cfg.Argv {
		parts[i] = shellQuote(a)
	}
	b.WriteString(strings.Join(parts, " "))
	if cfg.ExitOnCommandCompletion {
		b.WriteString(`; exit $?`)
	} else if cfg.Env["AO_SUPERVISED_PROCESS"] == "1" {
		b.WriteString(`; exec cat >/dev/null`)
	} else {
		b.WriteString(`; exec "${SHELL:-/bin/sh}" -i`)
	}
	return b.String()
}

func launchEnvScript(cfg ports.RuntimeConfig) string {
	path := cfg.Env["PATH"]
	if path == "" {
		path = getenv("PATH")
	}
	var b strings.Builder
	for _, key := range sortedKeys(cfg.Env) {
		if key == "PATH" || key == "COLORTERM" {
			continue
		}
		b.WriteString("export ")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(shellQuote(cfg.Env[key]))
		b.WriteString("\n")
	}
	b.WriteString("export COLORTERM='truecolor'\n")
	if path != "" {
		b.WriteString("export PATH=")
		b.WriteString(shellQuote(path))
		b.WriteString("\n")
	}
	return b.String()
}

const launchEnvMaxAge = 10 * time.Minute

func (r *Runtime) launchEnvDir(cfg ports.RuntimeConfig) (string, error) {
	if r.envDir != "" {
		abs, err := filepath.Abs(r.envDir)
		if err != nil {
			return "", fmt.Errorf("tmux runtime: resolve launch environment directory: %w", err)
		}
		return abs, nil
	}
	dataDir := cfg.Env["AO_DATA_DIR"]
	if dataDir == "" {
		dataDir = getenv("AO_DATA_DIR")
	}
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("tmux runtime: resolve AO data directory: %w", err)
		}
		dataDir = filepath.Join(home, ".ao", "data")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("tmux runtime: resolve AO data directory: %w", err)
	}
	return filepath.Join(abs, "runtime-env"), nil
}

func (r *Runtime) writeLaunchEnv(ctx context.Context, cfg ports.RuntimeConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := r.launchEnvDir(cfg)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("tmux runtime: create launch environment directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("tmux runtime: inspect launch environment directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("tmux runtime: launch environment directory is not a directory")
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory needs its search bit; 0700 is owner-only
			return "", fmt.Errorf("tmux runtime: restrict launch environment directory: %w", err)
		}
	}
	sweepLaunchEnv(ctx, dir)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "launch-*.env")
	if err != nil {
		return "", fmt.Errorf("tmux runtime: create launch environment: %w", err)
	}
	name := f.Name()
	if _, err := f.WriteString(launchEnvScript(cfg)); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("tmux runtime: write launch environment: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("tmux runtime: write launch environment: %w", err)
	}
	return name, nil
}

func sweepLaunchEnv(ctx context.Context, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), "launch-") || !strings.HasSuffix(entry.Name(), ".env") {
			continue
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > launchEnvMaxAge {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func sameDirectory(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	if errA == nil {
		a = absA
	}
	absB, errB := filepath.Abs(b)
	if errB == nil {
		b = absB
	}
	if realA, err := filepath.EvalSymlinks(a); err == nil {
		a = realA
	}
	if realB, err := filepath.EvalSymlinks(b); err == nil {
		b = realB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

type commandError struct {
	err    error
	output string
}

func (e commandError) Error() string {
	if e.output == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.output
}

func (e commandError) Unwrap() error { return e.err }
