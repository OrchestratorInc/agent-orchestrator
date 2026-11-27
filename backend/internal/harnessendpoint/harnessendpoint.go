// Package harnessendpoint stores a user-chosen API endpoint (base URL and API
// key) per harness and maps it onto the environment variables each harness
// reads natively. The registry is generic: a harness gains support by adding
// one Spec naming its own variables. Nothing here special-cases a provider.
//
// Configs live under AO's data dir beside its provider keys (never an
// OS-default app-data location), one owner-only JSON file per harness. The API key is never returned to
// clients; Status exposes only whether one is stored.
package harnessendpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AuthStyle selects which of a harness's credential variables carries the key.
type AuthStyle string

const (
	// AuthBearer sends the key as an Authorization bearer token. Most
	// third-party gateways expect this form.
	AuthBearer AuthStyle = "bearer"
	// AuthAPIKey sends the key in the provider's API-key header.
	AuthAPIKey AuthStyle = "api-key"
)

// Spec names the environment variables one harness reads for a custom endpoint.
type Spec struct {
	AgentID string
	// BaseURLEnv receives the endpoint URL.
	BaseURLEnv string
	// KeyEnv maps each supported auth style to the variable carrying the key.
	KeyEnv map[AuthStyle]string
	// DefaultAuthStyle is used when a request leaves the style empty.
	DefaultAuthStyle AuthStyle
	// ModelEnv optionally receives a model override; empty means unsupported.
	ModelEnv string
	// ClearEnv are variables blanked while the endpoint is in use: competing
	// credentials the harness would prefer over KeyEnv, and switches that route
	// it away from BaseURLEnv. Without this a subscription token from a bound
	// account or the daemon's environment could reach the custom endpoint.
	ClearEnv []string
	// ModelsPath is the endpoint's model listing under the base URL, a
	// read-only GET that needs the key and answers {"data":[{"id":...}]}. AO
	// asks it on save to learn the auth style the endpoint accepts and its
	// models, and when the model picker refreshes. Empty means no listing.
	ModelsPath string
	// ModelsHeaders are sent with the model listing beside the key.
	ModelsHeaders map[string]string
}

// apiVersionSegment is the leading path segment the harness puts before every
// request path, such as "/v1" for ModelsPath "/v1/models", or "" when its
// requests start right under the base URL.
func (s Spec) apiVersionSegment() string {
	segment, rest, ok := strings.Cut(strings.TrimPrefix(s.ModelsPath, "/"), "/")
	if !ok || segment == "" || rest == "" {
		return ""
	}
	return "/" + segment
}

// AuthStyles returns the spec's supported auth styles in stable order.
func (s Spec) AuthStyles() []AuthStyle {
	out := make([]AuthStyle, 0, len(s.KeyEnv))
	for style := range s.KeyEnv {
		out = append(out, style)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// specs is the reviewed registry. Variable names come from each harness's own
// documentation; clients can never supply variable names.
var specs = map[string]Spec{
	"claude-code": {
		AgentID:    "claude-code",
		BaseURLEnv: "ANTHROPIC_BASE_URL",
		KeyEnv: map[AuthStyle]string{ //nolint:gosec // G101: environment variable names, not credentials.
			AuthBearer: "ANTHROPIC_AUTH_TOKEN",
			AuthAPIKey: "ANTHROPIC_API_KEY",
		},
		ClearEnv: []string{ //nolint:gosec // G101: environment variable names, not credentials.
			"CLAUDE_CODE_OAUTH_TOKEN",
			"CLAUDE_CODE_USE_BEDROCK",
			"CLAUDE_CODE_USE_VERTEX",
			"CLAUDE_CODE_USE_FOUNDRY",
		},
		DefaultAuthStyle: AuthBearer,
		ModelEnv:         "ANTHROPIC_MODEL",
		// Claude Code's gateway check lists models the same way.
		ModelsPath:    "/v1/models",
		ModelsHeaders: map[string]string{"anthropic-version": "2023-06-01"},
	},
}

// Lookup returns the spec for agentID.
func Lookup(agentID string) (Spec, bool) {
	spec, ok := specs[agentID]
	return spec, ok
}

// ProbeTarget is what an auth-style probe for a save would use.
type ProbeTarget struct {
	BaseURL string
	Key     string
	// SavedStyle is set when the save keeps the saved base URL and key: the
	// style that already works is kept, so a save that changes only the model
	// never re-probes, and a gateway that is briefly down cannot reset it.
	SavedStyle AuthStyle
	// SavedModel is the saved default model when the save keeps the saved base
	// URL, so editing the key keeps the model the user chose.
	SavedModel string
}

// AgentIDs lists every harness with a custom endpoint spec, sorted.
func AgentIDs() []string {
	ids := make([]string, 0, len(specs))
	for id := range specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// UnusableMessage is the display-safe reason a saved endpoint cannot be used.
// Launches of the harness stop until the user replaces or removes it.
const UnusableMessage = "The saved custom endpoint can't be used. Edit it with a new API key, or remove it."

// Config is one harness's stored endpoint.
type Config struct {
	BaseURL   string    `json:"baseUrl"`
	AuthStyle AuthStyle `json:"authStyle"`
	APIKey    string    `json:"apiKey"`
	Model     string    `json:"model,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Input is a requested change. An empty APIKey keeps the stored key, so the
// UI can edit the URL or model without the key ever round-tripping.
type Input struct {
	BaseURL   string    `json:"baseUrl" description:"Absolute http(s) provider endpoint URL."`
	AuthStyle AuthStyle `json:"authStyle,omitempty" enum:"bearer,api-key" description:"Credential form; defaults per harness."`
	APIKey    string    `json:"apiKey,omitempty" description:"API key; omit to keep the stored key."`
	Model     string    `json:"model,omitempty" description:"Default model. Omit it to keep the saved model or use the first model the endpoint lists."`
}

// Status is the display-safe view of one harness's endpoint support.
type Status struct {
	Configured       bool        `json:"configured"`
	BaseURL          string      `json:"baseUrl,omitempty"`
	AuthStyle        AuthStyle   `json:"authStyle,omitempty" enum:"bearer,api-key"`
	Model            string      `json:"model,omitempty"`
	HasAPIKey        bool        `json:"hasApiKey"`
	AuthStyles       []AuthStyle `json:"authStyles"`
	DefaultAuthStyle AuthStyle   `json:"defaultAuthStyle" enum:"bearer,api-key"`
	Error            string      `json:"error,omitempty"`
	SupportsModel    bool        `json:"supportsModel"`
}

// ErrUnsupported reports a harness without a custom-endpoint spec.
var ErrUnsupported = errors.New("custom endpoint is not supported for this harness")

// ValidationError reports user-correctable input problems.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// Store persists endpoint configs under root/<agentID>/endpoint.json.
type Store struct {
	root string
}

// ForDataDir returns the store under AO's data directory. An empty dataDir
// yields a disabled store rather than one rooted at a relative path.
func ForDataDir(dataDir string) *Store {
	if strings.TrimSpace(dataDir) == "" {
		return NewStore("")
	}
	return NewStore(filepath.Join(dataDir, "harness-endpoints"))
}

// NewStore returns a store rooted at root. A nil or empty-root store reports
// nothing configured and refuses writes.
func NewStore(root string) *Store {
	return &Store{root: strings.TrimSpace(root)}
}

const fileName = "endpoint.json"

func (s *Store) path(agentID string) (string, error) {
	if s == nil || s.root == "" {
		return "", errors.New("harness endpoint store is unavailable")
	}
	if _, ok := specs[agentID]; !ok {
		return "", ErrUnsupported
	}
	return filepath.Join(s.root, agentID, fileName), nil
}

// Load returns the stored config for agentID.
func (s *Store) Load(agentID string) (Config, bool, error) {
	if s == nil || s.root == "" {
		return Config{}, false, nil
	}
	path, err := s.path(agentID)
	if errors.Is(err, ErrUnsupported) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read %s endpoint: %w", agentID, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse %s endpoint: %w", agentID, err)
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		return Config{}, false, fmt.Errorf("parse %s endpoint: base URL and API key are required", agentID)
	}
	return cfg, true, nil
}

// Status returns the display-safe view for agentID. ok is false when the
// harness has no spec.
func (s *Store) Status(agentID string) (Status, bool, error) {
	spec, ok := specs[agentID]
	if !ok {
		return Status{}, false, nil
	}
	status := Status{
		AuthStyles:       spec.AuthStyles(),
		DefaultAuthStyle: spec.DefaultAuthStyle,
		SupportsModel:    spec.ModelEnv != "",
	}
	cfg, found, err := s.Load(agentID)
	if err != nil {
		// Launches stop on this config, so say so: the user can replace it
		// with a new key or remove it.
		status.Error = UnusableMessage
		return status, true, err
	}
	if found {
		status.Configured = true
		status.BaseURL = cfg.BaseURL
		status.AuthStyle = cfg.AuthStyle
		status.Model = cfg.Model
		status.HasAPIKey = true
		if !secureEndpoint(cfg.BaseURL) {
			status.Error = UnusableMessage
		}
	}
	return status, true, nil
}

// ProbeTarget validates in for agentID and returns the base URL and key a save
// would store: the key from in, or the saved one when in leaves it empty. ok
// is false when there is no key to probe with.
func (s *Store) ProbeTarget(agentID string, in Input) (ProbeTarget, bool, error) {
	spec, supported := specs[agentID]
	if !supported {
		return ProbeTarget{}, false, ErrUnsupported
	}
	cfg, err := normalize(spec, in)
	if err != nil {
		return ProbeTarget{}, false, err
	}
	// Load reports nothing saved when the saved file is unreadable too: there
	// is no key to probe with, and Save itself reports the missing key.
	existing, saved, _ := s.Load(agentID)
	sameURL := saved && strings.TrimRight(existing.BaseURL, "/") == cfg.BaseURL
	if cfg.APIKey != "" {
		target := ProbeTarget{BaseURL: cfg.BaseURL, Key: cfg.APIKey}
		if sameURL {
			target.SavedModel = existing.Model
		}
		return target, true, nil
	}
	if !saved {
		return ProbeTarget{}, false, nil
	}
	// The saved key was given for the saved endpoint's host. A new host gets
	// it only when the user types it again, so nothing is sent to it first.
	if !sameOrigin(existing.BaseURL, cfg.BaseURL) {
		return ProbeTarget{}, false, errKeyForNewOrigin()
	}
	target := ProbeTarget{BaseURL: cfg.BaseURL, Key: existing.APIKey}
	if sameURL {
		target.SavedStyle = existing.AuthStyle
		target.SavedModel = existing.Model
	}
	return target, true, nil
}

// pathLocks serializes writes to one endpoint file. Callers build a new Store
// per request, so the lock is keyed by file path, not held on the Store.
var pathLocks sync.Map // map[string]*sync.Mutex

// lockPath locks path for a whole read-modify-write and returns its unlock.
func lockPath(path string) func() {
	key := filepath.Clean(path)
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	mu, _ := pathLocks.LoadOrStore(key, &sync.Mutex{})
	m, ok := mu.(*sync.Mutex)
	if !ok {
		panic("harnessendpoint: path lock is not a *sync.Mutex")
	}
	m.Lock()
	return m.Unlock
}

// Save validates in and stores it for agentID, keeping the stored key when
// in.APIKey is empty.
func (s *Store) Save(agentID string, in Input, now time.Time) (Status, error) {
	spec, ok := specs[agentID]
	if !ok {
		return Status{}, ErrUnsupported
	}
	path, err := s.path(agentID)
	if err != nil {
		return Status{}, err
	}
	// Hold the lock from reading the stored key to writing, so an update that
	// keeps the key cannot write back one a concurrent save just rotated.
	defer lockPath(path)()
	cfg, err := normalize(spec, in)
	if err != nil {
		return Status{}, err
	}
	existing, found, err := s.Load(agentID)
	if err != nil {
		// An unreadable config only blocks an update that must keep its key;
		// a complete replacement with a new key repairs it.
		if cfg.APIKey == "" {
			return Status{}, err
		}
		found = false
	}
	if cfg.APIKey == "" {
		if !found {
			return Status{}, &ValidationError{Message: "An API key is required."}
		}
		// Checked again under the lock: another save may have changed the
		// saved endpoint since the caller probed it.
		if !sameOrigin(existing.BaseURL, cfg.BaseURL) {
			return Status{}, errKeyForNewOrigin()
		}
		cfg.APIKey = existing.APIKey
	}
	cfg.UpdatedAt = now.UTC()
	data, err := json.MarshalIndent(cfg, "", "  ") //nolint:gosec // G117: Config intentionally persists the user's API key to an owner-only 0600 file so launches can pass it to the harness.
	if err != nil {
		return Status{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Status{}, fmt.Errorf("create %s endpoint dir: %w", agentID, err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return Status{}, fmt.Errorf("write %s endpoint: %w", agentID, err)
	}
	status, _, err := s.Status(agentID)
	return status, err
}

// Delete removes agentID's stored endpoint. Deleting a missing config is not
// an error.
func (s *Store) Delete(agentID string) error {
	path, err := s.path(agentID)
	if err != nil {
		return err
	}
	defer lockPath(path)()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s endpoint: %w", agentID, err)
	}
	return nil
}

// Configured reports whether agentID has a usable stored endpoint.
func (s *Store) Configured(agentID string) (bool, error) {
	_, found, err := s.Load(agentID)
	return found, err
}

// Env returns the variables a session of agentID should receive for its
// configured endpoint, or nil when none is configured. The key variable of
// every other auth style and each ClearEnv variable map to "" so a credential
// from the daemon's environment or a bound account is not used instead of, or
// sent alongside, the configured one. Callers
// decide precedence: project configuration should still win.
func (s *Store) Env(agentID string) (map[string]string, error) {
	spec, ok := specs[agentID]
	if !ok {
		return nil, nil
	}
	cfg, found, err := s.Load(agentID)
	if err != nil || !found {
		return nil, err
	}
	if !secureEndpoint(cfg.BaseURL) {
		return nil, fmt.Errorf("%s endpoint would send its key in cleartext", agentID)
	}
	style := cfg.AuthStyle
	if _, ok := spec.KeyEnv[style]; !ok {
		style = spec.DefaultAuthStyle
	}
	env := map[string]string{
		spec.BaseURLEnv:    cfg.BaseURL,
		spec.KeyEnv[style]: cfg.APIKey,
	}
	for other, name := range spec.KeyEnv {
		if other != style {
			env[name] = ""
		}
	}
	for _, name := range spec.ClearEnv {
		env[name] = ""
	}
	if spec.ModelEnv != "" && cfg.Model != "" {
		env[spec.ModelEnv] = cfg.Model
	}
	return env, nil
}

// ErrIncompleteOverride reports a project that points a harness at a different
// endpoint than the one the user saved, without the credential that endpoint
// needs. The launch stops rather than send the saved key, or any other
// credential, to that gateway.
var ErrIncompleteOverride = errors.New("project overrides the custom endpoint's base URL without an API key")

// ProjectEnv is Env for a launch that also carries projectEnv. A base URL and
// its key stay paired. A project URL on the saved endpoint's origin keeps the
// saved key. A project that sets the base URL to another origin (or blanks it)
// gets none of the saved endpoint: when it also sets a key, ProjectEnv returns
// that project gateway and key in the saved endpoint's place, with the other
// key variables and ClearEnv blank, so the caller applies them over any bound
// account's credential; when it sets no key, ProjectEnv returns
// ErrIncompleteOverride and the launch must stop. Callers still let every
// project value win key by key. caseInsensitive matches project keys the way
// Windows environments do.
func (s *Store) ProjectEnv(agentID string, projectEnv map[string]string, caseInsensitive bool) (map[string]string, error) {
	env, err := s.Env(agentID)
	if err != nil || len(env) == 0 {
		return env, err
	}
	spec := specs[agentID]
	lookup := func(name string) (string, bool) {
		for key, value := range projectEnv {
			if key == name || caseInsensitive && strings.EqualFold(key, name) {
				return value, true
			}
		}
		return "", false
	}
	projectURL, set := lookup(spec.BaseURLEnv)
	if !set || sameOrigin(projectURL, env[spec.BaseURLEnv]) {
		return env, nil
	}
	override := map[string]string{spec.BaseURLEnv: projectURL}
	hasKey := false
	for _, name := range spec.KeyEnv {
		value, _ := lookup(name)
		hasKey = hasKey || strings.TrimSpace(value) != ""
		override[name] = value
	}
	if !hasKey {
		return nil, fmt.Errorf("%w: set %s for %s or remove %s from the project", ErrIncompleteOverride, keyEnvNames(spec), agentID, spec.BaseURLEnv)
	}
	for _, name := range spec.ClearEnv {
		override[name] = ""
	}
	if spec.ModelEnv != "" {
		if model, ok := lookup(spec.ModelEnv); ok {
			override[spec.ModelEnv] = model
		}
	}
	return override, nil
}

// errKeyForNewOrigin refuses to move a saved key to another scheme, host or
// port without the user entering it again.
func errKeyForNewOrigin() error {
	return &ValidationError{Message: "Enter the API key again to use it with a different host."}
}

// sameOrigin reports whether two base URLs reach the same scheme, host and
// port, where a key saved for one is safe to send to the other.
func sameOrigin(a, b string) bool {
	left, errA := url.Parse(strings.TrimSpace(a))
	right, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil || left.Host == "" || right.Host == "" {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

func keyEnvNames(spec Spec) string {
	names := make([]string, 0, len(spec.KeyEnv))
	for _, name := range spec.KeyEnv {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, " or ")
}

func normalize(spec Spec, in Input) (Config, error) {
	baseURL := strings.TrimSpace(in.BaseURL)
	apiKey := strings.TrimSpace(in.APIKey)
	model := strings.TrimSpace(in.Model)
	for _, value := range []string{baseURL, apiKey, model} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return Config{}, &ValidationError{Message: "Values must be a single line."}
		}
	}
	if baseURL == "" {
		return Config{}, &ValidationError{Message: "A base URL is required."}
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return Config{}, &ValidationError{Message: "The base URL must be an absolute http:// or https:// URL."}
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return Config{}, &ValidationError{Message: "Use https:// for a remote endpoint. http:// is allowed only for localhost, so the API key never crosses the network in cleartext."}
	}
	if parsed.User != nil {
		return Config{}, &ValidationError{Message: "Put credentials in the API key field, not the base URL."}
	}
	// A harness whose requests start with an API version adds it itself, so a
	// base URL that already ends with it sends every request to a doubled path
	// such as /v1/v1/messages, which no endpoint serves.
	if version := spec.apiVersionSegment(); version != "" && strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), version) {
		return Config{}, &ValidationError{Message: fmt.Sprintf("Remove %s from the end of the base URL. This harness adds %s to every request itself.", version, version)}
	}
	style := in.AuthStyle
	if style == "" {
		style = spec.DefaultAuthStyle
	}
	if _, ok := spec.KeyEnv[style]; !ok {
		return Config{}, &ValidationError{Message: fmt.Sprintf("Unsupported auth style %q.", style)}
	}
	if model != "" && spec.ModelEnv == "" {
		return Config{}, &ValidationError{Message: "This harness does not support a model override here."}
	}
	if strings.ContainsAny(model, " \t") {
		return Config{}, &ValidationError{Message: "The model name must not contain spaces."}
	}
	return Config{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		AuthStyle: style,
		APIKey:    apiKey,
		Model:     model,
	}, nil
}

// secureEndpoint reports whether baseURL keeps a key off the network in
// cleartext: https anywhere, or http to a loopback host.
func secureEndpoint(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return parsed.Host != ""
	case "http":
		return isLoopbackHost(parsed.Hostname())
	}
	return false
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// writeFileAtomic writes data owner-only via a same-directory temp file and
// rename, so a crash cannot leave a truncated config and the key is never
// readable by other users, even briefly (CreateTemp opens files 0600).
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".endpoint-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
