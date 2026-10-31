// Package jcode implements AO's JCode agent adapter. JCode is a terminal-first
// Rust harness whose TUI is a client of a background `jcode serve` daemon, so
// AO launches the interactive TUI in the session terminal and gives every AO
// session its own daemon (JCODE_RUNTIME_DIR). The daemon inherits the session's
// AO environment, which is what lets JCode's lifecycle hooks report to AO.
//
// Verified against JCode v0.99.1:
//
//   - The TUI takes no initial prompt, so the first task is typed in after the
//     composer renders. Multi-line text must arrive as a bracketed paste: a
//     literal LF is Ctrl+J (jump between prompts) and is dropped.
//   - There is no system-prompt flag. JCode appends the project file
//     .jcode/prompt-overlay.md to its assembled system prompt (after a user
//     .jcode/system-prompt.md replaces only the base layer), so AO keeps its
//     instructions in an AO-owned, git-ignored overlay there.
//   - `--resume <id>` also matches short names and titles and reopens a session
//     in its saved working_dir from any cwd, so AO resumes only an exact id whose
//     saved working_dir is this workspace.
//   - JCode never asks for tool approval, so AO's permission modes have no
//     native flag (as for Pi and Amp). Tool allow/deny lists and effort are
//     rejected rather than silently dropped.
package jcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const adapterID = "jcode"

// Plugin is the JCode agent adapter. Binary resolution is cached.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register JCode adapter.
func New() *Plugin { return &Plugin{} }

var (
	_ adapters.Adapter                        = (*Plugin)(nil)
	_ ports.Agent                             = (*Plugin)(nil)
	_ ports.AgentBinaryResolver               = (*Plugin)(nil)
	_ ports.AgentBinaryResolutionInvalidator  = (*Plugin)(nil)
	_ ports.AgentPromptReadinessProvider      = (*Plugin)(nil)
	_ ports.AgentAfterStartPromptBuilder      = (*Plugin)(nil)
	_ ports.AgentRuntimeLaunchEnv             = (*Plugin)(nil)
	_ ports.AgentNativeSessionTerminator      = (*Plugin)(nil)
	_ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
	_ ports.AgentTerminalVersionRefresher     = (*Plugin)(nil)
	_ ports.AgentAuthChecker                  = (*Plugin)(nil)
)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:           adapterID,
		Name:         "jcode",
		Description:  "Run jcode interactive TUI sessions.",
		Version:      "0.0.1",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// GetConfigSpec reports the per-project agent config keys JCode understands.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `jcode --model`.")
}

// GetLaunchCommand builds the argv for a fresh interactive session:
//
//	jcode --no-update [--model <model>]
//
// The prompt is never in argv: AO types it in after start.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := unsupportedTuning(cfg.Config, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "--no-update"}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	return cmd, nil
}

func unsupportedTuning(cfg ports.AgentConfig, allowed, disallowed []string) error {
	if strings.TrimSpace(cfg.Effort) != "" {
		return fmt.Errorf("jcode: reasoning effort is configured per provider, not per launch: %w", ports.ErrUnsupportedEffort)
	}
	if strings.TrimSpace(cfg.Mode) != "" {
		return errors.New("jcode: mode overrides are not supported")
	}
	if len(allowed) > 0 || len(disallowed) > 0 {
		return errors.New("jcode: AO tool allow/deny lists are not supported; jcode runs tools without approval")
	}
	return nil
}

// GetPromptDeliveryStrategy reports after-start delivery: JCode's TUI has no
// initial-prompt argument (`jcode run` is one-shot and leaves no TUI).
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

// PromptReadinessHints waits for the empty first composer line, `1>`, which
// jcode renders on its own line only once the input box is live, or for its
// first-run start-choice page, which AfterStartCommand answers.
func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay: 500 * time.Millisecond,
		Patterns:     readinessPatterns,
		PollInterval: 200 * time.Millisecond,
		Timeout:      120 * time.Second,
		Lines:        80,
	}, nil
}

// bracketedPasteThreshold sends long single-line prompts as one paste too, so
// a >8 KiB task is not typed into the composer key by key.
const bracketedPasteThreshold = 512

// BuildAfterStartPrompt wraps multi-line or long prompts in a bracketed paste.
// ESC is stripped from the text so a prompt cannot end the paste early. The
// runtime pauses at least 300ms before Enter, past JCode's 150ms paste guard.
func (p *Plugin) BuildAfterStartPrompt(ctx context.Context, cfg ports.LaunchConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return pastePrompt(cfg.Prompt), nil
}

func pastePrompt(prompt string) string {
	text := strings.ReplaceAll(strings.ReplaceAll(prompt, "\r\n", "\n"), "\r", "\n")
	if !strings.Contains(text, "\n") && len(text) <= bracketedPasteThreshold {
		return prompt
	}
	return "\x1b[200~" + strings.ReplaceAll(text, "\x1b", "") + "\x1b[201~"
}

// GetRestoreCommand resumes the captured native session:
//
//	jcode --no-update [--model <model>] --resume <id>
//
// ok=false (a fresh launch) unless the id is an exact JCode session id whose
// saved working_dir is this workspace.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	id := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if _, ok := workspaceSession(id, cfg.Session.WorkspacePath, nil); !ok {
		return nil, false, nil
	}
	if err := unsupportedTuning(cfg.Config, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, false, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd := make([]string, 0, 6)
	cmd = append(cmd, binary, "--no-update")
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	return append(cmd, "--resume", id), true, nil
}

// SessionInfo adds JCode's saved title to the metadata AO's hooks captured.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	if native, found := workspaceSession(info.AgentSessionID, session.WorkspacePath, nil); found && info.Title == "" {
		info.Title = native.Title
		ok = true
	}
	return info, ok, nil
}

// NativeConversationExists reports whether JCode saved this exact session for
// this workspace.
func (p *Plugin) NativeConversationExists(ctx context.Context, session ports.SessionRef, id string, env map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, ok := workspaceSession(strings.TrimSpace(id), session.WorkspacePath, env)
	return ok, nil
}

// nativeIDPattern is JCode's session id: session_<word>_<millis>_<16 hex>.
var nativeIDPattern = regexp.MustCompile(`^session_[a-z0-9]+_\d+_[0-9a-f]{16}$`)

type nativeSession struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	WorkingDir string `json:"working_dir"`
}

func workspaceSession(id, workspace string, env map[string]string) (nativeSession, bool) {
	if !nativeIDPattern.MatchString(id) || strings.TrimSpace(workspace) == "" {
		return nativeSession{}, false
	}
	home, err := jcodeHome(env)
	if err != nil {
		return nativeSession{}, false
	}
	data, err := os.ReadFile(filepath.Join(home, "sessions", id+".json")) //nolint:gosec // id is pattern-checked
	if err != nil {
		return nativeSession{}, false
	}
	var session nativeSession
	if json.Unmarshal(data, &session) != nil || session.ID != id || !samePath(session.WorkingDir, workspace) {
		return nativeSession{}, false
	}
	return session, true
}

func samePath(a, b string) bool {
	if strings.TrimSpace(a) == "" {
		return false
	}
	canonical := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		return filepath.Clean(path)
	}
	return canonical(a) == canonical(b)
}

// jcodeHome is JCode's state root: $JCODE_HOME, else ~/.jcode.
func jcodeHome(env map[string]string) (string, error) {
	if home := strings.TrimSpace(env["JCODE_HOME"]); home != "" {
		return home, nil
	}
	if home := strings.TrimSpace(os.Getenv("JCODE_HOME")); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".jcode"), nil
}

// SupportsTerminalVersionRefresh declares unattended exact-session resume:
// restore names the captured id, re-applies the model, and the instructions
// overlay stays in the workspace.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

// versionPattern anchors JCode's `--version` output, e.g. "jcode v0.99.1 (7206efd0c)".
var versionPattern = regexp.MustCompile(`^jcode v\d+\.\d+\.\d+`)

var binarySpec = binaryutil.BinarySpec{
	Label:         "jcode",
	Names:         []string{"jcode"},
	WinNames:      []string{"jcode.exe"},
	UnixHomePaths: [][]string{{".local", "bin", "jcode"}},
	WinPaths:      []binaryutil.WinPath{{Base: binaryutil.WinLocalAppData, Parts: []string{"jcode", "bin", "jcode.exe"}}},
	ValidateIdentity: func(ctx context.Context, path string) bool {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		out, err := process.CommandContext(ctx, path, "--version").Output()
		return err == nil && versionPattern.Match(out)
	},
}

// ResolveBinary resolves the JCode executable, confirming its identity.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// InvalidateBinaryResolution forgets the cached executable path.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
