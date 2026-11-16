package openinterpreter

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// OpenAIProvider is Open Interpreter's default provider, authorized by native
// sign-in rather than an environment key.
const OpenAIProvider = "openai"

// GeneratedProvider is one of Open Interpreter's built-in model providers that
// reads its credential from an environment variable. Native sign-in covers
// only the default OpenAI provider; these are selected with
// `-c model_provider=<ID>`.
type GeneratedProvider struct {
	ID     string
	Label  string
	EnvKey string
	// ChatWire marks providers on the Chat Completions wire. The inspected
	// macOS release rejects them in every launch mode ("non-Responses wire
	// APIs require the compatibility transport"); the released Linux build
	// serves them, as the conformance job's chat provider shows.
	ChatWire bool
}

// GeneratedProviders lists the providers whose keys AO offers for Open
// Interpreter, in the order AO shows them. IDs are the native provider names
// accepted by `interpreter debug models -c model_provider=<ID>`; wire APIs are
// those of the native provider table in rust-v0.0.56.
var GeneratedProviders = []GeneratedProvider{
	{ID: "anthropic", Label: "Anthropic", EnvKey: "ANTHROPIC_API_KEY"},
	{ID: "google", Label: "Gemini", EnvKey: "GEMINI_API_KEY", ChatWire: true},
	{ID: "openrouter", Label: "OpenRouter", EnvKey: "OPENROUTER_API_KEY", ChatWire: true},
	{ID: "deepseek", Label: "DeepSeek", EnvKey: "DEEPSEEK_API_KEY", ChatWire: true},
	{ID: "zai", Label: "Z.AI", EnvKey: "ZAI_API_KEY", ChatWire: true},
}

// chatWireSupported reports whether this platform's release can run Chat
// Completions providers.
var chatWireSupported = runtime.GOOS == "linux"

// Usable reports whether provider can run on this platform.
func (provider GeneratedProvider) Usable() bool {
	return !provider.ChatWire || chatWireSupported
}

// providerModelSeparator joins a provider ID and its native model slug in an
// AO model ID. Slugs may contain "/" (OpenRouter), so ":" is used, and only a
// known provider ID before the first ":" selects a provider.
const providerModelSeparator = ":"

// ProviderModelID is the AO model ID for slug on provider.
func ProviderModelID(provider, slug string) string {
	return provider + providerModelSeparator + slug
}

// SplitProviderModel separates an AO model ID into a provider and its native
// slug. An ID without a known provider prefix belongs to the provider the
// native configuration selects and is returned unchanged with ok false.
func SplitProviderModel(model string) (provider, slug string, ok bool) {
	prefix, rest, found := strings.Cut(model, providerModelSeparator)
	if !found || strings.TrimSpace(rest) == "" {
		return "", model, false
	}
	if prefix == OpenAIProvider {
		return prefix, rest, true
	}
	for _, candidate := range GeneratedProviders {
		if prefix == candidate.ID {
			return prefix, rest, true
		}
	}
	return "", model, false
}

// systemConfigPath is the native system configuration layer, which sits below
// the user layer (core config loader in the pinned release).
var systemConfigPath = defaultSystemConfigPath()

// SystemConfigPath is the native system configuration file ConfiguredProvider
// reads below the user layer.
func SystemConfigPath() string { return systemConfigPath }

func defaultSystemConfigPath() string {
	if runtime.GOOS != "windows" {
		return "/etc/codex/config.toml"
	}
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "OpenAI", "Codex", "config.toml")
}

// ConfiguredProvider reports the provider native configuration selects: the
// user configuration in home, else the system configuration, else
// OpenAIProvider. Project layers cannot set model_provider. ok is false when
// the selection cannot be read exactly (a named profile, or an unreadable or
// malformed file), because those need native resolution.
func ConfiguredProvider(home string) (provider string, ok bool) {
	for _, path := range []string{filepath.Join(home, "config.toml"), systemConfigPath} {
		provider, ok := providerSelection(path)
		if !ok {
			return "", false
		}
		if provider != "" {
			return provider, true
		}
	}
	return OpenAIProvider, true
}

// providerSelection reads one configuration layer's model_provider: "" when
// the file is absent or names none, ok false when it cannot be read exactly.
func providerSelection(path string) (provider string, ok bool) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", true
	}
	const maxConfigBytes = 1 << 20
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxConfigBytes {
		return "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(data) > maxConfigBytes {
		return "", false
	}
	var config struct {
		Profile       string `toml:"profile"`
		ModelProvider string `toml:"model_provider"`
	}
	if toml.Unmarshal(data, &config) != nil || config.Profile != "" {
		return "", false
	}
	return config.ModelProvider, true
}

func generatedProviderKeyNames() []string {
	names := make([]string, 0, len(GeneratedProviders))
	for _, provider := range GeneratedProviders {
		if provider.Usable() {
			names = append(names, provider.EnvKey)
		}
	}
	return names
}
