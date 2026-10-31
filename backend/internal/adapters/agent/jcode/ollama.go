package jcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// jcode's keyless Ollama mode (`jcode login ollama` without a key) talks to
// Ollama's OpenAI-compatible endpoint, by default http://localhost:11434/v1.
// It has no account and no credential, so AO can only check what jcode
// depends on: that the Ollama server behind that endpoint answers and has the
// model jcode will use installed. Ollama's native inventory (GET /api/tags)
// is read-only and runs no inference.

const ollamaProbeTimeout = 3 * time.Second

// localEndpointMethod is how `jcode auth status --json` describes a keyless
// local provider. With an API key stored it appends "+ optional API key",
// which does not match: that is a keyed (possibly cloud) route, not this mode.
var localEndpointMethod = regexp.MustCompile("^local endpoint \\(`(https?://[^`]+)`\\)$")

type jcodeProviderStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Method string `json:"method"`
}

// OllamaInventoryURL returns the Ollama /api/tags URL behind jcode's keyless
// local Ollama provider, from `jcode auth status --json` output. It is false
// unless jcode reports Ollama available as a plain local endpoint on a
// loopback host at Ollama's OpenAI-compatible /v1 path.
func OllamaInventoryURL(authStatusJSON []byte) (string, bool) {
	var status struct {
		Providers []jcodeProviderStatus `json:"providers"`
	}
	if json.Unmarshal(authStatusJSON, &status) != nil {
		return "", false
	}
	return ollamaInventoryURL(status.Providers)
}

func ollamaInventoryURL(providers []jcodeProviderStatus) (string, bool) {
	for _, provider := range providers {
		if provider.ID != "ollama" || provider.Status != "available" {
			continue
		}
		match := localEndpointMethod.FindStringSubmatch(strings.TrimSpace(provider.Method))
		if match == nil {
			return "", false
		}
		base, err := url.Parse(match[1])
		if err != nil || base.User != nil || base.RawQuery != "" || base.Fragment != "" || strings.TrimRight(base.Path, "/") != "/v1" {
			return "", false
		}
		if host := base.Hostname(); host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return "", false
		}
		return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/api/tags"}).String(), true
	}
	return "", false
}

// InstalledOllamaModels reads the model names a local Ollama server has
// installed. It follows no redirects and sends no credential.
func InstalledOllamaModels(ctx context.Context, inventoryURL string) ([]string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, ollamaProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, inventoryURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama model inventory: HTTP %d", response.StatusCode)
	}
	var tags struct {
		Models *[]struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&tags); err != nil {
		return nil, fmt.Errorf("ollama model inventory: %w", err)
	}
	if tags.Models == nil {
		return nil, errors.New("ollama model inventory: no models field")
	}
	names := make([]string, 0, 2*len(*tags.Models))
	for _, model := range *tags.Models {
		for _, name := range []string{model.Name, model.Model} {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// OllamaModelInstalled reports whether model is one of the installed names.
// Ollama treats an untagged name as its ":latest" tag.
func OllamaModelInstalled(installed []string, model string) bool {
	want := ollamaTagged(model)
	if want == "" {
		return false
	}
	for _, name := range installed {
		if ollamaTagged(name) == want {
			return true
		}
	}
	return false
}

func ollamaTagged(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if !strings.Contains(name[strings.LastIndex(name, "/")+1:], ":") {
		name += ":latest"
	}
	return name
}
