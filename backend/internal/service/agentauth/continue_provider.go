package agentauth

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var continueConfigHome = os.UserHomeDir

var continueProviders = map[string]string{
	"ANTHROPIC_API_KEY":  "anthropic",
	"OPENAI_API_KEY":     "openai",
	"GEMINI_API_KEY":     "gemini",
	"OPENROUTER_API_KEY": "openrouter",
	"DEEPSEEK_API_KEY":   "deepseek",
}

// saveContinueProviderUndoable adds a model to Continue's native config and
// stores the key in its global .env. Continue's IDE extension does not read
// a shell env; both the CLI and IDE resolve ${{ secrets.NAME }} from this
// file. The returned function restores Continue's .env and config.yaml as
// they were, for a caller whose later step fails.
func saveContinueProviderUndoable(ctx context.Context, envName, modelID, secret string) (func(), error) {
	provider, ok := continueProviders[envName]
	if !ok {
		return nil, apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "This Continue provider is not supported here.", nil)
	}
	modelID = strings.TrimSpace(modelID)
	if !providerModelID.MatchString(modelID) {
		return nil, apierr.Invalid("AGENT_AUTH_MODEL_INVALID", "Enter a model ID for this Continue provider.", nil)
	}
	if strings.ContainsAny(secret, " \t#\"'\\") {
		return nil, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "This provider key contains characters that Continue's environment file cannot store safely.", nil)
	}
	dir, err := continueGlobalDir()
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(dir, "config.yaml")
	configData, err := readContinueFile(configPath)
	if err != nil {
		return nil, err
	}
	if configData == nil {
		// Continue loads config.yaml whenever it exists and falls back to the
		// legacy config.json only without it, so creating one would hide
		// every model, context provider and rule configured there.
		if _, err := os.Lstat(filepath.Join(dir, "config.json")); err == nil {
			return nil, apierr.Invalid("AGENT_AUTH_STORE_LEGACY", "Continue is set up with config.json. Move to config.yaml in Continue first, then save the key here.", nil)
		} else if !os.IsNotExist(err) {
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Continue's configuration file could not be inspected.")
		}
	}
	var doc yaml.Node
	if len(configData) != 0 && yaml.Unmarshal(configData, &doc) != nil {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Continue's configuration file is invalid; repair it before saving a key here.", nil)
	}
	root, err := mappingRoot(&doc)
	if err != nil {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Continue's configuration file must be a YAML mapping.", nil)
	}
	if len(configData) == 0 {
		for _, field := range [][2]string{{"name", "AO Providers"}, {"version", "0.0.1"}, {"schema", "v1"}} {
			if err := upsertYAMLMappingString(root, field[0], field[1]); err != nil {
				return nil, err
			}
		}
	}
	var models *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "models" {
			models = root.Content[i+1]
			break
		}
	}
	if models == nil {
		models = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "models"}, models)
	}
	if models.Kind != yaml.SequenceNode {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Continue's models setting is invalid; repair it before saving a key here.", nil)
	}
	var entry *yaml.Node
	for _, item := range models.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		var existingProvider, existingModel string
		for i := 0; i+1 < len(item.Content); i += 2 {
			switch item.Content[i].Value {
			case "provider":
				existingProvider = item.Content[i+1].Value
			case "model":
				existingModel = item.Content[i+1].Value
			}
		}
		if existingProvider == provider && existingModel == modelID {
			entry = item
			break
		}
	}
	fields := [][2]string{{"apiKey", "${{ secrets." + envName + " }}"}}
	if entry == nil {
		entry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		models.Content = append(models.Content, entry)
		fields = append([][2]string{{"name", modelID}, {"provider", provider}, {"model", modelID}}, fields...)
	}
	if provider == "openrouter" {
		fields = append(fields, [2]string{"apiBase", "https://openrouter.ai/api/v1"})
	}
	for _, field := range fields {
		if err := upsertYAMLMappingString(entry, field[0], field[1]); err != nil {
			return nil, err
		}
	}
	encoded, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Continue's configuration could not be encoded.")
	}
	envPath := filepath.Join(dir, ".env")
	envData, err := readContinueFile(envPath)
	if err != nil {
		return nil, err
	}
	var lines []string
	replaced := false
	for _, line := range strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(envData), "\r\n", "\n"), "\n"), "\n") {
		if line == "" && len(envData) == 0 {
			continue
		}
		if isContinueKeyLine(line, envName) {
			if !replaced {
				lines = append(lines, envName+"="+secret)
				replaced = true
			}
			continue
		}
		lines = append(lines, line)
	}
	if !replaced {
		lines = append(lines, envName+"="+secret)
	}
	// Restoring puts each file back as it was, or removes one this save
	// created, so a failed save leaves the earlier credential in place.
	restore := func(path string, original []byte) {
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if original == nil {
			_ = os.Remove(path)
			return
		}
		_ = atomicConfigWrite(restoreCtx, dir, path, original)
	}
	if err := atomicConfigWrite(ctx, dir, envPath, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return nil, err
	}
	if err := atomicConfigWrite(ctx, dir, configPath, encoded); err != nil {
		restore(envPath, envData)
		return nil, err
	}
	return func() {
		restore(configPath, configData)
		restore(envPath, envData)
	}, nil
}

// continueGlobalDir is the folder Continue reads its global config.yaml and
// .env from: CONTINUE_GLOBAL_DIR when set, as the Continue CLI and AO's model
// catalog honour it, else ~/.continue.
func continueGlobalDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CONTINUE_GLOBAL_DIR")); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "CONTINUE_GLOBAL_DIR must be an absolute path.", nil)
		}
		return dir, nil
	}
	home, err := continueConfigHome()
	if err != nil || home == "" {
		return "", apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Continue's configuration home is unavailable.")
	}
	return filepath.Join(home, ".continue"), nil
}

func isContinueKeyLine(line, name string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "export ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
	}
	return strings.HasPrefix(trimmed, name+"=") || strings.HasPrefix(trimmed, name+" =")
}

func readContinueFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Continue's configuration file could not be inspected.")
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Continue's configuration file is not a safe regular file.", nil)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Continue's configuration file could not be read.")
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Continue's configuration file changed while opening it.", nil)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 64*1024+1))
	_ = file.Close()
	if readErr != nil || len(data) > 64*1024 {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Continue's configuration file could not be read.")
	}
	return data, nil
}
