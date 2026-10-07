package shellterm

import (
	"path/filepath"
	"strings"
)

func cueCommandInput(argv []string, command string) (string, map[string]string) {
	if len(argv) == 0 {
		return command, nil
	}
	var input string
	switch strings.ToLower(filepath.Base(argv[0])) {
	case "bash", "zsh", "sh", "bash.exe", "sh.exe":
		input = `eval "unset AO_CUE_COMMAND; $AO_CUE_COMMAND"`
	case "fish":
		input = `eval "set -e AO_CUE_COMMAND; $AO_CUE_COMMAND"`
	case "pwsh.exe", "powershell.exe":
		input = `Invoke-Expression ('Remove-Item Env:AO_CUE_COMMAND; ' + $env:AO_CUE_COMMAND)`
	default:
		return command, nil
	}
	return input, map[string]string{"AO_CUE_COMMAND": command}
}
