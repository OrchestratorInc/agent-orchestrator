// Package sessionid validates Orchestrator session IDs shared across adapters.
package sessionid

import "regexp"

var pattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+(?:\.[a-zA-Z0-9_-]+)*$`)

// Valid accepts project-derived IDs while excluding dot path segments and
// characters unsafe for runtime handles or private session paths.
func Valid(id string) bool {
	return pattern.MatchString(id)
}
