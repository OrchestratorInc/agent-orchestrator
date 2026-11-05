package fsbrowser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// TestListDirectorySymlinks 保留目录链、外部目录、文件目标、悬空与循环
// 链接的过滤边界；Windows 使用真实 junction，目标内容必须保持不变。
func TestListDirectorySymlinks(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "normal", "child"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(external, ".git"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".worktree"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".worktree", ".git"), nil, 0o600))
	const fileContent = "file target stays unchanged"
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte(fileContent), 0o600))
	symlink(t, "normal", filepath.Join(root, "alias"))
	symlink(t, "alias", filepath.Join(root, "chain"))
	symlink(t, external, filepath.Join(root, "external"))
	symlink(t, ".worktree", filepath.Join(root, "worktree"))
	symlink(t, "normal", filepath.Join(root, ".hidden"))
	symlink(t, "file", filepath.Join(root, "file-alias"))
	symlink(t, "missing", filepath.Join(root, "broken"))
	symlink(t, "loop", filepath.Join(root, "loop"))

	listing, err := New().List(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, Listing{
		Path: root, Parent: filepath.Dir(root),
		Entries: []Entry{
			{Name: "alias", Path: filepath.Join(root, "alias")},
			{Name: "chain", Path: filepath.Join(root, "chain")},
			{Name: "external", Path: filepath.Join(root, "external"), GitRepo: true},
			{Name: "normal", Path: filepath.Join(root, "normal")},
			{Name: "worktree", Path: filepath.Join(root, "worktree"), GitRepo: true},
		},
	}, listing)

	for _, name := range []string{"alias", "chain"} {
		t.Run(name, func(t *testing.T) {
			alias := filepath.Join(root, name)
			got, err := New().List(context.Background(), alias)
			require.NoError(t, err)
			require.Equal(t, Listing{
				Path: alias, Parent: root,
				Entries: []Entry{{Name: "child", Path: filepath.Join(alias, "child")}},
			}, got)
		})
	}
	data, err := os.ReadFile(filepath.Join(root, "file"))
	require.NoError(t, err)
	require.Equal(t, fileContent, string(data))
}

// TestListSymlinkEntryLimit 验证隐藏、悬空及文件链接不占目录条数，只有第
// 501 个有效目录触发截断；重复浏览不能改写链接目标中的内容。
func TestListSymlinkEntryLimit(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	const targetContent = "directory target stays unchanged"
	require.NoError(t, os.WriteFile(filepath.Join(target, "keep.txt"), []byte(targetContent), 0o600))
	for i := range maxEntries {
		symlink(t, target, filepath.Join(root, fmt.Sprintf("dir-%04d", i)))
	}
	symlink(t, target, filepath.Join(root, ".hidden"))
	symlink(t, "missing", filepath.Join(root, "z-broken"))
	require.NoError(t, os.WriteFile(filepath.Join(root, "z-file"), nil, 0o600))
	symlink(t, "z-file", filepath.Join(root, "z-file-alias"))
	listing, err := New().List(context.Background(), root)
	require.NoError(t, err)
	require.Len(t, listing.Entries, maxEntries)
	require.False(t, listing.Truncated)
	for i, entry := range listing.Entries {
		name := fmt.Sprintf("dir-%04d", i)
		require.Equal(t, Entry{Name: name, Path: filepath.Join(root, name)}, entry)
	}
	symlink(t, target, filepath.Join(root, "z-overflow"))
	truncated, err := New().List(context.Background(), root)
	require.NoError(t, err)
	require.True(t, truncated.Truncated)
	require.Equal(t, listing.Entries, truncated.Entries)
	data, err := os.ReadFile(filepath.Join(target, "keep.txt"))
	require.NoError(t, err)
	require.Equal(t, targetContent, string(data))
}

func TestListSymlinkPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce Unix directory mode bits")
	}
	root := t.TempDir()
	blocked := t.TempDir()
	target := filepath.Join(blocked, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(root, "visible"), 0o700))
	alias := filepath.Join(root, "alias")
	symlink(t, target, alias)
	require.NoError(t, os.Chmod(blocked, 0))
	t.Cleanup(func() { require.NoError(t, os.Chmod(blocked, 0o700)) })
	_, err := os.Stat(target)
	if err == nil {
		t.Skip("process can traverse directories without permission bits")
	}
	require.ErrorIs(t, err, os.ErrPermission)
	listing, err := New().List(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []Entry{{Name: "visible", Path: filepath.Join(root, "visible")}}, listing.Entries)
	_, err = New().List(context.Background(), alias)
	var apiErr *apierr.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, apierr.KindForbidden, apiErr.Kind)
	require.Equal(t, "FS_FORBIDDEN", apiErr.Code)
}

// TestListDistinguishesMissingDirectoriesFromNonDirectoryPaths 验证不存在的
// 目录返回 404，而普通文件及以文件为父组件的路径返回非目录错误。
func TestListDistinguishesMissingDirectoriesFromNonDirectoryPaths(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	for _, test := range []struct {
		name string
		path string
		kind apierr.Kind
		code string
	}{
		{name: "missing directory", path: filepath.Join(root, "missing"), kind: apierr.KindNotFound, code: "FS_NOT_FOUND"},
		{name: "missing parent", path: filepath.Join(root, "missing", "child"), kind: apierr.KindNotFound, code: "FS_NOT_FOUND"},
		{name: "file", path: file, kind: apierr.KindInvalid, code: "FS_NOT_A_DIRECTORY"},
		{name: "file parent", path: filepath.Join(file, "child"), kind: apierr.KindInvalid, code: "FS_NOT_A_DIRECTORY"},
		{name: "file ancestor", path: filepath.Join(file, "missing", "child"), kind: apierr.KindInvalid, code: "FS_NOT_A_DIRECTORY"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := New().List(context.Background(), test.path)
			var apiErr *apierr.Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, test.kind, apiErr.Kind)
			require.Equal(t, test.code, apiErr.Code)
		})
	}
}
