package shellterm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type cueCommandDelivery struct {
	input   string
	file    string
	cleanup func()
}

func fishSingleQuote(value string) string {
	return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(value) + "'"
}

func prepareCueCommand(dataDir string, argv []string, command string) (cueCommandDelivery, error) {
	result := cueCommandDelivery{input: command, cleanup: func() {}}
	if len(argv) == 0 {
		return result, nil
	}
	name := strings.ToLower(filepath.Base(argv[0]))
	switch name {
	case "bash", "zsh", "sh", "bash.exe", "sh.exe", "fish", "pwsh.exe", "powershell.exe":
	default:
		return result, nil
	}
	root := filepath.Join(dataDir, "cue-commands")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return result, fmt.Errorf("prepare cue command: %w", err)
	}
	dir, err := newCueCommandDirectory(root)
	if err != nil {
		return result, fmt.Errorf("prepare cue command: %w", err)
	}
	result.file = filepath.Join(dir, "command")
	result.cleanup = func() { _ = os.RemoveAll(dir) }
	file := shellSingleQuote(filepath.ToSlash(result.file))
	quoted := shellSingleQuote(command)
	evaluation := "eval " + shellSingleQuote("unset AO_CUE_COMMAND; "+command) + "\n"
	result.input = ". " + file
	script := "unset AO_CUE_COMMAND\ncommand rm -- " + file + " || return\n"
	switch name {
	case "bash", "bash.exe":
		script += "if [[ -o history ]]; then\n_aoCueHistory=$(HISTTIMEFORMAT= builtin history 1)\n" +
			"_aoCueHistory=${_aoCueHistory#*[0-9]  }\n" +
			"if [ \"$_aoCueHistory\" = " + shellSingleQuote(result.input) + " ]; then builtin history -d -1; fi\n" +
			"unset _aoCueHistory\nbuiltin history -s " + quoted + "\nfi\n"
	case "zsh":
		script += "builtin print -s -- " + quoted + "\n"
	case "fish":
		file = fishSingleQuote(filepath.ToSlash(result.file))
		quoted = fishSingleQuote(command)
		evaluation = "eval " + fishSingleQuote("set -e AO_CUE_COMMAND; "+command) + "\n"
		result.input = "source " + file
		script = "set -e AO_CUE_COMMAND\ncommand rm -- " + file + "; or return\n"
	case "pwsh.exe", "powershell.exe":
		file = powershellSingleQuote(result.file)
		quoted = powershellSingleQuote(command)
		evaluation = "Invoke-Expression " + powershellSingleQuote("Remove-Item Env:AO_CUE_COMMAND -ErrorAction SilentlyContinue; "+command) + "\n"
		result.input = "$aoCueInput = [IO.File]::ReadAllText(" + file + ", [Text.Encoding]::UTF8); Remove-Item -LiteralPath " + file + " -ErrorAction Stop; Invoke-Expression ('Remove-Variable aoCueInput; ' + $aoCueInput)"
		script = "Remove-Item Env:AO_CUE_COMMAND -ErrorAction SilentlyContinue\n" +
			"[Console]::WriteLine(" + quoted + ")\n" +
			"Add-History -InputObject ([pscustomobject]@{CommandLine=" + quoted + ";ExecutionStatus='Completed';StartExecutionTime=[datetime]::Now;EndExecutionTime=[datetime]::Now})\n" +
			"if (Get-Module PSReadLine) { [Microsoft.PowerShell.PSConsoleReadLine]::AddToHistory(" + quoted + ") }\n"
	}
	if name != "pwsh.exe" && name != "powershell.exe" {
		script += "printf '%s\\n' " + quoted + "\n"
	}
	if err := os.WriteFile(result.file, []byte(script+evaluation), 0o600); err != nil {
		result.cleanup()
		return cueCommandDelivery{}, fmt.Errorf("prepare cue command: %w", err)
	}
	return result, nil
}

var cueCommandConsumeTimeout = 10 * time.Second

func waitForCueCommandConsumed(ctx context.Context, file string) error {
	if file == "" {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, cueCommandConsumeTimeout)
	defer cancel()
	ticker := time.NewTicker(initialInputPollInterval)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if err := ctx.Err(); err != nil {
				return err
			}
			return apierr.Conflict("CUE_COMMAND_NOT_CONSUMED", "The shell did not consume the command; its temporary payload was removed and delivery will not be retried", nil)
		case <-ticker.C:
		}
	}
}
