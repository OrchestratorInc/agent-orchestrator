// Package artifactversions keeps the version history of session artifact
// files. Once a file has settled on new content, the Capturer copies it into a
// content-addressed blob store inside the session's artifact directory and
// records it as the file's next version.
package artifactversions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

const (
	// MaxVersionsPerFile is how many versions are kept per artifact path; the
	// oldest are removed beyond it.
	MaxVersionsPerFile = 50
	// MaxFileBytes is the largest file that is versioned.
	MaxFileBytes = attachmentstore.MaxFileBytes
	// MaxBytesPerSession bounds how much one CaptureSession call copies. A
	// file over the budget waits for the next call; one file is always
	// allowed, so a file up to MaxFileBytes still gets captured.
	MaxBytesPerSession = 64 << 20
)

// ErrUnsettled reports a file that changed while it was being captured.
var ErrUnsettled = errors.New("artifact file changed during capture")

type versionStore interface {
	ListLatestSessionArtifactVersions(ctx context.Context, id domain.SessionID) ([]domain.SessionArtifactVersion, error)
	AppendSessionArtifactVersion(ctx context.Context, v domain.SessionArtifactVersion, keep int) (domain.SessionArtifactVersion, []string, error)
	ListSessionArtifactVersions(ctx context.Context, id domain.SessionID, path string) ([]domain.SessionArtifactVersion, error)
	CountSessionArtifactVersionsBySHA(ctx context.Context, id domain.SessionID, sha string) (int64, error)
	DeleteSessionArtifactVersions(ctx context.Context, id domain.SessionID) error
}

// writer says who a captured version is attributed to.
type writer struct {
	source domain.ArtifactVersionSource
	// harness is the agent running the session when the version was recorded,
	// not necessarily the one that wrote it: an edit made before an agent switch
	// but recorded after it carries the new agent. It is a best-effort label.
	harness domain.AgentHarness
	// restoredFrom is the version a restore wrote back; zero otherwise.
	restoredFrom int64
}

// observation is a file's size and modification time as seen on one call.
type observation struct {
	size  int64
	mtime int64
	// matched marks an observation whose content was hashed and equals the
	// latest version, so a file touched without a content change is read once.
	matched bool
}

// Capturer snapshots each session's artifact files. A file is captured when
// its size and modification time differ from its latest version and have
// stayed the same across two consecutive calls, and its content hash differs
// from the latest version's.
type Capturer struct {
	store   versionStore
	dataDir string
	logger  *slog.Logger
	now     func() time.Time

	mu sync.Mutex
	// observed holds, per session and path, the last observation of each file
	// that is not yet known to equal its latest version.
	observed map[domain.SessionID]map[string]observation
	// swept records the sessions whose interrupted blob writes were removed.
	swept map[domain.SessionID]bool
}

// New builds a Capturer. dataDir resolves the artifact directory of a session
// stored without one.
func New(store versionStore, dataDir string, logger *slog.Logger) *Capturer {
	return &Capturer{
		store:    store,
		dataDir:  dataDir,
		logger:   cmp.Or(logger, slog.Default()),
		now:      time.Now,
		observed: make(map[domain.SessionID]map[string]observation),
		swept:    make(map[domain.SessionID]bool),
	}
}

// CaptureSession records a new version of each artifact file of sess that
// has settled on new content. A file that fails to capture is logged and
// retried on a later call; it does not stop the others.
func (c *Capturer) CaptureSession(ctx context.Context, sess domain.SessionRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID))
	files, err := sessionartifacts.List(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		delete(c.observed, sess.ID)
		return nil
	}
	latestRows, err := c.store.ListLatestSessionArtifactVersions(ctx, sess.ID)
	if err != nil {
		return err
	}
	latest := make(map[string]domain.SessionArtifactVersion, len(latestRows))
	for _, v := range latestRows {
		latest[v.Path] = v
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	blobs := blobStore{root: root}
	if !c.swept[sess.ID] {
		if err := blobs.sweep(); err != nil {
			c.logger.Debug("artifact versions: sweep failed", "session", sess.ID, "err", err)
		}
		c.swept[sess.ID] = true
	}

	prev := c.observed[sess.ID]
	next := make(map[string]observation)
	var spent int64
	for _, f := range files {
		obs := observation{size: f.Size, mtime: f.UpdatedAt.UnixNano()}
		last, hasLast := latest[f.Path]
		if hasLast && last.Size == obs.size && last.ModTime.UnixNano() == obs.mtime {
			continue
		}
		p, seen := prev[f.Path]
		switch {
		case !seen || p.size != obs.size || p.mtime != obs.mtime:
			// Changed since the previous call: the agent may still be writing.
			next[f.Path] = obs
			continue
		case p.matched || obs.size > MaxFileBytes:
			next[f.Path] = p
			continue
		case spent > 0 && spent+obs.size > MaxBytesPerSession:
			next[f.Path] = obs
			continue
		}
		spent += obs.size
		_, captured, err := c.capture(ctx, sess.ID, dir, blobs, f.Path, obs, last, hasLast, writer{source: domain.ArtifactVersionSourceAgent, harness: sess.Harness})
		switch {
		case err != nil:
			if !errors.Is(err, ErrUnsettled) {
				c.logger.Debug("artifact versions: capture failed", "session", sess.ID, "path", f.Path, "err", err)
			}
			next[f.Path] = obs
		case !captured:
			obs.matched = true
			next[f.Path] = obs
		}
	}
	if len(next) == 0 {
		delete(c.observed, sess.ID)
	} else {
		c.observed[sess.ID] = next
	}
	return nil
}

// capture snapshots one file and returns the sha256 of the content it read. It
// reports false when the content equals the latest version, which records
// nothing.
func (c *Capturer) capture(ctx context.Context, id domain.SessionID, dir string, blobs blobStore, relPath string, obs observation, last domain.SessionArtifactVersion, hasLast bool, w writer) (string, bool, error) {
	file, info, _, err := previewutil.OpenWorkspaceFile(dir, relPath)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = file.Close() }()
	if info.Size() != obs.size || info.ModTime().UnixNano() != obs.mtime {
		return "", false, ErrUnsettled
	}
	staged, err := blobs.stage(file, MaxFileBytes)
	if err != nil {
		return "", false, err
	}
	if after, err := file.Stat(); err != nil || after.Size() != obs.size || after.ModTime().UnixNano() != obs.mtime {
		blobs.discard(staged)
		return "", false, ErrUnsettled
	}
	if hasLast && last.SHA256 == staged.sum {
		blobs.discard(staged)
		return staged.sum, false, nil
	}
	if err := blobs.commit(staged); err != nil {
		return "", false, err
	}
	_, pruned, err := c.store.AppendSessionArtifactVersion(ctx, domain.SessionArtifactVersion{
		SessionID:    id,
		Path:         relPath,
		SHA256:       staged.sum,
		Size:         staged.size,
		ContentType:  contentType(relPath),
		ModTime:      info.ModTime(),
		CreatedAt:    c.now().UTC(),
		Source:       w.source,
		Harness:      w.harness,
		RestoredFrom: w.restoredFrom,
	}, MaxVersionsPerFile)
	if err != nil {
		c.removeUnreferenced(ctx, id, blobs, staged.sum)
		return "", false, fmt.Errorf("record version: %w", err)
	}
	for _, sum := range pruned {
		c.removeUnreferenced(ctx, id, blobs, sum)
	}
	return staged.sum, true, nil
}

// removeUnreferenced deletes the blob named by sum when no version row of the
// session refers to it.
func (c *Capturer) removeUnreferenced(ctx context.Context, id domain.SessionID, blobs blobStore, sum string) {
	n, err := c.store.CountSessionArtifactVersionsBySHA(ctx, id, sum)
	if err == nil && n == 0 {
		err = blobs.remove(sum)
	}
	if err != nil {
		c.logger.Debug("artifact versions: blob cleanup failed", "session", id, "sha256", sum, "err", err)
	}
}

func contentType(relPath string) string {
	return cmp.Or(mime.TypeByExtension(strings.ToLower(path.Ext(relPath))), "application/octet-stream")
}

// RecordSave records the current content of one artifact file as a version
// written by the user, right after the app saved or restored it. Content equal
// to the latest version records nothing. It holds the capture lock, so it
// cannot interleave with a capture of the same file.
func (c *Capturer) RecordSave(ctx context.Context, sess domain.SessionRecord, relPath string) error {
	_, err := c.recordNow(ctx, sess, relPath, writer{source: domain.ArtifactVersionSourceUser, harness: sess.Harness}, 0)
	return err
}

// RecordRestore records the current content of one artifact file as a user
// version that restored version from, so the history can say where it came
// from. Otherwise it behaves like RecordSave.
func (c *Capturer) RecordRestore(ctx context.Context, sess domain.SessionRecord, relPath string, from int64) error {
	_, err := c.recordNow(ctx, sess, relPath, writer{source: domain.ArtifactVersionSourceUser, harness: sess.Harness, restoredFrom: from}, 0)
	return err
}

// SettleWindow is how long a file must have gone unmodified before Sync
// snapshots it, so a file the agent is still writing is not captured half done.
const SettleWindow = 2 * time.Second

// Sync records the current content of one artifact file as an agent version
// when it differs from the latest version and has not been modified for
// SettleWindow. The observer needs two ticks to notice a change; Sync lets a
// reader of the history see an edit as soon as it settles.
func (c *Capturer) Sync(ctx context.Context, sess domain.SessionRecord, relPath string) error {
	_, err := c.recordNow(ctx, sess, relPath, writer{source: domain.ArtifactVersionSourceAgent, harness: sess.Harness}, SettleWindow)
	return err
}

// Snapshot makes sure the current content of one artifact file is kept as a
// version, recording it as an agent version when it differs from the latest,
// with no settle window, and returns its sha256. A restore calls it just before
// overwriting the file and writes only while the file still hashes to that
// sum, so the content it replaces is always kept. A file that changes during
// the copy fails with ErrUnsettled, and one over MaxFileBytes with ErrTooLarge.
func (c *Capturer) Snapshot(ctx context.Context, sess domain.SessionRecord, relPath string) (string, error) {
	return c.recordNow(ctx, sess, relPath, writer{source: domain.ArtifactVersionSourceAgent, harness: sess.Harness}, 0)
}

// recordNow snapshots one file under the capture lock and returns the sha256
// of the content now kept as its latest version. A file modified within settle
// is left for a later call, which returns no sum.
func (c *Capturer) recordNow(ctx context.Context, sess domain.SessionRecord, relPath string, w writer, settle time.Duration) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID))
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	rows, err := c.store.ListSessionArtifactVersions(ctx, sess.ID, relPath)
	if err != nil {
		return "", err
	}
	var last domain.SessionArtifactVersion
	if len(rows) > 0 {
		last = rows[0]
	}
	info, err := root.Stat(filepath.FromSlash(relPath))
	if err != nil {
		return "", err
	}
	if len(rows) > 0 && last.Size == info.Size() && last.ModTime.UnixNano() == info.ModTime().UnixNano() {
		// Unchanged since its latest version: nothing to record, and no need to read it.
		return last.SHA256, nil
	}
	if settle > 0 && c.now().Sub(info.ModTime()) < settle {
		return "", nil
	}
	obs := observation{size: info.Size(), mtime: info.ModTime().UnixNano()}
	sum, _, err := c.capture(ctx, sess.ID, dir, blobStore{root: root}, relPath, obs, last, len(rows) > 0, w)
	if err != nil {
		return "", err
	}
	delete(c.observed[sess.ID], relPath)
	return sum, nil
}

// List returns every kept version of one artifact path, newest first.
func (c *Capturer) List(ctx context.Context, id domain.SessionID, relPath string) ([]domain.SessionArtifactVersion, error) {
	return c.store.ListSessionArtifactVersions(ctx, id, relPath)
}

// ReadVersion returns the content of v from the session's blob store.
func (c *Capturer) ReadVersion(sess domain.SessionRecord, v domain.SessionArtifactVersion) ([]byte, error) {
	dir := cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID))
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return blobStore{root: root}.read(v.SHA256, MaxFileBytes)
}
