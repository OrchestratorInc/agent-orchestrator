package artifactversions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

const trashDir = "artifacts-trash"

func (c *Capturer) DeleteSession(ctx context.Context, id domain.SessionID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trackMu.Lock()
	delete(c.freed, id)
	c.trackMu.Unlock()
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

func (c *Capturer) DeleteVersions(ctx context.Context, sess domain.SessionRecord, relPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.observed[sess.ID], relPath)
	sums, err := c.store.DeleteSessionArtifactPathVersions(ctx, sess.ID, relPath)
	if err != nil || len(sums) == 0 {
		return err
	}
	root, err := os.OpenRoot(cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID)))
	if err != nil {
		return fmt.Errorf("delete artifact versions %s %q: %w", sess.ID, relPath, err)
	}
	defer func() { _ = root.Close() }()
	blobs := blobStore{root: root}
	removed := make(map[string]bool, len(sums))
	for _, sum := range sums {
		if !removed[sum] {
			removed[sum] = true
			c.removeUnreferenced(ctx, sess.ID, blobs, sum)
		}
	}
	return nil
}

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
