package domain

// AgentHarness identifies which agent CLI/runtime a session drives.
type AgentHarness string

// Supported agent harnesses.
const (
	HarnessClaudeCode  AgentHarness = "claude-code"
	HarnessCodex       AgentHarness = "codex"
	HarnessAider       AgentHarness = "aider"
	HarnessOpenCode    AgentHarness = "opencode"
	HarnessOpenCodeV2  AgentHarness = "opencode-v2"
	HarnessGrok        AgentHarness = "grok"
	HarnessDroid       AgentHarness = "droid"
	HarnessAmp         AgentHarness = "amp"
	HarnessAgy         AgentHarness = "agy"
	HarnessCrush       AgentHarness = "crush"
	HarnessCursor      AgentHarness = "cursor"
	HarnessQwen        AgentHarness = "qwen"
	HarnessGemini      AgentHarness = "gemini"
	HarnessCopilot     AgentHarness = "copilot"
	HarnessGoose       AgentHarness = "goose"
	HarnessAuggie      AgentHarness = "auggie"
	HarnessContinue    AgentHarness = "continue"
	HarnessDevin       AgentHarness = "devin"
	HarnessCline       AgentHarness = "cline"
	HarnessKimi        AgentHarness = "kimi"
	HarnessMuse        AgentHarness = "muse"
	HarnessKiro        AgentHarness = "kiro"
	HarnessKilocode    AgentHarness = "kilocode"
	HarnessVibe        AgentHarness = "vibe"
	HarnessPi          AgentHarness = "pi"
	HarnessKimchi      AgentHarness = "kimchi"
	HarnessPrimeAgent  AgentHarness = "prime-agent"
	HarnessAutohand    AgentHarness = "autohand"
	HarnessOMP         AgentHarness = "omp"
	HarnessFX          AgentHarness = "fx"
	HarnessUnreal      AgentHarness = "unreal-agent"
	HarnessCodewhale   AgentHarness = "codewhale"
	HarnessMiMoCode    AgentHarness = "mimo-code"
	HarnessMemcode     AgentHarness = "memcode"
	HarnessDeepSeek    AgentHarness = "deepseek-harness"
	HarnessOpenHands   AgentHarness = "openhands"
	HarnessCommandCode AgentHarness = "command-code"
	HarnessHermes      AgentHarness = "hermes"
	// HarnessFake is retained for existing test fixtures and historical session
	// rows, but is not user-selectable.
	HarnessFake AgentHarness = "fake"
)

// AllHarnesses lists every supported harness. It is the canonical set used to
// validate user-supplied harness names (e.g. per-project role overrides).
var AllHarnesses = []AgentHarness{
	HarnessClaudeCode, HarnessCodex, HarnessAider, HarnessOpenCode, HarnessOpenCodeV2, HarnessGrok,
	HarnessDroid, HarnessAmp, HarnessAgy, HarnessCrush, HarnessCursor, HarnessQwen, HarnessGemini,
	HarnessCopilot, HarnessGoose, HarnessAuggie, HarnessContinue, HarnessDevin,
	HarnessCline, HarnessKimi, HarnessMuse, HarnessKiro, HarnessKilocode, HarnessVibe, HarnessPi,
	HarnessKimchi, HarnessPrimeAgent, HarnessAutohand,
	HarnessOMP, HarnessFX, HarnessUnreal, HarnessCodewhale, HarnessMiMoCode, HarnessDeepSeek, HarnessOpenHands,
	HarnessCommandCode, HarnessMemcode, HarnessHermes,
}

// DefaultHarnessPriority orders the agents a new session may default to by
// popularity and how well AO integrates with each. It is the only place the
// default is decided; a session's own harness is the source of truth after it
// is created.
var DefaultHarnessPriority = []AgentHarness{
	HarnessClaudeCode, HarnessCodex, HarnessCursor, HarnessOpenCode, HarnessOpenCodeV2, HarnessAider,
}

// ChooseDefaultHarness returns the first harness in DefaultHarnessPriority that
// readiness reports ready, else the first one it cannot rule out (unknown), else
// the top of the list so a launch fails with the harness's own install or login
// error instead of a generic one.
func ChooseDefaultHarness(readiness func(AgentHarness) AgentEffectiveReadiness) AgentHarness {
	for _, want := range []AgentEffectiveReadiness{AgentReadinessReady, AgentReadinessUnknown} {
		for _, h := range DefaultHarnessPriority {
			if readiness(h) == want {
				return h
			}
		}
	}
	return DefaultHarnessPriority[0]
}

// IsKnown reports whether h is one of the supported harnesses.
func (h AgentHarness) IsKnown() bool {
	for _, k := range AllHarnesses {
		if h == k {
			return true
		}
	}
	return false
}
