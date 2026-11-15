package modelcatalog

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var repoConfigRefPatterns = []string{"refs/heads", "refs/remotes", "refs/tags"}

func repoMayHoldConfig(workingDir string, names ...string) bool {
	if workingDir == "" || len(names) == 0 {
		return false
	}
	top, prefix, ok := gitTopLevel(workingDir)
	if !ok {
		return anyFileExists(workingDir, names)
	}
	var repoPaths []string
	for _, dir := range repoDirsDownTo(prefix) {
		if anyFileExists(filepath.Join(top, filepath.FromSlash(dir)), names) {
			return true
		}
		for _, name := range names {
			repoPaths = append(repoPaths, path.Join(dir, name))
		}
	}
	return anyRefHoldsAny(top, repoPaths)
}

func repoDirsDownTo(prefix string) []string {
	dirs := []string{""}
	current := ""
	for _, part := range strings.Split(strings.Trim(prefix, "/"), "/") {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		dirs = append(dirs, current)
	}
	return dirs
}

func anyFileExists(dir string, names []string) bool {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
			return true
		}
	}
	return false
}

func gitTopLevel(dir string) (top, prefix string, ok bool) {
	out, err := runGit(dir, "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", "", false
	}
	top = filepath.FromSlash(strings.TrimSpace(lines[0]))
	if len(lines) > 1 {
		prefix = strings.TrimSpace(lines[1])
	}
	return top, prefix, true
}

func anyRefHoldsAny(top string, repoPaths []string) bool {
	out, err := runGit(top, append([]string{"for-each-ref", "--format=%(objectname)"}, repoConfigRefPatterns...)...)
	if err != nil {
		return true
	}
	tips := []string{"HEAD"}
	seen := map[string]bool{}
	for _, tip := range strings.Fields(string(out)) {
		if !seen[tip] {
			seen[tip] = true
			tips = append(tips, tip)
		}
	}
	var query strings.Builder
	for _, tip := range tips {
		for _, repoPath := range repoPaths {
			query.WriteString(tip + ":" + repoPath + "\n")
		}
	}
	out, err = runGitInput(top, query.String(), "cat-file", "--batch-check")
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) == 3 && (fields[1] == "blob" || fields[1] == "tree") {
			return true
		}
	}
	return false
}

func runGit(dir string, args ...string) ([]byte, error) {
	return runGitInput(dir, "", args...)
}

func runGitInput(dir, input string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	return cmd.Output()
}
