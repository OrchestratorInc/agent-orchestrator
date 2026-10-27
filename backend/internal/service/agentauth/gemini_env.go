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

// geminiConfigHome is stubbed in tests; production resolves the user's home.
var geminiConfigHome = os.UserHomeDir

// geminiEnvLine matches GEMINI_API_KEY assignments with an optional `export `,
// per dotenv syntax, while leaving every other variable untouched.
const geminiEnvKey = "GEMINI_API_KEY"

// saveGeminiAPIKey writes the documented user-wide Gemini CLI key store
// (~/.gemini/.env). The file is line-based dotenv: an existing GEMINI_API_KEY
// assignment is replaced in place and every other line is preserved verbatim.
func saveGeminiAPIKey(ctx context.Context, method, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if method != "gemini_api_key" {
		return apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "This harness does not support that UI login method. Use terminal login instead.", nil)
	}
	home, err := geminiConfigHome()
	if err != nil || home == "" {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Gemini's configuration home is unavailable.")
	}
	dir := filepath.Join(home, ".gemini")
	path := filepath.Join(dir, ".env")
	var content strings.Builder
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 64*1024 {
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Gemini's environment file is not a safe regular file.", nil)
		}
		file, err := os.Open(path)
		if err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Gemini's environment file could not be read.")
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			_ = file.Close()
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Gemini's environment file changed while opening it.", nil)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 64*1024+1))
		_ = file.Close()
		if readErr != nil || len(data) > 64*1024 {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Gemini's environment file could not be read.")
		}
		replaced := false
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for _, raw := range lines {
			if isGeminiKeyLine(raw) {
				content.WriteString(geminiEnvKey + "=" + key + "\n")
				replaced = true
				continue
			}
			content.WriteString(raw)
			content.WriteString("\n")
		}
		if !replaced {
			content.WriteString(geminiEnvKey + "=" + key + "\n")
		}
	} else if !os.IsNotExist(err) {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Gemini's environment file could not be inspected.")
	} else {
		content.WriteString(geminiEnvKey + "=" + key + "\n")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Gemini also persists the chosen auth method in settings.json. A key in
	// .env alone does not switch an existing Google sign-in to API-key mode.
	settingsPath := filepath.Join(dir, "settings.json")
	settings, err := geminiAPIKeySettings(settingsPath)
	if err != nil {
		return err
	}
	if err := atomicConfigWrite(ctx, dir, path, []byte(content.String())); err != nil {
		return err
	}
	return atomicConfigWrite(ctx, dir, settingsPath, settings)
}

func geminiAPIKeySettings(path string) ([]byte, error) {
	root := map[string]json.RawMessage{}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > 64*1024 {
			return nil, apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Gemini's settings file is not a safe regular file.", nil)
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Gemini's settings file could not be read.")
		}
		opened, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, opened) {
			_ = file.Close()
			return nil, apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Gemini's settings file changed while opening it.", nil)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 64*1024+1))
		_ = file.Close()
		if readErr != nil || len(data) > 64*1024 {
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Gemini's settings file could not be read.")
		}
		if len(data) > 0 && (json.Unmarshal(data, &root) != nil || root == nil) {
			return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Gemini's settings file is invalid; repair it before saving a key here.", nil)
		}
	} else if !os.IsNotExist(err) {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Gemini's settings file could not be inspected.")
	}
	security := map[string]json.RawMessage{}
	if raw := root["security"]; len(raw) > 0 && (json.Unmarshal(raw, &security) != nil || security == nil) {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Gemini's security settings are invalid; repair them before saving a key here.", nil)
	}
	auth := map[string]json.RawMessage{}
	if raw := security["auth"]; len(raw) > 0 && (json.Unmarshal(raw, &auth) != nil || auth == nil) {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Gemini's auth settings are invalid; repair them before saving a key here.", nil)
	}
	if raw := auth["enforcedType"]; len(raw) > 0 {
		var enforced string
		if json.Unmarshal(raw, &enforced) != nil {
			return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Gemini's enforced auth type is invalid; repair it before saving a key here.", nil)
		}
		if enforced != "" && enforced != "gemini-api-key" {
			return nil, apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "Gemini's settings enforce another authentication method.", nil)
		}
	}
	auth["selectedType"] = json.RawMessage(`"gemini-api-key"`)
	security["auth"], _ = json.Marshal(auth)
	root["security"], _ = json.Marshal(security)
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, apierr.Internal("AGENT_AUTH_STORE_UNAVAILABLE", "Gemini's settings could not be encoded.")
	}
	return append(data, '\n'), nil
}

func isGeminiKeyLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "export ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
	}
	return strings.HasPrefix(trimmed, geminiEnvKey+"=") || strings.HasPrefix(trimmed, geminiEnvKey+" =")
}
