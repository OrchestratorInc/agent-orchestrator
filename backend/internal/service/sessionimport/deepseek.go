package sessionimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/deepseekharness"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func deepseekRoot(home string) string {
	root := envOr("DSH_HOME", filepath.Join(home, ".dsh"))
	if absolute, err := filepath.Abs(root); err == nil {
		return absolute
	}
	return root
}

func deepseekEntries(root *os.Root, path string) ([]os.DirEntry, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.ReadDir(-1)
}

func discoverDeepSeek(ctx context.Context, root string) ([]sourceFiles, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = canonical(absolute)
	r, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	projects, err := deepseekEntries(r, "sessions")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []sourceFiles
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !project.IsDir() || project.Type()&os.ModeSymlink != 0 {
			continue
		}
		sessions, err := deepseekEntries(r, filepath.Join("sessions", project.Name()))
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !session.IsDir() || session.Type()&os.ModeSymlink != 0 || !deepseekharness.IsImportSessionID(session.Name()) {
				continue
			}
			for _, name := range []string{"session.v4.jsonl.zstd", "session.v4.jsonl"} {
				path := filepath.Join(root, "sessions", project.Name(), session.Name(), name)
				// Recheck generations even when Scan can reuse a cached preview.
				if err := deepseekharness.CheckImportSource(root, path); err != nil {
					continue
				}
				info, err := os.Stat(path)
				if err != nil {
					continue
				}
				out = append(out, sourceFiles{harness: domain.HarnessDeepSeek, root: root, key: session.Name(), paths: []string{path}, modTime: info.ModTime(), size: info.Size()})
			}
		}
	}
	return out, nil
}

func readDeepSeekTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessDeepSeek}
	if len(paths) != 1 {
		return t, errors.New("deepseek: invalid transcript handle")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return t, err
	}
	t.root = canonical(absolute)
	t.path, err = filepath.Abs(paths[0])
	if err != nil {
		return t, err
	}
	t.paths = []string{t.path}
	a, err := deepseekharness.ReadImportArchive(ctx, t.root, t.path)
	if err != nil {
		return t, err
	}
	t.nativeID, t.cwd, t.fingerprint = a.ID, a.CWD, a.Fingerprint
	t.messages, t.shortened = a.Messages, a.Shortened
	for _, m := range a.Messages {
		if m.At.After(t.last) {
			t.last = m.At
		}
	}
	err = finishTranscript(&t)
	return t, err
}
