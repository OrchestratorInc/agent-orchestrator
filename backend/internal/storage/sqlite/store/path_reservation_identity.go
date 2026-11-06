package store

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reservationRepoIdentity 直接解析 Git 元数据，共享 common-dir 的不同 Project/Worktree 必须相互排斥。
func reservationRepoIdentity(repo string) (string, error) {
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		p, err := canonicalProjectPath(repo)
		return "path:" + p, err
	} else if err != nil {
		return "", err
	}
	gitDir := filepath.Join(repo, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return "", domain.ErrArtifactInvalid
	}
	if !info.IsDir() {
		pointer, err := readGitPathPointer(gitDir)
		if err != nil || !strings.HasPrefix(pointer, "gitdir: ") {
			return "", domain.ErrArtifactInvalid
		}
		gitDir = strings.TrimPrefix(pointer, "gitdir: ")
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(repo, gitDir)
		}
	}
	gitDir, err = filepath.EvalSymlinks(gitDir)
	if err != nil {
		return "", err
	}
	common, err := readGitPathPointer(filepath.Join(gitDir, "commondir"))
	if err == nil {
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		gitDir = common
	} else if !os.IsNotExist(err) {
		return "", err
	}
	info, err = os.Stat(gitDir)
	if err != nil || !info.IsDir() {
		return "", domain.ErrArtifactInvalid
	}
	p, err := canonicalProjectPath(gitDir)
	return "git:" + p, err
}

// readGitPathPointer 限制元数据读取大小，不执行 Git 配置或任何外部命令。
func readGitPathPointer(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(data) > 4096 || value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", domain.ErrArtifactInvalid
	}
	return value, nil
}
