package registry

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// CallerEnvTag is an environment variable that a coding agent sets in the
// shells and tools it runs, so a command started by that agent can tell who
// called it.
type CallerEnvTag struct {
	Env     string
	Harness domain.AgentHarness
}

// CallerAIAgentEnv is the cross-agent convention: AI_AGENT=<name>, sometimes
// with a version suffix (claude-code_2-1-326_agent). It is checked before the
// per-agent tags.
const CallerAIAgentEnv = "AI_AGENT"

// CallerEnvTags maps per-agent environment tags to registry ids, in the
// order they are checked. Keep it a plain data list: one row per tag.
var CallerEnvTags = []CallerEnvTag{
	{Env: "CLAUDECODE", Harness: domain.HarnessClaudeCode},
	{Env: "CLAUDE_CODE_ENTRYPOINT", Harness: domain.HarnessClaudeCode},
	{Env: "CODEX_THREAD_ID", Harness: domain.HarnessCodex},
	{Env: "CODEX_SANDBOX", Harness: domain.HarnessCodex},
	{Env: "CODEX_CI", Harness: domain.HarnessCodex},
	{Env: "GEMINI_CLI", Harness: domain.HarnessGemini},
	{Env: "CURSOR_AGENT", Harness: domain.HarnessCursor},
	{Env: "OPENCODE", Harness: domain.HarnessOpenCode},
	{Env: "OPENCODE_CLIENT", Harness: domain.HarnessOpenCode},
}

// callerAIAgentAliases maps AI_AGENT names that are not registry ids.
var callerAIAgentAliases = map[string]domain.AgentHarness{
	"claude":         domain.HarnessClaudeCode,
	"gemini-cli":     domain.HarnessGemini,
	"github-copilot": domain.HarnessCopilot,
	"copilot-cli":    domain.HarnessCopilot,
	"cursor-agent":   domain.HarnessCursor,
	"cursor-cli":     domain.HarnessCursor,
	"codex-cli":      domain.HarnessCodex,
}

// CallerMatch is the calling agent found in the environment and the variable
// that named it.
type CallerMatch struct {
	Harness domain.AgentHarness
	Env     string
	Value   string
}

// DetectCaller finds the coding agent that started this process from its
// environment: AI_AGENT first, then the per-agent tags. Only ids this
// registry ships are returned. getenv is os.Getenv in production.
func DetectCaller(getenv func(string) string) (CallerMatch, bool) {
	known := map[domain.AgentHarness]bool{}
	for _, item := range Harnessed() {
		known[item.Harness] = true
	}
	if raw := strings.TrimSpace(getenv(CallerAIAgentEnv)); raw != "" {
		if harness, ok := aiAgentHarness(raw, known); ok {
			return CallerMatch{Harness: harness, Env: CallerAIAgentEnv, Value: raw}, true
		}
	}
	for _, tag := range CallerEnvTags {
		value := strings.TrimSpace(getenv(tag.Env))
		if !envTagSet(value) || !known[tag.Harness] {
			continue
		}
		return CallerMatch{Harness: tag.Harness, Env: tag.Env, Value: value}, true
	}
	return CallerMatch{}, false
}

// aiAgentHarness resolves an AI_AGENT value. Registry ids use hyphens, so
// anything after the first underscore is a version or variant suffix.
func aiAgentHarness(raw string, known map[domain.AgentHarness]bool) (domain.AgentHarness, bool) {
	name := strings.ToLower(raw)
	candidates := []string{name}
	if base, _, ok := strings.Cut(name, "_"); ok && base != "" {
		candidates = append(candidates, base)
	}
	for _, candidate := range candidates {
		if harness, ok := callerAIAgentAliases[candidate]; ok && known[harness] {
			return harness, true
		}
		if harness := domain.AgentHarness(candidate); known[harness] {
			return harness, true
		}
	}
	return "", false
}

func envTagSet(value string) bool {
	switch strings.ToLower(value) {
	case "", "0", "false":
		return false
	}
	return true
}
