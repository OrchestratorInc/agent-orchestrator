package agentauth

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// saveContinueProvider adds a model to Continue's native config and stores the
// key in its global .env. Continue's IDE extension does not read a shell env;
// both the CLI and IDE resolve ${{ secrets.NAME }} from this file.
func saveContinueProvider(ctx context.Context, envName, modelID, secret string) error {
	provider, ok := continueProviders[envName]
	if !ok {
		return apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "This Continue provider is not supported here.", nil)
	}
	modelID = strings.TrimSpace(modelID)
	if !providerModelID.MatchString(modelID) {
		return apierr.Invalid("AGENT_AUTH_MODEL_INVALID", "Enter a model ID for this Continue provider.", nil)
	}
	if strings.ContainsAny(secret, " \t#\"'\\") {
		return apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "This provider key contains characters that Continue's environment file cannot store safely.", nil)
	}
	home, err := continueConfigHome()
	if err != nil || home == "" {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Continue's configuration home is unavailable.")
	}
	dir := filepath.Join(home, ".continue")
	configPath := filepath.Join(dir, "config.yaml")
	configData, err := readContinueFile(configPath)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if len(configData) != 0 && yaml.Unmarshal(configData, &doc) != nil {
		return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Continue's configuration file is invalid; repair it before saving a key here.", nil)
	}
	root, err := mappingRoot(&doc)
	if err != nil {
		return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Continue's configuration file must be a YAML mapping.", nil)
	}
	if len(configData) == 0 {
		upsertYAMLMappingString(root, "name", "AO Providers")
		upsertYAMLMappingString(root, "version", "0.0.1")
		upsertYAMLMappingString(root, "schema", "v1")
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
		return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Continue's models setting is invalid; repair it before saving a key here.", nil)
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
	if entry == nil {
		entry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		models.Content = append(models.Content, entry)
		upsertYAMLMappingString(entry, "name", modelID)
		upsertYAMLMappingString(entry, "provider", provider)
		upsertYAMLMappingString(entry, "model", modelID)
	}
	upsertYAMLMappingString(entry, "apiKey", "${{ secrets."+envName+" }}")
	if provider == "openrouter" {
		upsertYAMLMappingString(entry, "apiBase", "https://openrouter.ai/api/v1")
	}
	encoded, err := yaml.Marshal(&doc)
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Continue's configuration could not be encoded.")
	}
	envPath := filepath.Join(dir, ".env")
	envData, err := readContinueFile(envPath)
	if err != nil {
		return err
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
	if err := atomicConfigWrite(ctx, dir, envPath, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return err
	}
	return atomicConfigWrite(ctx, dir, configPath, encoded)
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
