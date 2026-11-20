package sessionimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/devin"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func discoverDevin(ctx context.Context, root string) ([]sourceFiles, error) {
	items, err := devin.ListImportSessions(ctx, root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]sourceFiles, 0, len(items))
	for _, a := range items {
		out = append(out, sourceFiles{harness: domain.HarnessDevin, root: root, key: a.ID, paths: []string{a.ID, a.Fingerprint}, modTime: a.Updated})
	}
	return out, nil
}

func readDevinTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessDevin, root: root, paths: paths}
	if len(paths) != 2 {
		return t, errors.New("invalid devin native selection")
	}
	a, err := devin.ReadImportArchive(ctx, root, paths[0])
	if err != nil {
		return t, err
	}
	if a.Fingerprint != paths[1] {
		return t, errors.New("devin native history changed; scan again")
	}
	t.nativeID, t.cwd, t.title, t.last = a.ID, a.CWD, a.Title, a.Updated
	t.path = filepath.Join(root, "cli", "sessions.db")
	t.messages = a.Messages
	t.fingerprint = a.Fingerprint
	t.shortened = a.Shortened
	if t.title == "" && len(a.Messages) > 0 {
		t.title = importTitle(a.Messages[0].Text)
	}
	return t, nil
}
