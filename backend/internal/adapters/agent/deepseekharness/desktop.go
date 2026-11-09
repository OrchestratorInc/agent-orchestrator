package deepseekharness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var desktopInvocation = regexp.MustCompile(`^"%~dp0([^"\r\n]+)" --expose-internals "%~dp0([^"\r\n]+)" %\*$`)

// PrepareCommand 只识别官方 Desktop shim，将 Electron 参数和环境一起交给进程边界。
// 直接启动保留长提示词和 shell 元字符，不经过 cmd.exe 的 8191 字符限制。
func (p *Plugin) PrepareCommand(ctx context.Context, argv []string, env map[string]string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(argv) == 0 || !strings.EqualFold(filepath.Base(argv[0]), "dsh.cmd") {
		return argv, nil
	}
	content, err := os.ReadFile(argv[0]) //nolint:gosec // 已解析、由用户选择的 Harness launcher。
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek launcher: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(content), "\r\n", "\n")), "\n")
	if len(lines) != 5 || lines[0] != "@echo off" || lines[1] != "setlocal DisableDelayedExpansion" ||
		lines[2] != `set "ELECTRON_RUN_AS_NODE=1"` || lines[4] != "exit /b %errorlevel%" {
		return argv, nil
	}
	match := desktopInvocation.FindStringSubmatch(lines[3])
	if match == nil {
		return argv, nil
	}
	dir := filepath.Dir(argv[0])
	executable := filepath.Clean(filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(match[1], `\`, "/"))))
	entry := filepath.Clean(filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(match[2], `\`, "/"))))
	if !isRegularFile(executable) {
		return nil, fmt.Errorf("DeepSeek Desktop runtime is missing: %s", executable)
	}
	archive, _, insideArchive := strings.Cut(filepath.ToSlash(entry), ".asar/")
	if (insideArchive && !isRegularFile(filepath.FromSlash(archive+".asar"))) || (!insideArchive && !isRegularFile(entry)) {
		return nil, fmt.Errorf("DeepSeek Desktop CLI is missing: %s", entry)
	}
	if env == nil {
		return nil, fmt.Errorf("DeepSeek Desktop command needs a launch environment")
	}
	env["ELECTRON_RUN_AS_NODE"] = "1"
	command := make([]string, 0, len(argv)+2)
	command = append(command, executable, "--expose-internals", entry)
	return append(command, argv[1:]...), nil
}
