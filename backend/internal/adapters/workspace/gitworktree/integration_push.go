package gitworktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LocalBareIdentity 仅接受物理绝对路径的 bare 仓库，拒绝 URL、链接和普通工作树。
// 本地模拟以 pack 传递对象和 ref CAS 等效实现租约推送，绝不调用网络传输或 receive hooks。
func LocalBareIdentity(ctx context.Context, root string) (string, error) {
	path, err := deliveryRoot(root)
	if err != nil {
		return "", err
	}
	bare, err := deliveryGit(ctx, path, "", nil, "rev-parse", "--is-bare-repository")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(bare)) != "true" {
		return "", domain.ErrDeliveryBlocked
	}
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:]), nil
}

// ObserveLocalPush 绑定登记路径与批准身份后，只读远端 ref 用于预检和恢复。
func ObserveLocalPush(ctx context.Context, root string, p domain.IntegrationPushTarget) (string, error) {
	identity, err := LocalBareIdentity(ctx, root)
	if err != nil {
		return "", err
	}
	if identity != p.RemoteIdentity || !strings.HasPrefix(p.TargetRef, "refs/heads/") || !domain.ValidGitSHA(p.CommitSHA) || !domain.ValidGitSHA(p.ExpectedRemoteSHA) {
		return "", domain.ErrArtifactStale
	}
	if _, err := deliveryGit(ctx, root, "", nil, "check-ref-format", p.TargetRef); err != nil {
		return "", err
	}
	out, err := deliveryGit(ctx, root, "", nil, "rev-parse", "--verify", p.TargetRef+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if !domain.ValidGitSHA(sha) {
		return "", domain.ErrArtifactInvalid
	}
	return sha, nil
}

// ApplyLocalPush 先传递确定对象，再于持久 guard 内按原远端 SHA 更新唯一 ref；失败只能对账。
func ApplyLocalPush(ctx context.Context, a domain.ArtifactSnapshot, root string, p domain.IntegrationPushTarget, guard func(context.Context, func(context.Context) error) error) error {
	if guard == nil {
		return domain.ErrDeliveryBlocked
	}
	sha, err := ObserveLocalPush(ctx, root, p)
	if err != nil {
		return err
	}
	if sha != p.ExpectedRemoteSHA {
		return domain.ErrArtifactStale
	}
	if _, _, err := verifyDeliveryRepo(ctx, a.Repo); err != nil {
		return err
	}
	target, err := DeliveryTargetSHA(ctx, a)
	if err != nil {
		return err
	}
	if target != p.CommitSHA || p.TargetRef != a.Repo.TargetRef {
		return domain.ErrArtifactStale
	}
	pack, err := deliveryGit(ctx, a.Repo.WorktreePath, "", []byte(p.CommitSHA+"\n"), "pack-objects", "--stdout", "--revs")
	if err != nil {
		return err
	}
	if _, err := deliveryGit(ctx, root, "", pack, "index-pack", "--stdin"); err != nil {
		return err
	}
	return guard(ctx, func(ctx context.Context) error {
		sha, err := ObserveLocalPush(ctx, root, p)
		if err != nil {
			return err
		}
		if sha != p.ExpectedRemoteSHA {
			return domain.ErrArtifactStale
		}
		_, err = deliveryGit(ctx, root, "", []byte(fmt.Sprintf("start\nupdate %s %s %s\nprepare\ncommit\n", p.TargetRef, p.CommitSHA, p.ExpectedRemoteSHA)), "update-ref", "--no-deref", "--stdin")
		return err
	})
}
