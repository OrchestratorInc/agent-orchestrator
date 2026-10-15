package codebuddy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nativeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_:-]{0,255}$`)

func historyRoot(env map[string]string) (string, error) {
	dir, overridden := env["CODEBUDDY_CONFIG_DIR"]
	if !overridden {
		dir = os.Getenv("CODEBUDDY_CONFIG_DIR")
	}
	if dir := strings.TrimSpace(dir); dir != "" {
		return filepath.Join(dir, "projects"), nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".codebuddy", "projects"), err
}

// Search by the explicit ID, then corroborate every message's durable cwd.
// Native path compression is not an identity boundary: different paths can
// compress alike. Missing, corrupt, cross-workspace or empty history fails hard.
func validateHistory(ctx context.Context, workspace, id string, env map[string]string) error {
	if !nativeID.MatchString(id) {
		return fmt.Errorf("codebuddy: exact native session ID required for restore")
	}
	if workspace == "" {
		return fmt.Errorf("codebuddy: workspace required for native restore")
	}
	cwd, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fmt.Errorf("codebuddy: resolve restore workspace: %w", err)
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return err
	}
	root, err := historyRoot(env)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("codebuddy: native history unavailable: %w", err)
	}
	matches := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(root, entry.Name(), id+".jsonl")
		stat, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("codebuddy: inspect native history: %w", err)
		}
		if !stat.Mode().IsRegular() || stat.Size() > 32<<20 {
			return fmt.Errorf("codebuddy: unsupported native history file")
		}
		if err := validateTranscript(ctx, path, cwd, id); err != nil {
			return err
		}
		matches++
	}
	if matches != 1 {
		return fmt.Errorf("codebuddy: exact native history missing or ambiguous")
	}
	return nil
}

func validateTranscript(ctx context.Context, path, cwd, id string) error {
	f, err := os.Open(path) //nolint:gosec // validated native history path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 4<<20)
	user, assistant := false, false
	for s.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var row struct {
			Type, Role, SessionID, Cwd string
			Content                    json.RawMessage
		}
		if err := json.Unmarshal(s.Bytes(), &row); err != nil {
			return fmt.Errorf("codebuddy: corrupt native history")
		}
		if row.Type != "message" {
			continue
		}
		if row.SessionID != id || filepath.Clean(row.Cwd) != filepath.Clean(cwd) {
			return fmt.Errorf("codebuddy: native history identity/workspace mismatch")
		}
		if len(row.Content) == 0 || string(row.Content) == "null" {
			continue
		}
		user = user || row.Role == "user"
		assistant = assistant || row.Role == "assistant"
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("codebuddy: read native history: %w", err)
	}
	if !user || !assistant {
		return fmt.Errorf("codebuddy: native history has no completed conversation")
	}
	return nil
}
