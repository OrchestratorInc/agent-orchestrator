package providerkeys

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// harnessFilename records, per variable, the harnesses whose own Account
// panel saved it. It never holds a value.
const harnessFilename = "provider-api-keys-harnesses.json"

// subscriptionKeys are variables that would override how a harness signs in:
// they make it bill an API account instead of its subscription, or beat the
// key kept in its own credential store (DeepSeek Harness ranks its launch
// environment first). A session of that harness receives the variable only
// when that harness's own panel saved it into AO's store.
var subscriptionKeys = map[string]string{
	"ANTHROPIC_API_KEY": "claude-code",
	"OPENAI_API_KEY":    "codex",
	"GEMINI_API_KEY":    "gemini",
	"DEEPSEEK_API_KEY":  "deepseek-harness",
}

// ClaimForHarness records that harness's own panel saved name.
func ClaimForHarness(ctx context.Context, dataDir, name, harness string) error {
	if !validName(name) || harness == "" {
		return errors.New("invalid provider credential")
	}
	mu.Lock()
	defer mu.Unlock()
	claims, err := loadClaims(dataDir)
	if err != nil {
		return err
	}
	for _, existing := range claims[name] {
		if existing == harness {
			return nil
		}
	}
	claims[name] = append(claims[name], harness)
	sort.Strings(claims[name])
	return writeJSON(ctx, dataDir, harnessFilename, claims)
}

func dropClaimsLocked(ctx context.Context, dataDir, name string) error {
	claims, err := loadClaims(dataDir)
	if err != nil {
		return err
	}
	if _, ok := claims[name]; !ok {
		return nil
	}
	delete(claims, name)
	return writeJSON(ctx, dataDir, harnessFilename, claims)
}

func loadClaims(dataDir string) (map[string][]string, error) {
	if dataDir == "" {
		return nil, errors.New("AO data directory is empty")
	}
	file := filepath.Join(dataDir, harnessFilename)
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("provider harness list is not a regular file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	claims := map[string][]string{}
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, errors.New("provider harness list is invalid")
	}
	for name := range claims {
		if !validName(name) {
			return nil, errors.New("provider harness list contains an invalid name")
		}
	}
	return claims, nil
}

// ForSession splits the stored keys for a session of harness: inject holds
// the keys it should receive, withhold the names it must not, so a key saved
// for another harness cannot replace its subscription sign-in. An unknown
// harness ("") receives every key.
func ForSession(dataDir, harness string) (inject map[string]string, withhold []string, err error) {
	keys, err := Load(dataDir)
	if err != nil {
		return nil, nil, err
	}
	mu.Lock()
	claims, err := loadClaims(dataDir)
	mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	inject = make(map[string]string, len(keys))
	for name, value := range keys {
		if owner, ok := subscriptionKeys[name]; ok && harness != "" && owner == harness && !claimed(claims[name], harness) {
			withhold = append(withhold, name)
			continue
		}
		inject[name] = value
	}
	sort.Strings(withhold)
	return inject, withhold, nil
}

func claimed(harnesses []string, harness string) bool {
	for _, h := range harnesses {
		if h == harness {
			return true
		}
	}
	return false
}
