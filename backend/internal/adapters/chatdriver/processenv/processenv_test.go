package processenv

import (
	"slices"
	"strings"
	"testing"
)

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

// TestMergeDropsLegacyWorkflowLease 删除最终过滤会使 daemon 或 overlay 的旧租约进入 Agent。
func TestMergeDropsLegacyWorkflowLease(t *testing.T) {
	t.Setenv("AO_WORKFLOW_LEASE", "fake-daemon-lease")
	got := Merge(map[string]string{
		"aO_wOrKfLoW_lEaSe": "fake-overlay-lease",
		"AO_SESSION_ID":     "session-1", "AO_BROWSER_CAPABILITY": "session-capability",
	})
	for _, entry := range got {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "AO_WORKFLOW_LEASE") {
			t.Fatal("child environment retained the legacy lease")
		}
	}
	if !slices.Contains(got, "AO_SESSION_ID=session-1") || !slices.Contains(got, "AO_BROWSER_CAPABILITY=session-capability") {
		t.Fatal("lease cleanup removed the Session capability or identity")
	}
}
