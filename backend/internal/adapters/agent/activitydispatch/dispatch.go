package activitydispatch

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitystate"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/amp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/auggie"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/commandcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/continueagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/fake"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/gemini"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimchi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kiro"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/memcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/mimocode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/minimaxcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/omp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/pi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/primeagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/vibe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type DeriveFunc func(event string, payload []byte) (domain.ActivityState, bool)

var Derivers = map[string]DeriveFunc{
	"claude-code":  claudecode.DeriveActivityState,
	"command-code": commandcode.DeriveActivityState,
	"grok":         claudecode.DeriveActivityState,
	"muse":         muse.DeriveActivityState,
	"omp":          omp.DeriveActivityState,
	"codex":        codex.DeriveActivityState,
	"continue":     continueagent.DeriveActivityState,
	"droid":        droid.DeriveActivityState,
	"agy":          agy.DeriveActivityState,
	"aider":        aider.DeriveActivityState,
	"kimchi":       kimchi.DeriveActivityState,
	"opencode":     opencode.DeriveActivityState,
	"opencode-v2":  opencodev2.DeriveActivityState,
	"prime-agent":  primeagent.DeriveActivityState,
	"mimo-code":    mimocode.DeriveActivityState,
	"minimax-code": minimaxcode.DeriveActivityState,
	"memcode":      memcode.DeriveActivityState,
	"amp":          amp.DeriveActivityState,
	"pi":           pi.DeriveActivityState,
	"auggie":       auggie.DeriveActivityState,
	"goose":        activitystate.StandardDeriveActivityState,
	"openhands":    activitystate.StandardDeriveActivityState,
	"devin":        activitystate.StandardDeriveActivityState,
	"cursor":       cursor.DeriveActivityState,
	"qwen":         activitystate.StandardDeriveActivityState,
	"gemini":       gemini.DeriveActivityState,
	"copilot":      activitystate.StandardDeriveActivityState,
	"kimi":         activitystate.StandardDeriveActivityState,
	"cline":        cline.DeriveActivityState,
	"kiro":         kiro.DeriveActivityState,
	"kilocode":     activitystate.StandardDeriveActivityState,
	"autohand":     activitystate.StandardDeriveActivityState,
	"vibe":         vibe.DeriveActivityState,
	"fake":         fake.DeriveActivityState,
}

func AgentSessionID(agent string, payload []byte) (string, bool) {
	if agent != "cline" {
		return "", false
	}
	return cline.SessionIDFromHook(payload)
}

type SignalCoverage uint8

const (
	SignalCoverageNone SignalCoverage = iota
	SignalCoveragePartial
	SignalCoverageComplete
)

var signalCoverageOverrides = map[domain.AgentHarness]SignalCoverage{
	domain.HarnessAider:       SignalCoveragePartial,
	domain.HarnessCommandCode: SignalCoveragePartial,
	domain.HarnessContinue:    SignalCoveragePartial,
	domain.HarnessMemcode:     SignalCoveragePartial,
}

func CoverageForHarness(h domain.AgentHarness) SignalCoverage {
	if coverage, ok := signalCoverageOverrides[h]; ok {
		return coverage
	}
	if _, ok := Derivers[string(h)]; ok {
		return SignalCoverageComplete
	}
	return SignalCoverageNone
}

func Derive(agent, event string, payload []byte) (domain.ActivityState, bool) {
	derive, found := Derivers[agent]
	if !found {
		return "", false
	}
	return derive(event, payload)
}

func SupportsHarness(h domain.AgentHarness) bool {
	return CoverageForHarness(h) != SignalCoverageNone
}

func FullySupportsHarness(h domain.AgentHarness) bool {
	return CoverageForHarness(h) == SignalCoverageComplete
}
