package agentlaunch

import (
	"path/filepath"
	"strings"
)

// SplitEnvAssignments splits a launch argv that may begin with a Unix-style
// `env NAME=VALUE ...` prefix into environment assignments and the real
// command. ok is false when argv is empty or the prefix has no command, which
// leaves the caller's normal missing-binary handling in charge.
//
// Adapters prepend `env KEY=value` to inject variables the CLI has no flag
// for. That works on macOS and Linux, where `env` is a real executable, but
// Windows has no `env` binary. Callers that exec argv[0] directly must apply
// the assignments to the child environment instead.
func SplitEnvAssignments(argv []string) (assignments, command []string, ok bool) {
	if len(argv) == 0 {
		return nil, nil, false
	}
	if filepath.Base(argv[0]) != "env" {
		return nil, argv, true
	}
	i := 1
	for i < len(argv) && strings.Contains(argv[i], "=") {
		i++
	}
	if i >= len(argv) {
		return argv[1:i], nil, false
	}
	return argv[1:i], argv[i:], true
}
