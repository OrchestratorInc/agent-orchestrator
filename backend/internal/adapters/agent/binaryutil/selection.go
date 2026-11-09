package binaryutil

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// executableSelections 按 Harness 保存显式入口；扫描规格与解析使用同一组命令名。
var executableSelections = struct {
	sync.RWMutex
	specs map[string]BinarySpec
	paths map[string]string
}{specs: make(map[string]BinarySpec), paths: make(map[string]string)}

// RegisterSpec 在适配器初始化时登记扫描规格，同时给公共解析器绑定 Harness 身份。
func RegisterSpec(harness string, spec BinarySpec) BinarySpec {
	spec.Harness = harness
	executableSelections.Lock()
	executableSelections.specs[harness] = spec
	executableSelections.Unlock()
	return spec
}

// RegisteredSpec 返回当前平台可扫描的适配器规格；内置 Agent 没有此登记。
func RegisteredSpec(harness string) (BinarySpec, bool) {
	executableSelections.RLock()
	defer executableSelections.RUnlock()
	spec, ok := executableSelections.specs[harness]
	return spec, ok
}

// SetSelectedBinaries 原子发布已持久化的选择，避免并发读取看到部分设置。
func SetSelectedBinaries(paths map[string]string) {
	next := make(map[string]string, len(paths))
	for harness, path := range paths {
		if path != "" {
			next[harness] = path
		}
	}
	executableSelections.Lock()
	executableSelections.paths = next
	executableSelections.Unlock()
}

// SelectedPath 只读取选择，不执行探测；安装器据此识别外部管理的安装。
func SelectedPath(harness string) string {
	executableSelections.RLock()
	defer executableSelections.RUnlock()
	return executableSelections.paths[harness]
}

// SelectedBinary 验证显式入口；selected 为 true 时调用者必须返回结果或错误，不能回退。
func SelectedBinary(ctx context.Context, harness string) (path string, selected bool, err error) {
	path = SelectedPath(harness)
	if path == "" {
		return "", false, nil
	}
	spec, ok := RegisteredSpec(harness)
	if !ok {
		return "", true, fmt.Errorf("%s: custom executable is unsupported", harness)
	}
	if err := ValidateCandidate(ctx, spec, path); err != nil {
		return "", true, err
	}
	return path, true, nil
}

// CandidateNames 返回复制后的平台命令名，供目录扫描使用。
func CandidateNames(spec BinarySpec) []string {
	return slices.Clone(namesForPlatform(spec))
}

// ValidateCandidate 保留适配器身份门禁，并拒绝目录、非绝对路径和其他命令名。
func ValidateCandidate(ctx context.Context, spec BinarySpec, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return fmt.Errorf("%s: executable must be an absolute file path", spec.Harness)
	}
	name := filepath.Base(path)
	known := slices.ContainsFunc(namesForPlatform(spec), func(candidate string) bool {
		return candidate == name || (runtime.GOOS == "windows" && strings.EqualFold(candidate, name))
	})
	if !known || !hookutil.IsExecutableFile(path) {
		return fmt.Errorf("%s: selected executable %q is missing or is not a supported CLI: %w", spec.Harness, path, ports.ErrAgentBinaryNotFound)
	}
	if spec.ValidateIdentity != nil && !spec.ValidateIdentity(ctx, path) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("%s: selected executable %q failed its identity or version check: %w", spec.Harness, path, ports.ErrAgentBinaryIdentityUnknown)
	}
	return nil
}
