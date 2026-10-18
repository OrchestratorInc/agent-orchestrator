package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readSettingsEnv reads just the gateway keys from one Claude settings file.
// A missing, malformed, or keyless file is an empty entry, not an error.
func readSettingsEnv(path string) map[string]string {
	raw, err := os.ReadFile(path) //nolint:gosec // documented Claude settings location
	if err != nil {
		return map[string]string{}
	}
	var payload struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return map[string]string{}
	}
	env := make(map[string]string, len(payload.Env))
	for _, key := range []string{keyBaseURL, keyToken, keyModel} {
		env[key] = strings.TrimSpace(payload.Env[key])
	}
	return env
}

// EntryFromSettings reads the three gateway keys from one Claude settings
// file's env block. ok is false when the file configures nothing at all. The
// launch wiring uses it to build the gateway-entry list that provider pins
// resolve against.
func EntryFromSettings(path string) (baseURL, token, model string, ok bool) {
	env := readSettingsEnv(path)
	baseURL, token, model = env[keyBaseURL], env[keyToken], env[keyModel]
	return baseURL, token, model, baseURL != "" || token != "" || model != ""
}

// applyToSettingsFile writes one tri-state gateway key into a Claude settings
// file: a nil update leaves the file untouched, a pointer to "" deletes the
// key, a pointer to a value writes it. Every other key in the document —
// mcpServers, permissions, hooks, whatever the user keeps there — is carried
// through untouched: the file is parsed as a generic map and re-encoded, only
// doc["env"] is mutated.
func applyToSettingsFile(path, key string, update *string) error {
	if update == nil {
		return nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // documented Claude settings location
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("gateway: read %s: %w", path, err)
		}
		raw = nil
	}
	var doc map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("gateway: %s is not valid JSON: %w", path, err)
		}
	}
	if doc == nil {
		doc = map[string]any{}
	}
	env, _ := doc["env"].(map[string]any)
	// Line above: env is nil exactly when the file has no env block yet; a
	// deletion into a nil map is a no-op, so only writes need it created.
	if env == nil && *update != "" {
		env = map[string]any{}
	}
	switch *update {
	case "":
		delete(env, key)
	default:
		env[key] = *update
	}
	if len(env) == 0 {
		delete(doc, "env")
	} else {
		doc["env"] = env
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return writeSettingsFile(path, encoded)
}

// writeSettingsFile replaces the file atomically: encode into a temp file in
// the same directory, preserve the original mode (0644 for new files), then
// rename. os.Rename over an existing target fails on some platforms, so the
// Windows path removes the target first — the window between remove and
// rename is a torn read for a concurrent reader, but the write is
// last-writer-wins by design anyway.
func writeSettingsFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	// A fresh machine may not have the settings directory yet; the reader
	// treats a missing file as unconfigured, so the writer creates it.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("gateway: create %s: %w", dir, err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("gateway: create temp next to %s: %w", path, err)
	}
	tempName := temp.Name()
	defer func() {
		if tempName != "" {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("gateway: write %s: %w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempName, mode); err != nil {
		return fmt.Errorf("gateway: chmod %s: %w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		// Some platforms refuse to rename over an existing target; remove the
		// target and retry once.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return fmt.Errorf("gateway: replace %s: %w", path, err)
		}
		if err := os.Rename(tempName, path); err != nil {
			return fmt.Errorf("gateway: replace %s: %w", path, err)
		}
	}
	tempName = ""
	return nil
}
