package cli

import (
	"os"
	"os/exec"
)

type processStartConfig struct {
	Path   string
	Args   []string
	Env    []string
	Stdout *os.File
	Stderr *os.File
}

// startProcess starts the desktop app, detached, with its stdio on the null
// device unless files are passed.
func startProcess(cfg processStartConfig) error {
	cmd := exec.Command(cfg.Path, cfg.Args...) //nolint:forbidigo // starts the desktop app detached; it outlives this CLI process.
	cmd.Env = cfg.Env
	// Assign only non-nil files. A nil *os.File in the io.Writer field is a
	// non-nil interface, so os/exec skips its /dev/null (NUL) default: on Unix the
	// child starts with that fd closed, and on Windows it gets a handle to this
	// process instead (#6261).
	if cfg.Stdout != nil {
		cmd.Stdout = cfg.Stdout
	}
	if cfg.Stderr != nil {
		cmd.Stderr = cfg.Stderr
	}
	// Detach the app into its own session/process group so a Ctrl-C in the
	// terminal where `ao start` runs doesn't also SIGINT it (it would otherwise
	// share the launcher's group).
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
