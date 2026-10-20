package artifactversions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// trashDir holds artifact directories moved aside by DeleteSession, relative
// to the data directory. It sits outside "artifacts" so nothing reads an entry
// there as a session.
const trashDir = "artifacts-trash"

// DeleteSession deletes a session's artifact files, version history included,
// and its version rows, and leaves an empty artifact directory behind so a
// restored session starts with one.
//
// It holds the capture lock, so a capture in flight finishes before the delete
// and a later one finds no files: neither can write a snapshot or a version row
// for files that are gone.
//
// The directory is first renamed into the trash, which either moves all of it
// or none of it. A file held open (on Windows, open handles block the rename)
// fails the delete with every file still in place, rather than partway
// through. Removing the trash afterwards is best effort; whatever stays is
// retried on the next delete.
func (c *Capturer) DeleteSession(ctx context.Context, id domain.SessionID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := sessionartifacts.Dir(c.dataDir, id)
	if dir == "" {
		return errors.New("delete artifacts: no data directory")
	}
	trash := filepath.Join(c.dataDir, trashDir)
	c.emptyTrash(trash)
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return fmt.Errorf("delete artifacts %s: %w", id, err)
	}
	moved := filepath.Join(trash, fmt.Sprintf("%s-%d", id, c.now().UnixNano()))
	if err := os.Rename(dir, moved); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete artifacts %s: %w", id, err)
		}
		moved = ""
	}
	delete(c.observed, id)
	delete(c.swept, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("delete artifacts %s: recreate dir: %w", id, err)
	}
	if err := c.store.DeleteSessionArtifactVersions(ctx, id); err != nil {
		return err
	}
	if moved != "" {
		if err := os.RemoveAll(moved); err != nil {
			c.logger.Warn("delete artifacts: trash cleanup failed; retried on the next delete", "session", id, "path", moved, "err", err)
		}
	}
	return nil
}

// emptyTrash removes what earlier deletes could not.
func (c *Capturer) emptyTrash(trash string) {
	entries, err := os.ReadDir(trash)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(trash, entry.Name())); err != nil {
			c.logger.Debug("delete artifacts: trash entry still locked", "path", entry.Name(), "err", err)
		}
	}
}
