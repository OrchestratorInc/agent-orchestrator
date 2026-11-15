package domain

type AgentHarness string

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
	HarnessMiniMaxCode AgentHarness = "minimax-code"
	HarnessMemcode     AgentHarness = "memcode"
	HarnessDeepSeek    AgentHarness = "deepseek-harness"
	HarnessOpenHands   AgentHarness = "openhands"
	HarnessCommandCode AgentHarness = "command-code"
	HarnessDockerAgent AgentHarness = "docker-agent"
	HarnessHermes      AgentHarness = "hermes"
	HarnessStakpak     AgentHarness = "stakpak"
	HarnessFake        AgentHarness = "fake"
)

var AllHarnesses = []AgentHarness{
	HarnessClaudeCode, HarnessCodex, HarnessAider, HarnessOpenCode, HarnessOpenCodeV2, HarnessGrok,
	HarnessDroid, HarnessAmp, HarnessAgy, HarnessCrush, HarnessCursor, HarnessQwen, HarnessGemini,
	HarnessCopilot, HarnessGoose, HarnessAuggie, HarnessContinue, HarnessDevin,
	HarnessCline, HarnessKimi, HarnessMuse, HarnessKiro, HarnessKilocode, HarnessVibe, HarnessPi,
	HarnessKimchi, HarnessPrimeAgent, HarnessAutohand,
	HarnessOMP, HarnessFX, HarnessUnreal, HarnessCodewhale, HarnessMiMoCode, HarnessMiniMaxCode, HarnessDeepSeek, HarnessOpenHands,
	HarnessCommandCode, HarnessDockerAgent, HarnessMemcode, HarnessHermes, HarnessStakpak,
}

func (h AgentHarness) ChatOnly() bool {
	return h == HarnessUnreal || h == HarnessDockerAgent
}

func (h AgentHarness) IsKnown() bool {
	for _, k := range AllHarnesses {
		if h == k {
			return true
		}
	}
	return false
}
