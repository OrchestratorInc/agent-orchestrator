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
	for _, major := range []int{1, 2} {
		agentID := map[int]string{1: "opencode", 2: "opencode-v2"}[major]
		t.Run(agentID, func(t *testing.T) {
			resolve := func(f openCodeFixture, env map[string]string) string {
				return configuredDefaultModel(agentID, f.pkg, env)
			}

			f := newOpenCodeFixture(t)
			if got := resolve(f, nil); got != "global/a" {
				t.Errorf("global only = %q, want global/a", got)
			}

			// Ancestor project config inside the repository applies, inner wins.
			writeConfig(t, filepath.Join(f.repo, "opencode.json"), modelJSON("repo/c"))
			if got := resolve(f, nil); got != "repo/c" {
				t.Errorf("ancestor repo config = %q, want repo/c", got)
			}
			writeConfig(t, filepath.Join(f.pkg, "opencode.jsonc"), "// pkg\n"+modelJSON("pkg/d"))
			if got := resolve(f, nil); got != "pkg/d" {
				t.Errorf("inner project config = %q, want pkg/d", got)
			}

			// Every .opencode file overrides every direct file, even an inner one.
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
	}{
		// v1 stops at the git root, so the outer config is never read.
		{agentID: "opencode", want: "global/a"},
		// v2 reads it, but it differs between the checkout and AO's worktrees.
		{agentID: "opencode-v2", want: ""},
	} {
		t.Run(tc.agentID, func(t *testing.T) {
			f := newOpenCodeFixture(t)
			writeConfig(t, filepath.Join(f.outer, "opencode.json"), modelJSON("outer/x"))
			if got := configuredDefaultModel(tc.agentID, f.pkg, nil); got != tc.want {
				t.Errorf("config above git root = %q, want %q", got, tc.want)
			}
			// An in-repository model overrides the outer one, which is then moot.
			writeConfig(t, filepath.Join(f.repo, "opencode.json"), modelJSON("repo/c"))
			if got := configuredDefaultModel(tc.agentID, f.pkg, nil); got != "repo/c" {
				t.Errorf("repo config over outer = %q, want repo/c", got)
			}
		})
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
