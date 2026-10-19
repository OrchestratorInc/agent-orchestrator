package zcodeappserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// handshakeTimeout bounds session/create, session/resume and session/read.
// These are local IPC calls; the server itself holds create for at most 15 s
// waiting on the runtime-preferences answer.
const handshakeTimeout = 60 * time.Second

// zcodePlugin is the subset of AO's existing zcode agent plugin the Chat driver
// reuses. Binary resolution, the auth probe and the credential-store pin live
// there and must not be reimplemented: a second copy would drift from what TUI
// sessions do.
type zcodePlugin interface {
	ResolveBinary(ctx context.Context) (string, error)
	AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error)
	AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, sessionID domain.SessionID, launchID string)
}

// process is a running app-server, abstracted so tests can substitute pipes for
// a child process.
type process struct {
	stdin  io.WriteCloser
	stdout io.Reader
	// reconnected means the provider process and its session survived a prior
	// daemon. The replacement must not create, resume or subscribe again.
	reconnected   bool
	nextRequestID int64
	// stop releases the process. It must be safe to call more than once.
	stop func() error
	// terminate destroys a persistent host for explicit session shutdown.
	terminate func() error
}

// spawnFunc launches an app-server. Injected so tests never exec anything.
type spawnFunc func(ctx context.Context, bin, workdir string, env []string) (*process, error)

type persistentConnectFunc func(context.Context, persistenthost.Config) (*persistenthost.Transport, error)

// Driver opens zcode conversations over `zcode app-server --stdio`.
type Driver struct {
	plugin         zcodePlugin
	log            *slog.Logger
	spawn          spawnFunc
	persistent     bool
	connectHost    persistentConnectFunc
	runtimeHeaders runtimeHeadersResolver
}

// New builds a Chat driver over the existing zcode agent plugin.
func New(plugin zcodePlugin, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Driver{
		plugin: plugin, log: log, spawn: spawnAppServer,
		persistent: true, connectHost: persistenthost.ConnectOrStart,
		runtimeHeaders: declineResolver{},
	}
}

var _ ports.ChatDriver = (*Driver)(nil)
var _ ports.ChatDriverReconnector = (*Driver)(nil)

// Harness reports which agent this driver serves.
func (d *Driver) Harness() domain.AgentHarness { return domain.HarnessZcode }

// capabilities is exactly what the Phase 1 captures proved (DESIGN 8, "2b");
// each entry names the fixture that is its evidence. Nothing STATIC is
// advertised: no steer (session/send during a turn is refused -32010), no
// compaction, fork, rollback or config options.
func capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming:   true, // model.streaming text_delta (p1-create-send-complete)
		ports.ChatCapabilityApprovals:   true, // interaction/requestPermission under mode edit (p5-approval-allow)
		ports.ChatCapabilityInterrupt:   true, // session/stop -> turn.completed cancelled (p4-stop)
		ports.ChatCapabilityResume:      true, // session/resume in a new process with memory (p8-resume)
		ports.ChatCapabilityTools:       true, // tool.updated scheduled/started/result (p5-approval-allow)
		ports.ChatCapabilityInteractive: true, // interaction/requestUserInput (p6-user-input)
		ports.ChatCapabilityUsage:       true, // turn.completed.usage, session/usage (p1, p9-usage-read-mcp)
		ports.ChatCapabilityModels:      true, // settings.model.available, modelSelection (p7-models)
		ports.ChatCapabilityHistory:     true, // session/read messages (p9-usage-read-mcp, p8-resume)
	}
}

// Probe reports what this install can do without creating a conversation.
//
// Auth is layer 1 of DESIGN 2.4: the result is cached per harness for the
// daemon's lifetime once it succeeds, so a credential that lapses later is only
// seen here while the cache is cold; Start and Resume re-check it, and the chat
// service drops the cached probe when they report ErrChatAuthRequired.
func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	if _, err := d.plugin.ResolveBinary(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}
	if err := d.requireAuth(ctx); err != nil {
		return nil, err
	}
	// No throwaway spawn: binary presence and the plugin's auth answer are the
	// whole probe. Every advertised capability is backed by a capture, not by
	// a per-install runtime/capabilities read (STATIC, never exercised).
	return capabilities(), nil
}

// requireAuth is the nativeacp auth rule: an unauthorized local probe is a
// typed refusal; an inconclusive one is logged and not treated as evidence.
func (d *Driver) requireAuth(ctx context.Context) error {
	status, err := d.plugin.AuthStatus(ctx)
	if err == nil && status == ports.AgentAuthStatusUnauthorized {
		return ports.ErrChatAuthRequired
	}
	if err != nil {
		d.log.Debug("zcode auth probe inconclusive; continuing", "error", err)
	}
	return nil
}

// sessionConfig is the subset of a start or fresh-resume config that
// session/create needs.
type sessionConfig struct {
	workspacePath string
	permissions   ports.PermissionMode
	readOnly      bool
	effort        string
	mcpServers    []ports.ChatMCPServerConfig
	ephemeral     bool
	systemPrompt  string
}

// Start opens a new zcode session in the session worktree.
func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if err := validateStart(cfg.WorkspacePath, cfg.Permissions, cfg.ReadOnly); err != nil {
		return nil, err
	}
	// Layer 2 of DESIGN 2.4: a cached successful Probe outlives the token.
	if err := d.requireAuth(ctx); err != nil {
		return nil, err
	}
	conv, reconnected, err := d.connectSession(ctx, cfg.SessionID, cfg.DataDir, cfg.WorkspacePath, cfg.Env, cfg.PrepareEnv, false)
	if err != nil {
		return nil, err
	}
	if reconnected {
		// A fresh-start request colliding with a surviving host is a durable-state
		// mismatch, not permission to destroy a provider that may still be working.
		_ = conv.Close()
		return nil, errors.New("persistent chat host already owns a provider session for a fresh session")
	}
	if err := d.createSession(ctx, conv, sessionConfig{
		workspacePath: cfg.WorkspacePath, permissions: cfg.Permissions, readOnly: cfg.ReadOnly,
		effort: cfg.Effort, mcpServers: cfg.MCPServers, ephemeral: cfg.Ephemeral, systemPrompt: cfg.SystemPrompt,
	}); err != nil {
		return nil, err
	}
	return conv, nil
}

// createSession runs session/create on an open connection, answers the
// runtime-preferences request through the router, subscribes and starts the
// pump. On failure the conversation is terminated.
//
// The create params are the PROVEN set (p1-create-send-complete.ndjson,
// p9-usage-read-mcp.ndjson): workspace, mode, titleGenerationEnabled:false
// (a title costs a model call per session) and mcpServers. The reasoning
// level is not a create param: it rides on each turn's modelSelection.
func (d *Driver) createSession(ctx context.Context, conv *conversation, cfg sessionConfig) error {
	mode, err := sessionMode(cfg.permissions, cfg.readOnly)
	if err != nil {
		_ = conv.Terminate()
		return err
	}
	titleGeneration := false
	params := zcodeproto.SessionCreateParams{
		Workspace:              zcodeproto.Workspace{WorkspacePath: cfg.workspacePath, WorkspaceKey: cfg.workspacePath},
		Mode:                   mode,
		MCPServers:             mcpServers(cfg.mcpServers),
		TitleGenerationEnabled: &titleGeneration,
	}
	if cfg.ephemeral {
		params.Persistence = "deferred"
	}
	createCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var snapshot zcodeproto.Snapshot
	if err := conv.conn.request(createCtx, zcodeproto.MethodSessionCreate, params, &snapshot); err != nil {
		_ = conv.Terminate()
		return fmt.Errorf("%s: %w", zcodeproto.MethodSessionCreate, err)
	}
	sessionID := conv.resolveSessionID(snapshot)
	if sessionID == "" {
		_ = conv.Terminate()
		return errors.New("session/create returned no session id")
	}
	conv.setSnapshot(snapshot)
	if err := conv.subscribe(createCtx, sessionID); err != nil {
		_ = conv.Terminate()
		return err
	}
	// A launch reasoning level (`ao spawn --effort`) the model does not offer
	// is refused here, against the create snapshot's catalog, rather than
	// substituted into every turn and refused there: the session would exist
	// but never send (modelSelectionFor applies the launch level before the
	// catalog check).
	if cfg.effort != "" {
		if _, err := conv.modelSelectionFor("", cfg.effort); err != nil {
			_ = conv.Terminate()
			return fmt.Errorf("launch reasoning level: %w", err)
		}
	}

	conv.mu.Lock()
	conv.workspacePath = cfg.workspacePath
	conv.launchMode = cfg.permissions
	conv.launchEffort = cfg.effort
	conv.preamble = cfg.systemPrompt
	conv.mu.Unlock()
	conv.start(sessionID)
	return nil
}

// Reconnect attaches to a surviving provider without launching a replacement.
func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}

// Resume reattaches to a stored zcode session after a daemon or app-server
// restart. A live host is rejoined without any session RPC; a replacement
// process gets session/resume and never a silent session/create.
func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ProviderConversationID == "" {
		return nil, fmt.Errorf("%w: no stored session id", ports.ErrChatResumeFailed)
	}
	if err := validateStart(cfg.WorkspacePath, cfg.Permissions, cfg.ReadOnly); err != nil {
		return nil, err
	}
	if err := d.requireAuth(ctx); err != nil {
		return nil, err
	}
	conv, reconnected, err := d.connectSession(ctx, cfg.SessionID, cfg.DataDir, cfg.WorkspacePath, cfg.Env, cfg.PrepareEnv, cfg.ReconnectOnly)
	if err != nil {
		return nil, err
	}
	conv.mu.Lock()
	conv.workspacePath = cfg.WorkspacePath
	conv.launchMode = cfg.Permissions
	conv.launchEffort = cfg.Effort
	if !cfg.HasPriorTurns {
		// The role has not reached the provider yet (the opening prompt is
		// dispatched asynchronously), so it still leads the first turn, on a
		// live reconnect as much as in a replacement process (DESIGN 4.3).
		conv.preamble = cfg.SystemPrompt
	}
	conv.mu.Unlock()

	if reconnected {
		// The host preserved the initialized connection and its session, whose
		// record already carries deliveryKind. Only the host-replayed server
		// requests need reconciling against what the server still lists. The
		// pump is not started here: the relay holds the replay until the service
		// restores the durable running turn through ActivateLiveReconnect.
		conv.mu.Lock()
		conv.sessionID = cfg.ProviderConversationID
		conv.mu.Unlock()
		readCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
		defer cancel()
		// The proven minimum limit, never 0: the server refuses a zero
		// messageLimit (-32602), which would keep every replayed request.
		snapshot, readErr := conv.readSnapshot(readCtx, minimumMessageLimit)
		if readErr != nil {
			d.log.Warn("session/read after live reconnect failed; keeping replayed requests", "error", readErr)
		}
		conv.reconcilePending(snapshot.Runtime.PendingRequestIDs, readErr == nil)
		conv.dropUnsupportedLaunchEffort()
		return conv, nil
	}

	resumeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var snapshot zcodeproto.Snapshot
	err = conv.conn.request(resumeCtx, zcodeproto.MethodSessionResume, zcodeproto.SessionResumeParams{
		SessionID: cfg.ProviderConversationID,
		Workspace: zcodeproto.Workspace{WorkspacePath: cfg.WorkspacePath, WorkspaceKey: cfg.WorkspacePath},
	}, &snapshot)
	if err != nil {
		if cfg.FreshIfMissing && isSessionNotFound(err) {
			// Only on the "Session not found:" message (PROVEN, p8-resume.ndjson):
			// the same code with "Session is not active:" is eviction (STATIC),
			// never a missing session.
			if createErr := d.createSession(ctx, conv, sessionConfig{
				workspacePath: cfg.WorkspacePath, permissions: cfg.Permissions, readOnly: cfg.ReadOnly,
				effort: cfg.Effort, mcpServers: cfg.MCPServers, systemPrompt: cfg.SystemPrompt,
			}); createErr != nil {
				return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, createErr)
			}
			return conv, nil
		}
		_ = conv.Terminate()
		// Deliberately not falling back to session/create: silently opening a new
		// session would present unrelated history as continuous.
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	sessionID := snapshot.Session.SessionID
	if sessionID == "" {
		sessionID = cfg.ProviderConversationID
	}
	conv.setSnapshot(snapshot)
	if err := conv.subscribe(resumeCtx, sessionID); err != nil {
		_ = conv.Terminate()
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	conv.dropUnsupportedLaunchEffort()
	conv.start(sessionID)
	return conv, nil
}

// mcpServers maps AO's stdio tool servers onto session/create.mcpServers
// (PROVEN, p9-usage-read-mcp.ndjson). The list REPLACES the operator's global
// config.mcp.servers for the session, which is intended: the global list is
// what loads ~240k tokens of tool schema into every turn
// (DESIGN-zcode-context-trim.md item 1), while AO's own `ao mcp` server is
// the one the session needs. HTTP servers have no proven shape and are
// skipped.
func mcpServers(servers []ports.ChatMCPServerConfig) []zcodeproto.MCPServerConfig {
	var out []zcodeproto.MCPServerConfig
	for _, server := range servers {
		if server.Type == "http" || server.Command == "" {
			continue
		}
		entry := zcodeproto.MCPServerConfig{
			Name: server.Name, Command: server.Command, Args: append([]string{}, server.Args...),
			Env: []zcodeproto.EnvEntry{}, Isolation: "session",
		}
		for _, name := range slices.Sorted(maps.Keys(server.Env)) {
			entry.Env = append(entry.Env, zcodeproto.EnvEntry{Name: name, Value: server.Env[name]})
		}
		out = append(out, entry)
	}
	return out
}

// connectSession opens the provider connection: through the persistent host for
// the shipped driver, or through the injected spawn for tests and probes.
func (d *Driver) connectSession(
	ctx context.Context,
	sessionID domain.SessionID,
	dataDir, workdir string,
	env map[string]string,
	prepareEnv func(context.Context) (map[string]string, error),
	reconnectOnly bool,
) (*conversation, bool, error) {
	if !d.persistent {
		if reconnectOnly {
			return nil, false, ports.ErrChatHostNotRunning
		}
		if prepareEnv != nil {
			var err error
			env, err = prepareEnv(ctx)
			if err != nil {
				return nil, false, err
			}
		}
		bin, err := d.plugin.ResolveBinary(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
		}
		proc, err := d.spawn(ctx, bin, workdir, d.processEnv(ctx, bin, env, dataDir, sessionID))
		if err != nil {
			return nil, false, fmt.Errorf("%w: launch app-server: %w", ports.ErrChatDriverUnavailable, err)
		}
		return newConversation(proc, d.log, d.runtimeHeaders), false, nil
	}

	var bin string
	if !reconnectOnly {
		var err error
		bin, err = d.plugin.ResolveBinary(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
		}
	}
	argv := appServerArgv(bin, workdir)
	hostConfig := persistenthost.Config{
		SessionID:     string(sessionID),
		ReconnectOnly: reconnectOnly,
		DataDir:       dataDir,
		Workdir:       workdir,
		Env:           d.processEnv(ctx, bin, env, dataDir, sessionID),
		Argv:          argv,
		Protocol:      persistenthost.ProtocolRaw,
	}
	if prepareEnv != nil {
		hostConfig.Prepare = func(prepareCtx context.Context) (persistenthost.PreparedProvider, error) {
			preparedEnv, prepareErr := prepareEnv(prepareCtx)
			if prepareErr != nil {
				return persistenthost.PreparedProvider{}, prepareErr
			}
			return persistenthost.PreparedProvider{
				Env: d.processEnv(prepareCtx, bin, preparedEnv, dataDir, sessionID), Argv: argv,
			}, nil
		}
	}
	transport, err := d.connectHost(ctx, hostConfig)
	if err != nil {
		if errors.Is(err, persistenthost.ErrNotRunning) {
			return nil, false, ports.ErrChatHostNotRunning
		}
		if errors.Is(err, persistenthost.ErrOwnershipInconclusive) ||
			errors.Is(err, persistenthost.ErrAttached) ||
			errors.Is(err, persistenthost.ErrIncompatible) ||
			errors.Is(err, persistenthost.ErrUnauthorized) {
			return nil, false, fmt.Errorf("%w: persistent host: %w", ports.ErrChatRecoveryInconclusive, err)
		}
		return nil, false, fmt.Errorf("%w: persistent host: %w", ports.ErrChatDriverUnavailable, err)
	}
	proc := &process{
		stdin:         transport.Stdin,
		stdout:        transport.Stdout,
		reconnected:   transport.Reconnected,
		nextRequestID: transport.NextRequestID,
		stop:          transport.Stdin.Close,
		terminate: func() error {
			_ = transport.Stdin.Close()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return persistenthost.Shutdown(shutdownCtx, dataDir, string(sessionID))
		},
	}
	return newConversation(proc, d.log, d.runtimeHeaders), transport.Reconnected, nil
}

// appServerArgv is the PROVEN spawn shape (probes/zcode_final_probe.js:17).
func appServerArgv(bin, workdir string) []string {
	return []string{bin, "app-server", "--stdio", "--cwd", workdir}
}

// processEnv assembles the provider environment (DESIGN 3.1): AO's overlay,
// the credential-store pin the TUI launcher also applies, the PATH augmentation
// for an `env node` wrapper, merged over the daemon's own environ.
//
// The chat launch path does not pin the store for us: it asserts an anonymous
// AugmentRuntimeEnv the zcode plugin does not implement, so without this call a
// chat session would silently use the wrapper's own store.
func (d *Driver) processEnv(ctx context.Context, bin string, env map[string]string, dataDir string, sessionID domain.SessionID) []string {
	overlay := make(map[string]string, len(env)+2)
	for key, value := range env {
		overlay[key] = value
	}
	d.plugin.AugmentRuntimeLaunchEnv(overlay, dataDir, sessionID, "")
	if _, ok := overlay["PATH"]; !ok {
		overlay["PATH"] = os.Getenv("PATH")
	}
	if bin != "" {
		agentlaunch.AugmentRuntimePATHForLaunchBinary(ctx, overlay, []string{bin}, exec.LookPath,
			agentlaunch.PinnedDir(os.Executable, overlay["AO_DATA_DIR"]))
	}
	return processenv.Merge(overlay)
}

// spawnAppServer is the real launcher for the direct (non-host) path.
func spawnAppServer(_ context.Context, bin, workdir string, env []string) (*process, error) {
	// Not CommandContext: the launch context ends with Start, and the process
	// must outlive it (codex does the same).
	argv := appServerArgv(bin, workdir)
	cmd := aoprocess.Command(argv[0], argv[1:]...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// stderr is discarded rather than captured: the CLI's token logger writes
	// there, and the ground rule is that token values are never logged.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", bin, err)
	}

	var stopped bool
	return &process{
		stdin:  stdin,
		stdout: stdout,
		stop: func() error {
			if stopped {
				return nil
			}
			stopped = true
			// Closing stdin is the graceful shutdown (the CLI exits 0 within its
			// 1.5 s hard timer, U10); kill only if it lingers.
			_ = stdin.Close()
			done := make(chan struct{})
			go func() { _, _ = cmd.Process.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
			}
			return nil
		},
	}, nil
}
