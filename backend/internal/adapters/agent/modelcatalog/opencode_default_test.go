package modelcatalog

import (
	"os"
	"path/filepath"
	"testing"
)

// openCodeFixture lays out root/outer/repo (a git repository) with the
// session working directory at repo/pkg and a global config selecting "global/a".
type openCodeFixture struct {
	outer, repo, pkg string
}

func newOpenCodeFixture(t *testing.T) openCodeFixture {
	t.Helper()
	home := isolateHome(t)
	root := t.TempDir()
	f := openCodeFixture{outer: filepath.Join(root, "outer")}
	f.repo = filepath.Join(f.outer, "repo")
	f.pkg = filepath.Join(f.repo, "pkg")
	if err := os.MkdirAll(filepath.Join(f.repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"model": "global/a"}`)
	return f
}

func modelJSON(model string) string { return `{"model": "` + model + `"}` }

func TestOpenCodeDefaultFollowsEachMajorsLayering(t *testing.T) {
	for _, tc := range []struct {
		agentID string
		// v2 also reads the launch worktree's ancestors above its git root,
		// which discovery cannot see. Their direct and .opencode files outrank
		// the global config and in-repository direct files, so those stay
		// unresolved for v2.
		global, repo, pkg string
	}{
		{agentID: "opencode", global: "global/a", repo: "repo/c", pkg: "pkg/d"},
		{agentID: "opencode-v2"},
	} {
		agentID := tc.agentID
		t.Run(agentID, func(t *testing.T) {
			resolve := func(f openCodeFixture, env map[string]string) string {
				return configuredDefaultModel(agentID, f.pkg, env)
			}

			f := newOpenCodeFixture(t)
			if got := resolve(f, nil); got != tc.global {
				t.Errorf("global only = %q, want %q", got, tc.global)
			}

			// Ancestor project config inside the repository applies, inner wins.
			writeConfig(t, filepath.Join(f.repo, "opencode.json"), modelJSON("repo/c"))
			if got := resolve(f, nil); got != tc.repo {
				t.Errorf("ancestor repo config = %q, want %q", got, tc.repo)
			}
			writeConfig(t, filepath.Join(f.pkg, "opencode.jsonc"), "// pkg\n"+modelJSON("pkg/d"))
			if got := resolve(f, nil); got != tc.pkg {
				t.Errorf("inner project config = %q, want %q", got, tc.pkg)
			}

			// Every .opencode file overrides every direct file, even an inner one.
			// For v2 an in-repository .opencode file also outranks every
			// ancestor .opencode file, so it is certain for both majors.
			writeConfig(t, filepath.Join(f.repo, ".opencode", "opencode.json"), modelJSON("dotdir/b"))
			if got := resolve(f, nil); got != "dotdir/b" {
				t.Errorf(".opencode config = %q, want dotdir/b", got)
			}

			// Inline content outranks files; managed config outranks everything.
			if got := resolve(f, map[string]string{"OPENCODE_CONFIG_CONTENT": modelJSON("inline/e")}); got != "inline/e" {
				t.Errorf("OPENCODE_CONFIG_CONTENT = %q, want inline/e", got)
			}
			managed := t.TempDir()
			openCodeManagedConfigDirs = func() []string { return []string{managed} }
			writeConfig(t, filepath.Join(managed, "opencode.json"), modelJSON("managed/f"))
			if got := resolve(f, map[string]string{"OPENCODE_CONFIG_CONTENT": modelJSON("inline/e")}); got != "managed/f" {
				t.Errorf("managed config = %q, want managed/f", got)
			}
		})
	}
}

func TestOpenCodeDefaultAboveGitRootDependsOnMajor(t *testing.T) {
	for _, tc := range []struct {
		agentID string
		want    string
		repo    string
	}{
		// v1 stops at the git root, so the outer config is never read.
		{agentID: "opencode", want: "global/a", repo: "repo/c"},
		// v2 reads the launch worktree's ancestors, not the checkout's, so
		// neither the outer config nor an in-repository direct file is trusted.
		{agentID: "opencode-v2", want: "", repo: ""},
	} {
		t.Run(tc.agentID, func(t *testing.T) {
			f := newOpenCodeFixture(t)
			writeConfig(t, filepath.Join(f.outer, "opencode.json"), modelJSON("outer/x"))
			if got := configuredDefaultModel(tc.agentID, f.pkg, nil); got != tc.want {
				t.Errorf("config above git root = %q, want %q", got, tc.want)
			}
			writeConfig(t, filepath.Join(f.repo, "opencode.json"), modelJSON("repo/c"))
			if got := configuredDefaultModel(tc.agentID, f.pkg, nil); got != tc.repo {
				t.Errorf("repo config over outer = %q, want %q", got, tc.repo)
			}
		})
	}
}

// Sessions launch from <AO_DATA_DIR>/worktrees/..., whose ancestors (such as
// <AO_DATA_DIR>/opencode.json) v2 reads but discovery on the checkout cannot
// see. A model that such a file could override must not be marked default.
func TestOpenCodeV2DefaultIgnoresUnseenLaunchWorktreeAncestors(t *testing.T) {
	f := newOpenCodeFixture(t)
	// The checkout's ancestors hold nothing, yet global/a is still unresolved:
	// an <AO_DATA_DIR>/opencode.json selecting another model would win at launch.
	if got := configuredDefaultModel("opencode-v2", f.pkg, nil); got != "" {
		t.Errorf("v2 global model with unseen launch ancestors = %q, want unresolved", got)
	}
	// An in-repository .opencode model outranks any ancestor file, direct or
	// .opencode, so it is what the launched session runs.
	writeConfig(t, filepath.Join(f.repo, ".opencode", "opencode.jsonc"), modelJSON("dotdir/b"))
	if got := configuredDefaultModel("opencode-v2", f.pkg, nil); got != "dotdir/b" {
		t.Errorf("v2 in-repository .opencode model = %q, want dotdir/b", got)
	}
}

func TestOpenCodeDefaultLeavesUncertainWinnersUnresolved(t *testing.T) {
	for _, agentID := range []string{"opencode", "opencode-v2"} {
		t.Run(agentID, func(t *testing.T) {
			f := newOpenCodeFixture(t)

			// AO replaces OPENCODE_CONFIG at TUI launch but keeps it for ACP.
			custom := filepath.Join(t.TempDir(), "custom.json")
			writeConfig(t, custom, modelJSON("custom/g"))
			if got := configuredDefaultModel(agentID, f.pkg, map[string]string{"OPENCODE_CONFIG": custom}); got != "" {
				t.Errorf("winning OPENCODE_CONFIG = %q, want unresolved", got)
			}

			configDir := t.TempDir()
			writeConfig(t, filepath.Join(configDir, "opencode.json"), modelJSON("dir/h"))
			if got := configuredDefaultModel(agentID, f.pkg, map[string]string{"OPENCODE_CONFIG_DIR": configDir}); got != "" {
				t.Errorf("OPENCODE_CONFIG_DIR model = %q, want unresolved", got)
			}

			// Sibling files that disagree have no documented merge order.
			writeConfig(t, filepath.Join(f.repo, "opencode.json"), modelJSON("repo/c"))
			writeConfig(t, filepath.Join(f.repo, "opencode.jsonc"), modelJSON("repo/other"))
			if got := configuredDefaultModel(agentID, f.pkg, nil); got != "" {
				t.Errorf("conflicting sibling configs = %q, want unresolved", got)
			}
		})
	}
}

func TestOpenCodeDefaultWithoutGitRootIsUnresolvedForProjectConfig(t *testing.T) {
	isolateHome(t)
	workDir := t.TempDir()
	writeConfig(t, filepath.Join(workDir, "opencode.json"), modelJSON("project/p"))
	for _, agentID := range []string{"opencode", "opencode-v2"} {
		if got := configuredDefaultModel(agentID, workDir, nil); got != "" {
			t.Errorf("%s without a git root = %q, want unresolved", agentID, got)
		}
	}
}
