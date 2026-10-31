//go:build windows

package systemexec

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func commandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, err
	}
	// process.CommandContext starts batch shims' targets directly, so argv never
	// goes through cmd.exe quoting (issue #7212).
	return aoprocess.CommandContext(ctx, name, args...), nil
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
