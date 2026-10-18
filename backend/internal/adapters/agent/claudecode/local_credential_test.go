package claudecode

import "testing"

// Claude Code sends the effort parameter only for model ids it recognizes as
// effort-capable; behind ANTHROPIC_BASE_URL a custom id like glm-… would
// silently run the provider's own default no matter what --effort says. The
// adapter opts every spawn into pass-through, and scrubs an inherited
// CLAUDE_CODE_EFFORT_LEVEL, which per Claude Code docs outranks --effort.
func TestAugmentRuntimeEnvForcesEffortPassthrough(t *testing.T) {
	env := map[string]string{
		"CLAUDE_CODE_EFFORT_LEVEL": "low",
		"PATH":                     "/usr/bin",
	}
	plugin := &Plugin{}
	plugin.AugmentRuntimeEnv(env, t.TempDir())
	if env["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"] != "1" {
		t.Fatalf("CLAUDE_CODE_ALWAYS_ENABLE_EFFORT = %q, want \"1\"", env["CLAUDE_CODE_ALWAYS_ENABLE_EFFORT"])
	}
	if _, ok := env["CLAUDE_CODE_EFFORT_LEVEL"]; ok {
		t.Fatal("inherited CLAUDE_CODE_EFFORT_LEVEL survived; it would override --effort")
	}
}
