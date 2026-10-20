package triage

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	chatregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	configuredCatalogVersion = 1
	maxConfiguredCatalogSize = 256 << 10
)

type configuredCatalog struct {
	Version int      `json:"version"`
	Choices []Choice `json:"choices"`
}

// Catalog reads a small configuration snapshot without opening AO's database
// or parsing its daemon configuration. Its first-run discovery reads only known
// native credential evidence, never installation or sign-in commands.
type Catalog struct {
	dataDir string
	mu      sync.Mutex
	labels  map[string]string
	resolve map[string]func(context.Context) (string, error)
	chat    *chatregistry.Registry
	native  func(context.Context) []Choice
}

// NewCatalog builds the daemon-independent configured-agent chooser.
func NewCatalog(dataDir string) *Catalog {
	c := &Catalog{
		dataDir: dataDir, labels: map[string]string{},
		resolve: map[string]func(context.Context) (string, error){},
		chat:    chatregistry.Build(slog.Default(), nil),
	}
	for _, item := range agentregistry.Harnessed() {
		id := string(item.Harness)
		c.labels[id] = item.Manifest.Name
		if resolver, ok := item.Agent.(ports.AgentBinaryResolver); ok {
			c.resolve[id] = resolver.ResolveBinary
		}
		if resolver, ok := item.Agent.(ports.AgentBinaryPresenceResolver); ok {
			c.resolve[id] = resolver.ResolveBinaryPresence
		}
	}
	c.native = nativeConfiguredChoices
	return c
}

// Configure replaces the explicitly configured choices. It never supplements
// their models with the provider's full public model catalog.
func (c *Catalog) Configure(ctx context.Context, choices []Choice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := configuredCatalogWithinLimits(choices); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(c.dataDir) == "" {
		return errors.New("triage data directory is required")
	}
	snapshot := configuredCatalog{Version: configuredCatalogVersion, Choices: c.filter(choices)}
	if err := configuredCatalogWithinLimits(snapshot); err != nil {
		return err
	}
	for _, dir := range []string{c.dataDir, filepath.Join(c.dataDir, "reports"), c.root()} {
		if err := privateDir(dir); err != nil {
			return errors.New("cannot prepare private triage configuration storage")
		}
	}
	if err := atomicPrivateJSON(c.path(), snapshot); err != nil {
		return errors.New("cannot save configured triage choices")
	}
	return nil
}

func (c *Catalog) root() string { return filepath.Join(c.dataDir, "reports", "triage") }
func (c *Catalog) path() string { return filepath.Join(c.root(), "configured.json") }

// Choices uses the configured snapshot, or local native settings on first use.
func (c *Catalog) Choices(ctx context.Context) ([]Choice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(c.dataDir) == "" {
		return nil, errors.New("triage data directory is required")
	}
	snapshot, err := c.load(ctx)
	choices := snapshot.Choices
	if errors.Is(err, os.ErrNotExist) {
		choices = c.native(ctx)
	} else if err != nil {
		return nil, err
	}
	choices = c.filter(choices)
	for index := range choices {
		choice := &choices[index]
		if choice.ModelsMissing {
			choice.Models = []ModelChoice{{ID: "", Label: "Configured default", Default: true}}
			choice.ModelsMissing = false
		}
		choice.Label = c.labels[choice.Harness]
		choice.Available = false
		if len(choice.Models) == 0 {
			choice.UnavailableReason = "No configured models are available for this agent."
			continue
		}
		if _, usesAccounts := domain.AgentAccountProviderFor(domain.AgentHarness(choice.Harness)); usesAccounts && len(choice.Accounts) == 0 {
			choice.UnavailableReason = "Configured account information is unavailable; refresh this agent in AO."
			continue
		}
		if resolve := c.resolve[choice.Harness]; resolve != nil {
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err = resolve(probeCtx)
			cancel()
			choice.Available = err == nil
		}
		if !choice.Available {
			choice.UnavailableReason = "The configured agent executable is unavailable."
		}
	}
	return choices, configuredCatalogWithinLimits(choices)
}

func (c *Catalog) load(ctx context.Context) (configuredCatalog, error) {
	var snapshot configuredCatalog
	for _, dir := range []string{c.dataDir, filepath.Join(c.dataDir, "reports"), c.root()} {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return snapshot, os.ErrNotExist
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return snapshot, errors.New("saved triage configuration directory is unavailable or unsafe")
		}
	}
	if err := readPrivateJSON(c.path(), maxConfiguredCatalogSize, &snapshot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snapshot, os.ErrNotExist
		}
		return configuredCatalog{}, errors.New("saved triage configuration is invalid; refresh configured agents")
	}
	if snapshot.Version != configuredCatalogVersion {
		return configuredCatalog{}, errors.New("saved triage configuration is invalid; refresh configured agents")
	}
	return snapshot, nil
}

func (c *Catalog) filter(choices []Choice) []Choice {
	out := make([]Choice, 0, len(choices))
	seen := map[string]bool{}
	for _, choice := range choices {
		if _, known := c.labels[choice.Harness]; !known || seen[choice.Harness] {
			continue
		}
		config := domain.AgentConfig{Permissions: choice.Permissions, Mode: choice.Mode}
		if config.Validate() != nil || choice.SessionMode != "" && !choice.SessionMode.Valid() {
			continue
		}
		item := Choice{Harness: choice.Harness, Models: []ModelChoice{}, ModelsMissing: choice.ModelsMissing && len(choice.Models) == 0, Permissions: choice.Permissions, Mode: choice.Mode, SessionMode: choice.SessionMode}
		if item.Mode != "" || !c.chat.SupportsChat(domain.AgentHarness(item.Harness)) {
			item.SessionMode = domain.SessionModeTUI
		} else if item.SessionMode == "" || item.Harness == string(domain.HarnessUnreal) {
			item.SessionMode = domain.SessionModeChat
		}
		models := map[string]bool{}
		modelDefaults := 0
		for _, model := range choice.Models {
			if models[model.ID] || !validConfiguredModel(choice.Harness, model.ID) {
				continue
			}
			label := catalogLabel(model.Label, model.ID)
			if model.ID == "" {
				label = "Configured default"
			}
			clean := ModelChoice{ID: model.ID, Label: label, Default: model.Default}
			efforts := map[string]bool{}
			for _, effort := range model.Efforts {
				if validCatalogID(effort, false) && !efforts[effort] {
					clean.Efforts = append(clean.Efforts, effort)
					efforts[effort] = true
				}
			}
			if clean.Default {
				modelDefaults++
			}
			item.Models = append(item.Models, clean)
			models[model.ID] = true
		}
		if modelDefaults > 1 {
			for i := range item.Models {
				item.Models[i].Default = false
			}
		}
		accounts := map[string]bool{}
		accountDefaults := 0
		for _, account := range choice.Accounts {
			if validConfiguredAccount(choice.Harness, account.ID) && !accounts[account.ID] {
				item.Accounts = append(item.Accounts, Account{ID: account.ID, Label: catalogLabel(account.Label, "Saved account"), Default: account.Default})
				accounts[account.ID] = true
				if account.Default {
					accountDefaults++
				}
			}
		}
		if len(choice.Accounts) > 0 && len(item.Accounts) == 0 {
			continue
		}
		if accountDefaults > 1 {
			for i := range item.Accounts {
				item.Accounts[i].Default = false
			}
		}
		out = append(out, item)
		seen[choice.Harness] = true
	}
	return out
}

func validConfiguredModel(harness, id string) bool {
	return (id == "" || validCatalogID(id, true)) && modelcatalog.Accepts(harness, id)
}

func configuredCatalogWithinLimits(value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil || len(raw)+1 > maxConfiguredCatalogSize {
		return errors.New("configured triage choices exceed 256 KiB")
	}
	return nil
}

func validCatalogID(id string, slash bool) bool {
	if id == "" || len(id) > 256 || !utf8.ValidString(id) || strings.HasPrefix(id, "-") || strings.HasPrefix(id, "/") || strings.Contains(id, "..") || Redact(id) != id {
		return false
	}
	for _, r := range id {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_.:@+", r) && (!slash || r != '/') {
			return false
		}
	}
	return true
}

func catalogLabel(label, fallback string) string {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 160 || !utf8.ValidString(label) || Redact(label) != label || strings.ContainsAny(label, "/\\~") {
		return fallback
	}
	if strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return fallback
	}
	return label
}

// ResolveSelection inherits only this configured choice's own saved settings.
// Native model, account and interface choices remain fixed at launch.
func (c *Catalog) ResolveSelection(ctx context.Context, selection Selection) (Selection, map[string]string, error) {
	choices, err := c.Choices(ctx)
	if err != nil {
		return selection, nil, err
	}
	selected, err := configuredSelection(selection, choices)
	if err != nil {
		return selection, nil, err
	}
	if selected.AccountID == "" {
		return selected, map[string]string{}, nil
	}
	env, err := configuredAccountEnv(ctx, c.dataDir, selected.Harness, selected.AccountID)
	return selected, env, err
}

// configuredSelection accepts only the saved harness, model, effort and account.
// An explicit valid permission mode may override that harness's default.
func configuredSelection(selection Selection, choices []Choice) (Selection, error) {
	for _, choice := range choices {
		if choice.Harness != selection.Harness {
			continue
		}
		if !choice.Available {
			return selection, errors.New(choice.UnavailableReason)
		}
		if selection.Mode != "" && selection.Mode != choice.Mode {
			return selection, errors.New("the selected agent mode has changed; choose the agent again")
		}
		if selection.Permissions == "" {
			selection.Permissions = choice.Permissions
		} else if !selection.Permissions.Valid() {
			return selection, errors.New("the selected permission mode is invalid")
		}
		if selection.SessionMode != "" && selection.SessionMode != choice.SessionMode {
			return selection, errors.New("the selected interface has changed; choose the agent again")
		}
		selection.Mode, selection.SessionMode = choice.Mode, choice.SessionMode
		model := slices.IndexFunc(choice.Models, func(model ModelChoice) bool { return model.ID == selection.Model })
		if model < 0 {
			return selection, errors.New("the selected model is not configured for triage")
		}
		if selection.Effort != "" && !slices.Contains(choice.Models[model].Efforts, selection.Effort) {
			return selection, errors.New("the selected effort is not configured for this model")
		}
		if selection.AccountID == "" && len(choice.Accounts) == 0 {
			return selection, nil
		}
		if slices.ContainsFunc(choice.Accounts, func(account Account) bool { return account.ID == selection.AccountID }) {
			return selection, nil
		}
		return selection, errors.New("select a configured account for triage")
	}
	return selection, errors.New("the selected agent is not configured for triage")
}
