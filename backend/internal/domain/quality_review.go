package domain

import "time"

// QualityReviewActor 仅由可信 principal 派生；Agent 身份与写代码的 Session/Task 必须独立。
type QualityReviewActor struct {
	OperatorRef string            `json:"operatorRef,omitempty"`
	AttemptID   WorkflowAttemptID `json:"attemptId,omitempty"`
	SessionID   SessionID         `json:"sessionId,omitempty"`
	Generation  string            `json:"generation,omitempty"`
}

// QualityFinding 以工件相对路径定位问题，结论不代表测试进程退出码。
type QualityFinding struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Priority int    `json:"priority"`
	Reason   string `json:"reason"`
}

// QualityReview 是当前工件、策略、任期与独立审查身份的一次不可重绑记录。
type QualityReview struct {
	ID                  string             `json:"id"`
	ArtifactID          string             `json:"artifactId"`
	ArtifactFingerprint string             `json:"artifactFingerprint"`
	PolicyRevision      int64              `json:"policyRevision"`
	PolicyDigest        string             `json:"policyDigest"`
	TargetSHA           string             `json:"targetSha"`
	Actor               QualityReviewActor `json:"actor"`
	Fence               SchedulerFence     `json:"-"`
	State               string             `json:"state"`
	Verdict             ReviewVerdict      `json:"verdict"`
	Findings            []QualityFinding   `json:"findings"`
	FixRequestID        string             `json:"fixRequestId,omitempty"`
	Revision            int64              `json:"revision"`
	CreatedAt           time.Time          `json:"createdAt"`
}
