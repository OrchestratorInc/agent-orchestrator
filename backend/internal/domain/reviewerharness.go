package domain

type ReviewerHarness string

const (
	ReviewerClaudeCode ReviewerHarness = "claude-code"
	ReviewerCodex      ReviewerHarness = "codex"
	ReviewerCopilot    ReviewerHarness = "copilot"
	ReviewerCursor     ReviewerHarness = "cursor"
	ReviewerKiloCode   ReviewerHarness = "kilocode"
	ReviewerKimchi     ReviewerHarness = "kimchi"
	ReviewerOpenCode   ReviewerHarness = "opencode"
	ReviewerOpenCodeV2 ReviewerHarness = "opencode-v2"
	ReviewerKiro       ReviewerHarness = "kiro"
	ReviewerPi         ReviewerHarness = "pi"
	ReviewerAgy        ReviewerHarness = "agy"
	ReviewerDevin      ReviewerHarness = "devin"
	ReviewerDroid      ReviewerHarness = "droid"
	ReviewerKimi       ReviewerHarness = "kimi"
	ReviewerMuse       ReviewerHarness = "muse"
	ReviewerAmp        ReviewerHarness = "amp"
	ReviewerAider      ReviewerHarness = "aider"
	ReviewerGrok       ReviewerHarness = "grok"
	ReviewerCrush      ReviewerHarness = "crush"
	ReviewerAuggie     ReviewerHarness = "auggie"
	ReviewerCline      ReviewerHarness = "cline"
	ReviewerAutohand   ReviewerHarness = "autohand"
)

var AllReviewerHarnesses = []ReviewerHarness{
	ReviewerClaudeCode,
	ReviewerCodex,
	ReviewerCopilot,
	ReviewerCursor,
	ReviewerKiloCode,
	ReviewerKimchi,
	ReviewerOpenCode,
	ReviewerOpenCodeV2,
	ReviewerKiro,
	ReviewerPi,
	ReviewerAgy,
	ReviewerDevin,
	ReviewerDroid,
	ReviewerKimi,
	ReviewerMuse,
	ReviewerAmp,
	ReviewerAider,
	ReviewerGrok,
	ReviewerCrush,
	ReviewerAuggie,
	ReviewerCline,
	ReviewerAutohand,
}

func (h ReviewerHarness) IsKnown() bool {
	for _, k := range AllReviewerHarnesses {
		if h == k {
			return true
		}
	}
	return false
}
