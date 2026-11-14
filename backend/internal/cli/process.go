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
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
