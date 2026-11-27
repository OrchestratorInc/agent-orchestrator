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

func startProcess(cfg processStartConfig) error {
	cmd := exec.Command(cfg.Path, cfg.Args...) //nolint:forbidigo // foreground CLI process must inherit the caller's console.
	cmd.Env = cfg.Env
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	// Detach the daemon into its own session/process group so a Ctrl-C in the
	// terminal where `ao start` is waiting for readiness doesn't also SIGINT the
	// freshly spawned daemon (it would otherwise share the launcher's group).
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// startDetachedProcess starts cfg detached like startProcess, and also
// returns the child's PID and a channel that reports when it exits, so a
// caller waiting for it to become ready can stop early if it dies.
func startDetachedProcess(cfg processStartConfig) (startedProcess, error) {
	cmd := exec.Command(cfg.Path, cfg.Args...) //nolint:forbidigo // the daemon must outlive this CLI process.
	cmd.Env = cfg.Env
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return startedProcess{}, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return startedProcess{PID: cmd.Process.Pid, Exited: exited, Kill: cmd.Process.Kill}, nil
}
