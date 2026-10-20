package freebuff

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// configDirEnv is Freebuff's own override for its settings, credentials and
// chat history directory. It must be absolute.
const configDirEnv = "FREEBUFF_CONFIG_DIR"

// configDir mirrors Freebuff's getConfigDir: FREEBUFF_CONFIG_DIR when set to an
// absolute path, otherwise ~/.config/manicode. A session's launch env wins over
// the daemon's environment.
func configDir(env map[string]string) string {
	for _, value := range []string{env[configDirEnv], os.Getenv(configDirEnv)} {
		if value = strings.TrimSpace(value); value != "" && filepath.IsAbs(value) {
			return value
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "manicode")
}

// applyModelSelection saves the AO model choice where Freebuff's /model picker
// keeps it. Freebuff validates the file on load and drops unknown models or
// efforts, so a stale choice degrades to its default model rather than failing.
// The saved catalog-row key is cleared because it would otherwise outrank the
// model id when Freebuff's server catalog is reachable.
func applyModelSelection(dir string, cfg ports.AgentConfig) error {
	model := strings.TrimSpace(cfg.Model)
	effort := strings.TrimSpace(cfg.Effort)
	if model == "" {
		return nil
	}
	if dir == "" {
		return errors.New("freebuff: cannot locate the Freebuff config directory to apply the model")
	}
	path := filepath.Join(dir, "settings.json")
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &settings); err != nil {
				return fmt.Errorf("freebuff: read %s: %w", path, err)
			}
		}
	case errors.Is(err, os.ErrNotExist):
		settings["mode"] = "DEFAULT"
		settings["adsEnabled"] = true
	default:
		return fmt.Errorf("freebuff: read %s: %w", path, err)
	}
	settings["freebuffModel"] = model
	delete(settings, "freebuffModelKey")
	if effort != "" {
		efforts, _ := settings["freebuffReasoningEfforts"].(map[string]any)
		if efforts == nil {
			efforts = map[string]any{}
		}
		efforts[model] = effort
		settings["freebuffReasoningEfforts"] = efforts
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("freebuff: create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("freebuff: write settings: %w", err)
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("freebuff: write settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("freebuff: write settings: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("freebuff: write settings: %w", err)
	}
	return nil
}

// chatsDir is where Freebuff keeps a project's conversations:
// <config>/projects/<basename(projectRoot)>/chats/<chat-id>.
func chatsDir(dir, workspace string) string {
	return filepath.Join(dir, "projects", filepath.Base(workspace), "chats")
}

// latestChatID returns the newest chat whose recorded project root is this
// workspace. Projects sharing a basename share a chats directory, so the
// directory name alone does not identify the workspace.
func latestChatID(dir, workspace string) (string, error) {
	if dir == "" {
		return "", nil
	}
	root := chatsDir(dir, workspace)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("freebuff: list chats: %w", err)
	}
	type chat struct {
		id      string
		modTime time.Time
	}
	chats := make([]chat, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		chats = append(chats, chat{id: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(chats, func(i, j int) bool { return chats[i].modTime.After(chats[j].modTime) })
	for _, c := range chats {
		if chatBelongsTo(filepath.Join(root, c.id), workspace) {
			return c.id, nil
		}
	}
	return "", nil
}

func chatExists(dir, workspace, chatID string) bool {
	if dir == "" || chatID == "" || chatID != filepath.Base(chatID) {
		return false
	}
	return chatBelongsTo(filepath.Join(chatsDir(dir, workspace), chatID), workspace)
}

func chatBelongsTo(chatDir, workspace string) bool {
	data, err := os.ReadFile(filepath.Join(chatDir, "run-state.json"))
	if err != nil {
		return false
	}
	var state struct {
		SessionState struct {
			FileContext struct {
				ProjectRoot string `json:"projectRoot"`
			} `json:"fileContext"`
		} `json:"sessionState"`
	}
	if json.Unmarshal(data, &state) != nil {
		return false
	}
	return samePath(state.SessionState.FileContext.ProjectRoot, workspace)
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
