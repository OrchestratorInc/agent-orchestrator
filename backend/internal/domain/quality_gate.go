package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"time"
)

// GateRule 是由服务端登记的完整命令策略，客户端和 Agent 只能引用策略 ID。
type GateRule struct {
	ID          string            `json:"id"`
	Level       int               `json:"level"`
	Required    bool              `json:"required"`
	Executable  string            `json:"executable"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Cwd         string            `json:"cwd"`
	Platforms   []string          `json:"platforms,omitempty"`
	WhenPaths   []string          `json:"whenPaths,omitempty"`
	Timeout     time.Duration     `json:"timeout"`
	MaxLogBytes int               `json:"maxLogBytes"`
}

// SkipReason 只允许策略声明的正常不适用；任务分支已有提交时保守运行，避免遗漏历史变更。
func (r GateRule) SkipReason(a ArtifactSnapshot) string {
	if len(r.Platforms) > 0 && !slices.Contains(r.Platforms, runtime.GOOS) {
		return "platform_not_applicable"
	}
	if r.Level == 1 || r.Level == 4 || len(r.WhenPaths) == 0 || a.Git.HeadSHA != a.Repo.CreationSHA {
		return ""
	}
	for _, file := range a.Git.Files {
		filePath, valid := CanonicalWorkflowPath(file.Path, false)
		if !valid {
			return ""
		}
		for _, scope := range r.WhenPaths {
			scope, valid = CanonicalWorkflowPath(scope, true)
			if !valid {
				return ""
			}
			if runtime.GOOS == "windows" {
				filePath, scope = strings.ToLower(filePath), strings.ToLower(scope)
			}
			if scope == "" || filePath == scope || strings.HasPrefix(filePath, scope+"/") {
				return ""
			}
		}
	}
	return "paths_not_changed"
}

// GatePolicy 冻结规则及有效期；修改规则必须换 revision，完整摘要仍检查同 revision 内容漂移。
type GatePolicy struct {
	ID       string        `json:"id"`
	Revision int64         `json:"revision"`
	Rules    []GateRule    `json:"rules"`
	ValidFor time.Duration `json:"validFor"`
	MaxFixes int64         `json:"maxFixes"`
}

// Digest 对策略全部执行字段取摘要，不能只按策略名称复用旧 PASS。
func (p GatePolicy) Digest() string {
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// WorkflowQualityPolicy 是一个 Workflow 的受信策略版本，Revision 用于 Operator 配置 CAS。
type WorkflowQualityPolicy struct {
	WorkflowID WorkflowID `json:"workflowId"`
	Policy     GatePolicy `json:"policy"`
	Digest     string     `json:"digest"`
	Revision   int64      `json:"revision"`
}

// GateResult 只记录实际进程观察，SKIPPED 的正常条件不冒充 PASS。
type GateResult struct {
	RuleID     string    `json:"ruleId"`
	State      string    `json:"state"`
	Reason     string    `json:"reason,omitempty"`
	ExitCode   *int      `json:"exitCode,omitempty"`
	Log        string    `json:"log"`
	LogDigest  string    `json:"logDigest"`
	Truncated  bool      `json:"truncated"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

// GateRun 绑定工件、策略、工具指纹和执行任期，重启后的 running 保持未验证。
type GateRun struct {
	ID                  string         `json:"id"`
	ArtifactID          string         `json:"artifactId"`
	ArtifactFingerprint string         `json:"artifactFingerprint"`
	PolicyDigest        string         `json:"policyDigest"`
	Fingerprint         string         `json:"fingerprint"`
	State               string         `json:"state"`
	Results             []GateResult   `json:"results"`
	Fence               SchedulerFence `json:"-"`
	Revision            int64          `json:"revision"`
	CreatedAt           time.Time      `json:"createdAt"`
	ExpiresAt           time.Time      `json:"expiresAt"`
}
