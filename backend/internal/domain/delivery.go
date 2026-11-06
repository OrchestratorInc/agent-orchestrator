package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrArtifactInvalid         = errors.New("invalid delivery artifact")
	ErrArtifactStale           = errors.New("delivery artifact changed")
	ErrPathReservationConflict = errors.New("workflow write paths are reserved")
	ErrDeliveryBlocked         = errors.New("delivery is blocked")
)

// DeliveryRepo 仅由持久化的项目、任务 scope 和 Session Worktree 绑定组装，不接受客户端路径。
type DeliveryRepo struct {
	RepoKey      WorkflowRepoKey `json:"repoKey"`
	ProjectID    ProjectID       `json:"projectId"`
	RepoPath     string          `json:"-"`
	WorktreePath string          `json:"-"`
	GitCommonDir string          `json:"-"`
	Branch       string          `json:"branch"`
	TargetRef    string          `json:"targetRef"`
	BaseSHA      string          `json:"baseSha"`
	CreationSHA  string          `json:"creationSha"`
	AllowedPaths []string        `json:"allowedPaths"`
}

// ArtifactFile 是交付树相对 HEAD 的完整文件清单，BlobSHA 来自未执行 filter 的真实文件字节。
type ArtifactFile struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	BlobSHA string `json:"blobSha"`
	Deleted bool   `json:"deleted"`
}

// GitArtifact 同时绑定交付树与工作区观察；范围外 dirty 参与失效判断但不进入交付树。
type GitArtifact struct {
	HeadSHA         string         `json:"headSha"`
	TreeSHA         string         `json:"treeSha"`
	IndexDigest     string         `json:"indexDigest"`
	WorkspaceDigest string         `json:"workspaceDigest"`
	Files           []ArtifactFile `json:"files"`
}

// ArtifactSnapshot 保存不可变代码身份；Revision 只用于质量侧车 CAS，ArtifactRevision 表示新代码版本。
type ArtifactSnapshot struct {
	ID               string            `json:"id"`
	WorkflowID       WorkflowID        `json:"workflowId"`
	TaskID           WorkflowTaskID    `json:"taskId"`
	AttemptID        WorkflowAttemptID `json:"attemptId"`
	SessionID        SessionID         `json:"sessionId"`
	Generation       string            `json:"-"`
	Repo             DeliveryRepo      `json:"repo"`
	Git              GitArtifact       `json:"git"`
	ArtifactRevision int64             `json:"artifactRevision"`
	Fingerprint      string            `json:"fingerprint"`
	State            string            `json:"state"`
	Revision         int64             `json:"revision"`
	CreatedAt        time.Time         `json:"createdAt"`
}

// Digest 覆盖不可变身份和本地路径；显示用 json:- 不能使同 SHA 换仓逃过指纹。
func (a ArtifactSnapshot) Digest() string {
	value := struct {
		Workflow                          WorkflowID
		Task                              WorkflowTaskID
		Attempt                           WorkflowAttemptID
		Session                           SessionID
		Generation                        string
		Repo                              DeliveryRepo
		RepoPath, WorktreePath, CommonDir string
		Git                               GitArtifact
		ArtifactRevision                  int64
	}{a.WorkflowID, a.TaskID, a.AttemptID, a.SessionID, a.Generation, a.Repo, a.Repo.RepoPath, a.Repo.WorktreePath, a.Repo.GitCommonDir, a.Git, a.ArtifactRevision}
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ValidGitSHA 拒绝 abbreviated SHA 与 Git ref 表达式。
func ValidGitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
