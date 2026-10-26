// Package registry resolves the Chat driver for a harness.
//
// Registration is the whole capability gate: a harness with no driver here cannot
// run in chat mode, and asking for one fails with ErrChatUnsupported rather than
// quietly producing a TUI session. Adding a harness to chat means adding it here,
// which keeps "which agents support chat" answerable by reading one file instead
// of inferring it from behavior.
package registry

import (
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	commandcodeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/commandcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/deepseekharness"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimchi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/omp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/pi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/qwen"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/unrealagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/agyacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/claudeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commandcodeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/copilotacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/cursoracp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/deepseekharnessacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/droidacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/kimchiacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/kimiacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/ompacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/opencodeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/opencodev2acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/piacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/qwenacp"
	unrealchat "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/unrealagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Registry maps a harness to its Chat driver.
type Registry struct {
	drivers map[domain.AgentHarness]ports.ChatDriver
	// plugins are the drivers' own agent plugins, each caching the executable
	// it resolved.
	plugins map[domain.AgentHarness]ports.AgentBinaryResolutionInvalidator
}

var _ ports.ChatDriverRegistry = (*Registry)(nil)

// New builds a registry from an explicit driver list. Tests use it to register a
// fake without touching the shipped set.
func New(drivers ...ports.ChatDriver) *Registry {
	byHarness := make(map[domain.AgentHarness]ports.ChatDriver, len(drivers))
	for _, driver := range drivers {
		if driver == nil {
			continue
		}
		byHarness[driver.Harness()] = driver
	}
	return &Registry{drivers: byHarness}
}

// Build returns the drivers the daemon ships.
//
// Codex uses its native app-server protocol. Claude Code uses AO's reusable ACP
// transport plus claude-agent-acp, pointed at the user's own Claude executable.
// Cursor, OpenCode, Droid, Kimi, Kimchi, Pi, OMP, Qwen, Command Code,
// DeepSeek Harness, and GitHub Copilot expose ACP themselves, so AO launches the exact executable
// resolved by each existing agent plugin. No path scrapes terminal output or
// packages a second provider CLI.
// Unreal Agent is compiled into AO and runs behind the same detached host seam.
//
// Every other harness stays TUI-only until the same is true of it. The driver
// reuses the harness's existing agent plugin for binary resolution and auth, so
// registration adds no second answer to "is this agent installed and logged in".
func Build(log *slog.Logger, onClaudeAuthRejected func()) *Registry {
	r := New()
	r.plugins = make(map[domain.AgentHarness]ports.AgentBinaryResolutionInvalidator)
	add := func(driver ports.ChatDriver, plugin any) {
		r.drivers[driver.Harness()] = driver
		if invalidator, ok := plugin.(ports.AgentBinaryResolutionInvalidator); ok {
			r.plugins[driver.Harness()] = invalidator
		}
	}
	codexPlugin := codex.New()
	add(codexappserver.New(codexPlugin, log), codexPlugin)
	claudePlugin := claudecode.New()
	add(claudeacp.New(claudePlugin, log, onClaudeAuthRejected), claudePlugin)
	opencodePlugin := opencode.New()
	add(opencodeacp.New(opencodePlugin, log), opencodePlugin)
	opencodeV2Plugin := opencodev2.New()
	add(opencodev2acp.New(opencodeV2Plugin, log), opencodeV2Plugin)
	droidPlugin := droid.New()
	add(droidacp.New(droidPlugin, log), droidPlugin)
	kimiPlugin := kimi.New()
	add(kimiacp.New(kimiPlugin, log), kimiPlugin)
	kimchiPlugin := kimchi.New()
	add(kimchiacp.New(kimchiPlugin, log), kimchiPlugin)
	piPlugin := pi.New()
	add(piacp.New(piPlugin, log), piPlugin)
	cursorPlugin := cursor.New()
	add(cursoracp.New(cursorPlugin, log), cursorPlugin)
	ompPlugin := omp.New()
	add(ompacp.New(ompPlugin, log), ompPlugin)
	qwenPlugin := qwen.New()
	add(qwenacp.New(qwenPlugin, log), qwenPlugin)
	unrealPlugin := unrealagent.New()
	add(unrealchat.New(unrealPlugin, log), unrealPlugin)
	deepseekPlugin := deepseekharness.New()
	add(deepseekharnessacp.New(deepseekPlugin, log), deepseekPlugin)
	commandCodePlugin := commandcodeagent.New()
	add(commandcodeacp.New(commandCodePlugin, log), commandCodePlugin)
	copilotPlugin := copilot.New()
	add(copilotacp.New(copilotPlugin, log), copilotPlugin)
	agyPlugin := agy.New()
	add(agyacp.New(agyPlugin, log), agyPlugin)
	return r
}

// InvalidateBinaryResolution makes harness's driver resolve its executable
// again, after an install changed which one sessions should run.
func (r *Registry) InvalidateBinaryResolution(harness domain.AgentHarness) {
	if plugin, ok := r.plugins[harness]; ok {
		plugin.InvalidateBinaryResolution()
	}
}

// Driver returns the driver for a harness.
func (r *Registry) Driver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	driver, ok := r.drivers[harness]
	if !ok {
		return nil, ports.ErrChatUnsupported
	}
	return driver, nil
}

// SupportsChat reports whether a harness has a driver at all, without probing the
// local install. Use it to decide whether Chat is even offerable; use Driver plus
// Probe to decide whether it will work right now.
func (r *Registry) SupportsChat(harness domain.AgentHarness) bool {
	_, ok := r.drivers[harness]
	return ok
}

// Harnesses lists the harnesses with a registered driver, for diagnostics and for
// telling a user which agents can run in chat mode.
func (r *Registry) Harnesses() []domain.AgentHarness {
	out := make([]domain.AgentHarness, 0, len(r.drivers))
	for harness := range r.drivers {
		out = append(out, harness)
	}
	return out
}
