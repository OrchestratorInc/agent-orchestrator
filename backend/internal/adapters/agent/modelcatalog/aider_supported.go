package modelcatalog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func init() { browseSupportedProviders["aider"] = aiderSupportedProviders }

const aiderProviderListTimeout = 30 * time.Second

// aiderProviderListScript prints LiteLLM's provider list, which is every
// provider Aider can route a model to. It reads the module only and makes no
// network or provider call.
const aiderProviderListScript = `import json, litellm
print(json.dumps([getattr(p, "value", p) for p in litellm.provider_list]))`

// aiderSupportedProviders asks the LiteLLM installed beside Aider for its
// provider list, using the interpreter Aider's own launcher names. A provider
// whose models the catalog already groups under a qualified name (LiteLLM's
// "vertex_ai" models arrive as "vertex_ai-language-models" and similar) is
// not listed a second time.
func aiderSupportedProviders(ctx context.Context, request ports.AgentModelDiscoveryRequest, shown []string) []string {
	python := aiderInterpreter(request.Binary)
	if python == "" {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, aiderProviderListTimeout)
	defer cancel()
	output, err := modelCommand(runCtx, python, []string{"-I", "-c", aiderProviderListScript}, "", request.Env).Output()
	if err != nil {
		return nil
	}
	return aiderProviderList(output, shown)
}

// aiderInterpreter returns the Python interpreter from the shebang of Aider's
// launcher script, or "" when the launcher is not a Python script.
func aiderInterpreter(binary string) string {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	file, err := os.Open(binary) //nolint:gosec // adapter-resolved binary
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) != 1 || !strings.Contains(filepath.Base(fields[0]), "python") || !filepath.IsAbs(fields[0]) {
		return ""
	}
	return fields[0]
}

func aiderProviderList(output []byte, shown []string) []string {
	var providers []string
	// LiteLLM can print notices before the list; the list is the last line.
	lines := bytes.Split(bytes.TrimSpace(output), []byte("\n"))
	if json.Unmarshal(lines[len(lines)-1], &providers) != nil {
		return nil
	}
	var out []string
	for _, provider := range providers {
		provider = strings.TrimSpace(provider)
		if provider == "" || groupedUnderQualifiedName(provider, shown) {
			continue
		}
		out = append(out, provider)
	}
	return out
}

func groupedUnderQualifiedName(provider string, shown []string) bool {
	for _, group := range shown {
		if group != provider && strings.HasPrefix(group, provider+"-") {
			return true
		}
	}
	return false
}
