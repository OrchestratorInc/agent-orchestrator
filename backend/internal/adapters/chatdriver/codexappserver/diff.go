package codexappserver

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxDiffFiles = 500

func parseTurnDiff(diff string) (files []ports.ChatDiffFile, truncated bool) {
	if strings.TrimSpace(diff) == "" {
		return nil, false
	}

	var current *ports.ChatDiffFile
	inHunk := false

	flush := func() {
		if current == nil {
			return
		}
		if current.Path != "" {
			files = append(files, *current)
		}
		current = nil
	}

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			if len(files) >= maxDiffFiles {
				return files, true
			}
			current = &ports.ChatDiffFile{Status: diffStatusModified}
			inHunk = false
			if path, ok := pathFromGitHeader(line); ok {
				current.Path = path
			}

		case current == nil:
			continue

		case strings.HasPrefix(line, "@@"):
			inHunk = true

		case inHunk && strings.HasPrefix(line, "+"):
			current.Additions++

		case inHunk && strings.HasPrefix(line, "-"):
			current.Deletions++

		case strings.HasPrefix(line, "new file mode"):
			current.Status = diffStatusAdded

		case strings.HasPrefix(line, "deleted file mode"):
			current.Status = diffStatusDeleted

		case strings.HasPrefix(line, "rename from "):
			current.Status = diffStatusRenamed
			current.OldPath = strings.TrimPrefix(line, "rename from ")

		case strings.HasPrefix(line, "rename to "):
			current.Status = diffStatusRenamed
			current.Path = strings.TrimPrefix(line, "rename to ")

		case strings.HasPrefix(line, "--- "):
			if path, ok := diffPath(line, "--- "); ok && current.Status == diffStatusDeleted {
				current.Path = path
			}

		case strings.HasPrefix(line, "+++ "):
			if path, ok := diffPath(line, "+++ "); ok {
				current.Path = path
			}
		}
	}
	flush()
	return files, false
}

const (
	diffStatusAdded    = "added"
	diffStatusModified = "modified"
	diffStatusDeleted  = "deleted"
	diffStatusRenamed  = "renamed"
)

func diffPath(line, prefix string) (string, bool) {
	value := strings.TrimPrefix(line, prefix)
	if idx := strings.IndexByte(value, '\t'); idx >= 0 {
		value = value[:idx]
	}
	value = strings.TrimSpace(value)
	if value == "" || value == "/dev/null" {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(value, "a/"), "b/"), true
}

func pathFromGitHeader(line string) (string, bool) {
	rest := strings.TrimPrefix(line, "diff --git ")
	fields := strings.Fields(rest)
	if len(fields) != 2 {
		return "", false
	}
	return strings.TrimPrefix(fields[1], "b/"), true
}
