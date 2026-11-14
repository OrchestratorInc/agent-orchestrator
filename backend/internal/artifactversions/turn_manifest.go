package artifactversions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// MaxKeptTurns is how many of a session's latest turns keep their artifact
// list and its content. A fork of an older turn falls back to the version
// store.
const MaxKeptTurns = 50

// SnapshotTurn keeps every artifact file of sess as it is now, like
// SnapshotSession, and records the list under each of turnIDs: the turns
// about to start, whose artifacts these are before they run. A file too large
// to version is left out of the list.
func (c *Capturer) SnapshotTurn(ctx context.Context, sess domain.SessionRecord, turnIDs []string) error {
	dir := cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID))
	files, err := sessionartifacts.List(ctx, dir)
	if err != nil {
		return err
	}
	latest := map[string]string{}
	if versions, err := c.store.ListLatestSessionArtifactVersions(ctx, sess.ID); err == nil {
		for _, v := range versions {
			latest[v.Path] = v.SHA256
		}
	}
	manifest := map[string]string{}
	var errs []error
	for _, f := range files {
		sum, err := c.Snapshot(ctx, sess, f.Path)
		switch {
		case err == nil && sum != "":
			manifest[f.Path] = sum
		case errors.Is(err, ErrUnsettled) && latest[f.Path] != "":
			// Being written right now: the turn starts with its last version.
			manifest[f.Path] = latest[f.Path]
		case err != nil && !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrUnsettled):
			errs = append(errs, fmt.Errorf("%s: %w", f.Path, err))
		}
	}
	// Under mu, so no blob is freed while its kept copy is made, and no kept
	// copy is pruned while another turn's list is being written to name it.
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, sum := range manifest {
		if err := keepBlob(dir, sum); err != nil {
			delete(manifest, path)
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("keep %s: %w", path, err))
			}
		}
	}
	keptAt := c.now()
	for _, turnID := range turnIDs {
		if err := sessionartifacts.WriteTurnArtifacts(dir, turnID, manifest, keptAt); err != nil {
			errs = append(errs, err)
		}
	}
	if err := sessionartifacts.PruneTurnArtifacts(dir, MaxKeptTurns); err != nil {
		errs = append(errs, fmt.Errorf("prune kept turns: %w", err))
	}
	return errors.Join(errs...)
}

// keepBlob links the blob with sum into keptDir, copying it where links
// aren't supported. Blobs are never changed once written, so sharing one is
// safe.
func keepBlob(dir, sum string) error {
	if len(sum) < 2 {
		return fmt.Errorf("bad content hash %q", sum)
	}
	target := sessionartifacts.KeptBlobPath(dir, sum)
	if _, err := os.Lstat(target); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	source := filepath.Join(dir, blobPath(sum))
	if err := os.Link(source, target); err == nil || errors.Is(err, fs.ErrExist) {
		return nil
	} else if errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return copyKept(source, target)
}

func copyKept(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".kept-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
