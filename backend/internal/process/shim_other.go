//go:build !windows

package process

import "os/exec"

// rewriteBatchShim is a no-op outside Windows: no other platform hides a command
// behind a batch shim that re-parses its arguments.
func rewriteBatchShim(*exec.Cmd) {}
