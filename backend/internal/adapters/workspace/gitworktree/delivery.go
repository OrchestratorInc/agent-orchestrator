package gitworktree

import (
	"bytes"
	"context"
	"crypto/sha1" // Git SHA-1 对象标识；安全工件指纹仍使用 SHA-256。
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const deliveryByteLimit = 64 << 20

// deliveryOutput 对外部 Git 输出硬限流，超过上限拒绝工件，不把截断清单当成完整证据。
type deliveryOutput struct{ bytes.Buffer }

func (b *deliveryOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > deliveryByteLimit {
		return 0, domain.ErrDeliveryBlocked
	}
	return b.Buffer.Write(p)
}

// deliveryGit 固定无交互 argv 和配置边界；不执行 hook、filter、fsmonitor 或懒拉取。
func deliveryGit(ctx context.Context, root, index string, input []byte, args ...string) ([]byte, error) {
	argv := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "protocol.allow=never", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "--no-pager"}
	cmd := process.CommandContext(ctx, "git", append(argv, args...)...)
	cmd.Dir = root
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	if index != "" {
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+index)
	}
	cmd.Stdin = bytes.NewReader(input)
	process.ConfigureTreeCancellation(cmd)
	var out, stderr deliveryOutput
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("delivery git %s: %w", args[0], err)
	}
	return out.Bytes(), nil
}

// deliveryPathEqual 对 Windows 仓库身份执行大小写折叠，其他平台保持文件系统通常的大小写语义。
func deliveryPathEqual(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// deliveryRoot 拒绝相对路径和通过 symlink/junction 改写的工作区身份。
func deliveryRoot(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", domain.ErrArtifactInvalid
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !deliveryPathEqual(resolved, path) {
		return "", domain.ErrArtifactInvalid
	}
	return filepath.Clean(resolved), nil
}

// verifyDeliveryRepo 重验注册 Repo 与 Worktree 的物理身份、任务分支及完整祖先链。
func verifyDeliveryRepo(ctx context.Context, repo domain.DeliveryRepo) (domain.DeliveryRepo, string, error) {
	if repo.RepoKey == "" || repo.ProjectID == "" || repo.Branch == "" || !strings.HasPrefix(repo.TargetRef, "refs/heads/") || "refs/heads/"+repo.Branch == repo.TargetRef || !domain.ValidGitSHA(repo.BaseSHA) || !domain.ValidGitSHA(repo.CreationSHA) {
		return repo, "", domain.ErrArtifactInvalid
	}
	var common string
	for _, root := range []string{repo.RepoPath, repo.WorktreePath} {
		canonical, err := deliveryRoot(root)
		if err != nil {
			return repo, "", err
		}
		top, err := deliveryGit(ctx, root, "", nil, "rev-parse", "--show-toplevel")
		if err != nil || !deliveryPathEqual(strings.TrimSpace(string(top)), canonical) {
			return repo, "", domain.ErrArtifactInvalid
		}
		gitDir, err := deliveryGit(ctx, root, "", nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return repo, "", err
		}
		actual, err := deliveryRoot(strings.TrimSpace(string(gitDir)))
		if err != nil || common != "" && !deliveryPathEqual(common, actual) {
			return repo, "", domain.ErrArtifactInvalid
		}
		common = actual
		configScopes := []string{"--local"}
		worktreeConfig, configErr := deliveryGit(ctx, root, "", nil, "config", "--local", "--type=bool", "--get", "extensions.worktreeConfig")
		var missingConfig *exec.ExitError
		if configErr != nil && (!errors.As(configErr, &missingConfig) || missingConfig.ExitCode() != 1) {
			return repo, "", domain.ErrDeliveryBlocked
		}
		if strings.TrimSpace(string(worktreeConfig)) == "true" {
			configScopes = append(configScopes, "--worktree")
		}
		for _, configScope := range configScopes {
			_, err = deliveryGit(ctx, root, "", nil, "config", configScope, "--no-includes", "--get-regexp", `^(filter\.|diff\..*\.(command|textconv)|merge\..*\.driver|include\.|includeif\.|core\.(fsmonitor|sshcommand|alternaterefscommand)|remote\..*\.promisor)`)
			var exit *exec.ExitError
			if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
				return repo, "", domain.ErrDeliveryBlocked
			}
		}
	}
	if repo.GitCommonDir != "" && !deliveryPathEqual(repo.GitCommonDir, common) {
		return repo, "", domain.ErrArtifactStale
	}
	repo.GitCommonDir = common
	if _, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "check-ref-format", repo.TargetRef); err != nil {
		return repo, "", domain.ErrArtifactInvalid
	}
	branch, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(branch)) != repo.Branch {
		return repo, "", domain.ErrArtifactStale
	}
	head, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "rev-parse", "HEAD")
	if err != nil {
		return repo, "", err
	}
	sha := strings.TrimSpace(string(head))
	if !domain.ValidGitSHA(sha) {
		return repo, "", domain.ErrArtifactInvalid
	}
	for _, pair := range [][2]string{{repo.BaseSHA, repo.CreationSHA}, {repo.CreationSHA, sha}} {
		if _, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "merge-base", "--is-ancestor", pair[0], pair[1]); err != nil {
			return repo, "", domain.ErrArtifactStale
		}
	}
	// 私有 index 继承 HEAD；历史中的越界提交不能借此绕过本次文件清单。
	committed, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", repo.CreationSHA, sha, "--")
	if err != nil {
		return repo, "", err
	}
	for _, path := range strings.Split(string(committed), "\x00") {
		if path != "" && !deliveryAllowed(path, repo.AllowedPaths) {
			return repo, "", domain.ErrDeliveryBlocked
		}
	}
	flags, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "ls-files", "-v", "-z")
	if err != nil {
		return repo, "", err
	}
	for _, entry := range strings.Split(string(flags), "\x00") {
		// skip-worktree 的磁盘缺失代表省略，不能解释为用户删除。
		if strings.HasPrefix(entry, "S ") || strings.HasPrefix(entry, "s ") {
			return repo, "", domain.ErrDeliveryBlocked
		}
	}
	return repo, sha, nil
}

// deliveryAllowed 只允许规范仓库相对路径，按组件匹配避免 src/a 错误授权 src/ab。
func deliveryAllowed(path string, scopes []string) bool {
	canonical, ok := domain.CanonicalWorkflowPath(path, false)
	if !ok || canonical == "" || canonical != path || strings.EqualFold(strings.Split(path, "/")[0], ".git") {
		return false
	}
	if len(scopes) == 0 {
		return true
	}
	for _, scope := range scopes {
		value, ok := domain.CanonicalWorkflowPath(scope, false)
		if !ok {
			return false
		}
		p := path
		if runtime.GOOS == "windows" {
			value, p = strings.ToLower(value), strings.ToLower(p)
		}
		if value == "" || p == value || strings.HasPrefix(p, value+"/") {
			return true
		}
	}
	return false
}

// deliveryFile 禁止任一祖先 symlink 与特殊文件，删除只接受真正的不存在，不能掩盖 I/O 错误。
func deliveryFile(root, path string) ([]byte, string, bool, error) {
	if !deliveryAllowed(path, nil) {
		return nil, "", false, domain.ErrArtifactInvalid
	}
	current := root
	parts := strings.Split(path, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil, "", true, nil
		}
		if err != nil {
			return nil, "", false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || i < len(parts)-1 && !info.IsDir() {
			return nil, "", false, domain.ErrArtifactInvalid
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() || info.Size() > deliveryByteLimit {
				return nil, "", false, domain.ErrDeliveryBlocked
			}
			data, err := os.ReadFile(current)
			if err != nil || len(data) > deliveryByteLimit {
				return nil, "", false, domain.ErrDeliveryBlocked
			}
			mode := "100644"
			if runtime.GOOS != "windows" && info.Mode()&0111 != 0 {
				mode = "100755"
			}
			return data, mode, false, nil
		}
	}
	return nil, "", false, domain.ErrArtifactInvalid
}

// InspectDelivery 构造私有 index 的交付树，不改变用户索引、HEAD 或工作区；前后观察不同则拒绝。
func InspectDelivery(ctx context.Context, repo domain.DeliveryRepo, scratch string) (domain.DeliveryRepo, domain.GitArtifact, error) {
	repo, head, err := verifyDeliveryRepo(ctx, repo)
	if err != nil {
		return repo, domain.GitArtifact{}, err
	}
	for _, scope := range repo.AllowedPaths {
		if _, ok := domain.CanonicalWorkflowPath(scope, false); !ok {
			return repo, domain.GitArtifact{}, domain.ErrArtifactInvalid
		}
	}
	before, files, err := deliveryInventory(ctx, repo)
	if err != nil {
		return repo, domain.GitArtifact{}, err
	}
	indexBefore, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "ls-files", "--stage", "-z")
	if err != nil {
		return repo, domain.GitArtifact{}, err
	}
	unmerged, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "ls-files", "--unmerged", "-z")
	if err != nil || len(unmerged) != 0 {
		return repo, domain.GitArtifact{}, domain.ErrDeliveryBlocked
	}
	if err = os.MkdirAll(scratch, 0700); err != nil {
		return repo, domain.GitArtifact{}, err
	}
	dir, err := os.MkdirTemp(scratch, "artifact-index-")
	if err != nil {
		return repo, domain.GitArtifact{}, err
	}
	defer os.RemoveAll(dir)
	index := filepath.Join(dir, "index")
	if _, err = deliveryGit(ctx, repo.WorktreePath, index, nil, "read-tree", head); err != nil {
		return repo, domain.GitArtifact{}, err
	}
	for _, file := range files {
		if file.Deleted {
			_, err = deliveryGit(ctx, repo.WorktreePath, index, nil, "update-index", "--force-remove", "--", file.Path)
		} else {
			_, err = deliveryGit(ctx, repo.WorktreePath, index, nil, "update-index", "--add", "--cacheinfo", file.Mode, file.BlobSHA, file.Path)
		}
		if err != nil {
			return repo, domain.GitArtifact{}, err
		}
	}
	tree, err := deliveryGit(ctx, repo.WorktreePath, index, nil, "write-tree")
	if err != nil {
		return repo, domain.GitArtifact{}, err
	}
	after, _, err := deliveryInventory(ctx, repo)
	if err != nil {
		return repo, domain.GitArtifact{}, err
	}
	indexAfter, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "ls-files", "--stage", "-z")
	if err != nil {
		return repo, domain.GitArtifact{}, err
	}
	_, headAfter, err := verifyDeliveryRepo(ctx, repo)
	if err != nil || head != headAfter || before != after || !bytes.Equal(indexBefore, indexAfter) {
		return repo, domain.GitArtifact{}, domain.ErrArtifactStale
	}
	sum := sha256.Sum256(indexBefore)
	return repo, domain.GitArtifact{HeadSHA: head, TreeSHA: strings.TrimSpace(string(tree)), IndexDigest: hex.EncodeToString(sum[:]), WorkspaceDigest: before, Files: files}, nil
}

// deliveryInventory 对全部受 Git 管理或未忽略的内容取摘要；只有允许范围内的变更进入交付清单。
func deliveryInventory(ctx context.Context, repo domain.DeliveryRepo) (string, []domain.ArtifactFile, error) {
	tracked, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		return "", nil, err
	}
	base := map[string]domain.ArtifactFile{}
	for _, entry := range strings.Split(string(tracked), "\x00") {
		if entry == "" {
			continue
		}
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || fields[0] != "100644" && fields[0] != "100755" {
			return "", nil, domain.ErrDeliveryBlocked
		}
		base[path] = domain.ArtifactFile{Path: path, Mode: fields[0], BlobSHA: fields[2]}
	}
	extra, err := deliveryGit(ctx, repo.WorktreePath, "", nil, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", nil, err
	}
	paths := make([]string, 0, len(base))
	for path := range base {
		paths = append(paths, path)
	}
	paths = append(paths, strings.Split(strings.TrimSuffix(string(extra), "\x00"), "\x00")...)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	if len(paths) > 10000 {
		return "", nil, domain.ErrDeliveryBlocked
	}
	workspaceHash := sha256.New()
	var files []domain.ArtifactFile
	total := 0
	for _, path := range paths {
		if path == "" {
			continue
		}
		data, mode, deleted, err := deliveryFile(repo.WorktreePath, path)
		if err != nil {
			return "", nil, err
		}
		total += len(data)
		if total > deliveryByteLimit {
			return "", nil, domain.ErrDeliveryBlocked
		}
		original, exists := base[path]
		if runtime.GOOS == "windows" && exists {
			mode = original.Mode
		}
		file := domain.ArtifactFile{Path: path, Mode: mode, Deleted: deleted}
		if !deleted {
			var objectHash hash.Hash = sha1.New()
			if len(repo.BaseSHA) == 64 {
				objectHash = sha256.New()
			}
			_, _ = fmt.Fprintf(objectHash, "blob %d\x00", len(data))
			_, _ = objectHash.Write(data)
			file.BlobSHA = hex.EncodeToString(objectHash.Sum(nil))
		}
		encoded, _ := json.Marshal(file)
		_, _ = workspaceHash.Write(encoded)
		_, _ = io.WriteString(workspaceHash, "\n")
		if deliveryAllowed(path, repo.AllowedPaths) && (exists && deleted || !deleted && (!exists || file.BlobSHA != original.BlobSHA || file.Mode != original.Mode)) {
			// 范围外内容只参与失效摘要，不能把用户无关或敏感的 dirty 文件写入 Git 对象库。
			if !deleted {
				blob, err := deliveryGit(ctx, repo.WorktreePath, "", data, "hash-object", "-w", "--stdin", "--no-filters")
				if err != nil {
					return "", nil, err
				}
				if strings.TrimSpace(string(blob)) != file.BlobSHA {
					return "", nil, domain.ErrArtifactStale
				}
			}
			files = append(files, file)
		}
	}
	return hex.EncodeToString(workspaceHash.Sum(nil)), files, nil
}
