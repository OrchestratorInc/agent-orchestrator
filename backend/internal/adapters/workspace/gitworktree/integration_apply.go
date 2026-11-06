package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// CheckIntegrationTarget 拒绝任何已检出的目标分支和无法满足的签名要求，避免索引与分支失配。
func CheckIntegrationTarget(ctx context.Context, a domain.ArtifactSnapshot, p domain.IntegrationRepoPreview) error {
	if p.Status != "clean" {
		return domain.ErrDeliveryBlocked
	}
	if err := VerifyIntegrationPreview(ctx, a, p); err != nil {
		return err
	}
	if _, err := PrepareDeliveryCommit(ctx, a); err != nil {
		return err
	}
	sign, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "config", "--type=bool", "--get", "merge.gpgsign")
	var exit *exec.ExitError
	if (err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1)) || strings.TrimSpace(string(sign)) == "true" {
		return domain.ErrDeliveryBlocked
	}
	return checkIntegrationTargetAvailable(ctx, a, p)
}

// checkIntegrationTargetAvailable 在预检和最终写锁内都检查目标检出状态，缩短长预检产生的竞态窗口。
func checkIntegrationTargetAvailable(ctx context.Context, a domain.ArtifactSnapshot, p domain.IntegrationRepoPreview) error {
	worktrees, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(worktrees), "\x00") {
		if entry == "branch "+p.TargetRef {
			return domain.ErrDeliveryBlocked
		}
	}
	return nil
}

// ApplyIntegrationTarget 只使用已批准的确定对象；最终 ref CAS 在调用方的持久任期 guard 内执行。
func ApplyIntegrationTarget(ctx context.Context, a domain.ArtifactSnapshot, p domain.IntegrationRepoPreview, guard func(context.Context, func(context.Context) error) error) error {
	if guard == nil {
		return domain.ErrDeliveryBlocked
	}
	if err := CheckIntegrationTarget(ctx, a, p); err != nil {
		return err
	}
	transaction := fmt.Sprintf("start\nverify refs/heads/%s %s\nupdate %s %s %s\nprepare\ncommit\n", a.Repo.Branch, p.TaskCommitSHA, p.TargetRef, p.MergeCommitSHA, p.ExpectedTargetSHA)
	return guard(ctx, func(ctx context.Context) error {
		if err := checkIntegrationTargetAvailable(ctx, a, p); err != nil {
			return err
		}
		_, err := deliveryGit(ctx, a.Repo.WorktreePath, "", []byte(transaction), "update-ref", "--no-deref", "--stdin")
		return err
	})
}

// ObserveIntegrationTarget 只读精确 ref；用于已持久意图的对账，不要求目标仍是旧 SHA。
func ObserveIntegrationTarget(ctx context.Context, a domain.ArtifactSnapshot, p domain.IntegrationRepoPreview) (string, error) {
	if a.Fingerprint != a.Digest() || p.ArtifactFingerprint != a.Fingerprint || p.TargetRef != a.Repo.TargetRef {
		return "", domain.ErrArtifactStale
	}
	if _, _, err := verifyDeliveryRepo(ctx, a.Repo); err != nil {
		return "", err
	}
	value, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "rev-parse", "--verify", p.TargetRef+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(value))
	if !domain.ValidGitSHA(sha) {
		return "", domain.ErrArtifactInvalid
	}
	return sha, nil
}
