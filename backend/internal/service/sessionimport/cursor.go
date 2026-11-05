package sessionimport

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursorstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func cursorRoot(home string) string {
	if override := os.Getenv("CURSOR_DATA_DIR"); override != "" {
		return override
	}
	return filepath.Join(home, ".cursor")
}

// cursorPlaceholderTitle is the name Cursor gives a chat it has not titled.
// Like OpenCode's "New session - …", it is treated as no title so the list
// shows the opening prompt instead of many identical rows.
const cursorPlaceholderTitle = "New Agent"

func readCursorTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessCursor, root: canonical(root)}
	if len(paths) != 1 {
		return t, errors.New("no cursor native database")
	}
	archive, err := cursorstore.Read(ctx, t.root, paths[0])
	if err != nil {
		return t, err
	}
	t.path, t.paths, t.nativeID, t.cwd = paths[0], paths, archive.ID, archive.CWD
	t.title, t.model, t.last = importTitle(archive.Title), archive.Model, archive.Updated
	if t.title == cursorPlaceholderTitle {
		t.title = ""
	}
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

func discoverCursor(ctx context.Context, root string) ([]sourceFiles, error) {
	root = canonical(root)
	var files []sourceFiles
	err := filepath.WalkDir(filepath.Join(root, "chats"), func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil || !entry.Type().IsRegular() || entry.Name() != "store.db" {
			return nil //nolint:nilerr // Missing or unreadable native files are ineligible.
		}
		rel, err := filepath.Rel(filepath.Join(root, "chats"), path)
		if err != nil || !filepath.IsLocal(rel) || filepath.Dir(filepath.Dir(filepath.Dir(rel))) != "." {
			return nil //nolint:nilerr // Only chats/<cwd hash>/<id>/store.db is native state.
		}
		fingerprint, err := cursorstore.Fingerprint(ctx, root, path)
		if err != nil {
			return nil //nolint:nilerr // Missing or unsafe archive inputs are ineligible.
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // A file can disappear during discovery.
		}
		archive, err := cursorstore.Read(ctx, root, path)
		if err != nil {
			return nil //nolint:nilerr // A corrupt conversation must not hide its neighbors.
		}
		// Keep every cwd bucket until Scan applies project eligibility. Otherwise
		// a newer AO copy could hide the eligible original with the same id.
		files = append(files, sourceFiles{harness: domain.HarnessCursor, root: root, paths: []string{path}, key: archive.ID, modTime: info.ModTime(), size: info.Size(), fingerprint: fingerprint})
		return nil
	})
	return files, err
}
