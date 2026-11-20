package kimi

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// IsImportedHome distinguishes a private S2 native binding from ordinary
// managed Kimi sessions. Missing proof inside this namespace is an error, not
// permission to seed credentials or create a fresh conversation.
func IsImportedHome(home, dataDir string) bool {
	if !filepath.IsAbs(home) || !filepath.IsAbs(dataDir) {
		return false
	}
	rel, err := filepath.Rel(filepath.Join(dataDir, "native-imports", "kimi"), home)
	return err == nil && filepath.Base(rel) == rel && strings.HasPrefix(rel, ".native-")
}

type kimiImportProvider struct {
	Type          string            `toml:"type"`
	BaseURL       string            `toml:"base_url"`
	APIKey        string            `toml:"api_key"`
	APIKeyEnv     string            `toml:"api_key_env"`
	CustomHeaders map[string]string `toml:"custom_headers"`
	Env           map[string]string `toml:"env"`
}

type kimiImportConfig struct {
	DefaultModel string                        `toml:"default_model"`
	Providers    map[string]kimiImportProvider `toml:"providers"`
	Models       map[string]map[string]any     `toml:"models"`
}

// ImportedRuntimeEnv reads the existing provider credential into process memory
// only. The private native config contains an api_key_env reference, never the
// value. This function is used only by the ACP process launch (whose env goes
// directly to exec), never the terminal mux's environment-file path.
func ImportedRuntimeEnv(ctx context.Context, home, dataDir, id, workspace, model string, env map[string]string) (map[string]string, bool, error) {
	if !IsImportedHome(home, dataDir) {
		return nil, false, nil
	}
	fail := func() (map[string]string, bool, error) {
		return nil, true, errors.New("kimi: imported native binding or API-key provider is unavailable")
	}
	status, err := os.Lstat(home)
	if err != nil || !status.IsDir() || status.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = root.Close() }()
	b, err := readKimiFile(ctx, root, ".ao-import.json", 1<<20)
	if err != nil {
		return nil, true, err
	}
	var proof kimiImportProof
	if json.Unmarshal(b, &proof) != nil || proof.ID != id || proof.Home != filepath.Base(home) || proof.Source == "" || proof.CWD == "" {
		return fail()
	}
	legacyProof := proof.WireBytes == 0 && proof.WireHash == ""
	if legacyProof {
		proof, err = recoverKimiRuntimeProof(ctx, home, proof)
		if err != nil {
			return nil, true, err
		}
	}
	currentModel, err := verifyKimiRuntimeHistory(ctx, root, proof)
	if err != nil {
		return nil, true, err
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fail()
	}
	if canonicalWorkspace != proof.CWD {
		return fail()
	}
	if legacyProof {
		data, err := json.Marshal(proof)
		if err != nil {
			return nil, true, err
		}
		if err := hookutil.AtomicWriteFile(filepath.Join(home, ".ao-import.json"), data, 0o600); err != nil {
			return nil, true, err
		}
	}
	source, err := os.OpenRoot(proof.Source)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = source.Close() }()
	data, err := readKimiFile(ctx, source, "config.toml", 1<<20)
	if err != nil {
		return nil, true, err
	}
	var config kimiImportConfig
	if toml.Unmarshal(data, &config) != nil {
		return fail()
	}
	if model == "" {
		model = currentModel
	}
	if model == "" {
		model = proof.Model
	}
	if model == "" {
		model = config.DefaultModel
	}
	selected, ok := config.Models[model]
	if !ok {
		return fail()
	}
	providerID, ok := selected["provider"].(string)
	if !ok {
		return fail()
	}
	provider, ok := config.Providers[providerID]
	if !ok || provider.Type == "" || len(provider.CustomHeaders) != 0 || len(provider.Env) != 0 {
		return fail()
	}
	endpoint, err := url.Parse(provider.BaseURL)
	if err != nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fail()
	}
	key := provider.APIKey
	if provider.APIKeyEnv != "" {
		if value, present := env[provider.APIKeyEnv]; present {
			key = value
		} else {
			key = os.Getenv(provider.APIKeyEnv)
		}
	}
	if strings.TrimSpace(key) == "" {
		return fail()
	}
	// Copy only documented non-secret model fields. Provider custom headers,
	// environment maps, OAuth references and arbitrary settings are excluded.
	nativeModel := map[string]any{}
	for _, name := range []string{"provider", "model", "max_context_size", "max_output_size", "capabilities", "display_name", "reasoning_key", "adaptive_thinking"} {
		if value, ok := selected[name]; ok {
			nativeModel[name] = value
		}
	}
	sanitized := map[string]any{"default_model": model, "telemetry": false, "providers": map[string]any{providerID: map[string]string{"type": provider.Type, "base_url": provider.BaseURL, "api_key_env": "AO_KIMI_IMPORT_API_KEY"}}, "models": map[string]any{model: nativeModel}}
	encoded, err := toml.Marshal(sanitized)
	if err != nil {
		return nil, true, err
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(home, "config.toml"), encoded, 0o600); err != nil {
		return nil, true, err
	}
	return map[string]string{"KIMI_CODE_HOME": home, "AO_KIMI_IMPORT_API_KEY": key}, true, nil
}
