//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Expand arguments after cmd's syntax parsing so percent signs, newlines and
// shell punctuation in prompts remain data. Batch shims forward argv via %*.
func configureCLIBatch(cmd *exec.Cmd) {
	ext := filepath.Ext(cmd.Path)
	if !strings.EqualFold(ext, ".cmd") && !strings.EqualFold(ext, ".bat") {
		return
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	refs := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		key := fmt.Sprintf("AO_CLI_BATCH_ARG_%d", i)
		value := syscall.EscapeArg(arg)
		if i == 0 {
			value = `"` + arg + `"`
		}
		env = append(env, key+"="+value)
		refs[i] = "!" + key + "!"
	}
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd.Path, cmd.Args, cmd.Env = shell, []string{shell}, env
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = `/d /v:on /s /c "` + strings.Join(refs, " ") + `"`
}
