package registry

import (
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/amp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/auggie"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/autohand"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codewhale"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/commandcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/continueagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/crush"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/deepseekharness"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/devin"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/dockeragent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/fx"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/gemini"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/goose"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/grok"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kilocode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimchi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kiro"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/memcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/mimocode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/minimaxcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/omp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/openhands"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/pi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/primeagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/qwen"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/stakpak"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/unrealagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/vibe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func Constructors() []adapters.Adapter {
	return []adapters.Adapter{
		claudecode.New(),
		codex.New(),
		opencode.New(),
		opencodev2.New(),
		grok.New(),
		cursor.New(),
		qwen.New(),
		gemini.New(),
		copilot.New(),
		kimi.New(),
		muse.New(),
		droid.New(),
		amp.New(),
		agy.New(),
		crush.New(),
		aider.New(),
		goose.New(),
		auggie.New(),
		continueagent.New(),
		devin.New(),
		omp.New(),
		codewhale.New(),
		openhands.New(),
		cline.New(),
		kiro.New(),
		kilocode.New(),
		vibe.New(),
		pi.New(),
		kimchi.New(),
		primeagent.New(),
		autohand.New(),
		fx.New(),
		commandcode.New(),
		hermes.New(),
		unrealagent.New(),
		mimocode.New(),
		minimaxcode.New(),
		memcode.New(),
		deepseekharness.New(),
		dockeragent.New(),
		stakpak.New(),
	}
}

func Build() (*adapters.Registry, error) {
	reg := adapters.NewRegistry()
	for _, a := range Constructors() {
		if err := reg.Register(a); err != nil {
			return nil, fmt.Errorf("register agent adapter %q: %w", a.Manifest().ID, err)
		}
	}
	return reg, nil
}

type HarnessAgent struct {
	Harness  domain.AgentHarness
	Manifest adapters.Manifest
	Agent    ports.Agent
}

func Harnessed() []HarnessAgent {
	cons := Constructors()
	out := make([]HarnessAgent, 0, len(cons))
	for _, a := range cons {
		agent, ok := a.(ports.Agent)
		if !ok {
			continue
		}
		out = append(out, HarnessAgent{
			Harness:  domain.AgentHarness(a.Manifest().ID),
			Manifest: a.Manifest(),
			Agent:    agent,
		})
	}
	return out
}
