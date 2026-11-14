package providerkeys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	filename          = "provider-api-keys.json"
	deviceFilename    = "provider-api-keys-device.json"
	userValueFilename = "provider-api-keys-device-user.json"
)

var mu sync.Mutex

func path(dataDir string) string { return filepath.Join(dataDir, filename) }

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
	return writeJSON(ctx, dataDir, filename, keys)
}

func Remove(ctx context.Context, dataDir, name string) error {
	if !validName(name) {
		return errors.New("invalid provider credential")
	}
	mu.Lock()
	defer mu.Unlock()
	keys, err := Load(dataDir)
	if err != nil {
		return err
	}
	if _, ok := keys[name]; ok {
		delete(keys, name)
		if err := writeJSON(ctx, dataDir, filename, keys); err != nil {
			return err
		}
	}
	if err := dropClaimsLocked(ctx, dataDir, name); err != nil {
		return err
	}
	return setDeviceLocked(ctx, dataDir, name, false)
}

func DeviceNames(dataDir string) (map[string]bool, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadDevice(dataDir)
}

func SetDevice(ctx context.Context, dataDir, name string, published bool) error {
	if !validName(name) {
		return errors.New("invalid provider credential")
	}
	mu.Lock()
	defer mu.Unlock()
	return setDeviceLocked(ctx, dataDir, name, published)
}

func UserValueNames(dataDir string) (map[string]bool, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadNames(dataDir, userValueFilename)
}

func SetUserValue(ctx context.Context, dataDir, name string, users bool) error {
	if !validName(name) {
		return errors.New("invalid provider credential")
	}
	mu.Lock()
	defer mu.Unlock()
	return setNameLocked(ctx, dataDir, userValueFilename, name, users)
}

func setDeviceLocked(ctx context.Context, dataDir, name string, published bool) error {
	if err := setNameLocked(ctx, dataDir, deviceFilename, name, published); err != nil {
		return err
	}
	if published {
		return nil
	}
	return setNameLocked(ctx, dataDir, userValueFilename, name, false)
}

func setNameLocked(ctx context.Context, dataDir, file, name string, on bool) error {
	names, err := loadNames(dataDir, file)
	if err != nil {
		return err
	}
	if names[name] == on {
		return nil
	}
	if on {
		names[name] = true
	} else {
		delete(names, name)
	}
	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	sort.Strings(list)
	return writeJSON(ctx, dataDir, file, list)
}

func loadDevice(dataDir string) (map[string]bool, error) {
	return loadNames(dataDir, deviceFilename)
}

func loadNames(dataDir, listFile string) (map[string]bool, error) {
	if dataDir == "" {
		return nil, errors.New("AO data directory is empty")
	}
	file := filepath.Join(dataDir, listFile)
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("provider device list is not a regular file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, errors.New("provider device list is invalid")
	}
	names := make(map[string]bool, len(list))
	for _, name := range list {
		if !validName(name) {
			return nil, errors.New("provider device list contains an invalid name")
		}
		names[name] = true
	}
	return names, nil
}

func writeJSON(ctx context.Context, dataDir, name string, value any) error {
	data, err := json.Marshal(value)
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
	if err := os.Rename(tmp.Name(), filepath.Join(dataDir, name)); err != nil {
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
