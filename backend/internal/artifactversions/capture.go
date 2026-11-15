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

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

const (
	MaxVersionsPerFile = 50
	MaxFileBytes       = attachmentstore.MaxFileBytes
	MaxBytesPerSession = 64 << 20
)

var ErrUnsettled = errors.New("artifact file changed during capture")

type versionStore interface {
	ListLatestSessionArtifactVersions(ctx context.Context, id domain.SessionID) ([]domain.SessionArtifactVersion, error)
	AppendSessionArtifactVersion(ctx context.Context, v domain.SessionArtifactVersion, keep int) (domain.SessionArtifactVersion, []string, error)
	ListSessionArtifactVersions(ctx context.Context, id domain.SessionID, path string) ([]domain.SessionArtifactVersion, error)
	CountSessionArtifactVersionsBySHA(ctx context.Context, id domain.SessionID, sha string) (int64, error)
	DeleteSessionArtifactVersions(ctx context.Context, id domain.SessionID) error
	DeleteSessionArtifactPathVersions(ctx context.Context, id domain.SessionID, path string) ([]string, error)
	ListSessionArtifacts(ctx context.Context, id domain.SessionID) ([]domain.SessionArtifactRecord, error)
	ApplySessionArtifactChanges(ctx context.Context, id domain.SessionID, changes domain.SessionArtifactChanges) ([]string, error)
}

type writer struct {
	source       domain.ArtifactVersionSource
	harness      domain.AgentHarness
	restoredFrom int64
}

type observation struct {
	size    int64
	mtime   int64
	matched bool
}

type Capturer struct {
	store   versionStore
	dataDir string
	logger  *slog.Logger
	now     func() time.Time

	mu       sync.Mutex
	trackMu  sync.Mutex
	freed    map[domain.SessionID][]string
	observed map[domain.SessionID]map[string]observation
	swept    map[domain.SessionID]bool
}

func New(store versionStore, dataDir string, logger *slog.Logger) *Capturer {
	return &Capturer{
		store:    store,
		dataDir:  dataDir,
		logger:   cmp.Or(logger, slog.Default()),
		now:      time.Now,
		observed: make(map[domain.SessionID]map[string]observation),
		swept:    make(map[domain.SessionID]bool),
		freed:    make(map[domain.SessionID][]string),
	}
}

func (c *Capturer) CaptureSession(ctx context.Context, sess domain.SessionRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID))
	rows, err := c.store.ListSessionArtifacts(ctx, sess.ID)
	if err != nil {
		return err
	}
	files, err := c.track(ctx, sess, rows)
	if err != nil {
		c.logger.Debug("artifact versions: track failed", "session", sess.ID, "err", err)
		if files, err = sessionartifacts.List(ctx, dir); err != nil {
			return err
		}
	}
	c.removeFreed(ctx, sess)
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

func (c *Capturer) Track(ctx context.Context, sess domain.SessionRecord, rows []domain.SessionArtifactRecord) ([]domain.SessionArtifactFile, error) {
	files, err := c.track(ctx, sess, rows)
	if c.mu.TryLock() {
		c.removeFreed(ctx, sess)
		c.mu.Unlock()
	}
	return files, err
}

func (c *Capturer) removeFreed(ctx context.Context, sess domain.SessionRecord) {
	c.trackMu.Lock()
	sums := c.freed[sess.ID]
	delete(c.freed, sess.ID)
	c.trackMu.Unlock()
	if len(sums) == 0 {
		return
	}
	root, err := os.OpenRoot(cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID)))
	if err != nil {
		return
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
}

func (c *Capturer) track(ctx context.Context, sess domain.SessionRecord, rows []domain.SessionArtifactRecord) ([]domain.SessionArtifactFile, error) {
	c.trackMu.Lock()
	defer c.trackMu.Unlock()
	dir := cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID))
	observation, err := sessionartifacts.Observe(ctx, dir, sessionartifacts.RegisteredPaths(rows))
	if err != nil {
		return nil, err
	}
	changes := observation.Changes(rows, c.now().UTC(), uuid.NewString)
	if changes.Empty() {
		return observation.Apply(rows), nil
	}
	freed, err := c.store.ApplySessionArtifactChanges(ctx, sess.ID, changes)
	if err != nil {
		if errors.Is(err, domain.ErrSessionArtifactsChanged) {
			err = nil
		}
		return observation.Apply(rows), err
	}
	if updated, err := c.store.ListSessionArtifacts(ctx, sess.ID); err == nil {
		rows = updated
	}
	if len(freed) > 0 {
		c.freed[sess.ID] = append(c.freed[sess.ID], freed...)
	}
	return observation.Apply(rows), nil
}

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
	c.trackMu.Lock()
	defer c.trackMu.Unlock()
	if now, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(relPath))); err != nil || !os.SameFile(now, info) {
		c.removeUnreferenced(ctx, id, blobs, staged.sum)
		return "", false, ErrUnsettled
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

func (c *Capturer) RecordSave(ctx context.Context, sess domain.SessionRecord, relPath string) error {
	_, err := c.recordNow(ctx, sess, relPath, writer{source: domain.ArtifactVersionSourceUser, harness: sess.Harness}, 0)
	return err
}

func (c *Capturer) RecordRestore(ctx context.Context, sess domain.SessionRecord, relPath string, from int64) error {
	_, err := c.recordNow(ctx, sess, relPath, writer{source: domain.ArtifactVersionSourceUser, harness: sess.Harness, restoredFrom: from}, 0)
	return err
}

const SettleWindow = 2 * time.Second

func (c *Capturer) Sync(ctx context.Context, sess domain.SessionRecord, relPath string) error {
	_, err := c.recordNow(ctx, sess, relPath, writer{source: domain.ArtifactVersionSourceAgent, harness: sess.Harness}, SettleWindow)
	return err
}

func (c *Capturer) Snapshot(ctx context.Context, sess domain.SessionRecord, relPath string) (string, error) {
	return c.recordNow(ctx, sess, relPath, writer{source: domain.ArtifactVersionSourceAgent, harness: sess.Harness}, 0)
}

func (c *Capturer) SnapshotSession(ctx context.Context, sess domain.SessionRecord) error {
	dir := cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID))
	files, err := sessionartifacts.List(ctx, dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range files {
		if _, err := c.Snapshot(ctx, sess, f.Path); err != nil && !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrUnsettled) {
			errs = append(errs, fmt.Errorf("%s: %w", f.Path, err))
		}
	}
	return errors.Join(errs...)
}

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

func (c *Capturer) List(ctx context.Context, id domain.SessionID, relPath string) ([]domain.SessionArtifactVersion, error) {
	return c.store.ListSessionArtifactVersions(ctx, id, relPath)
}

func (c *Capturer) ReadVersion(sess domain.SessionRecord, v domain.SessionArtifactVersion) ([]byte, error) {
	dir := cmp.Or(sess.Metadata.ArtifactDir, sessionartifacts.Dir(c.dataDir, sess.ID))
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return blobStore{root: root}.read(v.SHA256, MaxFileBytes)
}
