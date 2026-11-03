package cursorstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	var fingerprint strings.Builder
	for _, input := range []string{rel, rel + "-wal", filepath.Join(filepath.Dir(rel), "meta.json")} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		info, err := provider.Lstat(input)
		if errors.Is(err, os.ErrNotExist) && input == rel+"-wal" {
			fingerprint.WriteString("wal:absent;")
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("cursor archive input is not a regular file")
		}
		_, _ = fmt.Fprintf(&fingerprint, "%s:%d:%d;", input, info.Size(), info.ModTime().UnixNano())
	}
	return fingerprint.String(), nil
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
