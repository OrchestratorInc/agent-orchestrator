package gitworktree

import (
	"context"
	"crypto/sha1" // #nosec G505 -- 支持 Git SHA-1 仓库对象协议；SHA-256 仓库走独立分支。
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// VerifyMaterializedDelivery 防止测试先改动受测源码再报告成功；允许编译产生范围外输出，但原树字节必须保持。
func VerifyMaterializedDelivery(ctx context.Context, a domain.ArtifactSnapshot, dir string) error {
	if _, err := deliveryRoot(dir); err != nil {
		return err
	}
	listing, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "ls-tree", "-r", "-z", a.Git.TreeSHA)
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(listing), "\x00") {
		if entry == "" {
			continue
		}
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return domain.ErrArtifactInvalid
		}
		data, mode, deleted, err := deliveryFile(dir, path)
		if err != nil {
			return err
		}
		if deleted || (runtime.GOOS != "windows" && mode != fields[0]) {
			return domain.ErrArtifactStale
		}
		h := sha1.New() // #nosec G401 -- 此分支对应 Git SHA-1 对象 ID；对象完整交付身份另以 SHA-256 绑定。
		if len(fields[2]) == 64 {
			h = sha256.New()
		}
		_, _ = fmt.Fprintf(h, "blob %d\x00", len(data))
		_, _ = h.Write(data)
		if hex.EncodeToString(h.Sum(nil)) != fields[2] {
			return domain.ErrArtifactStale
		}
	}
	return nil
}

// DeliveryTargetSHA 只解析绑定的本地目标分支；不接受客户端 ref 表达式或触发远程读取。
func DeliveryTargetSHA(ctx context.Context, a domain.ArtifactSnapshot) (string, error) {
	if _, _, err := verifyDeliveryRepo(ctx, a.Repo); err != nil {
		return "", err
	}
	value, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "rev-parse", "--verify", a.Repo.TargetRef+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(value))
	if !domain.ValidGitSHA(sha) {
		return "", domain.ErrArtifactInvalid
	}
	return sha, nil
}

// MaterializeDelivery 导出冻结交付树供 Gate 读取；不用 checkout/filter，不把范围外 dirty 带进测试。
// 调用方负责移除返回的独立目录；此目录是隔离数据位置，并非限制测试进程权限的 OS 沙箱。
func MaterializeDelivery(ctx context.Context, a domain.ArtifactSnapshot, scratch string) (dir string, err error) {
	if a.Fingerprint != a.Digest() || !domain.ValidGitSHA(a.Git.TreeSHA) || !filepath.IsAbs(scratch) {
		return "", domain.ErrArtifactInvalid
	}
	if _, _, err = verifyDeliveryRepo(ctx, a.Repo); err != nil {
		return "", err
	}
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return "", err
	}
	if _, err = deliveryRoot(scratch); err != nil {
		return "", err
	}
	dir, err = os.MkdirTemp(scratch, "delivery-tree-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
			dir = ""
		}
	}()
	listing, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "ls-tree", "-r", "-z", a.Git.TreeSHA)
	if err != nil {
		return dir, err
	}
	seen := make(map[string]bool)
	total := 0
	for _, entry := range strings.Split(string(listing), "\x00") {
		if entry == "" {
			continue
		}
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") || !domain.ValidGitSHA(fields[2]) || !deliveryAllowed(path, nil) {
			return dir, domain.ErrDeliveryBlocked
		}
		key := path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(path)
			for _, part := range strings.Split(key, "/") {
				if strings.TrimRight(part, ". ") != part {
					return dir, domain.ErrDeliveryBlocked
				}
			}
		}
		if seen[key] || len(seen) >= 10000 {
			return dir, domain.ErrDeliveryBlocked
		}
		seen[key] = true
		content, err := deliveryGit(ctx, a.Repo.WorktreePath, "", nil, "cat-file", "blob", fields[2])
		if err != nil {
			return dir, err
		}
		total += len(content)
		if total > deliveryByteLimit {
			return dir, domain.ErrDeliveryBlocked
		}
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return dir, err
		}
		mode := os.FileMode(0o600)
		if fields[0] == "100755" {
			mode = 0o700
		}
		file, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return dir, err
		}
		_, writeErr := file.Write(content)
		closeErr := file.Close()
		if writeErr != nil {
			return dir, writeErr
		}
		if closeErr != nil {
			return dir, closeErr
		}
	}
	return dir, nil
}
