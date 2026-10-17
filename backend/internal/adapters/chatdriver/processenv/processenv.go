// Package processenv builds child-process environments shared by Chat drivers.
package processenv

import (
	"os"
	"runtime"
	"sort"
	"strings"
)

// Merge overlays session-specific values on the daemon environment and returns
// the KEY=VALUE form expected by os/exec. Names in envRemove are stripped from
// the inherited daemon environment before the overlay applies, so an overlay
// value for a removed name still wins. Sorting makes launches deterministic
// enough to inspect and compare in tests and process diagnostics.
func Merge(overlay map[string]string, envRemove []string) []string {
	return merge(os.Environ(), overlay, envRemove, runtime.GOOS == "windows")
}

func merge(environ []string, overlay map[string]string, envRemove []string, caseInsensitive bool) []string {
	removed := make(map[string]bool, len(envRemove))
	for _, key := range envRemove {
		removed[key] = true
	}
	isRemoved := func(key string) bool {
		if removed[key] {
			return true
		}
		if !caseInsensitive {
			return false
		}
		for name := range removed {
			if strings.EqualFold(name, key) {
				return true
			}
		}
		return false
	}
	merged := make(map[string]string, len(environ)+len(overlay))
	for _, entry := range environ {
		if key, _, ok := strings.Cut(entry, "="); ok {
			if isRemoved(key) {
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
