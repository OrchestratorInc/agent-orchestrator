package tau

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The pinned Tau sessions command lists index metadata even when its JSONL
// transcript is missing. The native loader then accepts an empty conversation.
// Read its existing index/history without creating or changing native files.
func validateRestoreHistory(ctx context.Context, id, workspace string, env map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	home := env["TAU_HOME"]
	if home == "" {
		home = os.Getenv("TAU_HOME")
	}
	if home == "" || home == "~" || strings.HasPrefix(home, "~/") {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("tau: native history home unavailable: %w", err)
		}
		switch home {
		case "":
			home = filepath.Join(userHome, ".tau")
		case "~":
			home = userHome
		default:
			home = filepath.Join(userHome, strings.TrimPrefix(home, "~/"))
		}
	}
	if !filepath.IsAbs(home) {
		return fmt.Errorf("tau: native history home must be absolute")
	}
	indexes, err := filepath.Glob(filepath.Join(home, "sessions", "*", "index.jsonl"))
	if err != nil {
		return fmt.Errorf("tau: native history index: %w", err)
	}
	indexes = append(indexes, filepath.Join(home, "sessions", "index.jsonl"))
	var history string
	for _, index := range indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.Open(index)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("tau: native history index unavailable: %w", err)
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var row struct {
				ID   string `json:"id"`
				Path string `json:"path"`
			}
			if json.Unmarshal(scanner.Bytes(), &row) != nil || row.ID != id {
				continue
			}
			if !filepath.IsAbs(row.Path) || (history != "" && filepath.Clean(history) != filepath.Clean(row.Path)) {
				_ = f.Close()
				return fmt.Errorf("tau: exact native history reference is ambiguous or invalid")
			}
			history = row.Path
		}
		scanErr := scanner.Err()
		closeErr := f.Close()
		if scanErr != nil {
			return fmt.Errorf("tau: native history index unreadable: %w", scanErr)
		}
		if closeErr != nil {
			return fmt.Errorf("tau: native history index close: %w", closeErr)
		}
	}
	if history == "" {
		return fmt.Errorf("tau: exact native history reference is missing")
	}
	f, err := os.Open(history)
	if err != nil {
		return fmt.Errorf("tau: exact native history is unavailable: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("tau: exact native history is empty or not a regular file")
	}
	var header struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if err := json.NewDecoder(io.LimitReader(f, 1024*1024)).Decode(&header); err != nil || header.Type != "session_info" {
		return fmt.Errorf("tau: exact native history header is invalid")
	}
	expected, err := os.Stat(workspace)
	if err != nil {
		return fmt.Errorf("tau: AO history workspace unavailable: %w", err)
	}
	actual, err := os.Stat(header.Cwd)
	if err != nil || !filepath.IsAbs(header.Cwd) || !actual.IsDir() || !os.SameFile(expected, actual) {
		return fmt.Errorf("tau: native history workspace does not match AO")
	}
	return nil
}
