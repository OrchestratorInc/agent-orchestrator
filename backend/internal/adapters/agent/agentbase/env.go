package agentbase

import "strings"

// ApplyEnvOverrides copies overrides into env. Windows env keys are
// case-insensitive and exec keeps the last duplicate, so there a caller key
// differing only in case is dropped first to let the override win.
func ApplyEnvOverrides(env, overrides map[string]string, caseInsensitive bool) {
	for key, value := range overrides {
		if caseInsensitive {
			for existing := range env {
				if existing != key && strings.EqualFold(existing, key) {
					delete(env, existing)
				}
			}
		}
		env[key] = value
	}
}
