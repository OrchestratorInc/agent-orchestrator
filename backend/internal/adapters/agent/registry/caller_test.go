package registry

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionenv"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetectCaller(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		want    domain.AgentHarness
		wantEnv string
		found   bool
	}{
		{name: "nothing set", env: map[string]string{}},
		{name: "AI_AGENT registry id", env: map[string]string{"AI_AGENT": "codex"}, want: domain.HarnessCodex, wantEnv: "AI_AGENT", found: true},
		{name: "AI_AGENT with version suffix", env: map[string]string{"AI_AGENT": "claude-code_2-1-326_agent"}, want: domain.HarnessClaudeCode, wantEnv: "AI_AGENT", found: true},
		{name: "AI_AGENT alias", env: map[string]string{"AI_AGENT": "github-copilot"}, want: domain.HarnessCopilot, wantEnv: "AI_AGENT", found: true},
		{name: "AI_AGENT beats tags", env: map[string]string{"AI_AGENT": "gemini", "CLAUDECODE": "1"}, want: domain.HarnessGemini, wantEnv: "AI_AGENT", found: true},
		{name: "unknown AI_AGENT falls through to tags", env: map[string]string{"AI_AGENT": "some-ide", "CODEX_THREAD_ID": "t1"}, want: domain.HarnessCodex, wantEnv: "CODEX_THREAD_ID", found: true},
		{name: "unknown AI_AGENT alone", env: map[string]string{"AI_AGENT": "some-ide"}},
		{name: "CLAUDECODE", env: map[string]string{"CLAUDECODE": "1"}, want: domain.HarnessClaudeCode, wantEnv: "CLAUDECODE", found: true},
		{name: "tag set to 0 is ignored", env: map[string]string{"CLAUDECODE": "0", "GEMINI_CLI": "1"}, want: domain.HarnessGemini, wantEnv: "GEMINI_CLI", found: true},
		{name: "cursor", env: map[string]string{"CURSOR_AGENT": "1"}, want: domain.HarnessCursor, wantEnv: "CURSOR_AGENT", found: true},
		{name: "opencode", env: map[string]string{"OPENCODE": "1"}, want: domain.HarnessOpenCode, wantEnv: "OPENCODE", found: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DetectCaller(envFrom(tc.env))
			if ok != tc.found || got.Harness != tc.want || (ok && got.Env != tc.wantEnv) {
				t.Fatalf("DetectCaller() = %+v, %v; want %q via %q, %v", got, ok, tc.want, tc.wantEnv, tc.found)
			}
		})
	}
}

// Every tag and alias must name an adapter the registry ships, so detection
// never hands the daemon an id it does not know.
func TestCallerTagsNameShippedAgents(t *testing.T) {
	known := map[domain.AgentHarness]bool{}
	for _, item := range Harnessed() {
		known[item.Harness] = true
	}
	for _, tag := range CallerEnvTags {
		if !known[tag.Harness] {
			t.Errorf("CallerEnvTags: %s maps to unknown agent %q", tag.Env, tag.Harness)
		}
	}
	for alias, harness := range callerAIAgentAliases {
		if !known[harness] {
			t.Errorf("callerAIAgentAliases: %s maps to unknown agent %q", alias, harness)
		}
	}
}

// Every tag that names the calling agent must also be scrubbed from the
// environment of a daemon that agent starts (sessionenv.ScrubForDaemon), or
// the daemon's sessions would think they run inside the caller.
func TestCallerTagsAreScrubbedFromDaemons(t *testing.T) {
	scrubbed := map[string]bool{}
	for _, k := range sessionenv.CallerMarkers {
		scrubbed[k] = true
	}
	for _, k := range append([]string{CallerAIAgentEnv}, callerTagEnvs()...) {
		if !scrubbed[k] {
			t.Errorf("%s detects the calling agent but is not in sessionenv.CallerMarkers", k)
		}
	}
}

func callerTagEnvs() []string {
	out := make([]string, 0, len(CallerEnvTags))
	for _, tag := range CallerEnvTags {
		out = append(out, tag.Env)
	}
	return out
}
