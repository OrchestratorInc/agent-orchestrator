// Package attachmentstore owns the durable copy of files attached to session
// messages. Worktree copies are projections for agents; the canonical bytes
// live under AO's data directory so worktree teardown cannot erase history.
package attachmentstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// WorkspaceDir is the worktree-relative directory named in chat messages.
	WorkspaceDir = ".ao/attachments"
	durableDir   = "attachments"
	// uploadsDir holds files streamed in through Upload until an attachment
	// names them. It sits beside durableDir so attaching one is a hard link.
	uploadsDir = "attachment-uploads"
	// MaxFileBytes bounds the files the daemon reads whole into memory: renders,
	// and legacy worktree files imported without an AO-written canonical copy
	// (agent-writable bytes AO never accepted). Message attachments are streamed
	// to and from disk and have no size cap.
	MaxFileBytes = 50 << 20
	// UploadTTL is how long an upload waits to be attached before a later
	// upload sweeps it. An attached upload is hard-linked into canonical
	// storage, so sweeping it frees nothing the session still uses.
	UploadTTL = 24 * time.Hour
	// noLimit leaves a write or open without a size cap.
	noLimit = 0
)

var (
	// ErrExists reports that the generated name is already owned by history.
	ErrExists = errors.New("attachment already exists")
	// ErrEmpty reports an attachment or upload with no bytes.
	ErrEmpty = errors.New("attachment is empty")
	// ErrUploadNotFound reports an upload id that names no pending upload: it
	// was never made here, or it waited past UploadTTL and was swept.
	ErrUploadNotFound = errors.New("attachment upload not found")
	errTooLarge       = errors.New("attachment is too large")
)

// Source is one attachment's bytes: Data held in memory (small files sent
// inline as base64), or Upload naming a file streamed in earlier through
// Upload. Exactly one is set.
type Source struct {
	Data   []byte
	Upload string
}

// Store persists canonical attachment bytes beneath an AO data directory.
type Store struct {
	dataDir string
	// ponytail: fixed stripes bound memory; use keyed locks if unrelated uploads contend.
	projectionLocks [64]sync.Mutex
}

func (s *Store) projectionLock(id domain.SessionID) *sync.Mutex {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return &s.projectionLocks[h.Sum64()%uint64(len(s.projectionLocks))]
}

// New returns a store rooted at dataDir.
func New(dataDir string) *Store {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = ""
	}
	return &Store{dataDir: dataDir}
}

// Put writes the canonical copy before projecting it into the live worktree.
// Returning success therefore means both the history copy and the agent-visible
// copy exist.
func (s *Store) Put(ctx context.Context, id domain.SessionID, workspacePath, name string, data []byte) error {
	return s.PutSource(ctx, id, workspacePath, name, Source{Data: data})
}

// PutSource is Put for bytes held in memory or streamed in through Upload. The
// worktree copy is streamed from the canonical file, so neither copy is ever
// read whole into memory.
func (s *Store) PutSource(ctx context.Context, id domain.SessionID, workspacePath, name string, src Source) error {
	lock := s.projectionLock(id)
	lock.Lock()
	defer lock.Unlock()
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("attachment workspace path is empty")
	}
	if err := s.PutCanonicalSource(ctx, id, name, src); err != nil {
		return err
	}
	sessionRoot, err := s.openCanonicalSession(ctx, id, true)
	if err != nil {
		return fmt.Errorf("open canonical attachment directory: %w", err)
	}
	defer func() { _ = sessionRoot.Close() }()
	rollback := func(cause error) error {
		if removeErr := sessionRoot.Remove(name); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return errors.Join(cause, fmt.Errorf("rollback canonical attachment: %w", removeErr))
		}
		return cause
	}
	canonical, _, err := openRegularFile(ctx, sessionRoot, name, noLimit)
	if err != nil {
		return rollback(fmt.Errorf("open canonical attachment: %w", err))
	}
	defer func() { _ = canonical.Close() }()
	if err := writeReaderAtomicUnder(ctx, workspacePath, filepath.FromSlash(WorkspaceDir), name, canonical, false, noLimit); err != nil {
		return rollback(fmt.Errorf("write workspace attachment: %w", err))
	}
	return nil
}

// PutCanonical stores an attachment for a session that has no worktree yet.
// An asynchronous spawn answers the API before its worktree exists, so a file
// attached to a message typed in that window has nowhere to be written; the
// canonical copy is already the durable source of truth, and
// MaterializeWorkspace replays it once the worktree lands.
func (s *Store) PutCanonical(ctx context.Context, id domain.SessionID, name string, data []byte) error {
	return s.PutCanonicalSource(ctx, id, name, Source{Data: data})
}

// PutCanonicalSource is PutCanonical for bytes held in memory or streamed in
// through Upload. An upload is hard-linked into place rather than copied.
func (s *Store) PutCanonicalSource(ctx context.Context, id domain.SessionID, name string, src Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSessionID(id); err != nil {
		return err
	}
	if err := validateName(name); err != nil {
		return err
	}
	if src.Upload != "" {
		return s.linkUpload(ctx, id, name, src.Upload)
	}
	if len(src.Data) == 0 {
		return ErrEmpty
	}
	return s.writeCanonicalReader(ctx, id, name, bytes.NewReader(src.Data), noLimit)
}

// writeCanonical stores in-memory bytes in a session's canonical directory
// only, up to MaxFileBytes. Callers validate the name first: attachment names
// and render names follow different rules.
func (s *Store) writeCanonical(ctx context.Context, id domain.SessionID, name string, data []byte) error {
	if len(data) == 0 {
		return ErrEmpty
	}
	if len(data) > MaxFileBytes {
		return errTooLarge
	}
	return s.writeCanonicalReader(ctx, id, name, bytes.NewReader(data), MaxFileBytes)
}

func (s *Store) writeCanonicalReader(ctx context.Context, id domain.SessionID, name string, source io.Reader, maxBytes int64) error {
	if s.dataDir == "" {
		return errors.New("attachment data directory is empty")
	}
	sessionRoot, err := s.openCanonicalSession(ctx, id, true)
	if err != nil {
		return fmt.Errorf("open canonical attachment directory: %w", err)
	}
	defer func() { _ = sessionRoot.Close() }()
	if err := writeReaderAtomicRoot(ctx, sessionRoot, ".", name, source, false, maxBytes); err != nil {
		return fmt.Errorf("write canonical attachment: %w", err)
	}
	return nil
}

// Upload streams body to disk as a pending upload and returns its id and size.
// It has no size cap: the bytes never sit in memory, and disk space is the only
// bound. An attachment then names the id instead of carrying its bytes inline.
// Uploads older than UploadTTL are swept first.
func (s *Store) Upload(ctx context.Context, body io.Reader) (string, int64, error) {
	if s.dataDir == "" {
		return "", 0, errors.New("attachment data directory is empty")
	}
	root, err := s.openUploadsRoot(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("open attachment uploads: %w", err)
	}
	defer func() { _ = root.Close() }()
	sweepUploads(ctx, root, time.Now())
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", 0, err
	}
	id := uploadPrefix + hex.EncodeToString(suffix[:])
	if err := writeReaderAtomicRoot(ctx, root, ".", id, body, false, noLimit); err != nil {
		return "", 0, err
	}
	info, err := root.Lstat(id)
	if err != nil {
		return "", 0, err
	}
	return id, info.Size(), nil
}

const uploadPrefix = "upload-"

// ValidateUploadID reports whether id has the shape Upload mints, so a caller
// can refuse a malformed id before any file is touched.
func ValidateUploadID(id string) error {
	suffix, ok := strings.CutPrefix(id, uploadPrefix)
	if !ok || len(suffix) != 32 {
		return fmt.Errorf("invalid attachment upload id %q", id)
	}
	if _, err := hex.DecodeString(suffix); err != nil || strings.ToLower(suffix) != suffix {
		return fmt.Errorf("invalid attachment upload id %q", id)
	}
	return nil
}

// linkUpload makes a pending upload the canonical copy of a session
// attachment. A hard link costs no copy and no extra disk; the upload itself
// stays until it is swept, so a retried request can still name it. A data
// directory that cannot hard-link (another volume mounted inside it) gets a
// streamed copy instead.
func (s *Store) linkUpload(ctx context.Context, id domain.SessionID, name, upload string) error {
	if ValidateUploadID(upload) != nil {
		return ErrUploadNotFound
	}
	if s.dataDir == "" {
		return errors.New("attachment data directory is empty")
	}
	sessionRoot, err := s.openCanonicalSession(ctx, id, true)
	if err != nil {
		return fmt.Errorf("open canonical attachment directory: %w", err)
	}
	defer func() { _ = sessionRoot.Close() }()
	dataRoot, err := os.OpenRoot(s.dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = dataRoot.Close() }()
	source := filepath.Join(uploadsDir, upload)
	file, info, err := openRegularFile(ctx, dataRoot, source, noLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrUploadNotFound
	}
	if err != nil {
		return fmt.Errorf("open attachment upload: %w", err)
	}
	defer func() { _ = file.Close() }()
	if info.Size() == 0 {
		return ErrEmpty
	}
	linkErr := dataRoot.Link(source, filepath.Join(durableDir, string(id), name))
	if linkErr == nil {
		return nil
	}
	if errors.Is(linkErr, fs.ErrExist) {
		return ErrExists
	}
	if err := writeReaderAtomicRoot(ctx, sessionRoot, ".", name, file, false, noLimit); err != nil {
		return fmt.Errorf("write canonical attachment: %w", err)
	}
	return nil
}

func (s *Store) openUploadsRoot(ctx context.Context) (*os.Root, error) {
	if err := os.MkdirAll(s.dataDir, 0o750); err != nil {
		return nil, err
	}
	dataRoot, err := os.OpenRoot(s.dataDir)
	if err != nil {
		return nil, err
	}
	root, err := openChildDir(ctx, dataRoot, uploadsDir, true, 0o700)
	_ = dataRoot.Close()
	return root, err
}

// sweepUploads removes uploads, and temporary files a crash left behind, last
// written more than UploadTTL ago. Best effort: a failure leaves the file for
// the next sweep. A cancelled request stops it between entries.
func sweepUploads(ctx context.Context, root *os.Root, now time.Time) {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < UploadTTL {
			continue
		}
		_ = root.Remove(entry.Name())
	}
}

// ImportWorkspace migrates legacy worktree-only attachments into canonical
// storage. Existing canonical files win because they are already the durable
// source of truth. Symlinks and non-regular entries are ignored.
func (s *Store) ImportWorkspace(ctx context.Context, id domain.SessionID, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSessionID(id); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("attachment workspace path is empty")
	}
	workspaceRoot, err := os.OpenRoot(workspacePath)
	if errors.Is(err, fs.ErrNotExist) {
		// A stale or already-removed worktree has no legacy bytes to import.
		// Teardown must still be able to finish for that session.
		return nil
	}
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = workspaceRoot.Close() }()
	aoRoot, err := openChildDir(ctx, workspaceRoot, ".ao", false, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open workspace .ao directory: %w", err)
	}
	defer func() { _ = aoRoot.Close() }()
	sourceRoot, err := openChildDir(ctx, aoRoot, "attachments", false, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open workspace attachments: %w", err)
	}
	defer func() { _ = sourceRoot.Close() }()
	entries, err := fs.ReadDir(sourceRoot.FS(), ".")
	if err != nil {
		return fmt.Errorf("list workspace attachments: %w", err)
	}
	var sessionRoot *os.Root
	defer func() {
		if sessionRoot != nil {
			_ = sessionRoot.Close()
		}
	}()

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || validateName(entry.Name()) != nil {
			continue
		}
		if sessionRoot == nil {
			sessionRoot, err = s.openCanonicalSession(ctx, id, true)
			if err != nil {
				return fmt.Errorf("open canonical attachment root: %w", err)
			}
		}
		// A file AO staged already has its canonical copy; checking first spares
		// copying a large one only to discard it.
		if _, statErr := sessionRoot.Lstat(entry.Name()); statErr == nil {
			continue
		}
		// What is left was written into the worktree without AO: legacy files,
		// or an agent's. Those are held to MaxFileBytes.
		file, _, openErr := openRegularFile(ctx, sourceRoot, entry.Name(), MaxFileBytes)
		if errors.Is(openErr, fs.ErrNotExist) {
			continue
		}
		if openErr != nil {
			return fmt.Errorf("open workspace attachment %q: %w", entry.Name(), openErr)
		}
		copyErr := writeReaderAtomicRoot(ctx, sessionRoot, ".", entry.Name(), file, false, MaxFileBytes)
		closeErr := file.Close()
		if errors.Is(copyErr, ErrExists) || errors.Is(copyErr, ErrEmpty) || errors.Is(copyErr, errTooLarge) {
			continue
		}
		if copyErr != nil {
			return fmt.Errorf("import workspace attachment %q: %w", entry.Name(), copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close workspace attachment %q: %w", entry.Name(), closeErr)
		}
	}
	return nil
}

// MaterializeWorkspace projects every canonical attachment into a restored
// worktree before its controller is relaunched. beforeWrite runs once, only
// when a file exists, and must succeed before any file becomes visible there.
func (s *Store) MaterializeWorkspace(ctx context.Context, id domain.SessionID, workspacePath string, beforeWrite func() error) (bool, error) {
	lock := s.projectionLock(id)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("attachment workspace path is empty")
	}
	if s.dataDir == "" {
		return false, nil
	}
	if err := validateSessionID(id); err != nil {
		return false, err
	}
	sessionRoot, err := s.openCanonicalSession(ctx, id, false)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open canonical attachment root: %w", err)
	}
	defer func() { _ = sessionRoot.Close() }()
	entries, err := fs.ReadDir(sessionRoot.FS(), ".")
	if err != nil {
		return false, fmt.Errorf("list canonical attachments: %w", err)
	}

	materialized := false
	prepared := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if entry.Type()&os.ModeSymlink != 0 || validateName(entry.Name()) != nil {
			continue
		}
		file, _, openErr := openRegularFile(ctx, sessionRoot, entry.Name(), noLimit)
		if errors.Is(openErr, fs.ErrNotExist) {
			continue
		}
		if openErr != nil {
			return false, fmt.Errorf("open canonical attachment %q: %w", entry.Name(), openErr)
		}
		if !prepared {
			if beforeWrite != nil {
				if err := beforeWrite(); err != nil {
					_ = file.Close()
					return false, fmt.Errorf("prepare attachment workspace: %w", err)
				}
			}
			prepared = true
		}
		copyErr := writeReaderAtomicUnder(ctx, workspacePath, filepath.FromSlash(WorkspaceDir), entry.Name(), file, true, noLimit)
		closeErr := file.Close()
		if copyErr != nil {
			return false, fmt.Errorf("materialize attachment %q: %w", entry.Name(), copyErr)
		}
		if closeErr != nil {
			return false, fmt.Errorf("close canonical attachment %q: %w", entry.Name(), closeErr)
		}
		materialized = true
	}
	return materialized, nil
}

// Open opens a regular canonical attachment for HTTP serving.
func (s *Store) Open(ctx context.Context, id domain.SessionID, name string) (*os.File, fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if s.dataDir == "" {
		return nil, nil, fs.ErrNotExist
	}
	if err := validateSessionID(id); err != nil {
		return nil, nil, fs.ErrNotExist
	}
	if err := validateName(name); err != nil {
		return nil, nil, fs.ErrNotExist
	}
	sessionRoot, err := s.openCanonicalSession(ctx, id, false)
	if err != nil {
		return nil, nil, fs.ErrNotExist
	}
	defer func() { _ = sessionRoot.Close() }()
	return openRegularFile(ctx, sessionRoot, name, noLimit)
}

// RemoveSession removes canonical files only when the owning session row was
// itself permanently deleted. Kill and cleanup intentionally do not call it.
func (s *Store) RemoveSession(ctx context.Context, id domain.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.dataDir == "" {
		return nil
	}
	if err := validateSessionID(id); err != nil {
		return err
	}
	root, err := s.openCanonicalRoot(ctx, false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(string(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("canonical attachment session path is not a directory")
	}
	if err := root.RemoveAll(string(id)); err != nil {
		return err
	}
	return ctx.Err()
}

// NameFromWorkspacePath recognizes a direct file in the attachment projection.
// Nested paths and traversal are deliberately rejected.
func NameFromWorkspacePath(raw string) (string, bool) {
	raw = strings.ReplaceAll(raw, `\`, "/")
	raw = strings.TrimPrefix(raw, "/")
	parts := strings.Split(raw, "/")
	if len(parts) != 3 || parts[0] != ".ao" || parts[1] != "attachments" {
		return "", false
	}
	if validateName(parts[2]) != nil {
		return "", false
	}
	return parts[2], true
}

func (s *Store) openCanonicalRoot(ctx context.Context, create bool) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.dataDir == "" {
		return nil, fs.ErrNotExist
	}
	if create {
		if err := os.MkdirAll(s.dataDir, 0o750); err != nil {
			return nil, err
		}
	} else {
		info, err := os.Stat(s.dataDir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fs.ErrNotExist
		}
	}
	dataRoot, err := os.OpenRoot(s.dataDir)
	if err != nil {
		return nil, err
	}
	root, err := openChildDir(ctx, dataRoot, durableDir, create, 0o700)
	_ = dataRoot.Close()
	return root, err
}

func (s *Store) openCanonicalSession(ctx context.Context, id domain.SessionID, create bool) (*os.Root, error) {
	root, err := s.openCanonicalRoot(ctx, create)
	if err != nil {
		return nil, err
	}
	sessionRoot, err := openChildDir(ctx, root, string(id), create, 0o700)
	_ = root.Close()
	return sessionRoot, err
}

func openChildDir(ctx context.Context, parent *os.Root, name string, create bool, perm fs.FileMode) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) && create {
		if err := parent.Mkdir(name, perm); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("attachment path is not a directory")
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	openedInfo, err := child.Stat(".")
	if err != nil {
		_ = child.Close()
		return nil, err
	}
	currentInfo, err := parent.Lstat(name)
	if err != nil || !currentInfo.IsDir() || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, currentInfo) {
		_ = child.Close()
		return nil, errors.New("attachment directory changed while opening")
	}
	return child, nil
}

func validateSessionID(id domain.SessionID) error {
	raw := string(id)
	if raw == "" || raw == "." || raw == ".." || strings.ContainsAny(raw, `/\`) || strings.ContainsRune(raw, 0) {
		return fmt.Errorf("invalid attachment session id %q", raw)
	}
	return nil
}

func validateName(name string) error {
	if name == "" || len(name) > 255 || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("invalid attachment name %q", name)
	}
	var remainder string
	if after, ok := strings.CutPrefix(name, "attachment-"); ok {
		remainder = after
	} else if after, ok := strings.CutPrefix(name, "image-"); ok {
		remainder = after
	} else {
		return fmt.Errorf("invalid attachment name %q", name)
	}
	dot := strings.IndexByte(remainder, '.')
	if dot <= 0 || dot == len(remainder)-1 || !validNamePart(remainder[:dot], false) || !validNamePart(remainder[dot+1:], true) {
		return fmt.Errorf("invalid attachment name %q", name)
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".svg", ".svgz":
		return fmt.Errorf("unsupported attachment type %q", name)
	}
	return nil
}

func validNamePart(part string, allowDot bool) bool {
	if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.Contains(part, "..") {
		return false
	}
	for _, r := range part {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || allowDot && r == '.' {
			continue
		}
		return false
	}
	return true
}

// writeReaderAtomicUnder is writeReaderAtomicRoot for a directory path.
func writeReaderAtomicUnder(ctx context.Context, rootPath, dir, name string, source io.Reader, replace bool, maxBytes int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return writeReaderAtomicRoot(ctx, root, dir, name, source, replace, maxBytes)
}

// writeReaderAtomicRoot streams source into dir/name through a temporary file.
// maxBytes caps the size; noLimit streams any size.
func writeReaderAtomicRoot(ctx context.Context, root *os.Root, dir, name string, source io.Reader, replace bool, maxBytes int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmpName := filepath.Join(dir, ".attachment-"+hex.EncodeToString(suffix[:]))
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = root.Remove(tmpName)
	}()
	var reader io.Reader = contextReader{ctx: ctx, reader: source}
	if maxBytes > noLimit {
		reader = io.LimitReader(reader, maxBytes+1)
	}
	written, err := io.Copy(tmp, reader)
	if err != nil {
		return err
	}
	if written == 0 {
		return ErrEmpty
	}
	if maxBytes > noLimit && written > maxBytes {
		return errTooLarge
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	target := filepath.Join(dir, name)
	if !replace {
		if err := root.Link(tmpName, target); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return ErrExists
			}
			return err
		}
		// Linking atomically commits the no-replace target. Temporary-link
		// cleanup is not part of that commit and the deferred removal retries it.
		_ = root.Remove(tmpName)
		return nil
	}
	if err := root.Rename(tmpName, target); err != nil {
		// Windows cannot atomically replace an existing destination with Rename.
		// This path is used only for a disposable worktree projection; canonical
		// creation always uses the no-replace hard-link commit above.
		if runtime.GOOS != "windows" {
			return err
		}
		info, statErr := root.Lstat(target)
		if statErr != nil || info.IsDir() {
			return err
		}
		if removeErr := root.Remove(target); removeErr != nil {
			return err
		}
		if retryErr := root.Rename(tmpName, target); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

// openRegularFile opens a non-empty regular file that was not swapped while
// opening. maxBytes refuses a larger one; noLimit opens any size.
func openRegularFile(ctx context.Context, root *os.Root, name string, maxBytes int64) (*os.File, fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fs.ErrNotExist
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() == 0 || (maxBytes > noLimit && openedInfo.Size() > maxBytes) {
		_ = file.Close()
		return nil, nil, fs.ErrNotExist
	}
	return file, openedInfo, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
