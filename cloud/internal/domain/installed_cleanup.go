package domain

import (
	"encoding/json"
	"strings"
)

// InstalledCleanupKey is the key of a sandbox's bootstrap_context that holds
// the Cleanup commands its worker was last given (worker.LaunchContext
// PreRemove at bootstrap or reconnect), which the worker installs as its
// Cleanup hook's commands. It is written as one key, beside the reconciler's
// providerFailureId, without a schema change; nothing that provisions or
// supervises the sandbox reads it.
const InstalledCleanupKey = "installedCleanup"

// InstalledCleanupLimit bounds the commands stored under InstalledCleanupKey
// (their JSON encoding): longer Cleanup keeps its first commands that fit.
const InstalledCleanupLimit = 16 << 10

// BoundInstalledCleanup returns the leading commands whose JSON encoding fits
// InstalledCleanupLimit.
func BoundInstalledCleanup(commands []string) []string {
	size := len("[]")
	for index, command := range commands {
		encoded, err := json.Marshal(command)
		if err != nil {
			return commands[:index]
		}
		if index > 0 {
			size++
		}
		size += len(encoded)
		if size > InstalledCleanupLimit {
			return commands[:index]
		}
	}
	return commands
}

// InstalledCleanupCommands reads the commands stored under InstalledCleanupKey
// of a sandbox's bootstrap_context, and whether any were stored (a sandbox
// from before they were, or whose worker never connected, has none).
func InstalledCleanupCommands(bootstrapContext json.RawMessage) ([]string, bool) {
	if len(bootstrapContext) == 0 {
		return nil, false
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(bootstrapContext, &document); err != nil {
		return nil, false
	}
	raw, ok := document[InstalledCleanupKey]
	if !ok {
		return nil, false
	}
	var commands []string
	if err := json.Unmarshal(raw, &commands); err != nil {
		return nil, false
	}
	kept := commands[:0]
	for _, command := range commands {
		if strings.TrimSpace(command) != "" {
			kept = append(kept, command)
		}
	}
	return kept, true
}
