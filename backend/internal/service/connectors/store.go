package connectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	fileName    = "mcp-connectors.json"
	fileVersion = 1
)

type record struct {
	ID          string            `json:"id"`
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	PresetID    string            `json:"presetId,omitempty"`
	Transport   Transport         `json:"transport"`
	URL         string            `json:"url,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Auth        Auth              `json:"auth"`
	ProjectID   string            `json:"projectId,omitempty"`
	Enabled     bool              `json:"enabled"`
	Status      Status            `json:"status"`
	StatusStep  string            `json:"statusStep,omitempty"`
	StatusError string            `json:"statusError,omitempty"`
	Tools       []string          `json:"tools,omitempty"`
	CheckedAt   time.Time         `json:"checkedAt,omitzero"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type fileData struct {
	Version    int      `json:"version"`
	Connectors []record `json:"connectors"`
}

func readFile(path string) ([]record, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is the daemon's own data file.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read connectors: %w", err)
	}
	var data fileData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("read connectors: %w", err)
	}
	return data.Connectors, nil
}

func writeFile(path string, records []record) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("write connectors: %w", err)
	}
	raw, err := json.Marshal(fileData{Version: fileVersion, Connectors: records})
	if err != nil {
		return fmt.Errorf("write connectors: %w", err)
	}
	tmp, err := os.CreateTemp(dir, fileName+".*")
	if err != nil {
		return fmt.Errorf("write connectors: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write connectors: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write connectors: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write connectors: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write connectors: %w", err)
	}
	return nil
}
