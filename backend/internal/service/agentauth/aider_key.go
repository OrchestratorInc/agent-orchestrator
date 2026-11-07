package agentauth

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// aiderConfigHome is stubbed in tests; production resolves the user's home.
var aiderConfigHome = os.UserHomeDir

// saveAiderAPIKey writes Aider's documented ~/.aider.conf.yml key entries.
// OpenAI and Anthropic use their dedicated top-level options; every other
// provider uses the documented `api-key` list of `provider=key` entries, which
// Aider translates to the provider's environment variable. Existing settings,
// comments, and unrelated entries are preserved through a YAML node round-trip.
func saveAiderAPIKey(ctx context.Context, method, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	provider, ok := aiderProvider(method)
	if !ok {
		return apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "This harness does not support that UI login method. Use terminal login instead.", nil)
	}
	home, err := aiderConfigHome()
	if err != nil || home == "" {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Aider's configuration home is unavailable.")
	}
	path := filepath.Join(home, ".aider.conf.yml")
	var doc yaml.Node
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 64*1024 {
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Aider's configuration file is not a safe regular file.", nil)
		}
		file, err := os.Open(path)
		if err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Aider's configuration file could not be read.")
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			_ = file.Close()
			return apierr.Invalid("AGENT_AUTH_STORE_UNSAFE", "Aider's configuration file changed while opening it.", nil)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 64*1024+1))
		_ = file.Close()
		if readErr != nil || len(data) > 64*1024 {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Aider's configuration file could not be read.")
		}
		if len(data) > 0 {
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Aider's configuration file is invalid; repair it before saving a key here.", nil)
			}
		}
	} else if !os.IsNotExist(err) {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "Aider's configuration file could not be inspected.")
	}
	root, err := mappingRoot(&doc)
	if err != nil {
		return err
	}
	if provider == "openai" {
		upsertYAMLMappingString(root, "openai-api-key", key)
	} else if provider == "anthropic" {
		upsertYAMLMappingString(root, "anthropic-api-key", key)
	} else if err := upsertYAMLProviderKey(root, provider, key); err != nil {
		return err
	}
	data, err := yaml.Marshal(&doc)
	if err != nil {
		return apierr.Internal("AGENT_AUTH_STORE_UNAVAILABLE", "Aider's configuration could not be encoded.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return atomicConfigWrite(ctx, filepath.Dir(path), path, data)
}

func aiderProvider(method string) (string, bool) {
	switch method {
	case "aider_openai_key":
		return "openai", true
	case "aider_anthropic_key":
		return "anthropic", true
	case "aider_openrouter_key":
		return "openrouter", true
	case "aider_deepseek_key":
		return "deepseek", true
	case "aider_gemini_key":
		return "gemini", true
	}
	return "", false
}

// mappingRoot returns the document's mapping node. Missing or empty documents
// get a fresh mapping so a first key save starts a valid file; any other root
// shape is rejected rather than rewritten.
func mappingRoot(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 || len(doc.Content) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{root}
		return root, nil
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		return doc.Content[0], nil
	}
	return nil, apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Aider's configuration file is invalid; repair it before saving a key here.", nil)
}

func upsertYAMLMappingString(root *yaml.Node, key, value string) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// upsertYAMLProviderKey maintains Aider's documented `api-key` list of
// `provider=key` entries, replacing only the chosen provider's entry.
func upsertYAMLProviderKey(root *yaml.Node, provider, key string) error {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "api-key" {
			continue
		}
		list := root.Content[i+1]
		switch list.Kind {
		case yaml.SequenceNode:
			entry := provider + "=" + key
			for j, item := range list.Content {
				if providerOfYAMLKeyEntry(item.Value) == provider {
					list.Content[j] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry}
					return nil
				}
			}
			list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry})
			return nil
		case yaml.ScalarNode:
			// A single provider string is valid YAML for the list option;
			// widen it to a list that keeps the existing entry.
			list.Kind = yaml.SequenceNode
			list.Tag = "!!seq"
			existing := list.Value
			list.Value = ""
			if existing != "" {
				list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: existing})
			}
			if providerOfYAMLKeyEntry(existing) == provider {
				list.Content[len(list.Content)-1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: provider + "=" + key}
				return nil
			}
			list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: provider + "=" + key})
			return nil
		default:
			return apierr.Invalid("AGENT_AUTH_STORE_INVALID", "Aider's api-key setting is invalid; repair it before saving a key here.", nil)
		}
	}
	list := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: provider + "=" + key}}}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "api-key"},
		list)
	return nil
}

// providerOfYAMLKeyEntry splits Aider's documented `provider=key` form on the
// first equals sign so keys containing '=' stay intact.
func providerOfYAMLKeyEntry(entry string) string {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i]
		}
	}
	return ""
}
