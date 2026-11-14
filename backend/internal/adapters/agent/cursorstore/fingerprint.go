package cursorstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Fingerprint captures every native input used by discovery and preparation.
// SQLite's main file alone does not identify a live WAL snapshot.
func Fingerprint(ctx context.Context, root, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := providerRoot(root)
	if err != nil {
		return "", err
	}
	path, err = confined(root, path)
	if err != nil {
		return "", err
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("cursor database is outside provider home")
	}
	fingerprint := sha256.New()
	buffer := make([]byte, 64<<10)
	for _, input := range []string{rel, rel + "-wal", filepath.Join(filepath.Dir(rel), "meta.json")} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		info, err := provider.Lstat(input)
		if errors.Is(err, os.ErrNotExist) && input == rel+"-wal" {
			_, _ = fmt.Fprintf(fingerprint, "%d:%s:absent;", len(input), input)
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Size() > maxStore {
			return "", errors.New("cursor archive input is not a regular file")
		}
		file, err := provider.Open(input)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(fingerprint, "%d:%s:present:%d:", len(input), input, info.Size())
		var total int64
		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return "", err
			}
			n, readErr := file.Read(buffer)
			total += int64(n)
			if total > maxStore {
				_ = file.Close()
				return "", errors.New("cursor archive input exceeds import limit")
			}
			_, _ = fingerprint.Write(buffer[:n])
			if readErr != nil {
				_ = file.Close()
				if !errors.Is(readErr, io.EOF) {
					return "", readErr
				}
				break
			}
		}
		if total != info.Size() {
			return "", errors.New("cursor archive input changed while fingerprinting")
		}
	}
	return hex.EncodeToString(fingerprint.Sum(nil)), nil
}

func checkFingerprint(ctx context.Context, root, path, expected string) error {
	if expected == "" {
		return nil
	}
	actual, err := Fingerprint(ctx, root, path)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("cursor native source changed since selection; scan again before importing")
	}
	return nil
}
