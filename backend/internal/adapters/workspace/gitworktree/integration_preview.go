package gitworktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// VerifyCommittedDelivery 比较确定提交和原索引、完整工作区；提交后的 HEAD 变化不能掩盖其他漂移。
func VerifyCommittedDelivery(ctx context.Context, a domain.ArtifactSnapshot, commitSHA string) error {
	state, err := ObserveDeliveryCommit(ctx, a, commitSHA)
	if err != nil || state != "committed" {
		return errors.Join(domain.ErrArtifactStale, err)
	}
	workspace, _, err := deliveryInventory(ctx, a.Repo)
	if err != nil {
		return err
	}
	index, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(index)
	if workspace != a.Git.WorkspaceDigest || hex.EncodeToString(sum[:]) != a.Git.IndexDigest {
		return domain.ErrArtifactStale
	}
	return nil
}

// PreviewDelivery 在专用 detached Worktree 运行 merge-tree；不修改任何分支 ref 或用户索引。
func PreviewDelivery(ctx context.Context, a domain.ArtifactSnapshot, taskCommitSHA, scratch string) (result domain.IntegrationRepoPreview, err error) {
	if !filepath.IsAbs(scratch) {
		return result, domain.ErrArtifactInvalid
	}
	if err := VerifyCommittedDelivery(ctx, a, taskCommitSHA); err != nil {
		return result, err
	}
	if _, err := PrepareDeliveryCommit(ctx, a); err != nil {
		return result, err
	}
	target, err := DeliveryTargetSHA(ctx, a)
	if err != nil {
		return result, err
	}
	if err := validatePreviewTree(ctx, a.Repo.WorktreePath, target); err != nil {
		return result, err
	}
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return result, err
	}
	if _, err := deliveryRoot(scratch); err != nil {
		return result, err
	}
	dir, err := os.MkdirTemp(scratch, "integration-worktree-")
	if err != nil {
		return result, err
	}
	if _, err = deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "worktree", "add", "--detach", dir, target); err != nil {
		// 失败时保留可能已登记的目录，不猜测 Git 是否完成登记。
		return result, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, cleanupErr := deliveryGit(cleanup, a.Repo.WorktreePath, "", nil, "worktree", "remove", dir)
		err = errors.Join(err, cleanupErr)
	}()
	output, mergeErr := deliveryGit(ctx, dir, "", nil, "merge-tree", "--write-tree", "--name-only", "-z", "--no-messages", target, taskCommitSHA)
	parts := strings.Split(string(output), "\x00")
	if len(parts) == 0 || !domain.ValidGitSHA(parts[0]) {
		return result, errors.Join(domain.ErrDeliveryBlocked, mergeErr)
	}
	result = domain.IntegrationRepoPreview{RepoKey: a.Repo.RepoKey, ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, TargetRef: a.Repo.TargetRef, ExpectedTargetSHA: target, TaskCommitSHA: taskCommitSHA, MergeMethod: "merge", Status: "clean", TreeSHA: parts[0], Revision: 1}
	if mergeErr != nil {
		var exit *exec.ExitError
		if !errors.As(mergeErr, &exit) || exit.ExitCode() != 1 {
			return result, mergeErr
		}
		result.Status = "conflict"
		for _, path := range parts[1:] {
			if path == "" {
				continue
			}
			if !deliveryAllowed(path, nil) {
				return result, domain.ErrArtifactInvalid
			}
			result.ConflictPaths = append(result.ConflictPaths, path)
		}
		if len(result.ConflictPaths) == 0 {
			return result, domain.ErrDeliveryBlocked
		}
		slices.Sort(result.ConflictPaths)
	} else {
		if err := validatePreviewTree(ctx, dir, result.TreeSHA); err != nil {
			return result, err
		}
		sha, err := deliveryGit(ctx, dir, "", integrationCommitObject(a, result), "hash-object", "-t", "commit", "-w", "--stdin")
		if err != nil {
			return result, err
		}
		result.MergeCommitSHA = strings.TrimSpace(string(sha))
		if !domain.ValidGitSHA(result.MergeCommitSHA) {
			return result, domain.ErrArtifactInvalid
		}
	}
	diff, err := deliveryGit(ctx, dir, "", nil, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--binary", target, result.TreeSHA, "--")
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(diff)
	result.DiffDigest = hex.EncodeToString(sum[:])
	after, err := DeliveryTargetSHA(ctx, a)
	if err != nil || after != target {
		return result, errors.Join(domain.ErrArtifactStale, err)
	}
	if err := VerifyCommittedDelivery(ctx, a, taskCommitSHA); err != nil {
		return result, err
	}
	return result, nil
}

// VerifyIntegrationPreview 复验冻结计划和当前仓库；多仓预演结束及审批写入前均不能使用旧目标。
func VerifyIntegrationPreview(ctx context.Context, a domain.ArtifactSnapshot, p domain.IntegrationRepoPreview) error {
	if p.PreviewDigest != p.Digest() || p.ArtifactID != a.ID || p.ArtifactFingerprint != a.Fingerprint || p.RepoKey != a.Repo.RepoKey || p.TargetRef != a.Repo.TargetRef || p.MergeMethod != "merge" {
		return domain.ErrArtifactStale
	}
	if err := VerifyCommittedDelivery(ctx, a, p.TaskCommitSHA); err != nil {
		return err
	}
	target, err := DeliveryTargetSHA(ctx, a)
	if err != nil {
		return err
	}
	if target != p.ExpectedTargetSHA {
		return domain.ErrArtifactStale
	}
	if p.Status == "clean" {
		if !domain.ValidGitSHA(p.MergeCommitSHA) || !domain.ValidGitSHA(p.TreeSHA) {
			return domain.ErrArtifactInvalid
		}
		body, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "cat-file", "commit", p.MergeCommitSHA)
		if err != nil {
			return err
		}
		if string(body) != string(integrationCommitObject(a, p)) {
			return domain.ErrArtifactStale
		}
	} else if p.Status != "conflict" {
		return domain.ErrDeliveryBlocked
	}
	return nil
}

// integrationCommitObject 固定两个父提交、树和时间，批准后只能应用同一个确定对象。
func integrationCommitObject(a domain.ArtifactSnapshot, p domain.IntegrationRepoPreview) []byte {
	stamp := a.CreatedAt.Unix()
	return []byte(fmt.Sprintf("tree %s\nparent %s\nparent %s\nauthor Agent Orchestrator <ao@localhost> %d +0000\ncommitter Agent Orchestrator <ao@localhost> %d +0000\n\nM4 integration %s\n", p.TreeSHA, p.ExpectedTargetSHA, p.TaskCommitSHA, stamp, stamp, a.Fingerprint))
}

// validatePreviewTree 在 checkout 前拒绝 symlink、submodule 和非法路径。
func validatePreviewTree(ctx context.Context, root, object string) error {
	listing, err := deliveryGit(ctx, root, "", nil, "ls-tree", "-r", "-z", object)
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(listing), "\x00") {
		if entry == "" {
			continue
		}
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") || !deliveryAllowed(path, nil) {
			return domain.ErrDeliveryBlocked
		}
	}
	return nil
}
