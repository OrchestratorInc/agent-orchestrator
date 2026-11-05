// Package fsbrowser provides read-only directory listings for remote clients.
package fsbrowser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const maxEntries = 500

// Entry is one visible subdirectory.
type Entry struct {
	Name    string
	Path    string
	GitRepo bool
}

// Listing is a bounded directory listing on the daemon host.
type Listing struct {
	Path      string
	Parent    string
	Entries   []Entry
	Truncated bool
}

// Service browses the daemon host's filesystem without exposing file contents.
type Service struct{}

// New constructs a directory browser backed by the host filesystem.
func New() *Service { return &Service{} }

// List returns the visible subdirectories of path. An empty path uses the
// daemon user's home directory.
// Windows 的 junction 在当前 Go 中标为 ModeIrregular，只有 Stat 确认目录
// 后才展示；与符号链接共用错误过滤和 maxEntries 上限，其他平台保持原分类。
func (*Service) List(ctx context.Context, path string) (Listing, error) {
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	if path == "" {
		var err error
		path, err = os.UserHomeDir()
		if err != nil {
			return Listing{}, err
		}
	}
	if !filepath.IsAbs(path) {
		return Listing{}, apierr.Invalid("FS_PATH_NOT_ABSOLUTE", "path must be absolute on the daemon host", nil)
	}
	path = filepath.Clean(path)

	entries, err := os.ReadDir(path)
	if err != nil {
		// Windows 把普通文件及其子路径也报告为不存在。最近的现存组件
		// 若不是目录，必须保留非目录错误，而真正缺失的目录仍返回 404。
		if errors.Is(err, os.ErrNotExist) {
			for current := path; ; {
				info, statErr := os.Stat(current)
				if statErr == nil {
					if !info.IsDir() {
						return Listing{}, apierr.Invalid("FS_NOT_A_DIRECTORY", "not a directory", nil)
					}
					break
				}
				parent := filepath.Dir(current)
				if !errors.Is(statErr, os.ErrNotExist) || parent == current {
					break
				}
				current = parent
			}
		}
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Listing{}, apierr.NotFound("FS_NOT_FOUND", "no such directory on the daemon host")
		case errors.Is(err, os.ErrPermission):
			return Listing{}, apierr.Forbidden("FS_FORBIDDEN", "the daemon may not read that directory")
		default:
			return Listing{}, apierr.Invalid("FS_NOT_A_DIRECTORY", "not a directory", nil)
		}
	}

	out := Listing{Path: path, Parent: filepath.Dir(path), Entries: []Entry{}}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child := filepath.Join(path, entry.Name())
		if !entry.IsDir() {
			isLink := entry.Type()&os.ModeSymlink != 0 || runtime.GOOS == "windows" && entry.Type()&os.ModeIrregular != 0
			if !isLink {
				continue
			}
			target, statErr := os.Stat(child)
			if statErr != nil || !target.IsDir() {
				continue
			}
		}
		if len(out.Entries) == maxEntries {
			out.Truncated = true
			break
		}
		// .git may be a directory (normal clone) or a file (worktree): both are repos.
		_, gitErr := os.Stat(filepath.Join(child, ".git"))
		out.Entries = append(out.Entries, Entry{Name: entry.Name(), Path: child, GitRepo: gitErr == nil})
	}
	return out, nil
}
