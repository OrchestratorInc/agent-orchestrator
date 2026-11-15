package providerkeys

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const harnessFilename = "provider-api-keys-harnesses.json"

var subscriptionKeys = map[string]string{
	"ANTHROPIC_API_KEY": "claude-code",
	"OPENAI_API_KEY":    "codex",
	"GEMINI_API_KEY":    "gemini",
	"DEEPSEEK_API_KEY":  "deepseek-harness",
}

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

func ReleaseHarness(ctx context.Context, dataDir, harness string) (string, error) {
	name := ""
	for variable, owner := range subscriptionKeys {
		if owner == harness {
			name = variable
		}
	}
	if name == "" {
		return "", nil
	}
	mu.Lock()
	defer mu.Unlock()
	claims, err := loadClaims(dataDir)
	if err != nil {
		return "", err
	}
	kept := make([]string, 0, len(claims[name]))
	for _, existing := range claims[name] {
		if existing != harness {
			kept = append(kept, existing)
		}
	}
	if len(kept) == len(claims[name]) {
		return "", nil
	}
	if len(kept) == 0 {
		delete(claims, name)
	} else {
		claims[name] = kept
	}
	if err := writeJSON(ctx, dataDir, harnessFilename, claims); err != nil {
		return "", err
	}
	return name, nil
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

func Withheld(dataDir, harness string) map[string]string {
	stored, storeErr := Load(dataDir)
	_, withhold, err := ForSession(dataDir, harness)
	if err != nil {
		withhold = SubscriptionKeys(harness)
	}
	out := make(map[string]string, len(withhold))
	for _, name := range withhold {
		if value, set, exported := Inherited(name); exported {
			if !set {
				value = ""
			}
			out[name] = value
		} else if storeErr == nil && stored[name] != "" && os.Getenv(name) == stored[name] {
			out[name] = ""
		}
	}
	return out
}

func HoldBack(ctx context.Context, env map[string]string, dataDir, harness string) map[string]string {
	if strings.TrimSpace(dataDir) == "" {
		return env
	}
	withheld := map[string]string{}
	if ctx.Err() != nil {
		for _, name := range SubscriptionKeys(harness) {
			withheld[name] = ""
		}
	} else {
		withheld = Withheld(dataDir, harness)
	}
	for name, value := range withheld {
		if env == nil {
			env = map[string]string{}
		}
		if _, set := env[name]; !set {
			env[name] = value
		}
	}
	return env
}

func WithheldForSignIn(harness string) map[string]string {
	out := map[string]string{}
	for _, name := range SubscriptionKeys(harness) {
		if value, set, exported := Inherited(name); exported {
			if !set {
				value = ""
			}
			out[name] = value
		}
	}
	return out
}

func EnvList(env []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+len(overrides))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := overrides[name]; !ok {
			out = append(out, entry)
		}
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, name+"="+overrides[name])
	}
	return out
}

func SubscriptionKeys(harness string) []string {
	var names []string
	for name, owner := range subscriptionKeys {
		if owner == harness {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func claimed(harnesses []string, harness string) bool {
	for _, h := range harnesses {
		if h == harness {
			return true
		}
	}
	return false
}
