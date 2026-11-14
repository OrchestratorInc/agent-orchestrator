package agentlaunch

import (
	"path/filepath"
	"strings"
)

// SplitEnvAssignments splits an AO launch argv that may begin with a Unix-style
// `env NAME=VALUE ...` prefix into environment assignments and the real
// command. Adapters use this prefix to pass process environment that their CLIs
// cannot accept as flags. Windows has no env binary, and both the ConPTY host
// and supervised process launcher execute argv[0] directly, so those callers
// apply the assignments themselves. They append assignments after the inherited
// environment; later values therefore win, case-insensitively on Windows. This
// also matches the env-prefix handling used when validating launch binaries.
//
// ok is false when argv is empty or the prefix has no command. In that case no
// assignments are returned and command is the original argv, allowing callers
// to preserve their existing invalid-command handling without applying a
// partial prefix.
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
		return nil, argv, false
	}
	return argv[1:i], argv[i:], true
}
