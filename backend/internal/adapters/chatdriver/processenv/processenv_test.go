package processenv

import (
	"slices"
	"strings"
	"testing"
)

// A daemon started from inside a Claude Code session (a developer running
// rebuild-ao.sh, or the desktop app launched from a terminal) inherits that
// session's own identity markers — CLAUDECODE, CLAUDE_CODE_SESSION_ID,
// CLAUDE_CODE_CHILD_SESSION, and friends. Forwarding those to every worker
// this daemon spawns makes a worker's own claude-code process misidentify
// itself as a child of that unrelated parent session ("Transcript saving is
// off — inherited CLAUDE_CODE_CHILD_SESSION marker"). None of these are read
// by any AO code path; they exist purely as ambient contamination. Auth
// credentials (ANTHROPIC_*, CLAUDE_CODE_OAUTH_TOKEN) are deliberately NOT
// blocked — claudecode.go reads them for its own auth probe, and blocking
// them would break every worker's ability to authenticate.
func TestMergeDropsInheritedClaudeSessionMarkers(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "parent-session-should-not-leak")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("CLAUDE_CODE_BRIDGE_SESSION_ID", "session_parent")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "parent-token")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/parent.sock")
	t.Setenv("CLAUDE_CODE_EXECPATH", "/parent/execpath")
	t.Setenv("CLAUDE_PID", "12345")
	t.Setenv("CLAUDE_PLUGIN_DATA", "/parent/plugins")
	t.Setenv("CLAUDE_EFFORT", "medium")
	t.Setenv("CLAUDE_CODE_MAX_CONTEXT_TOKENS", "1000")
	t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", "parent-model")
	// Auth credentials must survive — workers need them to authenticate.
	t.Setenv("ANTHROPIC_API_KEY", "sk-keep-me")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-keep-me")
	// A plain user-set var sharing the CLAUDE_ prefix but not a known parent-
	// session marker must also survive (denylist, not a blanket CLAUDE_ block).
	t.Setenv("CLAUDE_CUSTOM_USER_VAR", "keep-me-too")

	got := Merge(nil)

	blocked := []string{
		"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_BRIDGE_SESSION_ID",
		"CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_EXECPATH", "CLAUDE_PID", "CLAUDE_PLUGIN_DATA",
		"CLAUDE_EFFORT", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_SUBAGENT_MODEL",
	}
	for _, entry := range got {
		key, _, _ := strings.Cut(entry, "=")
		for _, b := range blocked {
			if key == b {
				t.Errorf("parent-session marker %s leaked into worker env: %q", key, entry)
			}
		}
	}

	kept := map[string]string{
		"ANTHROPIC_API_KEY":       "sk-keep-me",
		"CLAUDE_CODE_OAUTH_TOKEN": "oauth-keep-me",
		"CLAUDE_CUSTOM_USER_VAR":  "keep-me-too",
	}
	for _, entry := range got {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if want, exists := kept[key]; exists {
			if value != want {
				t.Errorf("%s = %q, want %q", key, value, want)
			}
			delete(kept, key)
		}
	}
	if len(kept) != 0 {
		t.Errorf("expected non-marker vars missing from worker env: %v", kept)
	}
}

func TestMergeInheritsDaemonEnvironmentAndAppliesOverlay(t *testing.T) {
	// Windows has prefix-related names such as PROGRAMFILES and PROGRAMFILES(X86).
	t.Setenv("AO_PROCESSENV", "prefix")
	t.Setenv("AO_PROCESSENV(X86)", "related")
	t.Setenv("AO_PROCESSENV_INHERITED", "parent")
	t.Setenv("AO_PROCESSENV_REPLACED", "old")

	got := Merge(map[string]string{
		"AO_PROCESSENV_REPLACED": "new",
		"AO_PROCESSENV_SESSION":  "session",
	})
	if !slices.IsSorted(got) {
		t.Fatalf("environment is not sorted: %v", got)
	}
	want := map[string]string{
		"AO_PROCESSENV_INHERITED": "parent",
		"AO_PROCESSENV_REPLACED":  "new",
		"AO_PROCESSENV_SESSION":   "session",
	}
	for _, entry := range got {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			if expected, exists := want[key]; exists {
				if value != expected {
					t.Fatalf("%s = %q, want %q", key, value, expected)
				}
				delete(want, key)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing environment values: %v", want)
	}
}

func TestMergeWindowsExactPATHWinsConflictingOverlaySpelling(t *testing.T) {
	for range 1000 {
		got := merge(
			[]string{"Path=inherited"},
			map[string]string{"Path": "project", "PATH": "ao-pinned"},
			true,
		)
		var paths []string
		for _, entry := range got {
			key, _, _ := strings.Cut(entry, "=")
			if strings.EqualFold(key, "PATH") {
				paths = append(paths, entry)
			}
		}
		if !slices.Equal(paths, []string{"PATH=ao-pinned"}) {
			t.Fatalf("PATH entries = %v, want protected AO PATH", paths)
		}
	}
}
