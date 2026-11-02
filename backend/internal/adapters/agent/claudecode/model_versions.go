package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Model version gating.
//
// The provider's model list says what the account may use, not what the
// installed Claude Code can drive. Anthropic enforces the second half itself:
// a too-old CLI asking for a newer model gets a 400 whose error_code is
// claude_code_version_too_old and whose message names the minimum version.
// AO learns that minimum the first time a turn hits it, persists it per model,
// and from then on marks the model unavailable in the picker until the
// installed CLI is new enough. Not every newer model is gated (Sonnet 5.5 runs
// on 2.0.67), so AO never infers a requirement it has not been told.

const versionTooOldErrorCode = "claude_code_version_too_old"

var (
	cliVersionPattern    = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)
	versionTooOldPattern = regexp.MustCompile(
		`Claude Code (\d+\.\d+\.\d+) does not support this model; version (\d+\.\d+\.\d+) or newer is required`)
)

// knownModelMinVersions seeds requirements AO has observed from the API, so a
// user on an old CLI sees the model disabled before their first failed turn.
// Learned requirements take precedence.
var knownModelMinVersions = map[string]string{
	"claude-opus-5-5":  "2.1.280",
	"claude-fable-5-1": "2.1.251",
}

// VersionTooOld is the parsed claude_code_version_too_old rejection.
type VersionTooOld struct {
	Installed string
	Required  string
}

// ParseVersionTooOld recognizes the API's rejection of a model the installed
// Claude Code is too old to use, in either the raw "API Error: 400 {json}"
// form or the plain message form.
func ParseVersionTooOld(text string) (VersionTooOld, bool) {
	match := versionTooOldPattern.FindStringSubmatch(text)
	if len(match) != 3 {
		return VersionTooOld{}, false
	}
	return VersionTooOld{Installed: match[1], Required: match[2]}, true
}

// IsVersionTooOld reports whether text carries the rejection's error code
// even when the message wording does not match.
func IsVersionTooOld(text string) bool {
	return strings.Contains(text, versionTooOldErrorCode)
}

type cliVersion [3]int

func parseCLIVersion(text string) (cliVersion, bool) {
	match := cliVersionPattern.FindStringSubmatch(text)
	if len(match) != 4 {
		return cliVersion{}, false
	}
	var parsed cliVersion
	for i := range parsed {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return cliVersion{}, false
		}
		parsed[i] = value
	}
	return parsed, true
}

func (v cliVersion) less(other cliVersion) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}

// ModelVersionRequirements stores the minimum Claude Code version per model ID.
// It is safe for concurrent use. An empty path keeps requirements in memory.
type ModelVersionRequirements struct {
	mu      sync.Mutex
	path    string
	learned map[string]string
}

// NewModelVersionRequirements loads requirements persisted at path. A missing
// or unreadable file starts empty: the API will teach AO again.
func NewModelVersionRequirements(path string) *ModelVersionRequirements {
	r := &ModelVersionRequirements{path: path, learned: map[string]string{}}
	if path == "" {
		return r
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return r
	}
	var stored map[string]string
	if json.Unmarshal(data, &stored) != nil {
		return r
	}
	for model, version := range stored {
		if _, ok := parseCLIVersion(version); ok && strings.TrimSpace(model) != "" {
			r.learned[model] = version
		}
	}
	return r
}

// Record stores the minimum version the API reported for model.
func (r *ModelVersionRequirements) Record(model, required string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("claude-code: record model version: empty model")
	}
	if _, ok := parseCLIVersion(required); !ok {
		return fmt.Errorf("claude-code: record model version: invalid version %q", required)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.learned[model] == required {
		return nil
	}
	r.learned[model] = required
	if r.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(r.learned, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("claude-code: record model version: %w", err)
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("claude-code: record model version: %w", err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		return fmt.Errorf("claude-code: record model version: %w", err)
	}
	return nil
}

// Required returns the minimum Claude Code version known for model.
func (r *ModelVersionRequirements) Required(model string) (string, bool) {
	model = strings.TrimSpace(model)
	r.mu.Lock()
	version, ok := r.learned[model]
	r.mu.Unlock()
	if ok {
		return version, true
	}
	version, ok = knownModelMinVersions[model]
	return version, ok
}

// GateCatalog marks models whose known minimum version is newer than
// installed. An unknown installed version gates nothing: AO only disables a
// model when it can prove the CLI is too old.
func (r *ModelVersionRequirements) GateCatalog(catalog ports.AgentModelCatalog, installed string) ports.AgentModelCatalog {
	current, ok := parseCLIVersion(installed)
	if !ok || len(catalog.Models) == 0 {
		return catalog
	}
	models := make([]ports.AgentModelInfo, len(catalog.Models))
	copy(models, catalog.Models)
	for i := range models {
		models[i].RequiresVersion = ""
		models[i].UnavailableReason = ""
		required, gated := r.tooNew(models[i].ID, current)
		if !gated {
			continue
		}
		models[i].RequiresVersion = required
		models[i].UnavailableReason = VersionTooOldMessage(installed, required)
		// A disabled model must not be what a picker preselects.
		models[i].IsDefault = false
	}
	catalog.Models = models
	return catalog
}

// tooNew reports the known minimum version for model when current predates it.
// A context variant ("claude-opus-5-5[1m]") needs at least what its base model
// needs, and may have learned a higher minimum of its own, so the higher wins.
func (r *ModelVersionRequirements) tooNew(model string, current cliVersion) (string, bool) {
	required, minimum, known := "", cliVersion{}, false
	candidates := []string{model}
	if base, _, variant := strings.Cut(model, "["); variant {
		candidates = append(candidates, base)
	}
	for _, candidate := range candidates {
		version, ok := r.Required(candidate)
		if !ok {
			continue
		}
		parsed, valid := parseCLIVersion(version)
		if valid && (!known || minimum.less(parsed)) {
			required, minimum, known = version, parsed, true
		}
	}
	if !known || !current.less(minimum) {
		return "", false
	}
	return required, true
}

// UnsupportedModelReason explains why the Claude Code at binary cannot use
// model, or returns "" when it can or AO cannot tell. Family aliases are never
// gated: each CLI version resolves them to a model it supports.
func UnsupportedModelReason(ctx context.Context, binary, model string) string {
	installed, ok := InstalledVersion(ctx, binary)
	if !ok {
		return ""
	}
	current, ok := parseCLIVersion(installed)
	if !ok {
		return ""
	}
	required, gated := DefaultModelVersionRequirements().tooNew(strings.TrimSpace(model), current)
	if !gated {
		return ""
	}
	return VersionTooOldMessage(installed, required)
}

// VersionTooOldMessage is the user-facing explanation and remedy.
func VersionTooOldMessage(installed, required string) string {
	if strings.TrimSpace(installed) == "" {
		return fmt.Sprintf("Requires Claude Code %s or newer. Run `claude update`, then try again.", required)
	}
	return fmt.Sprintf("Requires Claude Code %s or newer (installed: %s). Run `claude update`, then try again.", required, installed)
}

var (
	defaultRequirementsMu sync.RWMutex
	defaultRequirements   = NewModelVersionRequirements("")
)

// ConfigureModelVersionRequirements points the process-wide requirement store
// at a file under AO's state directory. The daemon calls it once at startup.
func ConfigureModelVersionRequirements(path string) {
	defaultRequirementsMu.Lock()
	defaultRequirements = NewModelVersionRequirements(path)
	defaultRequirementsMu.Unlock()
}

// DefaultModelVersionRequirements returns the process-wide requirement store.
func DefaultModelVersionRequirements() *ModelVersionRequirements {
	defaultRequirementsMu.RLock()
	defer defaultRequirementsMu.RUnlock()
	return defaultRequirements
}

// GateModelCatalog applies the process-wide requirements to a Claude catalog
// using the version of the binary that will serve it.
func GateModelCatalog(ctx context.Context, binary string, catalog ports.AgentModelCatalog) ports.AgentModelCatalog {
	installed, ok := InstalledVersion(ctx, binary)
	if !ok {
		return catalog
	}
	return DefaultModelVersionRequirements().GateCatalog(catalog, installed)
}

type installedVersionEntry struct {
	fingerprint string
	version     string
}

var (
	installedVersionMu    sync.Mutex
	installedVersionCache = map[string]installedVersionEntry{}
)

// claudeVersionCommand is replaced in tests.
var claudeVersionCommand = func(ctx context.Context, binary string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, binary, "--version").Output()
}

// InstalledVersion returns the semantic version of the Claude Code binary.
// The result is cached by the executable's path, size and mtime, so a
// `claude update` is noticed on the next call without re-running the CLI on
// every catalog read.
func InstalledVersion(ctx context.Context, binary string) (string, bool) {
	binary = strings.TrimSpace(binary)
	if binary == "" || ctx.Err() != nil {
		return "", false
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return "", false
	}
	if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = evaluated
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	fingerprint := fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	installedVersionMu.Lock()
	entry, hit := installedVersionCache[resolved]
	installedVersionMu.Unlock()
	if hit && entry.fingerprint == fingerprint {
		return entry.version, entry.version != ""
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := claudeVersionCommand(probeCtx, resolved)
	version := ""
	if err == nil {
		if parsed, ok := parseCLIVersion(string(output)); ok {
			version = fmt.Sprintf("%d.%d.%d", parsed[0], parsed[1], parsed[2])
		}
	}
	if err != nil && probeCtx.Err() != nil {
		// A timeout is not an answer; try again next time.
		return "", false
	}
	installedVersionMu.Lock()
	installedVersionCache[resolved] = installedVersionEntry{fingerprint: fingerprint, version: version}
	installedVersionMu.Unlock()
	return version, version != ""
}
