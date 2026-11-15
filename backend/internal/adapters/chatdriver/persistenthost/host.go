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
	"sync/atomic"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

const (
	ProtocolVersion      = 1
	maxDetachedBytes     = 32 << 20
	startupTimeout       = 10 * time.Second
	handshakeTimeout     = time.Second
	providerStdinGrace   = time.Second
	providerQuickGrace   = 300 * time.Millisecond
	providerStopDeadline = 3 * time.Second
)

type Protocol string

const (
	ProtocolRaw    Protocol = ""
	ProtocolACP    Protocol = "acp"
	ProtocolUnreal Protocol = "unreal"

	ACPPromptResultMethod        = "_ao/persistent_prompt_result"
	ACPPromptAckMethod           = "_ao/persistent_prompt_ack"
	ACPInteractionCommandMethod  = "_ao/persistent_interaction_command"
	ACPRequestIDMetaKey          = "ao.persistentRequestId"
	ACPInitialPermissionsMetaKey = "ao.initialPermissions"
	ACPSteerPromptMetaKey        = "ao.steerPrompt"
	ACPSteerKeepsTurnMetaKey     = "ao.steerKeepsTurn"
	ACPSteerTurnEndedDataKey     = "ao.steerTurnEnded"
	ACPPromptHandoffMethod       = "_ao/persistent_prompt_handoff"
)

var (
	ErrAttached              = errors.New("chat host already has a controller")
	ErrUnauthorized          = errors.New("chat host authentication failed")
	ErrIncompatible          = errors.New("chat host incompatible")
	ErrHostExists            = errors.New("chat host already exists")
	ErrOwnershipInconclusive = errors.New("chat host ownership is inconclusive")
	ErrNotRunning            = errors.New("chat host is not running")
)

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

type PreparedProvider struct {
	Env  []string
	Argv []string
}

type ACPState struct {
	InitialPermissions   string          `json:"initialPermissions,omitempty"`
	InitializeResult     json.RawMessage `json:"initializeResult,omitempty"`
	SessionResult        json.RawMessage `json:"sessionResult,omitempty"`
	SessionID            string          `json:"sessionId,omitempty"`
	ActivePrompt         bool            `json:"activePrompt,omitempty"`
	ActiveCompaction     bool            `json:"activeCompaction,omitempty"`
	PendingResultEventID string          `json:"pendingResultEventId,omitempty"`
	AvailableCommands    json.RawMessage `json:"availableCommands,omitempty"`
}

type Transport struct {
	Stdin         io.WriteCloser
	Stdout        io.Reader
	Reconnected   bool
	NextRequestID int64
	ACPState      *ACPState
}

type hello struct {
	Version   int    `json:"version"`
	Token     string `json:"token"`
	Action    string `json:"action"`
	QuickStop bool   `json:"quickStop,omitempty"`
}

type helloResponse struct {
	OK            bool      `json:"ok"`
	Error         string    `json:"error,omitempty"`
	NextRequestID int64     `json:"nextRequestId,omitempty"`
	ACPState      *ACPState `json:"acpState,omitempty"`
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

func readDescriptorOnce(dataDir, sessionID string) (Descriptor, error) {
	path, err := descriptorPath(dataDir, sessionID)
	if err != nil {
		return Descriptor{}, err
	}
	f, err := openHostFile(path)
	if err != nil {
		return Descriptor{}, err
	}
	b, err := io.ReadAll(f)
	_ = f.Close()
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
	if d.Protocol != cfg.Protocol {
		return fmt.Errorf("%w: host protocol=%q requested=%q", ErrIncompatible, d.Protocol, cfg.Protocol)
	}
	if d.Protocol == ProtocolRaw {
		return nil
	}
	if cfg.OwnershipFingerprint == "" || d.OwnershipFingerprint != cfg.OwnershipFingerprint {
		return fmt.Errorf("%w: provider launch configuration changed", ErrIncompatible)
	}
	return nil
}

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
		if processalive.Alive(d.PID) {
			return nil, fmt.Errorf("%w: %w", ErrOwnershipInconclusive, attachErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
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
		time.Sleep(5 * time.Millisecond)
	}
	if d, readErr := readDescriptor(cfg.DataDir, cfg.SessionID); readErr == nil && processalive.Alive(d.PID) {
		return nil, fmt.Errorf("%w: %w", ErrOwnershipInconclusive, lastErr)
	}
	return nil, fmt.Errorf("start chat host: %w", lastErr)
}

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
	if err := json.NewEncoder(conn).Encode(hello{Version: ProtocolVersion, Token: d.Token, Action: "attach"}); err != nil {
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
		default:
			return nil, errors.New(response.Error)
		}
	}
	return &Transport{
		Stdin: conn, Stdout: reader, Reconnected: reconnected,
		NextRequestID: response.NextRequestID, ACPState: response.ACPState,
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

func HostPID(dataDir, sessionID string) (int, bool) {
	d, err := readDescriptor(dataDir, sessionID)
	if err != nil || d.PID <= 0 || !processalive.Alive(d.PID) {
		return 0, false
	}
	return d.PID, true
}

func Shutdown(ctx context.Context, dataDir, sessionID string) error {
	return shutdown(ctx, dataDir, sessionID, false)
}

func ShutdownQuick(ctx context.Context, dataDir, sessionID string) error {
	return shutdown(ctx, dataDir, sessionID, true)
}

func readDescriptor(dataDir, sessionID string) (Descriptor, error) {
	return readDescriptorContext(context.Background(), dataDir, sessionID)
}

func readDescriptorContext(ctx context.Context, dataDir, sessionID string) (Descriptor, error) {
	d, err := readDescriptorOnce(dataDir, sessionID)
	if !isDescriptorSharingViolation(err) {
		return d, err
	}
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Descriptor{}, ctx.Err()
		case <-timeout.C:
			return Descriptor{}, err
		case <-ticker.C:
			d, err = readDescriptorOnce(dataDir, sessionID)
			if !isDescriptorSharingViolation(err) {
				return d, err
			}
		}
	}
}

func readShutdownDescriptor(ctx context.Context, dataDir, sessionID string) (Descriptor, error) {
	return readDescriptorContext(ctx, dataDir, sessionID)
}

func shutdown(ctx context.Context, dataDir, sessionID string, quick bool) error {
	d, err := readShutdownDescriptor(ctx, dataDir, sessionID)
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
	if err := json.NewEncoder(conn).Encode(hello{Version: ProtocolVersion, Token: d.Token, Action: "shutdown", QuickStop: quick}); err != nil {
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
	waitCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, readErr := readShutdownDescriptor(waitCtx, dataDir, sessionID)
		if readErr == nil && current.Token != d.Token {
			return nil
		}
		if errors.Is(readErr, os.ErrNotExist) {
			path, _ := lockPath(dataDir, sessionID)
			if _, lockErr := os.Stat(path); errors.Is(lockErr, os.ErrNotExist) {
				return nil
			} else if lockErr != nil {
				return lockErr
			}
		} else if readErr != nil && !hostFileBusy(readErr) {
			return readErr
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("wait for chat host shutdown: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

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
	if dir, dirErr := hostDir(cfg.DataDir, cfg.SessionID); dirErr == nil {
		adapterLog := openAdapterLog(dir, adapterLogMaxBytes)
		defer func() { _ = adapterLog.Close() }()
		child.Stderr = adapterLog
	}
	closeProviderTree, err := startProviderProcess(child)
	if err != nil {
		return err
	}
	closeProviderTree = sync.OnceFunc(closeProviderTree)
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
	reapProvider := func() {
		reaped := make(chan struct{})
		go func() { _ = wait(); close(reaped) }()
		select {
		case <-reaped:
		case <-time.After(time.Second):
		}
	}
	stopProvider := func() {
		_ = stdin.Close()
		deadline := time.After(providerStopDeadline)
		grace := providerStdinGrace
		if h.quickStop.Load() {
			grace = providerQuickGrace
		}
		select {
		case <-providerDone:
			reapProvider()
			_ = killProviderProcess(context.WithoutCancel(ctx), child)
			return
		case <-time.After(grace):
		}
		_ = terminateProviderProcess(child)
		exited := time.NewTicker(100 * time.Millisecond)
		defer exited.Stop()
	wait:
		for {
			select {
			case <-providerDone:
				break wait
			case <-deadline:
				break wait
			case <-exited.C:
				if child.Process != nil && !processalive.Alive(child.Process.Pid) {
					break wait
				}
			}
		}
		reapProvider()
		_ = killProviderProcess(context.WithoutCancel(ctx), child)
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
	closeProviderTree()
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
	quickStop        atomic.Bool
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
		if request.QuickStop {
			h.quickStop.Store(true)
		}
		h.shutdownOnce.Do(func() { close(h.shutdown) })
		return
	}
	if request.Action != "attach" {
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: "unknown chat host action"})
		_ = conn.Close()
		return
	}

	h.mu.Lock()
	if h.client != nil {
		h.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(helloResponse{Error: ErrAttached.Error()})
		_ = conn.Close()
		return
	}
	h.client = conn
	h.clientGeneration++
	generation := h.clientGeneration
	response := helloResponse{OK: true, NextRequestID: h.maxRequestID}
	if h.acp != nil {
		response.ACPState = h.acp.snapshot()
	}
	writeErr := json.NewEncoder(conn).Encode(response)
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
	if writeErr != nil {
		h.detach(conn)
		return
	}

	for {
		frame, readErr := reader.ReadBytes('\n')
		if len(frame) > 0 {
			providerFrame := frame
			var clientFrame []byte
			h.mu.Lock()
			if h.acp != nil {
				var relayErr error
				var relayed acpClientFrames
				relayed, relayErr = h.acp.clientFrame(h.ctx, frame, generation)
				providerFrame = relayed.provider
				clientFrame = relayed.client
				if relayErr != nil {
					h.shutdownOnce.Do(func() { close(h.shutdown) })
					h.mu.Unlock()
					h.detach(conn)
					return
				}
			}
			h.mu.Unlock()
			if len(providerFrame) > 0 {
				if _, err := h.stdin.Write(providerFrame); err != nil {
					readErr = err
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
			h.detach(conn)
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
	}
}

func (h *host) detach(conn net.Conn) {
	h.mu.Lock()
	if h.client == conn {
		h.client = nil
		h.bufferPendingRequestsLocked()
	}
	h.mu.Unlock()
	_ = conn.Close()
}

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
			if h.acp != nil {
				var relayErr error
				frame, retainedByACP, relayErr = h.acp.providerFrame(
					h.ctx, frame, h.clientGeneration, h.client != nil,
				)
				if relayErr != nil {
					h.mu.Unlock()
					return relayErr
				}
			}
			if len(frame) == 0 {
				h.mu.Unlock()
				if err != nil {
					return err
				}
				continue
			}
			h.deliverProviderFrameLocked(frame, retainedByACP)
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

func hostArgs(cfg Config) []string {
	args := []string{"chat-host", cfg.SessionID, cfg.DataDir, cfg.Workdir}
	if cfg.Protocol != ProtocolRaw {
		args = append(args, string(cfg.Protocol), cfg.OwnershipFingerprint)
	}
	return append(append(args, "--"), cfg.Argv...)
}
