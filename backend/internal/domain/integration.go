package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// IntegrationPlan 保存不可变的多仓预演输入；目标分支更新和 Operator 审批属于后续阶段。
type IntegrationPlan struct {
	ID         string     `json:"id"`
	WorkflowID WorkflowID `json:"workflowId"`
	Status     string     `json:"status"`
	Revision   int64      `json:"revision"`
	PlanDigest string     `json:"planDigest"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// IntegrationConflictPaths 保留冻结证据中 null/[] 的原始差异，HTTP 不得归一化后改变审批摘要。
type IntegrationConflictPaths []string

// IntegrationRepoPreview 记录一个 Repo 的目标 SHA、任务 Commit 和不可变预演结果。
type IntegrationRepoPreview struct {
	PlanID              string                   `json:"planId"`
	RepoKey             WorkflowRepoKey          `json:"repoKey"`
	ArtifactID          string                   `json:"artifactId"`
	TaskCommitID        string                   `json:"taskCommitId"`
	ArtifactFingerprint string                   `json:"artifactFingerprint"`
	PolicyDigest        string                   `json:"policyDigest"`
	TargetRef           string                   `json:"targetRef"`
	ExpectedTargetSHA   string                   `json:"expectedTargetSha"`
	TaskCommitSHA       string                   `json:"taskCommitSha"`
	MergeMethod         string                   `json:"mergeMethod"`
	Status              string                   `json:"status"`
	TreeSHA             string                   `json:"treeSha"`
	MergeCommitSHA      string                   `json:"mergeCommitSha"`
	DiffDigest          string                   `json:"diffDigest"`
	ConflictPaths       IntegrationConflictPaths `json:"conflictPaths"`
	PreviewDigest       string                   `json:"previewDigest"`
	Revision            int64                    `json:"revision"`
}

// Digest 将完整有序仓库集合绑定到工作流和 Plan ID；创建时间不是新的授权内容。
func (p IntegrationPlan) Digest(entries []IntegrationRepoPreview) string {
	data, _ := json.Marshal(struct {
		ID         string
		WorkflowID WorkflowID
		Entries    []IntegrationRepoPreview
	}{p.ID, p.WorkflowID, entries})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Digest 绑定工件身份、策略和候选结果，不能复用另一仓相同 SHA 的预演。
func (p IntegrationRepoPreview) Digest() string {
	p.PreviewDigest = ""
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
