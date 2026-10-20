//go:build !windows

package cli

import "os/exec"

func configureCLIBatch(_ *exec.Cmd) {}
