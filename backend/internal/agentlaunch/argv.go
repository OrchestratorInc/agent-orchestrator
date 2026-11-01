package agentlaunch

import (
	"path/filepath"
	"strings"
)

// SplitEnvAssignments splits a launch argv that may begin with a Unix-style
// `env NAME=VALUE ...` prefix into environment assignments and the real
// command. ok is false when argv is empty or the prefix has no command.
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
