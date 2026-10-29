package agentauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Pi guards auth.json with proper-lockfile: a "<file>.lock" directory that a
// live holder refreshes, treated as stale after 30 seconds.
const (
	piLockStale = 30 * time.Second
	piLockWait  = 5 * time.Second
)

func piAgentDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "PI_CODING_AGENT_DIR must be an absolute path.", nil)
		}
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Pi's credential home is unavailable.")
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// piKeyValue escapes a literal key for Pi's config-value templates, where $
// interpolates the environment and a leading ! runs a shell command.
func piKeyValue(key string) string {
	value := strings.ReplaceAll(key, "$", "$$")
	if strings.HasPrefix(value, "!") {
		value = "$" + value
	}
	return value
}

// savePiProviderKey stores key under providerID in Pi's auth.json, the same
// record Pi's /login writes for an API key, keeping every other entry.
func savePiProviderKey(ctx context.Context, providerID, key string) error {
	dir, err := piAgentDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Pi's credential home could not be prepared.")
	}
	path := filepath.Join(dir, "auth.json")
	unlock, err := lockPiAuth(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	entries := map[string]json.RawMessage{}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Pi's credential file is not a safe regular file.", nil)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Pi's credential file could not be read.")
		}
		if strings.TrimSpace(string(data)) != "" && (json.Unmarshal(data, &entries) != nil || entries == nil) {
			return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Pi's credential file is invalid; use Pi's /login to repair it.", nil)
		}
	} else if !os.IsNotExist(err) {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Pi's credential file could not be inspected.")
	}
	value, _ := json.Marshal(struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}{Type: "api_key", Key: piKeyValue(key)})
	entries[providerID] = value
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return apierr.Internal("AGENT_AUTH_STORE_UNAVAILABLE", "Pi's credential could not be encoded.")
	}
	return replaceCredentialFile(ctx, dir, path, append(data, '\n'), "Pi")
}

func lockPiAuth(ctx context.Context, path string) (func(), error) {
	lock := path + ".lock"
	deadline := time.Now().Add(piLockWait)
	for {
		err := os.Mkdir(lock, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lock) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Pi's credential file could not be locked.")
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > piLockStale {
			_ = os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, apierr.Conflict("AGENT_AUTH_STORE_BUSY", "Pi is updating its credentials. Try again in a moment.", nil)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// replaceCredentialFile atomically replaces path with a 0600 file. Harnesses
// that watch their credential file see the new inode.
func replaceCredentialFile(ctx context.Context, dir, path string, data []byte, harness string) error {
	tmp, err := os.CreateTemp(dir, ".ao-auth-*")
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential file could not be prepared.")
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential file could not be secured.")
	}
	_, writeErr := tmp.Write(data)
	if writeErr == nil {
		writeErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); writeErr != nil || closeErr != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential file could not be written.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential file could not be replaced.")
	}
	return nil
}

// saveOMPProviderKey imports key through `omp auth import -`, OMP's supported
// way to add a credential from outside. It replaces the provider's earlier
// API keys, keeps its OAuth sign-ins, and marks the key as a login so it
// outranks environment variables.
func saveOMPProviderKey(ctx context.Context, harness catalogHarness, providerID, key string) error {
	if strings.HasPrefix(key, "!") {
		return apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "OMP runs a key that starts with ! as a shell command. Enter the key itself.", nil)
	}
	if harness.binary == "" || harness.command == nil {
		return apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "omp was not found on PATH.")
	}
	payload, _ := json.Marshal(map[string]any{
		"version": 1,
		"credentials": []any{map[string]any{
			"provider":   providerID,
			"credential": map[string]string{"type": "api_key", "key": key, "source": "login"},
		}},
	})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := harness.command(ctx, harness.binary, "auth", "import", "-")
	process.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 3 * time.Second
	cmd.Dir = harness.workDir
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	cmd.Stdin = bytes.NewReader(payload)
	var output bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &limitedWriter{buf: &output, limit: 2048}
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(strings.ReplaceAll(output.String(), key, "[redacted]"))
		if line, _, ok := strings.Cut(reason, "\n"); ok {
			reason = line
		}
		message := "OMP could not save the key."
		if reason != "" {
			message += " " + reason
		}
		return apierr.Unavailable("AGENT_AUTH_IMPORT_FAILED", message)
	}
	return nil
}

type limitedWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}
