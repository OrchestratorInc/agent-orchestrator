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

// TestMergeDropsWorkflowSecrets 删除最终过滤会使 daemon 或 overlay 的宿主秘密进入 Agent。
func TestMergeDropsWorkflowSecrets(t *testing.T) {
	t.Setenv("AO_WORKFLOW_LEASE", "fake-daemon-lease")
	t.Setenv("AO_WORKFLOW_CREDENTIAL_A", "fake-daemon-key")
	t.Setenv("AO_WORKFLOW_PROVIDER_KEY", "fake-forwarded-key")
	got := Merge(map[string]string{
		"ao_workflow_credential_b": "fake-overlay-key",
		"ao_workflow_provider_key": "fake-overlay-forward",
		"aO_wOrKfLoW_lEaSe":        "fake-overlay-lease",
		"AO_SESSION_ID":            "session-1", "AO_BROWSER_CAPABILITY": "session-capability",
	})
	for _, entry := range got {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "AO_WORKFLOW_LEASE") || strings.HasPrefix(strings.ToUpper(key), "AO_WORKFLOW_CREDENTIAL_") || strings.EqualFold(key, "AO_WORKFLOW_PROVIDER_KEY") {
			t.Fatal("child environment retained a host secret")
		}
	}
	if !slices.Contains(got, "AO_SESSION_ID=session-1") || !slices.Contains(got, "AO_BROWSER_CAPABILITY=session-capability") {
		t.Fatal("lease cleanup removed the Session capability or identity")
	}
}
