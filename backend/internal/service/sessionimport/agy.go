package sessionimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func agyRoot(home string) string { return envOr("GEMINI_HOME", filepath.Join(home, ".gemini")) }

func discoverAgy(ctx context.Context, root string) ([]sourceFiles, error) {
	root = canonical(root)
	r, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	var out []sourceFiles
	for _, mode := range []string{agy.CLIStorageDir, agy.ACPStorageDir} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir := filepath.Join(mode, "conversations")
		f, err := r.Open(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries, err := f.ReadDir(-1)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id := strings.TrimSuffix(entry.Name(), ".db")
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".db") || !nativeIDPattern.MatchString(id) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			out = append(out, sourceFiles{harness: domain.HarnessAgy, root: root, key: id, paths: []string{filepath.Join(root, dir, entry.Name())}, modTime: info.ModTime(), size: info.Size()})
		}
	}
	return out, nil
}

func readAgyTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessAgy, root: canonical(root), paths: paths}
	if len(paths) != 1 {
		return t, errors.New("agy: invalid native transcript handle")
	}
	a, err := agy.ReadImportArchive(ctx, t.root, paths[0])
	if err != nil {
		return t, err
	}
	t.path, t.nativeID, t.cwd, t.fingerprint = paths[0], a.ID, a.CWD, a.Fingerprint
	t.messages, t.shortened = a.Messages, a.Shortened
	for _, m := range a.Messages {
		if m.At.After(t.last) {
			t.last = m.At
		}
	}
	err = finishTranscript(&t)
	return t, err
}
