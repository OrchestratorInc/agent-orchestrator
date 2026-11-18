package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Account model availability.
//
// Codex accepts some model IDs its model/list leaves out, so AO lets a spawn
// with an unlisted model go ahead and lets Codex decide. With a ChatGPT
// sign-in, Codex refuses a model the account cannot use only when the first
// turn reaches it, about half a minute after the worker started. AO learns that
// refusal the first time it sees it and, from then on, refuses a new session on
// the model before it starts, naming the models the account lists. An
// orchestrator that keeps delegating with a stale model ID gets the answer at
// spawn instead of a fleet of workers that each fail their first turn (#4518).
// AO never refuses a model it has not seen Codex refuse.

var chatGPTUnavailableModelPattern = regexp.MustCompile(
	`The '([^']+)' model is not supported when using Codex with a ChatGPT account`)

// ParseUnavailableModel returns the model ID in Codex's refusal of a model the
// signed-in ChatGPT account cannot use.
func ParseUnavailableModel(text string) (string, bool) {
	match := chatGPTUnavailableModelPattern.FindStringSubmatch(text)
	if len(match) != 2 || strings.TrimSpace(match[1]) == "" {
		return "", false
	}
	return strings.TrimSpace(match[1]), true
}

// UnavailableModelHint is the next step shown with Codex's refusal.
const UnavailableModelHint = "Choose another model in the model picker: it lists the models this Codex account can use. " +
	"New Codex sessions on this model are refused until the account lists it."

// UnavailableModelMessage explains a refused spawn and names the models the
// account lists, when AO knows them.
func UnavailableModelMessage(model string, available []string) string {
	message := fmt.Sprintf("Codex said %q is not available on this ChatGPT account", model)
	if len(available) == 0 {
		return message + "; choose another model"
	}
	return message + "; choose one of: " + strings.Join(available, ", ")
}

// UnavailableModels stores the model IDs Codex refused for the signed-in
// account. It is safe for concurrent use. An empty path keeps them in memory.
type UnavailableModels struct {
	mu      sync.Mutex
	path    string
	refused map[string]time.Time
}

// NewUnavailableModels loads the refusals persisted at path. A missing or
// unreadable file starts empty: Codex will refuse again.
func NewUnavailableModels(path string) *UnavailableModels {
	u := &UnavailableModels{path: path, refused: map[string]time.Time{}}
	if path == "" {
		return u
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return u
	}
	var stored map[string]time.Time
	if json.Unmarshal(data, &stored) != nil {
		return u
	}
	for model, at := range stored {
		if model = strings.TrimSpace(model); model != "" {
			u.refused[model] = at
		}
	}
	return u
}

// Record stores a model Codex refused for the account.
func (u *UnavailableModels) Record(model string, at time.Time) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("codex: record unavailable model: empty model")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, known := u.refused[model]; known {
		return nil
	}
	u.refused[model] = at.UTC()
	return u.persistLocked()
}

// Forget drops a refusal, for a model the account lists again.
func (u *UnavailableModels) Forget(model string) error {
	model = strings.TrimSpace(model)
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, known := u.refused[model]; !known {
		return nil
	}
	delete(u.refused, model)
	return u.persistLocked()
}

// Unavailable reports whether Codex refused model for the account.
func (u *UnavailableModels) Unavailable(model string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	_, refused := u.refused[strings.TrimSpace(model)]
	return refused
}

func (u *UnavailableModels) persistLocked() error {
	if u.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(u.refused, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(u.path), 0o700); err != nil {
		return fmt.Errorf("codex: record unavailable model: %w", err)
	}
	tmp := u.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("codex: record unavailable model: %w", err)
	}
	if err := os.Rename(tmp, u.path); err != nil {
		return fmt.Errorf("codex: record unavailable model: %w", err)
	}
	return nil
}

var (
	defaultUnavailableMu sync.RWMutex
	defaultUnavailable   = NewUnavailableModels("")
)

// ConfigureUnavailableModels points the process-wide store at path. The
// daemon calls it once at startup with a file under its state directory.
func ConfigureUnavailableModels(path string) {
	defaultUnavailableMu.Lock()
	defaultUnavailable = NewUnavailableModels(path)
	defaultUnavailableMu.Unlock()
}

// DefaultUnavailableModels returns the process-wide store.
func DefaultUnavailableModels() *UnavailableModels {
	defaultUnavailableMu.RLock()
	defer defaultUnavailableMu.RUnlock()
	return defaultUnavailable
}

// RecordUnavailableModelFrom records the model in a Codex refusal message.
// It reports the model it recorded, or "" when text is not such a refusal.
func RecordUnavailableModelFrom(text string, at time.Time) string {
	model, ok := ParseUnavailableModel(text)
	if !ok {
		return ""
	}
	_ = DefaultUnavailableModels().Record(model, at)
	return model
}
