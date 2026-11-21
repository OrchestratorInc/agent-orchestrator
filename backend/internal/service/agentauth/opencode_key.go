package agentauth

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// saveOpenCodeAPIKey writes OpenCode 1's native auth.json format. OpenCode 2
// owns a separate SQLite credential store and must use its native login flow.
func saveOpenCodeAPIKey(ctx context.Context, method, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	provider := strings.TrimSuffix(method, "_key") // method was checked by loginArgs
	root := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR"))
	if root == "" {
		if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
			root = filepath.Join(dataHome, "opencode")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "OpenCode's credential home is unavailable.")
			}
			root = filepath.Join(home, ".local", "share", "opencode")
		}
	}
	return saveModelsDevAuth(ctx, root, "OpenCode", provider, key)
}

// saveModelsDevAuth writes {"type":"api","key"} under provider in root's
// auth.json, the format OpenCode and its forks (Kilo Code, MiMo Code) share.
func saveModelsDevAuth(ctx context.Context, root, harness, provider, key string) error {
	if !filepath.IsAbs(root) {
		return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", harness+"'s credential home must be absolute.", nil)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential home could not be prepared.")
	}
	path := filepath.Join(root, "auth.json")
	entries := make(map[string]json.RawMessage)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", harness+"'s credential file is not a safe regular file.", nil)
		}
		file, err := os.Open(path)
		if err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential file could not be read.")
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			_ = file.Close()
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", harness+"'s credential file changed while opening it.", nil)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		_ = file.Close()
		if readErr != nil || len(data) > 1024*1024 || json.Unmarshal(data, &entries) != nil || entries == nil {
			return apierr.Invalid("AGENT_AUTH_STORE_INVALID", harness+"'s credential file is invalid; use its native login to repair it.", nil)
		}
	} else if !os.IsNotExist(err) {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential file could not be inspected.")
	}
	value, _ := json.Marshal(struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}{Type: "api", Key: key})
	entries[provider] = value
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return apierr.Internal("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential could not be encoded.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(root, ".ao-auth-*")
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential file could not be prepared.")
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", harness+"'s credential file could not be secured.")
	}
	_, writeErr := tmp.Write(append(data, '\n'))
	if writeErr == nil {
		writeErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
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
