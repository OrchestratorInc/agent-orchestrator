package agentlaunch

import (
	"encoding/json"
	"fmt"
	"os"
)

const EnvSpecPath = "AO_LAUNCH_SPEC"

type Spec struct {
	WorkspacePath string   `json:"workspacePath"`
	Argv          []string `json:"argv"`
	FallbackArgv  []string `json:"fallbackArgv,omitempty"`
}

func WriteTemp(spec Spec) (string, error) {
	file, err := os.CreateTemp(os.TempDir(), "ao-launch-*.json")
	if err != nil {
		return "", fmt.Errorf("create launch spec: %w", err)
	}
	path := file.Name()
	enc := json.NewEncoder(file)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(spec); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write launch spec: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close launch spec: %w", err)
	}
	return path, nil
}

func ReadAndRemove(path string) (Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("read launch spec: %w", err)
	}
	_ = os.Remove(path)

	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return Spec{}, fmt.Errorf("parse launch spec: %w", err)
	}
	if len(spec.Argv) == 0 {
		return Spec{}, fmt.Errorf("launch spec: argv is required")
	}
	return spec, nil
}
