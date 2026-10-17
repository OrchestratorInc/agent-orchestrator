package acp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// StandbyConfig opts a binding into starting its provider ahead of an agent
// switch. Bindings whose provider starts in well under a second gain little and
// should leave it nil.
type StandbyConfig struct {
	// Warm runs once after initialize to load provider state that the first
	// session/new would otherwise load. It must not create a session. Errors
	// are logged and ignored: the standby is still initialized.
	Warm func(context.Context, RawRequest) error
	// PromptAtLaunch reports that Launch bakes LaunchConfig.SystemPrompt into the
	// process, as a flag or a file. A standby is launched before the switch's
	// handoff exists, so on adoption the handoff block travels ahead of the
	// first prompt instead.
	PromptAtLaunch bool
}

// RawRequest sends one JSON-RPC request to the provider and waits for its
// outcome, discarding the result body.
type RawRequest func(ctx context.Context, method string, params any) error

const standbyWarmTimeout = 30 * time.Second

var _ ports.ChatStandbyDriver = (*Driver)(nil)

// hostKey is the persistent host key a process answers to. It changes once,
// when an agent switch adopts a standby onto its session.
type hostKey struct {
	mu    sync.Mutex
	value string
}

func newHostKey(value string) *hostKey { return &hostKey{value: value} }

func (k *hostKey) get() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.value
}

func (k *hostKey) set(value string) {
	k.mu.Lock()
	k.value = value
	k.mu.Unlock()
}

// standby is an initialized provider under persistenthost.StandbyKey, waiting
// for the switch that will adopt it.
type standby struct {
	driver *Driver
	cfg    ports.ChatStartConfig
	launch LaunchConfig
	key    *hostKey
	conv   *conversation
	init   acpsdk.InitializeResponse

	mu        sync.Mutex
	adopted   bool
	discarded bool
}

func (s *standby) Harness() domain.AgentHarness { return s.driver.cfg.Harness }

func (s *standby) Discard() {
	s.mu.Lock()
	if s.adopted || s.discarded {
		s.mu.Unlock()
		return
	}
	s.discarded = true
	s.mu.Unlock()
	_ = s.conv.Terminate()
}

// SupportsStandby reports whether the binding opted into standbys.
func (d *Driver) SupportsStandby() bool { return d.cfg.Standby != nil }

// Prewarm starts and initializes the provider a later Start with the same
// inputs will use, under a host key of its own so the session's current agent
// keeps its host until the switch stops it.
func (d *Driver) Prewarm(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatStandby, error) {
	if d.cfg.Standby == nil {
		return nil, fmt.Errorf("%w: %s has no standby support", ports.ErrChatUnsupported, d.cfg.Harness)
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}
	if strings.TrimSpace(cfg.DataDir) == "" || cfg.SessionID == "" {
		return nil, errors.New("ACP standby requires a data dir and session id")
	}
	if d.cfg.ValidateTurnSettings != nil {
		if err := d.cfg.ValidateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{
			Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions,
		}); err != nil {
			return nil, fmt.Errorf("validate ACP standby settings: %w", err)
		}
	}
	launch := LaunchConfig{
		SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env:   maps.Clone(cfg.Env),
		Model: cfg.Model, Permissions: cfg.Permissions, SystemPrompt: cfg.SystemPrompt,
	}
	// The caller discards any earlier standby for the session; each one runs
	// under its own key.
	key := newHostKey(persistenthost.StandbyKey(string(cfg.SessionID)))
	started := time.Now()
	proc, err := d.connectProcessAt(ctx, launch, nil, key)
	if err != nil {
		return nil, err
	}
	conv, init, live, err := d.initialize(ctx, launch, proc)
	if err != nil {
		return nil, err
	}
	if live != nil {
		_ = conv.Terminate()
		return nil, errors.New("ACP standby host already held a session")
	}
	if d.cfg.Standby.Warm != nil {
		warmCtx, cancel := context.WithTimeout(ctx, standbyWarmTimeout)
		// A provider that exits mid warm-up never answers; stop waiting then.
		go func() {
			select {
			case <-conv.conn.Done():
				cancel()
			case <-warmCtx.Done():
			}
		}()
		if warmErr := d.cfg.Standby.Warm(warmCtx, conv.legacyWire.request); warmErr != nil {
			d.log.Info("chat: ACP standby warm-up failed", "sessionID", cfg.SessionID,
				"harness", d.cfg.Harness, "error", warmErr)
		}
		cancel()
		select {
		case <-conv.conn.Done():
			_ = conv.Terminate()
			return nil, errors.New("ACP standby provider exited while warming up")
		default:
		}
	}
	d.logStartStage(cfg.SessionID, "standby_ready", started, nil)
	stored := cfg
	stored.Env = launch.Env
	stored.Standby = nil
	return &standby{driver: d, cfg: stored, launch: launch, key: key, conv: conv, init: init}, nil
}

// adoptStandby takes over a standby whose launch inputs match this Start and
// moves its host onto the session's key. Any other standby is discarded.
func (d *Driver) adoptStandby(
	ctx context.Context,
	candidate ports.ChatStandby,
	cfg ports.ChatStartConfig,
	launch LaunchConfig,
) (*conversation, acpsdk.InitializeResponse, error) {
	s, ok := candidate.(*standby)
	if !ok || s.driver != d {
		candidate.Discard()
		return nil, acpsdk.InitializeResponse{}, errors.New("standby belongs to another driver")
	}
	if reason := s.mismatch(cfg); reason != "" {
		s.Discard()
		return nil, acpsdk.InitializeResponse{}, fmt.Errorf("standby launch inputs changed: %s", reason)
	}
	// A cold start would launch with PrepareEnv's environment; the standby must
	// have been launched with that same one, credential included.
	env := cfg.Env
	if cfg.PrepareEnv != nil {
		prepared, err := cfg.PrepareEnv(ctx)
		if err != nil {
			s.Discard()
			return nil, acpsdk.InitializeResponse{}, err
		}
		env = prepared
	}
	if !maps.Equal(env, s.cfg.Env) {
		s.Discard()
		return nil, acpsdk.InitializeResponse{}, errors.New("standby launch inputs changed: environment")
	}
	s.mu.Lock()
	if s.adopted || s.discarded {
		s.mu.Unlock()
		return nil, acpsdk.InitializeResponse{}, errors.New("standby is no longer available")
	}
	s.adopted = true
	s.mu.Unlock()
	fail := func(err error) (*conversation, acpsdk.InitializeResponse, error) {
		_ = s.conv.Terminate()
		return nil, acpsdk.InitializeResponse{}, err
	}
	select {
	case <-s.conv.conn.Done():
		return fail(errors.New("standby provider exited"))
	default:
	}
	if err := persistenthost.Rekey(ctx, launch.DataDir, s.key.get(), string(launch.SessionID),
		d.ownershipFingerprint(launch)); err != nil {
		return fail(fmt.Errorf("rekey standby host: %w", err))
	}
	s.key.set(string(launch.SessionID))
	s.conv.setProviderScope(launch.ProviderScopeID)
	return s.conv, s.init, nil
}

// mismatch names the first launch input that differs between the standby and
// the Start adopting it, or returns "" when it can be adopted.
func (s *standby) mismatch(cfg ports.ChatStartConfig) string {
	want := s.cfg
	switch {
	case cfg.SessionID != want.SessionID:
		return "session"
	case filepath.Clean(cfg.DataDir) != filepath.Clean(want.DataDir):
		return "data dir"
	case filepath.Clean(cfg.WorkspacePath) != filepath.Clean(want.WorkspacePath):
		return "workspace"
	case ports.NormalizePermissionMode(cfg.Permissions) != ports.NormalizePermissionMode(want.Permissions):
		return "permissions"
	case cfg.Model != want.Model:
		return "model"
	case cfg.Effort != want.Effort:
		return "effort"
	case cfg.ReadOnly != want.ReadOnly || cfg.DisableNetwork != want.DisableNetwork:
		return "sandbox"
	case strings.TrimSpace(withoutContinuation(cfg.SystemPrompt)) != strings.TrimSpace(want.SystemPrompt):
		// The standby launched with the standing instructions alone; only a
		// switch's handoff block may differ, and it travels separately.
		return "standing instructions"
	}
	return ""
}

const (
	// The block always opens with attributes; the continuation protocol ahead
	// of it names the bare tag in prose, and transcript text inside it has
	// "<ao-" escaped.
	continuationOpenTag  = "<ao-continuation "
	continuationCloseTag = "</ao-continuation>"
)

// StandingInstructions returns a system prompt without an agent switch's
// handoff block. The handoff is a one-time message for the switch's first turn;
// a provider that keeps its own history must not be handed it again.
func StandingInstructions(systemPrompt string) string {
	return strings.TrimSpace(withoutContinuation(systemPrompt))
}

// withoutContinuation removes an agent switch's handoff block from a system
// prompt, leaving the standing instructions.
func withoutContinuation(systemPrompt string) string {
	start := strings.Index(systemPrompt, continuationOpenTag)
	end := strings.LastIndex(systemPrompt, continuationCloseTag)
	if start < 0 || end < start {
		return systemPrompt
	}
	return systemPrompt[:start] + systemPrompt[end+len(continuationCloseTag):]
}

// ContinuationPreamble returns an agent switch's handoff block from a system
// prompt as a first-prompt text block, or nil when the prompt has none.
func ContinuationPreamble(systemPrompt string) []acpsdk.ContentBlock {
	start := strings.Index(systemPrompt, continuationOpenTag)
	end := strings.LastIndex(systemPrompt, continuationCloseTag)
	if start < 0 || end < start {
		return nil
	}
	return []acpsdk.ContentBlock{acpsdk.TextBlock(systemPrompt[start : end+len(continuationCloseTag)])}
}
