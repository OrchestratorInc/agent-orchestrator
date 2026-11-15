package conpty

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty/ptyregistry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

const (
	runtimeLaunchIDEnv    = "AO_RUNTIME_LAUNCH_ID"
	unresolvedHostAddress = ptyregistry.UnresolvedPipePath
)

var _ ports.Runtime = (*Runtime)(nil)
var _ ports.FencedRuntimeProber = (*Runtime)(nil)
var _ ports.StyledTerminalOutputReader = (*Runtime)(nil)

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

func conptyCreateFailure(err error) error {
	return runtimeEffectFailure{err: err, effect: ports.RuntimeEffectNone, cleanup: ports.RuntimeCleanupNotAttempted}
}

func conptyPartialCreateFailure(err error, handle ports.RuntimeHandle, cleanup ports.RuntimeCleanupOutcome) error {
	return runtimeEffectFailure{err: err, handle: handle, effect: ports.RuntimeEffectPossible, cleanup: cleanup}
}

var validSessionID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type hostSession struct {
	addr             string
	pid              int
	launchID         string
	currentOwner     bool
	protocolVersion  int
	protocolResolved bool
}

type Options struct {
	Spawner hostSpawner

	RunFilePath string

	UnregisterHost func(context.Context, string) error
}

type Runtime struct {
	spawner        hostSpawner
	killHost       func(string) error
	pidIsAlive     func(int) bool
	processFinder  func(int) (processKiller, error)
	registerHost   func(context.Context, ptyregistry.Entry) error
	unregisterHost func(context.Context, string) error
	destroyWait    time.Duration
	destroyPoll    time.Duration
	processTable   func(context.Context) ([]procmem.Process, error)

	mu       sync.Mutex
	sessions map[string]*hostSession
}

func New(opts Options) *Runtime {
	ptyregistry.SetRunFilePath(opts.RunFilePath)
	sp := opts.Spawner
	if sp == nil {
		sp = defaultSpawnHost
	}
	unregisterHost := ptyregistry.Unregister
	if opts.UnregisterHost != nil {
		unregisterHost = opts.UnregisterHost
	}
	return &Runtime{
		spawner:        sp,
		killHost:       clientKill,
		pidIsAlive:     pidAlive,
		processFinder:  findProcess,
		registerHost:   ptyregistry.Register,
		unregisterHost: unregisterHost,
		destroyWait:    500 * time.Millisecond,
		destroyPoll:    25 * time.Millisecond,
		processTable:   snapshotProcesses,
		sessions:       make(map[string]*hostSession),
	}
}

func (r *Runtime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	id := string(cfg.SessionID)
	if !validSessionID.MatchString(id) {
		return ports.RuntimeHandle{}, conptyCreateFailure(fmt.Errorf("conpty: invalid session id %q: must match ^[a-zA-Z0-9_-]+$", id))
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, conptyCreateFailure(fmt.Errorf("conpty: workspace path required"))
	}
	if len(cfg.Argv) == 0 {
		return ports.RuntimeHandle{}, conptyCreateFailure(fmt.Errorf("conpty: argv required"))
	}

	r.mu.Lock()
	if _, dup := r.sessions[id]; dup {
		r.mu.Unlock()
		return ports.RuntimeHandle{}, conptyCreateFailure(fmt.Errorf("conpty: session %q already exists; destroy before re-creating", id))
	}
	r.mu.Unlock()
	existing, resolveErr := r.resolveWithEvidence(ctx, id)
	if resolveErr != nil {
		return ports.RuntimeHandle{}, conptyCreateFailure(fmt.Errorf("conpty: inspect existing ownership for %q: %w", id, resolveErr))
	}
	if existing != nil {
		return ports.RuntimeHandle{}, conptyCreateFailure(fmt.Errorf("conpty: session %q already exists; destroy before re-creating", id))
	}

	r.mu.Lock()
	if _, dup := r.sessions[id]; dup {
		r.mu.Unlock()
		return ports.RuntimeHandle{}, conptyCreateFailure(fmt.Errorf("conpty: session %q already exists; destroy before re-creating", id))
	}
	reservation := &hostSession{
		addr: unresolvedHostAddress, launchID: cfg.Env[runtimeLaunchIDEnv], currentOwner: true,
	}
	r.sessions[id] = reservation
	r.mu.Unlock()
	if err := r.registerHost(ctx, ptyregistry.Entry{
		SessionID: id, PtyHostPID: 0, PipePath: unresolvedHostAddress,
		LaunchID: reservation.launchID, RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		r.mu.Lock()
		delete(r.sessions, id)
		r.mu.Unlock()
		return ports.RuntimeHandle{}, conptyCreateFailure(fmt.Errorf("conpty: reserve pty-host ownership for %q: %w", id, err))
	}

	addr, pid, err := r.spawner(ctx, id, cfg.WorkspacePath, cfg.Argv, cfg.Env,
		HostOptions{StartOnAttach: cfg.StartOnAttach, LazySurface: cfg.LazyStyledOutput, Cols: cfg.InitialCols, Rows: cfg.InitialRows})
	if err != nil {
		cause := fmt.Errorf("conpty: spawn pty-host for %q: %w", id, err)
		handle := ports.RuntimeHandle{ID: id}
		if addr == "" && pid == 0 {
			if unregisterErr := r.unregisterHost(ctx, id); unregisterErr != nil {
				cause = errors.Join(cause, fmt.Errorf("remove unused pty-host reservation for %q: %w", id, unregisterErr))
				return ports.RuntimeHandle{}, conptyPartialCreateFailure(cause, handle, ports.RuntimeCleanupFailed)
			}
			r.mu.Lock()
			delete(r.sessions, id)
			r.mu.Unlock()
			return ports.RuntimeHandle{}, conptyCreateFailure(cause)
		}
		if addr == "" && pid > 0 {
			sess := &hostSession{addr: unresolvedHostAddress, pid: pid, launchID: cfg.Env[runtimeLaunchIDEnv], currentOwner: true}
			r.mu.Lock()
			r.sessions[id] = sess
			r.mu.Unlock()
			registryErr := r.registerHost(ctx, ptyregistry.Entry{
				SessionID: id, PtyHostPID: pid, PipePath: unresolvedHostAddress,
				LaunchID: sess.launchID, RegisteredAt: time.Now().UTC().Format(time.RFC3339),
			})
			if registryErr != nil {
				cause = errors.Join(cause, fmt.Errorf("retain unresolved pty-host ownership for %q: %w", id, registryErr))
			}
			return ports.RuntimeHandle{}, conptyPartialCreateFailure(cause, handle, ports.RuntimeCleanupFailed)
		}
		if addr == "" || pid <= 0 {
			return ports.RuntimeHandle{}, conptyPartialCreateFailure(cause, handle, ports.RuntimeCleanupFailed)
		}
		r.mu.Lock()
		r.sessions[id] = &hostSession{addr: addr, pid: pid, launchID: cfg.Env[runtimeLaunchIDEnv], currentOwner: true}
		r.mu.Unlock()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		cleanupErr := r.Destroy(cleanupCtx, handle)
		cancel()
		if cleanupErr != nil {
			return ports.RuntimeHandle{}, conptyPartialCreateFailure(errors.Join(cause, cleanupErr), handle, ports.RuntimeCleanupFailed)
		}
		return ports.RuntimeHandle{}, conptyPartialCreateFailure(cause, handle, ports.RuntimeCleanupSucceeded)
	}

	sess := &hostSession{
		addr: addr, pid: pid, launchID: cfg.Env[runtimeLaunchIDEnv], currentOwner: true,
		protocolVersion: conPTYHostProtocolVersion, protocolResolved: true,
	}

	r.mu.Lock()
	r.sessions[id] = sess
	r.mu.Unlock()

	if err := r.registerHost(ctx, ptyregistry.Entry{
		SessionID:    id,
		PtyHostPID:   pid,
		PipePath:     addr,
		LaunchID:     sess.launchID,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		handle := ports.RuntimeHandle{ID: id}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		cleanupErr := r.Destroy(cleanupCtx, handle)
		cancel()
		cause := fmt.Errorf("conpty: register pty-host for %q: %w", id, err)
		if cleanupErr != nil {
			return ports.RuntimeHandle{}, conptyPartialCreateFailure(errors.Join(cause, cleanupErr), handle, ports.RuntimeCleanupFailed)
		}
		return ports.RuntimeHandle{}, conptyPartialCreateFailure(cause, handle, ports.RuntimeCleanupSucceeded)
	}

	return ports.RuntimeHandle{ID: id}, nil
}

func (r *Runtime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return errors.Join(ports.ErrRuntimeProbeInconclusive, fmt.Errorf("conpty: resolve runtime %q for destroy: %w", handle.ID, err))
	}
	if sess == nil {
		return nil
	}
	if sess.addr == unresolvedHostAddress && !sess.currentOwner {
		return fmt.Errorf("conpty: recovered unresolved ownership for %q cannot be safely killed by PID alone: %w", handle.ID, ports.ErrRuntimeProbeInconclusive)
	}

	var gracefulErr error
	if sess.addr != unresolvedHostAddress {
		gracefulErr = r.killHost(sess.addr)
	}
	exited, waitErr := r.waitForPIDExit(ctx, sess.pid)
	if waitErr != nil {
		return errors.Join(gracefulErr, waitErr)
	}

	var forceErr error
	if !exited {
		process, findErr := r.processFinder(sess.pid)
		if findErr != nil {
			forceErr = fmt.Errorf("find pty-host pid %d: %w", sess.pid, findErr)
		} else if err := process.Kill(); err != nil {
			forceErr = fmt.Errorf("force-kill pty-host pid %d: %w", sess.pid, err)
		}
		exited, waitErr = r.waitForPIDExit(ctx, sess.pid)
		if waitErr != nil {
			return errors.Join(gracefulErr, forceErr, waitErr)
		}
	}
	if !exited {
		return errors.Join(gracefulErr, forceErr, fmt.Errorf("conpty: pty-host pid %d is still alive after teardown", sess.pid))
	}

	if err := r.unregisterHost(ctx, handle.ID); err != nil {
		return fmt.Errorf("conpty: unregister destroyed session %q: %w", handle.ID, err)
	}

	r.mu.Lock()
	delete(r.sessions, handle.ID)
	r.mu.Unlock()
	return nil
}

func (r *Runtime) waitForPIDExit(ctx context.Context, pid int) (bool, error) {
	if pid <= 0 {
		return true, nil
	}
	if !r.pidIsAlive(pid) {
		return true, nil
	}
	wait := r.destroyWait
	if wait <= 0 {
		return false, nil
	}
	poll := r.destroyPoll
	if poll <= 0 {
		poll = 25 * time.Millisecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
			return !r.pidIsAlive(pid), nil
		case <-ticker.C:
			if !r.pidIsAlive(pid) {
				return true, nil
			}
		}
	}
}

func (r *Runtime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return false, err
	}
	if sess == nil {
		return false, nil
	}
	if sess.addr == unresolvedHostAddress {
		if sess.pid <= 0 || r.pidIsAlive(sess.pid) {
			return false, fmt.Errorf("conpty: pty-host pid %d started without a READY address: %w", sess.pid, ports.ErrRuntimeProbeInconclusive)
		}
		return false, nil
	}
	return clientIsAlive(sess.addr)
}

func (r *Runtime) IsChildAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return false, err
	}
	if sess == nil {
		return false, nil
	}
	if sess.addr == unresolvedHostAddress {
		if sess.pid <= 0 || r.pidIsAlive(sess.pid) {
			return false, fmt.Errorf("conpty: child status unavailable for unresolved pty-host pid %d: %w", sess.pid, ports.ErrRuntimeProbeInconclusive)
		}
		return false, nil
	}
	status, hostAlive, err := clientStatusContext(ctx, sess.addr)
	if err != nil || !hostAlive {
		return false, err
	}
	if !status.Alive && status.ExitCode == nil {
		return false, fmt.Errorf("conpty: child status lacks exit evidence: %w", ports.ErrRuntimeProbeInconclusive)
	}
	return status.Alive, nil
}

func (r *Runtime) IsUnsupervisedReviewerAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	return r.IsChildAlive(ctx, handle)
}

func (r *Runtime) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	if ref.Handle.ID == "" || ref.SessionID == "" || ref.Generation == "" || ref.Handle.ID != string(ref.SessionID) {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonIdentityMissing}
	}
	sess, err := r.resolveWithEvidence(ctx, ref.Handle.ID)
	if err != nil {
		reason := ports.FencedReasonRegistryUnreadable
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason = ports.FencedReasonProbeFailed
		} else if errors.Is(err, ptyregistry.ErrRegistryMalformed) {
			reason = ports.FencedReasonRegistryMalformed
		}
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: reason}
	}
	if sess == nil {
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	if sess.launchID == "" {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonIdentityMissing}
	}
	if sess.launchID != ref.Generation {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonGenerationMismatch}
	}
	if sess.addr == unresolvedHostAddress {
		if sess.pid <= 0 || r.pidIsAlive(sess.pid) {
			return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
		}
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	status, hostAlive, err := clientStatusContext(ctx, sess.addr)
	if err != nil || !hostAlive {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	if status.Alive {
		return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
	}
	return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
}

func (r *Runtime) IsSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return false, fmt.Errorf("conpty: resolve supervised runtime %q: %w", handle.ID, err)
	}
	if sess == nil {
		return false, nil
	}
	if ref.SessionID != "" && string(ref.SessionID) != handle.ID {
		return false, nil
	}
	if ref.LaunchID != "" && (sess.launchID == "" || sess.launchID != ref.LaunchID) {
		return false, nil
	}
	status, hostAlive, err := clientStatus(sess.addr)
	if err != nil {
		return false, err
	}
	if !hostAlive {
		return false, nil
	}
	return status.Alive, nil
}

func (r *Runtime) IsExactSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	if ref.SessionID == "" || ref.LaunchID == "" {
		return false, errors.New("conpty: exact supervisor session and launch are required")
	}
	return r.IsSupervisedProcessAlive(ctx, handle, ref)
}

func (r *Runtime) ProcessRootPIDs(ctx context.Context, handle ports.RuntimeHandle) ([]int, error) {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return nil, fmt.Errorf("conpty: resolve runtime %q: %w", handle.ID, err)
	}
	if sess == nil || sess.pid <= 0 {
		return nil, nil
	}
	return []int{sess.pid}, nil
}

func (r *Runtime) ServerPID(ctx context.Context) (int, bool) {
	return 0, false
}

func (r *Runtime) HasSupervisedProcessRecord(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return false, fmt.Errorf("conpty: resolve supervised runtime %q: %w", handle.ID, err)
	}
	return sess != nil && strings.TrimSpace(sess.launchID) != "", nil
}

func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return fmt.Errorf("conpty: resolve runtime %q for message: %w", handle.ID, err)
	}
	if sess == nil {
		return fmt.Errorf("conpty: session %q not found", handle.ID)
	}
	return clientSendMessage(sess.addr, message)
}

func (r *Runtime) Interrupt(ctx context.Context, handle ports.RuntimeHandle) error {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return fmt.Errorf("conpty: resolve runtime %q for interrupt: %w", handle.ID, err)
	}
	if sess == nil {
		return fmt.Errorf("conpty: session %q not found", handle.ID)
	}
	return clientSendInput(sess.addr, "\x03")
}

func (r *Runtime) SendInput(ctx context.Context, handle ports.RuntimeHandle, input string) error {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return fmt.Errorf("conpty: resolve runtime %q for input: %w", handle.ID, err)
	}
	if sess == nil {
		return fmt.Errorf("conpty: session %q not found", handle.ID)
	}
	return clientSendInput(sess.addr, input)
}

func (r *Runtime) GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	if lines <= 0 {
		return "", fmt.Errorf("conpty: lines must be > 0")
	}
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return "", fmt.Errorf("conpty: resolve runtime %q for output: %w", handle.ID, err)
	}
	if sess == nil {
		return "", fmt.Errorf("conpty: session %q not found", handle.ID)
	}
	return clientGetOutput(ctx, sess.addr, lines)
}

func (r *Runtime) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	if lines <= 0 {
		return "", fmt.Errorf("conpty: lines must be > 0")
	}
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return "", fmt.Errorf("conpty: resolve runtime %q for styled output: %w", handle.ID, err)
	}
	if sess == nil {
		return "", fmt.Errorf("conpty: session %q not found", handle.ID)
	}
	protocolVersion, err := r.resolveHostProtocol(ctx, sess)
	if err != nil {
		return "", err
	}
	if protocolVersion < conPTYStyledOutputProtocolVersion {
		return "", fmt.Errorf("conpty: pty-host protocol version %d: %w",
			protocolVersion, ports.ErrStyledTerminalOutputUnavailable)
	}
	return clientGetStyledOutput(ctx, sess.addr, lines)
}

func (r *Runtime) resolveHostProtocol(ctx context.Context, sess *hostSession) (int, error) {
	r.mu.Lock()
	if sess.protocolResolved {
		version := sess.protocolVersion
		r.mu.Unlock()
		return version, nil
	}
	r.mu.Unlock()

	status, hostAlive, err := clientStatusContext(ctx, sess.addr)
	if err != nil {
		return 0, fmt.Errorf("conpty: negotiate pty-host protocol: %w", err)
	}
	if !hostAlive {
		return 0, errors.New("conpty: negotiate pty-host protocol: host is not reachable")
	}

	r.mu.Lock()
	if !sess.protocolResolved {
		sess.protocolVersion = status.ProtocolVersion
		sess.protocolResolved = true
	}
	version := sess.protocolVersion
	r.mu.Unlock()
	return version, nil
}

func (r *Runtime) resolveWithEvidence(ctx context.Context, id string) (*hostSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	sess, exists := r.sessions[id]
	r.mu.Unlock()
	if sess != nil {
		return sess, nil
	}
	if exists {
		return nil, errors.New("conpty: runtime creation is still unresolved")
	}

	entries, complete, err := ptyregistry.Scan(ctx)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, errors.New("conpty: pty registry scan incomplete")
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.SessionID != id {
			continue
		}
		recovered := &hostSession{addr: e.PipePath, pid: e.PtyHostPID, launchID: e.LaunchID}
		r.mu.Lock()
		if r.sessions[id] == nil {
			r.sessions[id] = recovered
		} else {
			recovered = r.sessions[id]
		}
		r.mu.Unlock()
		return recovered, nil
	}
	return nil, nil
}

func findProcess(pid int) (processKiller, error) {
	p, err := osProcessFinder(pid)
	return p, err
}

type processKiller interface {
	Kill() error
}

var osProcessFinder = defaultOSProcessFinder

var _ ports.TerminalForegroundWatcher = (*Runtime)(nil)

func (r *Runtime) WatchTerminalForeground(ctx context.Context, handle ports.RuntimeHandle, fn func(ports.TerminalForeground)) error {
	if goruntime.GOOS == "windows" {
		return fmt.Errorf("conpty: no foreground process group on windows: %w", ports.ErrTerminalForegroundUnavailable)
	}
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return fmt.Errorf("conpty: resolve runtime %q for foreground: %w", handle.ID, err)
	}
	if sess == nil {
		return fmt.Errorf("conpty: session %q not found: %w", handle.ID, ports.ErrTerminalForegroundUnavailable)
	}
	protocolVersion, err := r.resolveHostProtocol(ctx, sess)
	if err != nil {
		return err
	}
	if protocolVersion < conPTYForegroundProtocolVersion {
		return fmt.Errorf("conpty: pty-host protocol version %d: %w", protocolVersion, ports.ErrTerminalForegroundUnavailable)
	}
	return clientWatchForeground(ctx, sess.addr, func(state ForegroundPayload) {
		fn(ports.TerminalForeground{PGID: state.PGID, Argv: state.Argv, Busy: state.Busy, EchoOnly: state.EchoOnly})
	})
}

var _ ports.TerminalTitleWatcher = (*Runtime)(nil)

func (r *Runtime) WatchTerminalTitle(ctx context.Context, handle ports.RuntimeHandle, fn func(string)) error {
	sess, err := r.resolveWithEvidence(ctx, handle.ID)
	if err != nil {
		return fmt.Errorf("conpty: resolve runtime %q for title: %w", handle.ID, err)
	}
	if sess == nil {
		return fmt.Errorf("conpty: session %q not found: %w", handle.ID, ports.ErrTerminalTitleUnavailable)
	}
	protocolVersion, err := r.resolveHostProtocol(ctx, sess)
	if err != nil {
		return err
	}
	if protocolVersion < conPTYTitleProtocolVersion {
		return fmt.Errorf("conpty: pty-host protocol version %d: %w", protocolVersion, ports.ErrTerminalTitleUnavailable)
	}
	return clientWatchTitle(ctx, sess.addr, func(state TitlePayload) { fn(state.Title) })
}
