package binaryutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const scanMaxDepth = 6
const scanMaxEntries = 10_000

// ExecutableCandidate 是所选目录中的 CLI 入口；探测失败保留路径和错误供用户处理。
type ExecutableCandidate struct {
	Path    string `json:"path"`
	Source  string `json:"source" enum:"directory,desktop"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ExecutableScan 区分完整的空结果与受边界或权限限制的扫描。
type ExecutableScan struct {
	Directory  string                `json:"directory"`
	Candidates []ExecutableCandidate `json:"candidates"`
	Truncated  bool                  `json:"truncated"`
}

// ScanExecutables 只遍历用户指定根目录，先检查常见布局，再按深度和条目总量限制遍历。
func ScanExecutables(ctx context.Context, harness, directory string) (ExecutableScan, error) {
	spec, ok := RegisteredSpec(harness)
	names := CandidateNames(spec)
	if !ok || len(names) == 0 {
		return ExecutableScan{}, fmt.Errorf("%s does not support a custom CLI on this platform", harness)
	}
	directory = strings.TrimSpace(directory)
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, "\x00\r\n") {
		return ExecutableScan{}, errors.New("scan directory must be an absolute path")
	}
	directory = filepath.Clean(directory)
	stat, err := os.Stat(directory)
	if err != nil {
		return ExecutableScan{}, fmt.Errorf("read scan directory: %w", err)
	}
	if !stat.IsDir() {
		return ExecutableScan{}, errors.New("scan path is not a directory")
	}
	result := ExecutableScan{Directory: directory, Candidates: []ExecutableCandidate{}}
	seen := make(map[string]bool)
	add := func(path string) {
		key := candidateKey(path)
		if seen[key] {
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		seen[key] = true
		source := "directory"
		if harness == "deepseek-harness" && strings.HasSuffix(strings.ToLower(filepath.ToSlash(filepath.Dir(path))),
			"/resources/runtime/cli/bin") {
			source = "desktop"
		}
		result.Candidates = append(result.Candidates, ExecutableCandidate{Path: path, Source: source})
	}
	// 已知 Desktop 布局先查，避免大安装目录在找到 CLI 前耗尽扫描预算。
	for _, relative := range []string{".", "bin", filepath.Join("resources", "runtime", "cli", "bin"), filepath.Join("Contents", "Resources", "runtime", "cli", "bin")} {
		candidateDir := filepath.Join(directory, relative)
		if hasSymlinkDirectory(directory, candidateDir) {
			continue
		}
		for _, name := range names {
			add(filepath.Join(candidateDir, name))
		}
	}
	entries := 0
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := os.Open(dir) //nolint:gosec // 用户选择的目录，限深度和条目数，只读遍历。
		if err != nil {
			result.Truncated = true
			return nil
		}
		defer func() { _ = file.Close() }()
		for entries < scanMaxEntries {
			children, readErr := file.ReadDir(min(128, scanMaxEntries-entries))
			for _, child := range children {
				entries++
				path := filepath.Join(dir, child.Name())
				if child.IsDir() {
					if depth >= scanMaxDepth {
						result.Truncated = true
					} else if err := walk(path, depth+1); err != nil {
						return err
					}
				} else {
					for _, name := range names {
						if candidateKey(name) == candidateKey(child.Name()) {
							add(path)
							break
						}
					}
				}
				if entries >= scanMaxEntries {
					result.Truncated = true
					return nil
				}
			}
			if readErr != nil {
				result.Truncated = result.Truncated || !errors.Is(readErr, io.EOF)
				return nil
			}
		}
		return nil
	}
	if err := walk(directory, 0); err != nil {
		return ExecutableScan{}, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for i := range result.Candidates {
		candidate := &result.Candidates[i]
		candidate.Version, err = ProbeExecutable(probeCtx, harness, candidate.Path)
		if err != nil {
			candidate.Error = err.Error()
		}
		if probeCtx.Err() != nil {
			result.Truncated = true
		}
	}
	return result, ctx.Err()
}

// hasSymlinkDirectory 防止优先布局检查绕过“不跟随目录链接”的遍历边界。
func hasSymlinkDirectory(root, path string) bool {
	for path != root {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
		path = filepath.Dir(path)
	}
	return false
}

// ProbeExecutable 在应用选择前重新检查同一入口，只运行有超时和输出上限的版本探测。
func ProbeExecutable(ctx context.Context, harness, path string) (string, error) {
	spec, ok := RegisteredSpec(harness)
	if !ok {
		return "", fmt.Errorf("%s does not support a custom CLI", harness)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ValidateCandidate(probeCtx, spec, path); err != nil {
		return "", err
	}
	cmd := process.CommandContext(probeCtx, path, "--version")
	process.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 100 * time.Millisecond
	switch harness {
	case "memcode":
		cmd.Env = append(os.Environ(), "MEMCODE_AUTO_UPDATE=off")
	case "muse":
		cmd.Env = append(os.Environ(), "MUSE_NO_AUTO_UPDATE=1")
	}
	out := &probeOutput{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		if probeCtx.Err() != nil {
			return "", fmt.Errorf("version probe: %w", probeCtx.Err())
		}
		return "", fmt.Errorf("version probe failed: %w: %s", err, strings.TrimSpace(out.String()))
	}
	version := strings.TrimSpace(out.String())
	if version == "" {
		return "", errors.New("version probe returned no version")
	}
	return version, nil
}

// probeOutput 继续消费子进程输出，但最多保留 8 KiB，避免异常入口耗尽内存。
type probeOutput struct{ bytes.Buffer }

// Write 丢弃超限部分并继续接收输出，避免子进程因管道阻塞而卡住。
func (b *probeOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 8192 - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(n, remaining)])
	}
	return n, nil
}
