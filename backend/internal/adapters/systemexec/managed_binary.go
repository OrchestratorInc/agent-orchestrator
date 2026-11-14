package systemexec

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// managedBinaryMaxBytes bounds a release download and an extracted binary.
// cloudflared's largest asset is ~55 MB; the cap only stops a misbehaving
// server from filling the disk.
const managedBinaryMaxBytes = 256 << 20

// InstallManagedBinary downloads a pinned release into AO's bin directory.
// The bytes land in a private temp file beside the destination and are hashed
// as they arrive. Only a file whose SHA-256 matches the pin is extracted, made
// executable and renamed over the destination, so a partial or tampered
// download never becomes runnable there.
func (a Adapter) InstallManagedBinary(ctx context.Context, download ports.ManagedBinaryDownload) (string, error) {
	if !filepath.IsAbs(a.binDir) {
		return "", errors.New("managed bin directory is not configured")
	}
	if download.Name == "" || filepath.Base(download.Name) != download.Name {
		return "", fmt.Errorf("invalid managed binary name %q", download.Name)
	}
	want, err := hex.DecodeString(download.SHA256)
	if err != nil || len(want) != sha256.Size {
		return "", fmt.Errorf("invalid pinned sha256 for %s", download.Name)
	}
	if err := os.MkdirAll(a.binDir, 0o750); err != nil {
		return "", fmt.Errorf("create managed bin directory: %w", err)
	}
	tmp, err := os.CreateTemp(a.binDir, "."+download.Name+".download-*")
	if err != nil {
		return "", fmt.Errorf("create download file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	// A transfer that stops sending is given up, so a retry can run within
	// the install's time.
	stallAfter := cmp.Or(a.downloadStall, managedBinaryStallTimeout)
	transferCtx, stalled := context.WithCancelCause(ctx)
	defer stalled(nil)
	stall := time.AfterFunc(stallAfter, func() { stalled(fmt.Errorf("%w: no data for %s", errDownloadStalled, stallAfter)) })
	defer stall.Stop()
	// A request the stall timer cut off fails with the stall, not "canceled".
	stallCause := func(err error) error {
		if cause := context.Cause(transferCtx); err != nil && errors.Is(cause, errDownloadStalled) {
			return cause
		}
		return err
	}
	response, err := a.getHTTPS(transferCtx, download.URL, download.Name)
	if err = stallCause(err); err != nil {
		return "", errors.Join(err, tmp.Close())
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), stallReader{stall, stallAfter, io.LimitReader(response.Body, managedBinaryMaxBytes+1)})
	err = stallCause(err)
	// Synced before the rename: a power loss must not leave a truncated
	// binary in place, which nothing would download again.
	if err = errors.Join(err, response.Body.Close(), tmp.Sync(), tmp.Close()); err != nil {
		return "", fmt.Errorf("download %s: %w", download.Name, err)
	}
	if n > managedBinaryMaxBytes {
		return "", fmt.Errorf("%w: %s exceeds %d-byte download limit", ports.ErrDownloadRejected, download.Name, managedBinaryMaxBytes)
	}
	if got := hash.Sum(nil); !bytes.Equal(got, want) {
		return "", fmt.Errorf("%w: %s checksum mismatch: got sha256 %x, want %s", ports.ErrDownloadRejected, download.Name, got, download.SHA256)
	}

	verified := tmp.Name()
	if download.ArchiveMember != "" {
		extracted, err := extractTarGzMember(ctx, verified, download.ArchiveMember, a.binDir, download.Name)
		if err != nil {
			return "", err
		}
		defer func() { _ = os.Remove(extracted) }()
		verified = extracted
	}
	if err := os.Chmod(verified, 0o755); err != nil { //nolint:gosec // The managed binary must be executable.
		return "", fmt.Errorf("make %s executable: %w", download.Name, err)
	}
	dest := filepath.Join(a.binDir, download.Name)
	if err := os.Rename(verified, dest); err != nil {
		return "", fmt.Errorf("install %s: %w", download.Name, err)
	}
	return dest, nil
}

// extractTarGzMember copies the regular file named member out of an already
// verified .tar.gz into a private temp file in dir and returns its path.
func extractTarGzMember(ctx context.Context, archive, member, dir, name string) (string, error) {
	file, err := os.Open(archive) //nolint:gosec // Daemon-created temp file.
	if err != nil {
		return "", fmt.Errorf("open %s archive: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("read %s archive: %w", name, err)
	}
	entries := tar.NewReader(gz)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("%w: %s archive has no %s", ports.ErrDownloadRejected, name, member)
		}
		if err != nil {
			return "", fmt.Errorf("read %s archive: %w", name, err)
		}
		if header.Typeflag != tar.TypeReg || path.Clean(header.Name) != member {
			continue
		}
		out, err := os.CreateTemp(dir, "."+name+".extract-*")
		if err != nil {
			return "", fmt.Errorf("create %s: %w", name, err)
		}
		n, copyErr := io.Copy(out, contextReader{ctx, io.LimitReader(entries, managedBinaryMaxBytes+1)})
		if copyErr == nil && n > managedBinaryMaxBytes {
			copyErr = fmt.Errorf("%w: %s exceeds %d bytes", ports.ErrDownloadRejected, member, managedBinaryMaxBytes)
		}
		if err := errors.Join(copyErr, out.Sync(), out.Close()); err != nil {
			_ = os.Remove(out.Name())
			return "", fmt.Errorf("extract %s: %w", name, err)
		}
		return out.Name(), nil
	}
}

// managedBinaryStallTimeout is how long a download may go without a byte.
const managedBinaryStallTimeout = time.Minute

var errDownloadStalled = errors.New("download stalled")

// stallReader pushes the stall timer back on every read that brings data.
type stallReader struct {
	timer *time.Timer
	after time.Duration
	r     io.Reader
}

func (s stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(s.after)
	}
	return n, err
}

// contextReader stops a copy once ctx ends: an install timeout or a daemon
// shutdown must not wait out a long extraction.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
