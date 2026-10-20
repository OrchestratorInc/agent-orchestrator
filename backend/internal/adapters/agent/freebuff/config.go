package freebuff

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
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// configDirEnv is Freebuff's own override for its settings, credentials and
// chat history directory. It must be absolute.
const configDirEnv = "FREEBUFF_CONFIG_DIR"

// configDir mirrors Freebuff's getConfigDir. A present session override wins
// even when empty (which clears a daemon override). An invalid relative
// override cannot identify a native config directory.
func configDir(env map[string]string) string {
	value, present := env[configDirEnv]
	if !present {
		value = os.Getenv(configDirEnv)
	}
	if value = strings.TrimSpace(value); value != "" {
		if !filepath.IsAbs(value) {
			return ""
		}
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "manicode")
}

type startupGate struct {
	available chan struct{}
	owner     *startupOwner
}

type startupOwner struct {
	sessionID string
	model     string
	effort    string
	startedAt time.Time
}

// BeginStartup serializes the complete seed/read/native-picker transaction,
// not merely the atomic settings write. The native picker then pins the choice
// in this process before another AO launch can seed a different preference.
func (p *Plugin) BeginStartup(ctx context.Context, cfg ports.LaunchConfig) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.SessionID) == "" {
		return nil, errors.New("freebuff: native startup requires an AO session id")
	}
	model, effort := strings.TrimSpace(cfg.Config.Model), strings.TrimSpace(cfg.Config.Effort)
	if model != "" || effort != "" {
		info, ok := startupModel(model)
		if !ok || (effort != "" && !containsString(info.Efforts, effort)) {
			return nil, errors.New("freebuff: unsupported native model or reasoning effort")
		}
	}
	dir := configDir(cfg.Env)
	if dir == "" {
		return nil, errors.New("freebuff: FREEBUFF_CONFIG_DIR must be an absolute path")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("freebuff: prepare config directory: %w", err)
	}
	key := startupConfigKey(dir)
	p.startupMu.Lock()
	if p.startupGates == nil {
		p.startupGates = map[string]*startupGate{}
	}
	gate := p.startupGates[key]
	if gate == nil {
		gate = &startupGate{available: make(chan struct{}, 1)}
		gate.available <- struct{}{}
		p.startupGates[key] = gate
	}
	p.startupMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gate.available:
	}
	if err := ctx.Err(); err != nil {
		gate.available <- struct{}{}
		return nil, err
	}
	startedAt := time.Now()
	if err := applyModelSelection(dir, cfg.Config); err != nil {
		gate.available <- struct{}{}
		return nil, err
	}
	owner := &startupOwner{sessionID: cfg.SessionID, model: model, effort: effort, startedAt: startedAt}
	p.startupMu.Lock()
	gate.owner = owner
	p.startupMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.startupMu.Lock()
			gate.owner = nil
			p.startupMu.Unlock()
			gate.available <- struct{}{}
		})
	}, nil
}

func startupConfigKey(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(dir)
}

func (p *Plugin) ownsStartup(cfg ports.LaunchConfig) bool {
	_, ok := p.startupOwnerFor(cfg)
	return ok
}

func (p *Plugin) startupOwnerFor(cfg ports.LaunchConfig) (startupOwner, bool) {
	key := startupConfigKey(configDir(cfg.Env))
	p.startupMu.Lock()
	defer p.startupMu.Unlock()
	gate := p.startupGates[key]
	if gate == nil || gate.owner == nil || gate.owner.sessionID != cfg.SessionID ||
		gate.owner.model != strings.TrimSpace(cfg.Config.Model) || gate.owner.effort != strings.TrimSpace(cfg.Config.Effort) {
		return startupOwner{}, false
	}
	return *gate.owner, true
}

// This is Freebuff's current one-time default migration stamp. Mark an AO
// canonical pick explicit, so the native default migration cannot replace an
// explicitly requested DeepSeek model with MiMo before the picker opens.
const defaultModelMigration = "glm-5.3-flash-2026-09-05"

func applyModelSelection(dir string, cfg ports.AgentConfig) error {
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil
	}
	path := filepath.Join(dir, "settings.json")
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &settings); err != nil {
				return fmt.Errorf("freebuff: read settings: %w", err)
			}
		}
	case errors.Is(err, os.ErrNotExist):
		settings["mode"] = "DEFAULT"
		settings["adsEnabled"] = true
	default:
		return fmt.Errorf("freebuff: read settings: %w", err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	settings["freebuffModel"] = model
	// Native compares exact identity, so any other stamp would still migrate
	// this explicit pick when it is the previous default.
	settings["freebuffModelDefaultMigration"] = defaultModelMigration
	delete(settings, "freebuffModelKey")
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("freebuff: encode settings: %w", err)
	}
	file, err := os.CreateTemp(dir, ".ao-freebuff-settings-*.json")
	if err != nil {
		return fmt.Errorf("freebuff: prepare settings: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(out); err != nil {
		_ = file.Close()
		return fmt.Errorf("freebuff: write settings: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("freebuff: close settings: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("freebuff: replace settings: %w", err)
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
