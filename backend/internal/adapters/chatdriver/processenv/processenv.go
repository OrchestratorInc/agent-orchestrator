// Package processenv builds child-process environments shared by Chat drivers.
package processenv

import (
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/envfilter"
)

// Merge overlays session-specific values on the daemon environment and returns
// the KEY=VALUE form expected by os/exec. Sorting makes launches deterministic
// enough to inspect and compare in tests and process diagnostics.
//
// Parent-session identity markers (see envfilter) are dropped: if this daemon
// happens to have been started from inside a Claude Code session, its own
// CLAUDECODE/CLAUDE_CODE_SESSION_ID/etc must not leak into a worker's process,
// or the worker's own claude-code misidentifies itself as a child session.
func Merge(overlay map[string]string) []string {
	return merge(os.Environ(), overlay, runtime.GOOS == "windows")
}

func merge(environ []string, overlay map[string]string, caseInsensitive bool) []string {
	merged := make(map[string]string, len(environ)+len(overlay))
	for _, entry := range environ {
		if key, _, ok := strings.Cut(entry, "="); ok {
			if envfilter.IsParentSessionMarker(key) {
				continue
			}
			if caseInsensitive {
				key = strings.ToUpper(key)
			}
			merged[key] = entry
		}
	}
	keys := make([]string, 0, len(overlay))
	for key := range overlay {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if caseInsensitive && strings.EqualFold(keys[i], "PATH") != strings.EqualFold(keys[j], "PATH") {
			return !strings.EqualFold(keys[i], "PATH")
		}
		if caseInsensitive && (keys[i] == "PATH") != (keys[j] == "PATH") {
			return keys[j] == "PATH"
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		value := overlay[key]
		entry := key + "=" + value
		if caseInsensitive {
			key = strings.ToUpper(key)
		}
		merged[key] = entry
	}

	out := make([]string, 0, len(merged))
	for _, entry := range merged {
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}
