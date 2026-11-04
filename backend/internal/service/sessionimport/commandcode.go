package sessionimport

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/commandcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func commandCodeRoot(home string) string { return filepath.Join(home, ".commandcode") }

// Command Code's v3 native history uses Pi's id/parentId message tree. Reuse
// that parser, including its active-branch walk and confined streaming reads.
// Older flat versions are omitted until their native format is supported here.
func readCommandCodeTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessCommandCode, root: canonical(root)}
	if len(paths) != 1 {
		return t, errors.New("no command-code transcript")
	}
	id := strings.TrimSuffix(filepath.Base(paths[0]), ".jsonl")
	if err := commandcode.ValidateNativeImport(ctx, t.root, paths[0], id); err != nil {
		return t, err
	}
	t, err := readPiTranscript(ctx, t.root, paths...)
	t.harness = domain.HarnessCommandCode
	t.title = importTitle(t.title)
	return t, err
}

func discoverCommandCode(ctx context.Context, root string) ([]sourceFiles, error) {
	provider, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = provider.Close() }()
	var files []sourceFiles
	err = fs.WalkDir(provider.FS(), "projects", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil //nolint:nilerr // Skip unreadable entries so other conversations remain discoverable.
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") || !nativeIDPattern.MatchString(id) {
			return nil
		}
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			files = append(files, sourceFiles{harness: domain.HarnessCommandCode, root: root, paths: []string{filepath.Join(root, filepath.FromSlash(path))}, key: id, modTime: info.ModTime(), size: info.Size()})
		}
		return nil
	})
	return files, err
}
