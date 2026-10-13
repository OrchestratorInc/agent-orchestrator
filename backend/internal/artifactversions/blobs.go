package artifactversions

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// incomingPrefix names a blob being written. A file with this prefix left in
// the blobs directory is the remainder of an interrupted write.
const incomingPrefix = "incoming-"

var errTooLarge = errors.New("artifact file exceeds the version size limit")

// blobsDir is the blob store's directory, relative to the artifact directory.
var blobsDir = filepath.Join(sessionartifacts.VersionsDir, "blobs")

// blobStore holds a session's snapshots as immutable files named by the
// sha256 of their content, at .versions/blobs/<sha[0:2]>/<sha> inside the
// artifact directory. Every operation goes through an os.Root at the artifact
// directory, so ".." and symlinks cannot redirect it outside.
type blobStore struct {
	root *os.Root
}

// stagedBlob is content written to a temporary file and hashed, not yet
// placed in the store.
type stagedBlob struct {
	tmp  string
	sum  string
	size int64
}

func blobPath(sum string) string {
	return filepath.Join(blobsDir, sum[:2], sum)
}

// stage copies src into a temporary file in the store while hashing it, so
// the stored bytes are exactly the hashed ones. Content larger than limit is
// discarded and reported as errTooLarge.
func (b blobStore) stage(src io.Reader, limit int64) (stagedBlob, error) {
	if err := b.root.MkdirAll(blobsDir, 0o755); err != nil {
		return stagedBlob{}, err
	}
	tmp, f, err := b.createIncoming()
	if err != nil {
		return stagedBlob{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(h, f), io.LimitReader(src, limit+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > limit {
		err = errTooLarge
	}
	if err != nil {
		_ = b.root.Remove(tmp)
		return stagedBlob{}, err
	}
	return stagedBlob{tmp: tmp, sum: hex.EncodeToString(h.Sum(nil)), size: n}, nil
}

func (b blobStore) createIncoming() (string, *os.File, error) {
	for range 3 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", nil, err
		}
		name := filepath.Join(blobsDir, incomingPrefix+hex.EncodeToString(suffix[:]))
		f, err := b.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return name, f, err
	}
	return "", nil, fs.ErrExist
}

// commit moves a staged blob into place. When the store already holds that
// content the staged copy is dropped instead.
func (b blobStore) commit(s stagedBlob) error {
	target := blobPath(s.sum)
	if _, err := b.root.Stat(target); err == nil {
		return b.root.Remove(s.tmp)
	}
	if err := b.root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		_ = b.root.Remove(s.tmp)
		return err
	}
	if err := b.root.Rename(s.tmp, target); err != nil {
		_ = b.root.Remove(s.tmp)
		return err
	}
	return nil
}

func (b blobStore) discard(s stagedBlob) {
	_ = b.root.Remove(s.tmp)
}

// remove deletes the blob named by sum. A blob that is already gone is not an
// error.
func (b blobStore) remove(sum string) error {
	if err := b.root.Remove(blobPath(sum)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// sweep removes temporary files left by interrupted writes.
func (b blobStore) sweep() error {
	dir, err := b.root.Open(blobsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), incomingPrefix) {
			if err := b.root.Remove(filepath.Join(blobsDir, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
