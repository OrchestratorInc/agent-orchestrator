// Package persistenthost keeps a provider stdio process alive while AO's daemon
// is replaced. Raw protocols are forwarded unchanged. ACP uses a small relay
// that preserves connection-scoped initialization, session, request-correlation,
// and in-flight prompt state across daemon attachments.
package persistenthost

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

const (
	// ProtocolVersion fences daemon/host control-plane compatibility.
	ProtocolVersion  = 1
	maxDetachedBytes = 32 << 20
	startupTimeout   = 10 * time.Second
	// Host hello is local control-plane I/O and must not stall daemon recovery.
	handshakeTimeout = time.Second
)

// Protocol selects the provider-wire behavior owned by the host.
type Protocol string

const (
	// ProtocolRaw preserves the original newline-delimited forwarding profile.
	ProtocolRaw Protocol = ""
	// ProtocolACP enables the host-owned ACP correlation and replay profile.
	ProtocolACP Protocol = "acp"
	// ProtocolCodex retains conversation notifications until durable projection.
	ProtocolCodex Protocol = "codex"
	// ProtocolUnreal preserves Unreal Agent's AO-private JSONL stream while
	// fencing ownership with the same stable launch identity used by ACP hosts.
	ProtocolUnreal Protocol = "unreal"

	// ACPPromptResultMethod is an AO-private notification emitted when an ACP
	// prompt finishes after the daemon attachment that issued it has gone away.
	ACPPromptResultMethod = "_ao/persistent_prompt_result"
	// ACPPromptAckMethod acknowledges that AO durably committed a replayed prompt
	// result, allowing the host to discard its prompt journal.
	ACPPromptAckMethod = "_ao/persistent_prompt_ack"
	// ACPInteractionCommandMethod records a user interaction decision in the host
	// before the daemon releases the blocked ACP responder.
	ACPInteractionCommandMethod = "_ao/persistent_interaction_command"
	// ACPRequestIDMetaKey carries one host-stable identity on replayable
	// provider-to-client requests such as permissions and elicitations.
	ACPRequestIDMetaKey = "ao.persistentRequestId"
	// ACPInitialPermissionsMetaKey records the process-launch approval policy,
	// which can differ from mutable session settings after reconnect.
	ACPInitialPermissionsMetaKey = "ao.initialPermissions"
	// CodexEventMethod wraps one replayable conversation notification.
	CodexEventMethod = "_ao/persistent_codex_event"
	// CodexAckMethod acknowledges durable projection of a retained notification.
	CodexAckMethod = "_ao/persistent_codex_ack"
	// CodexReplayMethod queries fresh state as a request, or opens replay as a
	// notification after durable turn ownership is restored.
	CodexReplayMethod = "_ao/persistent_codex_replay"
)

// CodexEvent is an AO-private wrapper; Frame remains the native provider frame.
type CodexEvent struct {
	EventID    string          `json:"eventId"`
	Frame      json.RawMessage `json:"frame"`
	Replayed   bool            `json:"replayed,omitempty"`
	ObservedAt time.Time       `json:"observedAt"`
}

// CodexState preserves native activity without replaying an obsolete start.
type CodexState struct {
	ActiveTurns   map[string]string            `json:"activeTurns,omitempty"`
	RetainedTurns map[string][]string          `json:"retainedTurns,omitempty"`
	AcceptedTurns map[string]map[string]string `json:"acceptedTurns,omitempty"`
}

var (
	// ErrAttached reports that another daemon controller owns the host.
	ErrAttached = errors.New("chat host already has a controller")
	// ErrUnauthorized reports an invalid host capability.
	ErrUnauthorized = errors.New("chat host authentication failed")
	// ErrIncompatible reports a control-plane or provider-ownership mismatch.
	ErrIncompatible = errors.New("chat host incompatible")
	// ErrHostExists reports an atomic per-session launch-lock conflict.
	ErrHostExists = errors.New("chat host already exists")
	// ErrOwnershipInconclusive means a descriptor or live host exists, but this
	// client could not prove that it is safe to replace. Callers must preserve the
	// durable session rather than treating the failed attachment as provider death.
	ErrOwnershipInconclusive = errors.New("chat host ownership is inconclusive")
	// ErrNotRunning means a reconnect-only probe found no surviving host.
	ErrNotRunning = errors.New("chat host is not running")
)

// Descriptor is the private connection record published by a running host.
type Descriptor struct {
	Version              int       `json:"version"`
	SessionID            string    `json:"sessionId"`
	Protocol             Protocol  `json:"protocol,omitempty"`
	OwnershipFingerprint string    `json:"ownershipFingerprint,omitempty"`
	Address              string    `json:"address"`
	Token                string    `json:"token"`
	PID                  int       `json:"pid"`
	StartedAt            time.Time `json:"startedAt"`
}

// Config identifies one provider process and its AO session ownership.
type Config struct {
	ReconnectOnly        bool
	SessionID            string
	DataDir              string
	Workdir              string
	Env                  []string
	Argv                 []string
	Protocol             Protocol
	OwnershipFingerprint string
	Prepare              func(context.Context) (PreparedProvider, error)
}

// PreparedProvider is resolved only when no compatible live host can be
// adopted. This keeps launch-only credential rotation out of reconnects.
type PreparedProvider struct {
	Env  []string
	Argv []string
}

// ACPState is the connection-scoped state needed to reconstruct an ACP client
// without initializing or resuming the same live provider a second time.
type ACPState struct {
	InitialPermissions   string          `json:"initialPermissions,omitempty"`
	InitializeResult     json.RawMessage `json:"initializeResult,omitempty"`
	SessionResult        json.RawMessage `json:"sessionResult,omitempty"`
	SessionID            string          `json:"sessionId,omitempty"`
	ActivePrompt         bool            `json:"activePrompt,omitempty"`
	ActiveCompaction     bool            `json:"activeCompaction,omitempty"`
	PendingResultEventID string          `json:"pendingResultEventId,omitempty"`
}

// Transport is one authenticated attachment to a persistent provider host.
type Transport struct {
	Stdin         io.WriteCloser
	Stdout        io.Reader
	Reconnected   bool
	NextRequestID int64
	ACPState      *ACPState
	CodexReplay   bool
	CodexState    *CodexState
}

type hello struct {
	Version     int    `json:"version"`
	Token       string `json:"token"`
	Action      string `json:"action"`
	CodexReplay bool   `json:"codexReplay,omitempty"`
}

type helloResponse struct {
	OK            bool        `json:"ok"`
	Error         string      `json:"error,omitempty"`
	NextRequestID int64       `json:"nextRequestId,omitempty"`
	ACPState      *ACPState   `json:"acpState,omitempty"`
	CodexReplay   bool        `json:"codexReplay,omitempty"`
	CodexState    *CodexState `json:"codexState,omitempty"`
}

func hostDir(dataDir, sessionID string) (string, error) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\\`) {
		return "", errors.New("invalid chat host session id")
	}
	if !filepath.IsAbs(dataDir) {
		return "", errors.New("chat host data dir must be absolute")
	}
	return filepath.Join(dataDir, "chat-hosts", sessionID), nil
}

func descriptorPath(dataDir, sessionID string) (string, error) {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "host.json"), nil
}

func lockPath(dataDir, sessionID string) (string, error) {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "host.lock"), nil
}

func acquireHostLock(dataDir, sessionID string) (func(), error) {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // Directories need execute permission; access is owner-only.
		return nil, err
	}
	path, _ := lockPath(dataDir, sessionID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // AO-owned capability directory.
	if errors.Is(err, os.ErrExist) {
		owner, readErr := os.ReadFile(path) //nolint:gosec // AO-owned path derived from validated session id.
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(owner)))
		if readErr != nil || parseErr != nil || pid <= 0 || processalive.Alive(pid) {
			return nil, ErrHostExists
		}
		// A host can die after atomically creating its launch lock but before it
		// publishes host.json. Reclaim only a lock whose recorded owner is proven
		// dead, then retry the same O_EXCL acquisition once.
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, ErrHostExists
		}
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // AO-owned capability directory.
		if errors.Is(err, os.ErrExist) {
			return nil, ErrHostExists
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

func readDescriptor(dataDir, sessionID string) (Descriptor, error) {
	path, err := descriptorPath(dataDir, sessionID)
	if err != nil {
		return Descriptor{}, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // AO-owned path derived from validated session id.
	if err != nil {
		return Descriptor{}, err
	}
	var d Descriptor
	if err := json.Unmarshal(b, &d); err != nil {
		return Descriptor{}, fmt.Errorf("decode chat host descriptor: %w", err)
	}
	if d.SessionID != sessionID || d.Address == "" || d.Token == "" {
		return Descriptor{}, errors.New("invalid chat host descriptor")
	}
	return d, nil
}

func writeDescriptor(dataDir string, d Descriptor) error {
	dir, err := hostDir(dataDir, d.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // Directories need execute permission; access is owner-only.
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".host.json.tmp-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	path := filepath.Join(dir, "host.json")
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0o600)
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validateDescriptor(cfg Config, d Descriptor) error {
	legacyCodex := cfg.Protocol == ProtocolCodex && d.Protocol == ProtocolRaw
	if d.Protocol != cfg.Protocol && !legacyCodex {
		return fmt.Errorf("%w: host protocol=%q requested=%q", ErrIncompatible, d.Protocol, cfg.Protocol)
	}
	// Codex raw hosts predate this ACP compatibility contract and retain their
	// existing version/protocol fencing.
	if d.Protocol == ProtocolRaw {
		return nil
	}
	if cfg.OwnershipFingerprint == "" || d.OwnershipFingerprint != cfg.OwnershipFingerprint {
		return fmt.Errorf("%w: provider launch configuration changed", ErrIncompatible)
	}
	return nil
}

// ConnectOrStart attaches to an existing compatible host or starts one with the
// current AO executable. Failed/incompatible probes never terminate that host.
func ConnectOrStart(ctx context.Context, cfg Config) (*Transport, error) {
	if d, err := readDescriptor(cfg.DataDir, cfg.SessionID); err == nil {
		if err := validateDescriptor(cfg, d); err != nil && processalive.Alive(d.PID) {
			return nil, err
		}
		transport, attachErr := attach(ctx, d, true)
		if attachErr == nil {
			return transport, nil
		}
		if errors.Is(attachErr, ErrAttached) {
			// Desktop updater handoff can briefly start the replacement daemon
			// before the old controller has detached. Wait for exclusive ownership;
			// never turn that overlap into a competing provider process.
			deadline := time.Now().Add(startupTimeout)
			for time.Now().Before(deadline) && processalive.Alive(d.PID) {
				timer := time.NewTimer(20 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
				transport, attachErr = attach(ctx, d, true)
				if attachErr == nil {
					return transport, nil
				}
				if !errors.Is(attachErr, ErrAttached) {
					break
				}
			}
		}
		// A failed socket probe is not proof that a provider is dead. Only an OS
		// process observation permits clearing the descriptor and falling back to
		// native resume in a new host.
		if processalive.Alive(d.PID) {
			return nil, fmt.Errorf("%w: %w", ErrOwnershipInconclusive, attachErr)
		}
		// Do not remove stale ownership files here. Another starter may have
		// already reclaimed the dead lock and published a replacement descriptor
		// since this client read d. The detached host's O_EXCL lock acquisition is
		// the single authority for reclaiming dead ownership.
	} else if !errors.Is(err, os.ErrNotExist) {
		// A malformed or unreadable ownership record is not proof that no host
		// exists. Fail closed instead of launching a competing process.
		return nil, fmt.Errorf("%w: %w", ErrOwnershipInconclusive, err)
	}
	if cfg.ReconnectOnly {
		return nil, ErrNotRunning
	}
	if !filepath.IsAbs(cfg.Workdir) {
		return nil, errors.New("chat host start requires an absolute workdir")
	}
	if cfg.Prepare != nil {
		prepared, err := cfg.Prepare(ctx)
		if err != nil {
			return nil, err
		}
		cfg.Env = prepared.Env
		cfg.Argv = prepared.Argv
	}
	if len(cfg.Argv) == 0 {
		return nil, errors.New("chat host start requires provider argv")
	}
	if err := spawnDetached(ctx, cfg); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(startupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, err := readDescriptor(cfg.DataDir, cfg.SessionID)
		if err == nil {
			if compatibleErr := validateDescriptor(cfg, d); compatibleErr != nil {
				return nil, compatibleErr
			}
			transport, attachErr := attach(ctx, d, false)
			if attachErr == nil {
				return transport, nil
			}
			lastErr = attachErr
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d, readErr := readDescriptor(cfg.DataDir, cfg.SessionID); readErr == nil && processalive.Alive(d.PID) {
		return nil, fmt.Errorf("%w: %w", ErrOwnershipInconclusive, lastErr)
	}
	return nil, fmt.Errorf("start chat host: %w", lastErr)
}

// Reconcile terminates compatible authenticated hosts whose durable session no
// longer exists. Live-session hosts, incompatible hosts, and inconclusive probes
// are preserved.
func Reconcile(ctx context.Context, dataDir string, keep map[string]struct{}) error {
	root := filepath.Join(dataDir, "chat-hosts")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		if _, ok := keep[sessionID]; ok {
			continue
		}
		d, readErr := readDescriptor(dataDir, sessionID)
		if readErr != nil {
			continue
		}
		if d.Version != ProtocolVersion {
			continue
		}
		if !processalive.Alive(d.PID) {
			// Claim the launch lock before cleaning a dead descriptor. If a new host
			// already owns it, preserve its descriptor instead of deleting ownership
			// state observed by a stale reconciler.
			release, lockErr := acquireHostLock(dataDir, sessionID)
			if lockErr != nil {
				continue
			}
			path, _ := descriptorPath(dataDir, sessionID)
			_ = os.Remove(path)
			release()
			continue
		}
		if shutdownErr := Shutdown(ctx, dataDir, sessionID); shutdownErr != nil {
			errs = append(errs, fmt.Errorf("reap chat host %s: %w", sessionID, shutdownErr))
		}
	}
	return errors.Join(errs...)
}

func attach(ctx context.Context, d Descriptor, reconnected bool) (*Transport, error) {
	if d.Version != ProtocolVersion {
		return nil, fmt.Errorf("%w: host=%d daemon=%d", ErrIncompatible, d.Version, ProtocolVersion)
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(handshakeCtx, "tcp", d.Address)
	if err != nil {
		return nil, err
	}
	finishHandshake := bindConnToContext(handshakeCtx, conn)
	reader := bufio.NewReader(conn)
	if err := json.NewEncoder(conn).Encode(hello{Version: ProtocolVersion, Token: d.Token, Action: "attach", CodexReplay: d.Protocol == ProtocolCodex}); err != nil {
		_ = conn.Close()
		return nil, finishHandshake(err)
	}
	var response helloResponse
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return nil, finishHandshake(err)
	}
	if err := finishHandshake(nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := json.Unmarshal(line, &response); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !response.OK {
		_ = conn.Close()
		switch response.Error {
		case ErrAttached.Error():
			return nil, ErrAttached
		case ErrUnauthorized.Error():
			return nil, ErrUnauthorized
		case ErrIncompatible.Error():
			return nil, ErrIncompatible
		default:
			return nil, errors.New(response.Error)
		}
	}
	if d.Protocol == ProtocolCodex && !response.CodexReplay {
		_ = conn.Close()
		return nil, ErrIncompatible
	}
	return &Transport{
		Stdin: conn, Stdout: reader, Reconnected: reconnected,
		NextRequestID: response.NextRequestID, ACPState: response.ACPState, CodexReplay: response.CodexReplay, CodexState: response.CodexState,
	}, nil
}

func bindConnToContext(ctx context.Context, conn net.Conn) func(error) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func(err error) error {
		stop()
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = conn.Close()
			return ctxErr
		}
		return err
	}
}

// HostPID returns the live provider host pid recorded for sessionID, for
// memory accounting of runtime-less Chat sessions. A missing descriptor or an
// exited host reports false; nothing is ever started or stopped here.
func HostPID(dataDir, sessionID string) (int, bool) {
	d, err := readDescriptor(dataDir, sessionID)
	if err != nil || d.PID <= 0 || !processalive.Alive(d.PID) {
		return 0, false
	}
	return d.PID, true
}

// Shutdown terminates current session ownership and waits for it to end.
// Missing/dead hosts are harmless; unknown live owners fail closed.
// The protocol acknowledgement only confirms that shutdown was requested.
func Shutdown(ctx context.Context, dataDir, sessionID string) error {
	d, err := readDescriptor(dataDir, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Version != ProtocolVersion {
		return ErrIncompatible
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(handshakeCtx, "tcp", d.Address)
	if err != nil {
		if !processalive.Alive(d.PID) {
			return nil
		}
		return err
	}
	defer func() { _ = conn.Close() }()
	finishHandshake := bindConnToContext(handshakeCtx, conn)
	if err := json.NewEncoder(conn).Encode(hello{Version: ProtocolVersion, Token: d.Token, Action: "shutdown"}); err != nil {
		return finishHandshake(err)
	}
	var response helloResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return finishHandshake(err)
	}
	if err := finishHandshake(nil); err != nil {
		return err
	}
	if !response.OK {
		return errors.New(response.Error)
	}
	// The host's own shutdown can take about 6 s: 3 s for the provider to exit
	// once stdin closes, a 1 s reap, and 2 s for its process group to exit on
	// SIGTERM before SIGKILL. Wait past that, with room for a loaded machine.
	waitCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, readErr := readDescriptor(dataDir, sessionID)
		if readErr == nil && current.Token != d.Token {
			return nil // A new owner could only publish after the old lock released.
		}
		if errors.Is(readErr, os.ErrNotExist) {
			path, _ := lockPath(dataDir, sessionID)
			if _, lockErr := os.Stat(path); errors.Is(lockErr, os.ErrNotExist) {
				return nil
			} else if lockErr != nil {
				return lockErr
			}
		} else if readErr != nil {
			return readErr
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("wait for chat host shutdown: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

// Run owns the provider until it exits or an authenticated shutdown arrives.
func Run(ctx context.Context, cfg Config) error {
	if len(cfg.Argv) == 0 || !filepath.IsAbs(cfg.Workdir) {
		return errors.New("chat host requires provider argv and absolute workdir")
	}
	releaseLock, err := acquireHostLock(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return err
	}
	defer releaseLock()
	token, err := randomToken()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	child := aoprocess.Command(cfg.Argv[0], cfg.Argv[1:]...) //nolint:gosec // provider argv is constructed by AO's driver.
	child.Dir = cfg.Workdir
	child.Env = cfg.Env
	configureProviderProcess(child)
	stdin, err := child.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		return err
	}
	child.Stderr = io.Discard
	closeProviderTree, err := startProviderProcess(child)
	if err != nil {
		return err
	}
	// The job owns descendants even if the direct provider exits first. Close
	// it before releasing the host lock so Shutdown follows the kill request.
	defer closeProviderTree()

	d := Descriptor{
		Version: ProtocolVersion, SessionID: cfg.SessionID, Protocol: cfg.Protocol,
		OwnershipFingerprint: cfg.OwnershipFingerprint,
		Address:              listener.Addr().String(), Token: token, PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	if err := writeDescriptor(cfg.DataDir, d); err != nil {
		_ = killProviderProcess(context.WithoutCancel(ctx), child)
		return err
	}
	path, _ := descriptorPath(cfg.DataDir, cfg.SessionID)
	defer func() { _ = os.Remove(path) }()

	h := &host{
		ctx: ctx, listener: listener, child: child, stdin: stdin, token: token,
		detached: make([][]byte, 0, 64), pendingRequests: make(map[string]*pendingRequest),
		shutdown: make(chan struct{}),
	}
	if cfg.Protocol == ProtocolACP {
		h.acp, err = newACPRelay(ctx, filepath.Join(filepath.Dir(path), "acp-prompt.journal"))
		if err != nil {
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
			return err
		}
		defer func() { _ = h.acp.close(context.WithoutCancel(ctx)) }()
	}
	if cfg.Protocol == ProtocolCodex {
		h.codex, err = newCodexRelay(ctx, filepath.Join(filepath.Dir(path), "codex-events.journal"))
		if err != nil {
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
			return err
		}
		defer func() { h.stopCodex(); _ = h.codex.journal.close(context.WithoutCancel(ctx)) }()
	}
	h.cond = sync.NewCond(&h.mu)
	providerDone := make(chan error, 1)
	go func() { providerDone <- h.forwardProvider(stdout) }()
	if h.acp != nil {
		watchCtx, stopWatch := context.WithCancel(ctx)
		defer stopWatch()
		go h.watchPromptStall(watchCtx)
	}
	acceptDone := make(chan error, 1)
	go func() { acceptDone <- h.accept() }()

	wait := sync.OnceValue(child.Wait)
	// reapProvider waits up to a second for the provider to exit once its
	// output ended, so its zombie does not hold the process group open while
	// the group is asked to exit. A provider still running is left to the kill.
	reapProvider := func() {
		reaped := make(chan struct{})
		go func() { _ = wait(); close(reaped) }()
		select {
		case <-reaped:
		case <-time.After(time.Second):
		}
	}
	stopProvider := func() {
		h.stopCodex()
		_ = stdin.Close()
		select {
		case <-providerDone:
			// Wrapper adapters may exit before their provider child. The hosted
			// process tree is the ownership boundary, so explicit shutdown reaps
			// any descendant that did not follow stdin closure.
			reapProvider()
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
		case <-time.After(3 * time.Second):
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
		}
	}
	var runErr error
	select {
	case runErr = <-providerDone:
		reapProvider()
		_ = killProviderProcess(context.WithoutCancel(ctx), child)
	case <-h.shutdown:
		stopProvider()
	case <-ctx.Done():
		runErr = ctx.Err()
		stopProvider()
	case err := <-acceptDone:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
			return err
		}
	}
	_ = listener.Close()
	_ = wait()
	return runErr
}

type host struct {
	ctx      context.Context
	listener net.Listener
	child    *exec.Cmd
	stdin    io.WriteCloser
	token    string
	acp      *acpRelay
	codex    *codexRelay

	mu               sync.Mutex
	cond             *sync.Cond
	client           net.Conn
	clientGeneration uint64
	detached         [][]byte
	detachedBytes    int
	pendingRequests  map[string]*pendingRequest
	pendingOrder     []string
	maxRequestID     int64
	shutdown         chan struct{}
	shutdownOnce     sync.Once
}

type pendingRequest struct {
	frame    []byte
	buffered bool
}

func (h *host) accept() error {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			return err
		}
		go h.handle(conn)
	}
}

func (h *host) handle(conn net.Conn) {
	reader := bufio.NewReader(conn)
	var request hello
	if err := json.NewDecoder(reader).Decode(&request); err != nil {
		_ = conn.Close()
		return
	}
	if request.Version != ProtocolVersion {
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrIncompatible.Error()})
		_ = conn.Close()
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Token), []byte(h.token)) != 1 {
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrUnauthorized.Error()})
		_ = conn.Close()
		return
	}
	if request.Action == "shutdown" {
		_ = json.NewEncoder(conn).Encode(helloResponse{OK: true})
		_ = conn.Close()
		h.shutdownOnce.Do(func() { close(h.shutdown) })
		return
	}
	if request.Action != "attach" {
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: "unknown chat host action"})
		_ = conn.Close()
		return
	}
	if h.codex != nil && !request.CodexReplay {
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrIncompatible.Error()})
		_ = conn.Close()
		return
	}

	h.mu.Lock()
	if h.client != nil || (h.codex != nil && h.codex.stopped) {
		h.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrAttached.Error()})
		_ = conn.Close()
		return
	}
	h.client = conn
	h.clientGeneration++
	generation := h.clientGeneration
	response := helloResponse{OK: true, NextRequestID: h.maxRequestID}
	var attachment *codexAttachment
	var writeErr error
	if h.codex != nil {
		attachment = &codexAttachment{conn: conn, cutoff: h.codex.next, inFlight: make(map[string]int64)}
		h.codex.attachment = attachment
		response.CodexReplay, response.CodexState = true, h.codex.snapshot()
		h.mu.Unlock()
		writeErr = json.NewEncoder(conn).Encode(response)
		if writeErr == nil {
			// Independent socket I/O lets the reader accept ACKs during replay.
			go h.writeCodex(attachment)
		}
	} else {
		if h.acp != nil {
			response.ACPState = h.acp.snapshot()
		}
		writeErr = json.NewEncoder(conn).Encode(response)
		if writeErr == nil && h.acp != nil {
			writeErr = h.acp.replayTo(h.ctx, conn)
		}
		if writeErr == nil {
			for _, frame := range h.detached {
				if _, writeErr = conn.Write(frame); writeErr != nil {
					break
				}
			}
		}
		if writeErr == nil {
			h.detached = h.detached[:0]
			h.detachedBytes = 0
			for _, pending := range h.pendingRequests {
				pending.buffered = false
			}
			h.cond.Broadcast()
		}
		h.mu.Unlock()
	}
	if writeErr != nil {
		h.detach(conn)
		return
	}

	defer h.detach(conn)
	for {
		frame, readErr := reader.ReadBytes('\n')
		if len(frame) > 0 {
			providerFrame := frame
			var clientFrame []byte
			var relayErr error
			var startRequestID string
			h.mu.Lock()
			if h.codex != nil {
				if h.codex.attachment != attachment {
					h.mu.Unlock()
					return
				}
				var control struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params struct {
						EventID  string `json:"eventId"`
						ThreadID string `json:"threadId"`
					} `json:"params"`
				}
				_ = json.Unmarshal(frame, &control)
				switch control.Method {
				case CodexReplayMethod:
					providerFrame = nil
					if len(control.ID) > 0 {
						attachment.stateRequest = &codexStateRequest{id: control.ID, threadID: control.Params.ThreadID}
						h.cond.Broadcast()
					} else if !attachment.open {
						attachment.open = true
						for _, entry := range h.codex.entries {
							if entry.request != "" {
								if pending := h.pendingRequests[entry.request]; pending != nil {
									attachment.queue = append(attachment.queue, codexOutbound{frame: pending.frame})
									attachment.directBytes += len(pending.frame)
								}
							} else {
								attachment.queue = append(attachment.queue, codexOutbound{entry: entry, replayed: entry.sequence <= attachment.cutoff})
							}
						}
						h.cond.Broadcast()
					}
				case CodexAckMethod:
					providerFrame = nil
					if size, sent := attachment.inFlight[control.Params.EventID]; sent {
						delete(attachment.inFlight, control.Params.EventID)
						attachment.inFlightBytes -= size
						relayErr = h.codex.acknowledge(h.ctx, control.Params.EventID)
					}
					h.cond.Broadcast()
				}
				if len(providerFrame) > 0 {
					startRequestID = h.observeCodexRequestLocked(providerFrame)
				}
			} else if h.acp != nil {
				var relayed acpClientFrames
				relayed, relayErr = h.acp.clientFrame(h.ctx, frame, generation)
				providerFrame = relayed.provider
				clientFrame = relayed.client
				if relayErr != nil {
					h.shutdownOnce.Do(func() { close(h.shutdown) })
				}
			}
			h.mu.Unlock()
			if relayErr != nil {
				return
			}
			if len(providerFrame) > 0 {
				if written, err := h.stdin.Write(providerFrame); err != nil {
					readErr = err
					if written == 0 && startRequestID != "" {
						h.mu.Lock()
						_ = h.codex.resolveStart(h.ctx, startRequestID, "")
						h.cond.Broadcast()
						h.mu.Unlock()
					}
				} else {
					h.observeClientFrame(providerFrame)
				}
			}
			if readErr == nil && len(clientFrame) > 0 {
				if _, err := conn.Write(clientFrame); err != nil {
					readErr = err
				}
			}
		}
		if readErr != nil {
			return
		}
	}
}

func (h *host) observeClientFrame(frame []byte) {
	var value struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(frame, &value) != nil || len(value.ID) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if value.Method != "" {
		var id int64
		if json.Unmarshal(value.ID, &id) == nil && id > h.maxRequestID {
			h.maxRequestID = id
		}
		return
	}
	requestID := string(value.ID)
	if _, ok := h.pendingRequests[requestID]; ok {
		delete(h.pendingRequests, requestID)
		for i, id := range h.pendingOrder {
			if id == requestID {
				h.pendingOrder = append(h.pendingOrder[:i], h.pendingOrder[i+1:]...)
				break
			}
		}
		if h.codex != nil {
			for i, entry := range h.codex.entries {
				if entry.request == requestID {
					copy(h.codex.entries[i:], h.codex.entries[i+1:])
					h.codex.entries[len(h.codex.entries)-1] = nil
					h.codex.entries = h.codex.entries[:len(h.codex.entries)-1]
					break
				}
			}
		}
	}
}

func (h *host) detach(conn net.Conn) {
	h.mu.Lock()
	if h.client == conn {
		h.client = nil
		if h.codex != nil {
			h.codex.attachment = nil
			h.cond.Broadcast()
		} else {
			h.bufferPendingRequestsLocked()
		}
	}
	h.mu.Unlock()
	_ = conn.Close()
}

// deliverProviderFrameLocked routes one adapter frame to the attached
// controller, or buffers it for replay while detached. Callers must hold
// h.mu. retainedByACP marks frames the ACP relay owns for its own replay, so
// they must not also enter the generic detached buffer.
func (h *host) deliverProviderFrameLocked(frame []byte, retainedByACP bool) {
	if requestID, ok := serverRequestID(frame); ok && !retainedByACP {
		if _, exists := h.pendingRequests[requestID]; !exists {
			h.pendingRequests[requestID] = &pendingRequest{frame: append([]byte(nil), frame...)}
			h.pendingOrder = append(h.pendingOrder, requestID)
		}
	}
	for h.client == nil && h.detachedBytes+len(frame) > maxDetachedBytes {
		h.cond.Wait()
	}
	if h.client != nil {
		if _, writeErr := h.client.Write(frame); writeErr != nil {
			_ = h.client.Close()
			h.client = nil
			h.bufferPendingRequestsLocked()
			if _, pending := serverRequestID(frame); !pending && !retainedByACP {
				h.bufferFrameLocked(frame)
			}
		}
	} else if !retainedByACP {
		if requestID, pending := serverRequestID(frame); !pending || !h.pendingRequests[requestID].buffered {
			h.bufferFrameLocked(frame)
			if pending {
				h.pendingRequests[requestID].buffered = true
			}
		}
	}
}

// watchPromptStall terminates an in-flight prompt whose adapter went silent.
// Turn settlement lives inside the ACP adapter, and the host discards the
// adapter's stderr, so a lost completion (upstream claude-agent-acp#1145
// class) leaves the session's Working indicator hanging with nothing to
// observe. The watchdog gives such turns a terminal error and cancels the
// adapter's turn. It never races forwardProvider: both relay under h.mu.
func (h *host) watchPromptStall(ctx context.Context) {
	ticker := time.NewTicker(promptStallCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			frames, err := h.acp.failStalledPrompt(ctx, h.clientGeneration, h.client != nil, time.Now())
			if err == nil && len(frames.client) > 0 {
				h.deliverProviderFrameLocked(frames.client, true)
			}
			h.mu.Unlock()
			// Like client frames, written outside h.mu: a full stdin pipe must
			// not stall the provider reader.
			if len(frames.provider) > 0 {
				_, _ = h.stdin.Write(frames.provider)
			}
		}
	}
}

func (h *host) forwardProvider(stdout io.Reader) error {
	reader := bufio.NewReader(stdout)
	for {
		frame, err := reader.ReadBytes('\n')
		if len(frame) > 0 {
			h.mu.Lock()
			retainedByACP := false
			var relayErr error
			if h.codex != nil {
				relayErr = h.codexProviderFrameLocked(frame)
				frame = nil // The Codex writer owns delivery.
			} else if h.acp != nil {
				frame, retainedByACP, relayErr = h.acp.providerFrame(
					h.ctx, frame, h.clientGeneration, h.client != nil,
				)
			}
			if relayErr != nil {
				h.mu.Unlock()
				return relayErr
			}
			if len(frame) > 0 {
				h.deliverProviderFrameLocked(frame, retainedByACP)
			}
			h.mu.Unlock()
		}
		if err != nil {
			return err
		}
	}
}

func serverRequestID(frame []byte) (string, bool) {
	var value struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(frame, &value) != nil || len(value.ID) == 0 || value.Method == "" {
		return "", false
	}
	return string(value.ID), true
}

func (h *host) bufferPendingRequestsLocked() {
	for _, requestID := range h.pendingOrder {
		pending := h.pendingRequests[requestID]
		if pending == nil || pending.buffered {
			continue
		}
		h.bufferFrameLocked(pending.frame)
		pending.buffered = true
	}
}

func (h *host) bufferFrameLocked(frame []byte) {
	h.detached = append(h.detached, append([]byte(nil), frame...))
	h.detachedBytes += len(frame)
}

// hostArgs keeps the Unix and Windows detached entry points on one wire format.
func hostArgs(cfg Config) []string {
	args := []string{"chat-host", cfg.SessionID, cfg.DataDir, cfg.Workdir}
	if cfg.Protocol != ProtocolRaw {
		args = append(args, string(cfg.Protocol), cfg.OwnershipFingerprint)
	}
	return append(append(args, "--"), cfg.Argv...)
}
