package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
//
// A refusal belongs to the account that produced it. AO runs each Codex
// account under its own CODEX_HOME, so refusals are kept per account home: a
// session on another account, whose plan may include the model, is never
// refused for it. AO never refuses a model it has not seen Codex refuse for
// that account.

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
const UnavailableModelHint = "Choose another model. AO will refuse new sessions on this model for this account for now."

// UnavailableModelMessage explains a refused spawn and names the models the
// account lists, when AO knows them.
func UnavailableModelMessage(model string, available []string) string {
	message := fmt.Sprintf("Codex said %q is not available on this ChatGPT account", model)
	if len(available) == 0 {
		return message + "; choose another model"
	}
	return message + "; choose one of: " + strings.Join(available, ", ")
}

// AccountKey identifies the Codex account a session runs under by its home
// directory. An empty home is the computer's own: CODEX_HOME, or ~/.codex.
func AccountKey(home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		home = codexHomeDir()
	}
	if home == "" {
		return ""
	}
	key := filepath.Clean(home)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

// AccountHomeOfRollout is the CODEX_HOME a rollout transcript was written
// under (<home>/sessions/…), or "" when the path does not show one.
func AccountHomeOfRollout(path string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	index := strings.LastIndex(clean, "/sessions/")
	if index <= 0 {
		return ""
	}
	return filepath.FromSlash(clean[:index])
}

type unavailableModelsFile struct {
	// Refused maps an account key to the models Codex refused for it and when.
	Refused map[string]map[string]time.Time `json:"refused"`
	// Forgotten maps an account key to the models the account listed again and
	// when, so an older refusal read again from a transcript stays forgotten.
	Forgotten map[string]map[string]time.Time `json:"forgotten,omitempty"`
}

// UnavailableModels stores the models Codex refused, per account. It is safe
// for concurrent use. An empty path keeps them in memory.
type UnavailableModels struct {
	mu    sync.Mutex
	path  string
	state unavailableModelsFile
	// unsaved is set while the file lags the memory state, so the next write
	// retries even when it changes nothing.
	unsaved bool
}

// NewUnavailableModels loads the refusals persisted at path. A missing or
// unreadable file starts empty: Codex will refuse again.
func NewUnavailableModels(path string) *UnavailableModels {
	u := &UnavailableModels{path: path, state: unavailableModelsFile{
		Refused: map[string]map[string]time.Time{}, Forgotten: map[string]map[string]time.Time{},
	}}
	if path == "" {
		return u
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return u
	}
	var stored unavailableModelsFile
	if json.Unmarshal(data, &stored) != nil {
		return u
	}
	for account, models := range stored.Refused {
		for model, at := range models {
			setModelTime(u.state.Refused, account, model, at)
		}
	}
	for account, models := range stored.Forgotten {
		for model, at := range models {
			setModelTime(u.state.Forgotten, account, model, at)
		}
	}
	return u
}

func setModelTime(byAccount map[string]map[string]time.Time, account, model string, at time.Time) {
	account, model = strings.TrimSpace(account), strings.TrimSpace(model)
	if account == "" || model == "" {
		return
	}
	if byAccount[account] == nil {
		byAccount[account] = map[string]time.Time{}
	}
	byAccount[account][model] = at.UTC()
}

func deleteModel(byAccount map[string]map[string]time.Time, account, model string) {
	delete(byAccount[account], model)
	if len(byAccount[account]) == 0 {
		delete(byAccount, account)
	}
}

// Record stores a model Codex refused, at at, for the account with home. A
// refusal no newer than the account's last listing of the model is ignored:
// it is an old failure read again, not news.
func (u *UnavailableModels) Record(home, model string, at time.Time) error {
	account, model := AccountKey(home), strings.TrimSpace(model)
	if account == "" || model == "" {
		return errors.New("codex: record unavailable model: empty account or model")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if forgotten, ok := u.state.Forgotten[account][model]; ok && !at.After(forgotten) {
		return u.retryLocked()
	}
	if _, known := u.state.Refused[account][model]; known {
		return u.retryLocked()
	}
	setModelTime(u.state.Refused, account, model, at)
	deleteModel(u.state.Forgotten, account, model)
	return u.persistLocked()
}

// Forget drops a refusal for a model the account listed again when observed
// at at. A refusal recorded after that observation is newer news and stays.
// The latest observation is kept even when another check already dropped the
// refusal, so a delayed refusal older than it cannot bring the model back.
func (u *UnavailableModels) Forget(home, model string, at time.Time) error {
	account, model := AccountKey(home), strings.TrimSpace(model)
	u.mu.Lock()
	defer u.mu.Unlock()
	refusedAt, refused := u.state.Refused[account][model]
	if refused && refusedAt.After(at) {
		return u.retryLocked()
	}
	forgotten, known := u.state.Forgotten[account][model]
	advance := !known || at.After(forgotten)
	if !refused && (!known || !advance) {
		// Nothing to drop, and no newer observation of a model AO refused.
		return u.retryLocked()
	}
	if refused {
		deleteModel(u.state.Refused, account, model)
	}
	if advance {
		setModelTime(u.state.Forgotten, account, model, at)
	}
	return u.persistLocked()
}

// ForgetExpired allows a retry when a refusal has outlived the period for
// which AO can trust it without a model catalog for that account. The expiry
// is recorded so replaying an older failed rollout cannot restore the refusal.
func (u *UnavailableModels) ForgetExpired(home, model string, now time.Time, maxAge time.Duration) (bool, error) {
	account, model := AccountKey(home), strings.TrimSpace(model)
	if maxAge <= 0 {
		return false, errors.New("codex: expire unavailable model: nonpositive maximum age")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	refusedAt, refused := u.state.Refused[account][model]
	if !refused || now.Before(refusedAt.Add(maxAge)) {
		return false, nil
	}
	deleteModel(u.state.Refused, account, model)
	expiredAt := refusedAt.Add(maxAge)
	if forgotten, known := u.state.Forgotten[account][model]; !known || expiredAt.After(forgotten) {
		setModelTime(u.state.Forgotten, account, model, expiredAt)
	}
	return true, u.persistLocked()
}

// Unavailable reports whether Codex refused model for the account with home.
func (u *UnavailableModels) Unavailable(home, model string) bool {
	account, model := AccountKey(home), strings.TrimSpace(model)
	u.mu.Lock()
	defer u.mu.Unlock()
	_, refused := u.state.Refused[account][model]
	return refused
}

// retryLocked writes the file again when an earlier write failed.
func (u *UnavailableModels) retryLocked() error {
	if !u.unsaved {
		return nil
	}
	return u.persistLocked()
}

func (u *UnavailableModels) persistLocked() error {
	if u.path == "" {
		return nil
	}
	u.unsaved = true
	data, err := json.MarshalIndent(u.state, "", "  ")
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
	u.unsaved = false
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

// RecordUnavailableModelFrom records the model in a Codex refusal message for
// the account with home. It reports the model, or "" when text is not such a
// refusal.
func RecordUnavailableModelFrom(home, text string, at time.Time) string {
	model, ok := ParseUnavailableModel(text)
	if !ok {
		return ""
	}
	_ = DefaultUnavailableModels().Record(home, model, at)
	return model
}
