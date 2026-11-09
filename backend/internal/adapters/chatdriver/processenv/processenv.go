// Package processenv builds child-process environments shared by Chat drivers.
package processenv

import (
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Merge overlays session-specific values on the daemon environment and returns
// the KEY=VALUE form expected by os/exec. Sorting makes launches deterministic
// enough to inspect and compare in tests and process diagnostics.
func Merge(overlay map[string]string) []string {
	return merge(os.Environ(), overlay, runtime.GOOS == "windows")
}

// StripWorkflowSecrets 在最终进程边界移除宿主平台凭据、旧租约及大小写别名。
// 返回独立切片，避免修改调用方的环境；其他变量保持原值和顺序。
func StripWorkflowSecrets(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if upper != "AO_WORKFLOW_LEASE" && upper != ports.ProviderProfileKeyEnv && !strings.HasPrefix(upper, ports.ProviderProfileCredentialPrefix) {
			out = append(out, entry)
		}
	}
	return out
}

// merge 按平台合并环境，最后清除禁止外发的宿主秘密，防止 overlay 再次注入。
func merge(environ []string, overlay map[string]string, caseInsensitive bool) []string {
	merged := make(map[string]string, len(environ)+len(overlay))
	for _, entry := range environ {
		if key, _, ok := strings.Cut(entry, "="); ok {
			if caseInsensitive {
				key = strings.ToUpper(key)
			}
			merged[key] = entry
		}
	}
	keys := make([]string, 0, len(overlay))
	for key := range overlay {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if caseInsensitive && strings.EqualFold(keys[i], "PATH") != strings.EqualFold(keys[j], "PATH") {
			return !strings.EqualFold(keys[i], "PATH")
		}
		if caseInsensitive && (keys[i] == "PATH") != (keys[j] == "PATH") {
			return keys[j] == "PATH"
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		value := overlay[key]
		entry := key + "=" + value
		if caseInsensitive {
			key = strings.ToUpper(key)
		}
		merged[key] = entry
	}

	out := make([]string, 0, len(merged))
	for _, entry := range merged {
		out = append(out, entry)
	}
	sort.Strings(out)
	return StripWorkflowSecrets(out)
}
