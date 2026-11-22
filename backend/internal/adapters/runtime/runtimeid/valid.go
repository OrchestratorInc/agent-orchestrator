// Package runtimeid holds the ID rules shared by terminal runtimes.
package runtimeid

import "regexp"

var dottedSessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+(?:\.[a-zA-Z0-9_-]+)*$`)

// ValidDottedSessionID accepts project-derived IDs while excluding dot path
// segments and characters unsafe for runtime handles.
func ValidDottedSessionID(id string) bool {
	return dottedSessionIDPattern.MatchString(id)
}
