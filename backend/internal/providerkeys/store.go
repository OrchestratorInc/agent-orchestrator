// Package providerkeys stores provider environment credentials for AO-launched
// harnesses. Values never appear in API responses or command arguments.
package providerkeys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const filename = "provider-api-keys.json"

var mu sync.Mutex

func path(dataDir string) string { return filepath.Join(dataDir, filename) }

// Load returns a fresh copy of the credentials stored for this AO device.
func Load(dataDir string) (map[string]string, error) {
	if dataDir == "" {
		return nil, errors.New("AO data directory is empty")
	}
	file := path(dataDir)
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("provider key store is not a regular file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var keys map[string]string
	if err := json.Unmarshal(data, &keys); err != nil || keys == nil {
		return nil, errors.New("provider key store is invalid")
	}
	for name, value := range keys {
		if !validName(name) || !validValue(value) {
			return nil, errors.New("provider key store contains an invalid credential")
		}
	}
	return keys, nil
}

// Save atomically updates one reviewed environment variable. The caller owns
// the allowlist; this package only enforces environment-safe syntax.
func Save(ctx context.Context, dataDir, name, value string) error {
	if !validName(name) || !validValue(value) || value == "" {
		return errors.New("invalid provider credential")
	}
	mu.Lock()
	defer mu.Unlock()
	keys, err := Load(dataDir)
	if err != nil {
		return err
	}
	keys[name] = value
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dataDir, ".ao-provider-keys-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path(dataDir)); err != nil {
		return fmt.Errorf("replace provider key store: %w", err)
	}
	return nil
}

func validName(name string) bool {
	if name == "" || strings.HasPrefix(name, "AO_") {
		return false
	}
	for _, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validValue(value string) bool {
	return len(value) <= 16*1024 && !strings.ContainsAny(value, "\r\n\x00")
}
