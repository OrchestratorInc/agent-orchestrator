package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// normalizeAdditionalDirectories 先排除主目录已覆盖的真实子目录，再检查外部目录能力，保留符号链接的访问边界。
func normalizeAdditionalDirectories(ctx context.Context, cwd string, directories []string, supported bool) ([]string, error) {
	if len(directories) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, rootErr := os.OpenRoot(cwd)
	if rootErr == nil {
		defer func() { _ = root.Close() }()
	}
	seen := map[string]struct{}{filepath.Clean(cwd): {}}
	out := make([]string, 0, len(directories))
	for _, directory := range directories {
		if !filepath.IsAbs(directory) {
			return nil, fmt.Errorf("additional workspace directory must be absolute, got %q", directory)
		}
		clean := filepath.Clean(directory)
		if _, duplicate := seen[clean]; duplicate {
			continue
		}
		seen[clean] = struct{}{}
		if rel, err := filepath.Rel(cwd, clean); err == nil && filepath.IsLocal(rel) && rootErr == nil {
			if info, err := root.Stat(rel); err == nil && info.IsDir() {
				continue
			}
		}
		out = append(out, clean)
	}
	if len(out) > 0 && !supported {
		return nil, fmt.Errorf("ACP agent does not support additional workspace directories")
	}
	return out, nil
}

func normalizeMCPServers(configs []ports.ChatMCPServerConfig, caps acpsdk.McpCapabilities) ([]acpsdk.McpServer, error) {
	if len(configs) == 0 {
		return []acpsdk.McpServer{}, nil
	}
	servers := make([]acpsdk.McpServer, 0, len(configs))
	for _, config := range configs {
		name := strings.TrimSpace(config.Name)
		if name == "" {
			return nil, fmt.Errorf("ACP MCP server name is required")
		}
		switch strings.ToLower(strings.TrimSpace(config.Type)) {
		case "", "stdio":
			if strings.TrimSpace(config.Command) == "" {
				return nil, fmt.Errorf("ACP MCP server %q requires a command", name)
			}
			servers = append(servers, acpsdk.McpServer{Stdio: &acpsdk.McpServerStdio{
				Name: name, Command: config.Command, Args: config.Args,
				Env: environmentVariables(config.Env),
			}})
		case "http":
			if !caps.Http {
				return nil, fmt.Errorf("ACP agent does not support HTTP MCP server %q", name)
			}
			if strings.TrimSpace(config.URL) == "" {
				return nil, fmt.Errorf("ACP MCP server %q requires a URL", name)
			}
			servers = append(servers, acpsdk.McpServer{Http: &acpsdk.McpServerHttpInline{
				Name: name, Type: "http", Url: config.URL, Headers: httpHeaders(config.Headers),
			}})
		case "sse":
			if !caps.Sse {
				return nil, fmt.Errorf("ACP agent does not support SSE MCP server %q", name)
			}
			if strings.TrimSpace(config.URL) == "" {
				return nil, fmt.Errorf("ACP MCP server %q requires a URL", name)
			}
			servers = append(servers, acpsdk.McpServer{Sse: &acpsdk.McpServerSseInline{
				Name: name, Type: "sse", Url: config.URL, Headers: httpHeaders(config.Headers),
			}})
		default:
			return nil, fmt.Errorf("unsupported ACP MCP transport %q for %q", config.Type, name)
		}
	}
	return servers, nil
}

func environmentVariables(values map[string]string) []acpsdk.EnvVariable {
	keys := sortedMapKeys(values)
	out := make([]acpsdk.EnvVariable, 0, len(keys))
	for _, key := range keys {
		out = append(out, acpsdk.EnvVariable{Name: key, Value: values[key]})
	}
	return out
}

func httpHeaders(values map[string]string) []acpsdk.HttpHeader {
	keys := sortedMapKeys(values)
	out := make([]acpsdk.HttpHeader, 0, len(keys))
	for _, key := range keys {
		out = append(out, acpsdk.HttpHeader{Name: key, Value: values[key]})
	}
	return out
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
