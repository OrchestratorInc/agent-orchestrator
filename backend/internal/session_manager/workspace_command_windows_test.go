//go:build windows

package sessionmanager

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPostCreateAndCleanupWindowsQuotedPaths(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"setup", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				command string
				exit    int
			}{
				{name: "quoted arguments", command: `git -C "%AO_WORKTREE_PATH%" rev-parse --is-inside-work-tree`},
				{name: "quoted executable", command: `"%PROJECT_GIT_BINARY%" -C "%AO_WORKTREE_PATH%" rev-parse --is-inside-work-tree`},
				{name: "nonzero exit", command: `git -C "%AO_WORKTREE_PATH%" rev-parse --is-inside-work-tree`, exit: 7},
			} {
				t.Run(tc.name, func(t *testing.T) {
					command := tc.command + ` > "git result.txt" && type "%AO_SOURCE_TREE_PATH%\source file.txt" > "copied file.txt"`
					if tc.exit != 0 {
						command += ` && exit /b 7`
					}
					m, st, _, rec := newCleanupFixture(t, command)
					workspace := filepath.Join(rec.Metadata.WorkspacePath, "worktree with spaces")
					source := filepath.Join(t.TempDir(), "source with spaces")
					for _, dir := range []string{workspace, source} {
						if err := os.MkdirAll(dir, 0o750); err != nil {
							t.Fatal(err)
						}
					}
					if output, err := exec.CommandContext(t.Context(), git, "init", workspace).CombinedOutput(); err != nil {
						t.Fatalf("git init: %v\n%s", err, output)
					}
					if err := os.WriteFile(filepath.Join(source, "source file.txt"), []byte("source fixture"), 0o600); err != nil {
						t.Fatal(err)
					}
					project := st.projects[string(rec.ProjectID)]
					project.Path = source
					project.Config.Env = map[string]string{"PROJECT_GIT_BINARY": git}
					st.projects[string(rec.ProjectID)] = project

					var err error
					if stage == "setup" {
						err = runPostCreate(t.Context(), workspace, source, []string{command}, project.Config.Env)
					} else {
						err = m.runPreRemove(rec.ProjectID, workspace)
					}
					if tc.exit == 0 {
						if err != nil {
							t.Fatalf("quoted %s command: %v", stage, err)
						}
					} else {
						var exit *exec.ExitError
						if !errors.As(err, &exit) || exit.ExitCode() != tc.exit {
							t.Fatalf("%s error = %v, want exit status %d", stage, err, tc.exit)
						}
					}
					for name, want := range map[string]string{"git result.txt": "true", "copied file.txt": "source fixture"} {
						data, err := os.ReadFile(filepath.Join(workspace, name))
						if err != nil || strings.TrimSpace(string(data)) != want {
							t.Fatalf("%s = %q, err=%v; want %q", name, data, err, want)
						}
					}
				})
			}
		})
	}
}
