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
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/dockeragent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/grok"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
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
	dockeragentacp "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/dockeragentacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/droidacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/grokacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/hermesacp"
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

type Registry struct {
	drivers map[domain.AgentHarness]ports.ChatDriver
	plugins map[domain.AgentHarness]ports.AgentBinaryResolutionInvalidator
}

var _ ports.ChatDriverRegistry = (*Registry)(nil)

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
	grokPlugin := grok.New()
	add(grokacp.New(grokPlugin, log), grokPlugin)
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
	dockerAgentPlugin := dockeragent.New()
	add(dockeragentacp.New(dockerAgentPlugin, log), dockerAgentPlugin)
	hermesPlugin := hermes.New()
	add(hermesacp.New(hermesPlugin, log), hermesPlugin)
	return r
}

func (r *Registry) InvalidateBinaryResolution(harness domain.AgentHarness) {
	if plugin, ok := r.plugins[harness]; ok {
		plugin.InvalidateBinaryResolution()
	}
}

func (r *Registry) Driver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	driver, ok := r.drivers[harness]
	if !ok {
		return nil, ports.ErrChatUnsupported
	}
	return driver, nil
}

func (r *Registry) SupportsChat(harness domain.AgentHarness) bool {
	_, ok := r.drivers[harness]
	return ok
}

func (r *Registry) Harnesses() []domain.AgentHarness {
	out := make([]domain.AgentHarness, 0, len(r.drivers))
	for harness := range r.drivers {
		out = append(out, harness)
	}
	return out
}
