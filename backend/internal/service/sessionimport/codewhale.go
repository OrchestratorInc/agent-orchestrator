package sessionimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codewhalestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func codewhaleRoot(home string) string {
	if root := os.Getenv("CODEWHALE_HOME"); root != "" {
		return root
	}
	root := filepath.Join(home, ".codewhale")
	if info, err := os.Stat(filepath.Join(root, "sessions")); err == nil && info.IsDir() {
		return root
	}
	legacy := filepath.Join(home, ".deepseek")
	if info, err := os.Stat(filepath.Join(legacy, "sessions")); err == nil && info.IsDir() {
		return legacy
	}
	return root
}

func readCodewhaleTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessCodewhale, root: canonical(root)}
	if len(paths) != 1 {
		return t, errors.New("no codewhale native snapshot")
	}
	archive, err := codewhalestore.ReadImport(ctx, t.root, paths[0])
	if err != nil {
		return t, err
	}
	t.path, t.paths, t.nativeID, t.cwd = paths[0], paths, archive.ID, archive.CWD
	t.title, t.model, t.last = importTitle(archive.Title), archive.Model, archive.Updated
	t.messages = make([]Message, 0, len(archive.Messages))
	for _, message := range archive.Messages {
		role := domain.MessageRoleAssistant
		if message.Role == "user" {
			role = domain.MessageRoleUser
			if t.title == "" {
				t.title = importTitle(message.Text)
			}
		}
		t.messages = append(t.messages, Message{Role: role, Text: message.Text, At: message.At})
	}
	return t, nil
}

func discoverCodewhale(ctx context.Context, root string) ([]sourceFiles, error) {
	provider, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = provider.Close() }()
	dir, err := provider.Open("sessions")
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return nil, err
	}
	var files []sourceFiles
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, "sessions", entry.Name())
		archive, err := codewhalestore.ReadImport(ctx, root, path)
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, sourceFiles{harness: domain.HarnessCodewhale, root: root, paths: []string{path}, key: archive.ID, modTime: info.ModTime(), size: info.Size()})
	}
	return files, nil
}
