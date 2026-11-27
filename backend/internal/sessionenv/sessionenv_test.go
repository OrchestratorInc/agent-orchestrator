package sessionenv

import (
	"slices"
	"testing"
)

func TestScrubForDaemon(t *testing.T) {
	in := []string{
		"PATH=/bin", "CLAUDECODE=1", "CLAUDE_CODE_HOST_SESSION_ID=x", "CLAUDE_AGENT_SDK_VERSION=1",
		"CLAUDE_CODE_USE_BEDROCK=1", "AI_AGENT=codex", "AO_SESSION_ID=a", "AO_TMUX_SOCKET_NAME=s",
		"AO_DATA_DIR=/d", "AO_RUN_FILE=/r", "AO_PORT=1", "AO_AGENT_BROWSER_URL=http://127.0.0.1:9", "AO_AGENT_BROWSER_TOKEN=t",
		"AO_TELEMETRY_REMOTE=off", "NOEQUALS",
	}
	got := ScrubForDaemon(in)
	want := []string{"PATH=/bin", "CLAUDE_CODE_USE_BEDROCK=1", "AO_DATA_DIR=/d", "AO_RUN_FILE=/r", "AO_PORT=1", "AO_AGENT_BROWSER_URL=http://127.0.0.1:9", "AO_AGENT_BROWSER_TOKEN=t", "AO_TELEMETRY_REMOTE=off", "NOEQUALS"}
	if !slices.Equal(got, want) {
		t.Fatalf("ScrubForDaemon =\n%v\nwant\n%v", got, want)
	}
}

// Every session variable is either scrubbed or deliberately kept as daemon
// configuration.
func TestEverySessionVarIsHandled(t *testing.T) {
	for _, k := range SessionVars {
		out := ScrubForDaemon([]string{k + "=v"})
		if len(out) == 1 && !daemonConfigVars[k] {
			t.Errorf("%s survives the scrub but is not daemon configuration", k)
		}
	}
}

// Inside a Claude Code session, the host's per-session CLAUDE_* settings go;
// documented user settings stay. Outside one, CLAUDE_* settings are the user's.
func TestScrubForDaemonClaudeHostSettings(t *testing.T) {
	host := []string{"CLAUDECODE=1", "CLAUDE_CODE_TERMINAL_MCP_TOOLS=x", "CLAUDE_CODE_SIMPLE=0", "CLAUDE_EFFORT=medium", "CLAUDE_PREVIEW_CLASSIFIER_FLOOR=1", "CLAUDE_CODE_USE_BEDROCK=1", "CLAUDE_CONFIG_DIR=/c", "PATH=/bin"}
	if got, want := ScrubForDaemon(host), []string{"CLAUDE_CODE_USE_BEDROCK=1", "CLAUDE_CONFIG_DIR=/c", "PATH=/bin"}; !slices.Equal(got, want) {
		t.Fatalf("inside Claude Code: %v, want %v", got, want)
	}
	plain := []string{"CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1", "CLAUDE_EFFORT=high", "PATH=/bin"}
	if got := ScrubForDaemon(plain); !slices.Equal(got, plain) {
		t.Fatalf("plain shell: %v, want all kept", got)
	}
}
