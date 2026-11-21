package envfilter

import "testing"

func TestIsParentSessionMarkerBlocksKnownMarkers(t *testing.T) {
	blocked := []string{
		"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_BRIDGE_SESSION_ID",
		"CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_EXECPATH", "CLAUDE_PID", "CLAUDE_PLUGIN_DATA",
		"CLAUDE_EFFORT", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_SUBAGENT_MODEL",
	}
	for _, key := range blocked {
		if !IsParentSessionMarker(key) {
			t.Errorf("IsParentSessionMarker(%q) = false, want true", key)
		}
	}
}

func TestIsParentSessionMarkerKeepsAuthAndUserVars(t *testing.T) {
	// Auth credentials must never be blocked — workers need them to
	// authenticate, and claudecode.go reads CLAUDE_CODE_OAUTH_TOKEN directly.
	kept := []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		// A plain user-set var sharing the CLAUDE_ prefix but not a known
		// parent-session marker: this is a denylist, not a blanket prefix block.
		"CLAUDE_CUSTOM_USER_VAR",
		"PATH", "HOME", "LANG",
	}
	for _, key := range kept {
		if IsParentSessionMarker(key) {
			t.Errorf("IsParentSessionMarker(%q) = true, want false", key)
		}
	}
}

func TestDropParentSessionMarkersFiltersEntries(t *testing.T) {
	in := []string{
		"PATH=/bin",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"ANTHROPIC_API_KEY=sk-keep",
		"CLAUDECODE=1",
	}
	got := DropParentSessionMarkers(in)
	want := []string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-keep"}
	if len(got) != len(want) {
		t.Fatalf("DropParentSessionMarkers(%v) = %v, want %v", in, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DropParentSessionMarkers(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestDropParentSessionMarkersPreservesMalformedEntries(t *testing.T) {
	// An entry with no "=" is malformed but must not be silently dropped —
	// that would be a behavior change unrelated to this filter's purpose.
	in := []string{"PATH=/bin", "MALFORMED_NO_EQUALS"}
	got := DropParentSessionMarkers(in)
	if len(got) != 2 || got[1] != "MALFORMED_NO_EQUALS" {
		t.Fatalf("DropParentSessionMarkers(%v) = %v, want malformed entry preserved", in, got)
	}
}
