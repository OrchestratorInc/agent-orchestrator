// Package nativetransfer moves an agent CLI's native conversation files from
// one machine to another, so the CLI on the second machine resumes the same
// conversation from a different working directory.
//
// An archive is a gzip tar: manifest.json first, then each listed file under
// files/. Paths in the manifest are relative to the directory that held the
// transcript on the source machine.
package nativetransfer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	manifestName    = "manifest.json"
	filesPrefix     = "files/"
	formatVersion   = 1
	maxManifestSize = 1 << 20
)

// MaxArchiveBytes caps the total size of the files one archive may carry.
var MaxArchiveBytes int64 = 4 << 30

// Manifest describes one exported conversation.
type Manifest struct {
	Version  int    `json:"version"`
	Harness  string `json:"harness"`
	NativeID string `json:"nativeId"`
	// SourceCwd is the working directory the CLI recorded on the source machine.
	SourceCwd string `json:"sourceCwd"`
	// SourceProjectDir is the directory that held the transcript on the source machine.
	SourceProjectDir string `json:"sourceProjectDir"`
	// CLIVersion is the CLI version that wrote the newest record.
	CLIVersion string `json:"cliVersion,omitempty"`
	Files      []File `json:"files"`
}

// File is one file in an archive.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ErrChanged reports that a source file changed while it was being exported,
// which means the agent was still writing to it.
var ErrChanged = errors.New("nativetransfer: a source file changed during export; stop the agent and retry")

func (m Manifest) validate() error {
	if m.Version != formatVersion {
		return fmt.Errorf("nativetransfer: unsupported archive version %d", m.Version)
	}
	if !filepath.IsAbs(m.SourceCwd) || !filepath.IsAbs(m.SourceProjectDir) {
		return errors.New("nativetransfer: manifest source paths must be absolute")
	}
	var total int64
	seen := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		if err := validRelPath(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return fmt.Errorf("nativetransfer: duplicate file %q", f.Path)
		}
		seen[f.Path] = true
		if f.Size < 0 || len(f.SHA256) != sha256.Size*2 {
			return fmt.Errorf("nativetransfer: invalid size or checksum for %q", f.Path)
		}
		total += f.Size
	}
	if total > MaxArchiveBytes {
		return fmt.Errorf("nativetransfer: archive holds %d bytes, over the %d byte limit", total, MaxArchiveBytes)
	}
	return nil
}

// validRelPath accepts a clean, relative, slash-separated path that stays inside its root.
func validRelPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return fmt.Errorf("nativetransfer: unsafe archive path %q", p)
	}
	return nil
}

// hashFile returns the size and hex SHA-256 of a regular file.
func hashFile(name string) (int64, string, error) {
	f, err := os.Open(name)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// writeArchive writes m and the files it lists, read from root. A file whose
// content no longer matches the manifest fails with ErrChanged.
func writeArchive(ctx context.Context, w io.Writer, m Manifest, root string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	for _, f := range m.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeArchiveFile(tw, filepath.Join(root, filepath.FromSlash(f.Path)), f); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeArchiveFile(tw *tar.Writer, name string, f File) error {
	src, err := os.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	if err := tw.WriteHeader(&tar.Header{Name: filesPrefix + f.Path, Mode: 0o600, Size: f.Size, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(tw, io.TeeReader(io.LimitReader(src, f.Size), h))
	if err != nil {
		return err
	}
	// A file that grew is caught by the extra byte; one that shrank or changed by the hash.
	if extra, _ := src.Read(make([]byte, 1)); n != f.Size || extra != 0 || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return ErrChanged
	}
	return nil
}

// readManifest opens an archive and returns its manifest and a reader positioned at the first file.
func readManifest(r io.Reader) (Manifest, *tar.Reader, func() error, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return Manifest{}, nil, nil, fmt.Errorf("nativetransfer: open archive: %w", err)
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		_ = gz.Close()
		return Manifest{}, nil, nil, fmt.Errorf("nativetransfer: read archive: %w", err)
	}
	if hdr.Name != manifestName || hdr.Size > maxManifestSize {
		_ = gz.Close()
		return Manifest{}, nil, nil, errors.New("nativetransfer: archive does not start with a manifest")
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(tr, maxManifestSize)).Decode(&m); err != nil {
		_ = gz.Close()
		return Manifest{}, nil, nil, fmt.Errorf("nativetransfer: decode manifest: %w", err)
	}
	if err := m.validate(); err != nil {
		_ = gz.Close()
		return Manifest{}, nil, nil, err
	}
	return m, tr, gz.Close, nil
}
