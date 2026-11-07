package agentauth

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var qwenConfigHome = os.UserHomeDir
var providerModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)

type qwenRoute struct {
	authType string
	baseURL  string
}

var qwenRoutes = map[string]qwenRoute{
	"OPENAI_API_KEY":     {"openai", "https://api.openai.com/v1"},
	"ANTHROPIC_API_KEY":  {"anthropic", "https://api.anthropic.com/v1"},
	"GEMINI_API_KEY":     {"gemini", ""},
	"OPENROUTER_API_KEY": {"openai", "https://openrouter.ai/api/v1"},
	"DASHSCOPE_API_KEY":  {"openai", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
}

// saveQwenProvider selects a usable model route in Qwen's user settings.
// Qwen's default auth is OAuth, so an environment key alone is insufficient.
func saveQwenProvider(ctx context.Context, envName, modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if !providerModelID.MatchString(modelID) {
		return apierr.Invalid("AGENT_AUTH_MODEL_INVALID", "Enter a model ID for this Qwen provider.", nil)
	}
	route, ok := qwenRoutes[envName]
	if !ok {
		return apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "This Qwen provider is not supported here.", nil)
	}
	home, err := qwenConfigHome()
	if err != nil || home == "" {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Qwen's configuration home is unavailable.")
	}
	dir := filepath.Join(home, ".qwen")
	if configured := strings.TrimSpace(os.Getenv("QWEN_HOME")); configured != "" {
		switch {
		case configured == "~":
			dir = home
		case strings.HasPrefix(configured, "~/"):
			dir = filepath.Join(home, configured[2:])
		case filepath.IsAbs(configured):
			dir = configured
		default:
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "QWEN_HOME must be an absolute path.", nil)
		}
	}
	path := filepath.Join(dir, "settings.json")
	root, err := readQwenSettings(path)
	if err != nil {
		return err
	}
	security, err := qwenObject(root["security"])
	if err != nil {
		return err
	}
	auth, err := qwenObject(security["auth"])
	if err != nil {
		return err
	}
	if raw := auth["enforcedType"]; len(raw) != 0 {
		var enforced string
		if json.Unmarshal(raw, &enforced) != nil || (enforced != "" && enforced != route.authType) {
			return apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "Qwen's settings enforce another authentication method.", nil)
		}
	}
	providers, err := qwenObject(root["modelProviders"])
	if err != nil {
		return err
	}
	var models []map[string]json.RawMessage
	if raw := providers[route.authType]; len(raw) != 0 && json.Unmarshal(raw, &models) != nil {
		return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Qwen's provider models are invalid.", nil)
	}
	found := false
	for _, existing := range models {
		var id, key, baseURL string
		_ = json.Unmarshal(existing["id"], &id)
		_ = json.Unmarshal(existing["envKey"], &key)
		_ = json.Unmarshal(existing["baseUrl"], &baseURL)
		if id != modelID {
			continue
		}
		if key != envName || (baseURL != "" && baseURL != route.baseURL) {
			return apierr.Invalid("AGENT_AUTH_MODEL_CONFLICT", "Qwen already uses this model ID with another credential. Choose another model ID or use native setup.", nil)
		}
		found = true
	}
	if !found {
		entry := map[string]json.RawMessage{}
		entry["id"], _ = json.Marshal(modelID)
		entry["envKey"], _ = json.Marshal(envName)
		if route.baseURL != "" {
			entry["baseUrl"], _ = json.Marshal(route.baseURL)
		}
		models = append(models, entry)
	}
	providers[route.authType], _ = json.Marshal(models)
	root["modelProviders"], _ = json.Marshal(providers)
	auth["selectedType"], _ = json.Marshal(route.authType)
	security["auth"], _ = json.Marshal(auth)
	root["security"], _ = json.Marshal(security)
	model, err := qwenObject(root["model"])
	if err != nil {
		return err
	}
	model["name"], _ = json.Marshal(modelID)
	root["model"], _ = json.Marshal(model)
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Qwen's settings could not be encoded.")
	}
	return atomicConfigWrite(ctx, dir, path, append(data, '\n'))
}

func qwenObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Qwen's settings are invalid; repair them before saving a key here.", nil)
	}
	return value, nil
}

func readQwenSettings(path string) (map[string]json.RawMessage, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Qwen's settings could not be inspected.")
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Qwen's settings are not a safe regular file.", nil)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Qwen's settings could not be read.")
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Qwen's settings changed while opening them.", nil)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 64*1024+1))
	_ = file.Close()
	if readErr != nil || len(data) > 64*1024 {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Qwen's settings could not be read.")
	}
	if len(data) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Qwen's settings are invalid; repair them before saving a key here.", nil)
	}
	return root, nil
}
