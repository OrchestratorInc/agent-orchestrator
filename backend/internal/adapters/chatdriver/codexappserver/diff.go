package codexappserver

import (
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// Turn-diff parsing.
//
// `turn/diff/updated` does not carry a structured file list. Its whole payload is
// one aggregated git unified diff string for the turn (measured against
// codex-cli 0.146.0; the generated schema agrees -- V2TurnDiffUpdatedNotification
// is `{threadId, turnId, diff: string}`). Per-file path, counts, and change kind
// therefore have to be read out of the diff text here, in the adapter, rather
// than arriving ready-made.
//
// The structured alternative, `item/fileChange/patchUpdated`, reports one ITEM's
// changes. It cannot answer "what has this turn changed so far" without AO
// accumulating and de-duplicating items itself, which is a second source of truth
// for something the provider already aggregates correctly.

// maxDiffFiles bounds one turn diff. A refactor that rewrites a generated
// directory can produce thousands of entries, and this row is re-read by every
// snapshot poll; a caller is told the list was cut rather than shown a short list
// as if it were whole.
const maxDiffFiles = 500

// parseTurnDiff reads a git unified diff into per-file entries.
//
// Patch is deliberately left empty. The turn view needs path, counts, and kind;
// carrying every hunk would put the entire diff into a row that a client polls
// once a second, and the full text is already recoverable from the worktree.
func parseTurnDiff(diff string) (files []ports.ChatDiffFile, truncated bool) {
	if strings.TrimSpace(diff) == "" {
		return nil, false
	}

	var current *ports.ChatDiffFile
	// Counting only inside hunks keeps the `---`/`+++` header lines from being
	// mistaken for a deletion and an addition on every single file.
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
			// The a/ b/ header is the fallback path source: it is ambiguous when a
			// filename contains a space, so it only applies if no ---/+++ line
			// follows.
			if path, ok := pathFromGitHeader(line); ok {
				current.Path = path
			}

		case current == nil:
			// Preamble before the first file header, if the provider ever adds one.
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
			// Only useful for a deletion: for every other change the +++ line names
			// the same path, and /dev/null on this side means the file is new.
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

// Change kinds AO reports. These are the neutral names, not the provider's:
// `item/fileChange/patchUpdated` spells them add/delete/update.
const (
	diffStatusAdded    = "added"
	diffStatusModified = "modified"
	diffStatusDeleted  = "deleted"
	diffStatusRenamed  = "renamed"
)

// diffPath reads the path out of a `--- a/x` or `+++ b/x` line. /dev/null is
// reported as absent rather than as a path, since it means "this side has no
// file" and is never something to show a user.
func diffPath(line, prefix string) (string, bool) {
	value := strings.TrimPrefix(line, prefix)
	// git appends a tab and a timestamp in some configurations.
	if idx := strings.IndexByte(value, '\t'); idx >= 0 {
		value = value[:idx]
	}
	value = strings.TrimSpace(value)
	if value == "" || value == "/dev/null" {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(value, "a/"), "b/"), true
}

// pathFromGitHeader recovers the path from `diff --git a/x b/x`.
//
// Only correct when the path has no spaces, which is why it is the fallback: the
// ---/+++ lines that follow are unambiguous and overwrite this.
func pathFromGitHeader(line string) (string, bool) {
	rest := strings.TrimPrefix(line, "diff --git ")
	fields := strings.Fields(rest)
	if len(fields) != 2 {
		return "", false
	}
	return strings.TrimPrefix(fields[1], "b/"), true
}
