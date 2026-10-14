package modelcatalog

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func init() {
	browseSupportedProviders["cline"] = func(_ context.Context, request ports.AgentModelDiscoveryRequest, _ []string) []string {
		return clineSupportedProviders(request.Binary)
	}
	browseSupportedProviders["continue"] = func(_ context.Context, request ports.AgentModelDiscoveryRequest, _ []string) []string {
		return continueSupportedProviders(request.Binary)
	}
}

// bundleReadLimit bounds a harness bundle read; Continue's CLI bundle is
// about 13 MiB.
const bundleReadLimit = 64 << 20

var (
	clineProviderEnumPattern       = regexp.MustCompile(`(?s)enum BUILT_IN_PROVIDER\s*\{(.*?)\}`)
	clineGeneratedProvidersPattern = regexp.MustCompile(`(?s)GENERATED_PROVIDER_IDS:\s*readonly\s*\[(.*?)\]`)
	quotedValuePattern             = regexp.MustCompile(`"([^"\s]+)"`)
	continueProviderSwitchPattern  = regexp.MustCompile(`switch\([A-Za-z_$][\w$]*\.provider\)\{`)
	continueCaseLabelPattern       = regexp.MustCompile(`case"([^"]+)":`)
)

// clineSupportedProviders reads Cline's built-in provider registry from the
// type declarations of the @cline/llms package installed with the CLI: its
// handwritten BUILT_IN_PROVIDER enum plus the models.dev-generated IDs.
func clineSupportedProviders(binary string) []string {
	root := resolvedPackageRoot(binary)
	if root == "" {
		return nil
	}
	for _, dir := range []string{
		filepath.Join(root, "node_modules", "@cline", "llms", "dist", "providers"),
		filepath.Join(filepath.Dir(root), "@cline", "llms", "dist", "providers"),
	} {
		enum, err := readBundle(filepath.Join(dir, "ids.d.ts"))
		if err != nil {
			continue
		}
		var providers []string
		if match := clineProviderEnumPattern.FindSubmatch(enum); match != nil {
			providers = append(providers, quotedValues(match[1])...)
		}
		if generated, err := readBundle(filepath.Join(dir, "provider-ids.generated.d.ts")); err == nil {
			if match := clineGeneratedProvidersPattern.FindSubmatch(generated); match != nil {
				providers = append(providers, quotedValues(match[1])...)
			}
		}
		return uniqueProviders(providers)
	}
	return nil
}

// continueSupportedProviders reads the provider names Continue's CLI can
// construct a model for: the case labels of its `switch(x.provider)` in the
// bundled dist/index.js, without the test-only "mock" provider.
func continueSupportedProviders(binary string) []string {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	bundle, err := readBundle(filepath.Join(filepath.Dir(binary), "index.js"))
	if err != nil {
		return nil
	}
	var best []string
	for _, loc := range continueProviderSwitchPattern.FindAllIndex(bundle, -1) {
		body := switchBody(bundle[loc[1]:])
		var labels []string
		for _, match := range continueCaseLabelPattern.FindAllSubmatch(body, -1) {
			if label := string(match[1]); label != "mock" {
				labels = append(labels, label)
			}
		}
		if len(labels) > len(best) {
			best = labels
		}
	}
	return uniqueProviders(best)
}

// resolvedPackageRoot returns the npm package directory of a launcher at
// <package>/bin/<name>.
func resolvedPackageRoot(binary string) string {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	return filepath.Dir(filepath.Dir(binary))
}

// switchBody returns the source up to the brace that closes a switch whose
// opening brace was just consumed.
func switchBody(source []byte) []byte {
	depth := 1
	for i, c := range source {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[:i]
			}
		}
	}
	return nil
}

func quotedValues(source []byte) []string {
	matches := quotedValuePattern.FindAllSubmatch(source, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, string(match[1]))
	}
	return values
}

func readBundle(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // path is derived from the adapter-resolved binary
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, bundleReadLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > bundleReadLimit {
		return nil, io.ErrShortBuffer
	}
	return raw, nil
}
