// Package testgate 执行服务端登记的命令；限制 argv、环境和生命周期，不声称提供 OS 沙箱。
package testgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ValidatePolicy 拒绝零规则、shell 包装与不受限环境；可信配置仍须显式审核工具及 argv 的行为。
func ValidatePolicy(p domain.GatePolicy) error {
	if p.ID == "" || p.Revision < 1 || len(p.Rules) == 0 || len(p.Rules) > 32 || p.ValidFor <= 0 || p.ValidFor > 24*time.Hour || p.MaxFixes < 1 || p.MaxFixes > 3 {
		return domain.ErrDeliveryBlocked
	}
	seen := map[string]bool{}
	required := false
	for _, r := range p.Rules {
		name := strings.ToLower(strings.TrimSuffix(filepath.Base(r.Executable), ".exe"))
		if r.ID == "" || seen[r.ID] || !filepath.IsAbs(r.Executable) || r.Level < 1 || r.Level > 4 || r.Timeout <= 0 || r.Timeout > 30*time.Minute || r.MaxLogBytes < 1 || r.MaxLogBytes > 1<<20 || len(r.Args) > 100 {
			return domain.ErrDeliveryBlocked
		}
		if slices.Contains([]string{"sh", "bash", "zsh", "fish", "cmd", "powershell", "pwsh", "wscript", "cscript"}, name) || slices.Contains([]string{".bat", ".cmd", ".ps1", ".sh"}, strings.ToLower(filepath.Ext(r.Executable))) {
			return domain.ErrDeliveryBlocked
		}
		if _, ok := domain.CanonicalWorkflowPath(r.Cwd, true); !ok {
			return domain.ErrDeliveryBlocked
		}
		for _, path := range r.WhenPaths {
			if _, ok := domain.CanonicalWorkflowPath(path, true); !ok {
				return domain.ErrDeliveryBlocked
			}
		}
		for key, value := range r.Env {
			if !slices.Contains([]string{"PATH", "SystemRoot", "WINDIR", "LANG", "LC_ALL", "CI", "CGO_ENABLED", "GOOS", "GOARCH", "GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "GONOSUMDB", "NODE_ENV", "TZ"}, key) || strings.ContainsAny(value, "\x00\r\n") {
				return domain.ErrDeliveryBlocked
			}
		}
		for _, arg := range r.Args {
			if strings.ContainsRune(arg, 0) {
				return domain.ErrDeliveryBlocked
			}
		}
		seen[r.ID] = true
		required = required || r.Required
	}
	if !required {
		return domain.ErrDeliveryBlocked
	}
	return nil
}

// Fingerprint 绑定完整工件、策略、平台和实际可执行文件字节；缺工具也有摘要，但执行只能 BLOCKED。
func Fingerprint(a domain.ArtifactSnapshot, p domain.GatePolicy) (string, error) {
	if err := ValidatePolicy(p); err != nil {
		return "", err
	}
	tools := make([]string, 0, len(p.Rules))
	for _, r := range p.Rules {
		physical, err := filepath.EvalSymlinks(r.Executable)
		if os.IsNotExist(err) {
			tools = append(tools, "missing:"+r.Executable)
			continue
		}
		if err != nil || !samePath(physical, r.Executable) {
			return "", domain.ErrDeliveryBlocked
		}
		f, err := os.Open(r.Executable)
		if err != nil {
			return "", err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return "", err
		}
		tools = append(tools, hex.EncodeToString(h.Sum(nil)))
	}
	data, _ := json.Marshal(struct {
		Artifact, Policy, OS, Arch string
		Tools                      []string
	}{a.Fingerprint, p.Digest(), runtime.GOOS, runtime.GOARCH, tools})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// logCapture 在并发 stdout/stderr 写入下保留有界内容并对完整流取摘要。
type logCapture struct {
	mu        sync.Mutex
	h         hash.Hash
	data      []byte
	max       int
	truncated bool
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.h.Write(p)
	n := min(len(p), l.max-len(l.data))
	l.data = append(l.data, p[:n]...)
	l.truncated = l.truncated || n < len(p)
	return len(p), nil
}

// Run 返回真实退出和完整日志摘要；无法确认进程树停止时 UNVERIFIED，输出 PASS 字样无授权效果。
func Run(ctx context.Context, r domain.GateRule, root string) domain.GateResult {
	result := domain.GateResult{RuleID: r.ID, State: "BLOCKED", StartedAt: time.Now().UTC()}
	finish := func(reason string) domain.GateResult {
		result.Reason = reason
		result.FinishedAt = time.Now().UTC()
		return result
	}
	check := r
	check.Required = true
	if err := ValidatePolicy(domain.GatePolicy{ID: "validation", Revision: 1, Rules: []domain.GateRule{check}, ValidFor: time.Hour, MaxFixes: 2}); err != nil {
		return finish("invalid_rule")
	}
	if len(r.Platforms) > 0 && !slices.Contains(r.Platforms, runtime.GOOS) {
		result.State = "SKIPPED"
		return finish("platform_not_applicable")
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil || !samePath(root, physical) {
		return finish("invalid_workspace")
	}
	executable, err := filepath.EvalSymlinks(r.Executable)
	if err != nil {
		return finish("tool_unavailable")
	}
	if !samePath(executable, r.Executable) {
		return finish("tool_identity_changed")
	}
	if samePath(filepath.VolumeName(root), filepath.VolumeName(executable)) {
		rel, err := filepath.Rel(root, executable)
		if err != nil || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return finish("workspace_executable_refused")
		}
	}
	cwd := filepath.Join(root, filepath.FromSlash(r.Cwd))
	physical, err = filepath.EvalSymlinks(cwd)
	if err != nil || !samePath(cwd, physical) {
		return finish("invalid_cwd")
	}
	child, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	cmd := process.CommandContext(child, executable, r.Args...)
	cmd.Dir = cwd
	cmd.Env = []string{}
	keys := make([]string, 0, len(r.Env))
	for key := range r.Env {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+r.Env[key])
	}
	home, err := os.MkdirTemp(filepath.Dir(root), "gate-home-")
	if err != nil {
		return finish("scratch_unavailable")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(home)
		}
	}()
	cmd.Env = append(cmd.Env, "HOME="+home, "USERPROFILE="+home, "AO_DATA_DIR="+filepath.Join(home, "ao"), "TMP="+home, "TEMP="+home, "TMPDIR="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	process.ConfigureTreeCancellation(cmd)
	originalCancel := cmd.Cancel
	var cancelErr error
	var cancelMu sync.Mutex
	cmd.Cancel = func() error { err := originalCancel(); cancelMu.Lock(); cancelErr = err; cancelMu.Unlock(); return err }
	cmd.WaitDelay = 2 * time.Second
	log := &logCapture{h: sha256.New(), max: r.MaxLogBytes}
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Run()
	log.mu.Lock()
	result.Log = string(log.data)
	result.LogDigest = hex.EncodeToString(log.h.Sum(nil))
	result.Truncated = log.truncated
	log.mu.Unlock()
	cancelMu.Lock()
	stopped := cancelErr == nil
	cancelMu.Unlock()
	if child.Err() != nil {
		result.State = "FAIL"
		if !stopped {
			result.State = "UNVERIFIED"
			cleanup = false
		}
		return finish("timeout_or_cancelled")
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		result.State = "UNVERIFIED"
		cleanup = false
		return finish("child_output_not_closed")
	}
	if cmd.ProcessState == nil {
		return finish("tool_start_failed")
	}
	code := cmd.ProcessState.ExitCode()
	result.ExitCode = &code
	result.State = "FAIL"
	if err == nil && code == 0 {
		result.State = "PASS"
	}
	return finish("")
}

// samePath 保留平台路径身份语义，不接受路径别名来替换可执行工具。
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
