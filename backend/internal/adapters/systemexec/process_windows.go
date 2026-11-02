//go:build windows

package systemexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func commandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	if !isBatchFile(resolved) {
		return aoprocess.CommandContext(ctx, resolved, args...), nil //nolint:gosec // Callers supply server-owned argv.
	}
	// An npm-style shim is started as the target its own launch line names, so
	// argv never goes through cmd.exe quoting (issue #7212).
	if argv, ok := aoprocess.ResolveBatchShim(resolved, args); ok {
		return aoprocess.CommandContext(ctx, argv[0], argv[1:]...), nil //nolint:gosec // Callers supply server-owned argv.
	}
	// A shim this package cannot follow keeps its previous launch: cmd.exe with
	// /d /s /c. Go's default batch launch cannot carry a quoted shim path
	// together with a quoted argument (`C:\Program Files\nodejs\npm.cmd` would
	// fail as `'C:\Program' is not recognized`), and it drops /d, so cmd AutoRun
	// scripts would run again.
	shell := strings.TrimSpace(os.Getenv("ComSpec"))
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := aoprocess.CommandContext(ctx, shell) //nolint:gosec // ComSpec is Windows' configured batch interpreter.
	cmd.Args = nil
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = `/d /s /c "` + windowsBatchCommandLine(resolved, args) + `"`
	return cmd, nil
}

func isBatchFile(path string) bool {
	extension := filepath.Ext(path)
	return strings.EqualFold(extension, ".cmd") || strings.EqualFold(extension, ".bat")
}

func windowsBatchCommandLine(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteWindowsBatchArg(executable))
	for _, arg := range args {
		parts = append(parts, quoteWindowsBatchArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteWindowsBatchArg(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func refreshExecutablePath() {
	paths := []string{os.Getenv("Path")}
	paths = append(paths,
		registryPath(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`),
		registryPath(registry.CURRENT_USER, `Environment`),
	)
	if merged := mergeWindowsPath(paths...); merged != "" {
		_ = os.Setenv("Path", merged)
	}
}

func registryPath(root registry.Key, keyPath string) string {
	key, err := registry.OpenKey(root, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = key.Close() }()
	value, valueType, err := key.GetStringValue("Path")
	if err != nil {
		return ""
	}
	if valueType == registry.EXPAND_SZ {
		if expanded, expandErr := registry.ExpandString(value); expandErr == nil {
			value = expanded
		}
	}
	return value
}

func mergeWindowsPath(values ...string) string {
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, value := range values {
		for _, entry := range strings.Split(value, ";") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			key := strings.ToLower(strings.TrimRight(entry, `\/`))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, entry)
		}
	}
	return strings.Join(merged, ";")
}
