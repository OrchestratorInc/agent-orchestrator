package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// deliveryCommitObject 固定作者和 Artifact 时间，重启对账可重算唯一对象而无需再次创建提交。
func deliveryCommitObject(a domain.ArtifactSnapshot) ([]byte, error) {
	if a.Fingerprint != a.Digest() || a.CreatedAt.IsZero() || !domain.ValidGitSHA(a.Git.TreeSHA) || !domain.ValidGitSHA(a.Git.HeadSHA) {
		return nil, domain.ErrArtifactInvalid
	}
	stamp := a.CreatedAt.Unix()
	return []byte(fmt.Sprintf("tree %s\nparent %s\nauthor Agent Orchestrator <ao@localhost> %d +0000\ncommitter Agent Orchestrator <ao@localhost> %d +0000\n\nM4 delivery %s\n", a.Git.TreeSHA, a.Git.HeadSHA, stamp, stamp, a.Fingerprint)), nil
}

// PrepareDeliveryCommit 只计算确定的对象 SHA；不写对象、不运行 hook、不修改索引或 ref。
func PrepareDeliveryCommit(ctx context.Context, a domain.ArtifactSnapshot) (string, error) {
	body, err := deliveryCommitObject(a)
	if err != nil {
		return "", err
	}
	if _, _, err := verifyDeliveryRepo(ctx, a.Repo); err != nil {
		return "", err
	}
	signing, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "config", "--type=bool", "--get", "commit.gpgsign")
	var absent *exec.ExitError
	if (err != nil && (!errors.As(err, &absent) || absent.ExitCode() != 1)) || strings.TrimSpace(string(signing)) == "true" {
		return "", domain.ErrDeliveryBlocked
	}
	result, err := deliveryGit(ctx, a.Repo.WorktreePath, "", body, "hash-object", "-t", "commit", "--stdin")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(result))
	if !domain.ValidGitSHA(sha) {
		return "", domain.ErrArtifactInvalid
	}
	return sha, nil
}

// ApplyDeliveryCommit 仅写快照中的任务 ref，旧 HEAD CAS 保护并发；失败不推断是否已写入。
// 原 index 和 dirty 不变，提交后的 staged 视图可能与新 HEAD 不同。
func ApplyDeliveryCommit(ctx context.Context, a domain.ArtifactSnapshot, expected, targetSHA, scratch string, guard func(context.Context, func(context.Context) error) error) error {
	if !domain.ValidGitSHA(expected) || !domain.ValidGitSHA(targetSHA) || guard == nil {
		return domain.ErrArtifactInvalid
	}
	repo, current, err := InspectDelivery(ctx, a.Repo, scratch)
	if err != nil {
		return err
	}
	observed := a
	observed.Repo, observed.Git = repo, current
	if observed.Digest() != a.Fingerprint {
		return domain.ErrArtifactStale
	}
	sha, err := PrepareDeliveryCommit(ctx, a)
	if err != nil {
		return err
	}
	if sha != expected {
		return domain.ErrArtifactStale
	}
	body, err := deliveryCommitObject(a)
	if err != nil {
		return err
	}
	result, err := deliveryGit(ctx, a.Repo.WorktreePath, "", body, "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(result)) != expected {
		return domain.ErrArtifactStale
	}
	target, err := DeliveryTargetSHA(ctx, a)
	if err != nil {
		return err
	}
	if target != targetSHA {
		return domain.ErrArtifactStale
	}
	// 同一 Git ref 事务核验目标 SHA 和任务旧 HEAD，防止两次命令之间发生目标漂移。
	transaction := fmt.Sprintf("start\nverify %s %s\nupdate refs/heads/%s %s %s\nprepare\ncommit\n", a.Repo.TargetRef, targetSHA, a.Repo.Branch, expected, a.Git.HeadSHA)
	return guard(ctx, func(ctx context.Context) error {
		_, err := deliveryGit(ctx, a.Repo.WorktreePath, "", []byte(transaction), "update-ref", "--no-deref", "--stdin")
		return err
	})
}

// ObserveDeliveryCommit 只读核验原仓库、分支和精确提交字节；旧 parent 仍在也不授权自动重试。
func ObserveDeliveryCommit(ctx context.Context, a domain.ArtifactSnapshot, expected string) (string, error) {
	if !domain.ValidGitSHA(expected) {
		return "unknown", domain.ErrArtifactInvalid
	}
	_, head, err := verifyDeliveryRepo(ctx, a.Repo)
	if err != nil {
		return "unknown", err
	}
	if head != expected {
		return "unknown", nil
	}
	body, err := deliveryCommitObject(a)
	if err != nil {
		return "unknown", err
	}
	value, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "cat-file", "commit", expected)
	if err != nil {
		return "unknown", err
	}
	if !bytes.Equal(value, body) {
		return "unknown", domain.ErrArtifactStale
	}
	return "committed", nil
}
