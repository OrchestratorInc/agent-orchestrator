package domain

import "time"

// TaskCommit 记录唯一工件的受限 Git 意图；requested/unknown 只允许对账，不能重新执行。
type TaskCommit struct {
	ID                  string         `json:"id"`
	ArtifactID          string         `json:"artifactId"`
	ArtifactFingerprint string         `json:"artifactFingerprint"`
	ArtifactRevision    int64          `json:"artifactRevision"`
	PolicyDigest        string         `json:"policyDigest"`
	GateID              string         `json:"gateId"`
	ReviewID            string         `json:"reviewId"`
	ParentSHA           string         `json:"parentSha"`
	TreeSHA             string         `json:"treeSha"`
	CommitSHA           string         `json:"commitSha"`
	TargetSHA           string         `json:"targetSha"`
	State               string         `json:"state"`
	Fence               SchedulerFence `json:"-"`
	Revision            int64          `json:"revision"`
	CreatedAt           time.Time      `json:"createdAt"`
}
