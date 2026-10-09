package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A login detected by any readiness check — AO's login terminal, the agent's
// own CLI, or a renewed token — must reach caches built while signed out.
func TestReadinessReportsRecoveredAuthenticationOnce(t *testing.T) {
	t.Parallel()
	var status atomic.Value
	status.Store(ports.AgentAuthStatusUnauthorized)
	agent := &readinessTestAgent{
		resolve: func(context.Context) (string, error) { return "/bin/claude", nil },
		auth: func(context.Context) (ports.AgentAuthStatus, error) {
			return status.Load().(ports.AgentAuthStatus), nil
		},
	}
	recovered := make(chan string, 4)
	coordinator := newReadinessCoordinator(readinessCoordinatorConfig{
		Agents:                    []agentregistry.HarnessAgent{readinessHarness("claude-code", "Claude Code", agent)},
		OnAuthenticationRecovered: func(agentID string) { recovered <- agentID },
	})
	ensure := func() {
		t.Helper()
		if _, err := coordinator.Force(context.Background(), []string{"claude-code"}, domain.AgentReadinessPurposeDisplay); err != nil {
			t.Fatal(err)
		}
	}

	ensure()
	select {
	case id := <-recovered:
		t.Fatalf("first observation (unknown to unauthorized) reported recovery for %s", id)
	case <-time.After(50 * time.Millisecond):
	}

	status.Store(ports.AgentAuthStatusAuthorized)
	ensure()
	select {
	case id := <-recovered:
		if id != "claude-code" {
			t.Fatalf("recovered agent = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("signed-out to signed-in transition was not reported")
	}

	ensure()
	select {
	case id := <-recovered:
		t.Fatalf("a repeated authorized observation reported recovery again for %s", id)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestModelCatalogClassifiesAuthFailuresForClients(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"rejected credential", fmt.Errorf("claude-code model discovery: %w", wrapAuthError(ports.ErrAgentModelDiscoveryCredentialRejected, "Anthropic rejected the credential")), ports.ModelCatalogWarningAuthRequired},
		{"expired stored login", fmt.Errorf("claude-code model discovery: %w", wrapAuthError(ports.ErrAgentModelDiscoveryCredentialExpired, "login token expired")), ports.ModelCatalogWarningAuthExpired},
		{"ordinary failure", errors.New("provider offline"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &fakeModelCache{}
			discoverer := &fakeModelDiscoverer{version: "v1", err: tc.err, catalog: ports.AgentModelCatalog{
				SelectionMode: ports.ModelSelectionCatalog,
				Models:        []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}},
				Source:        "catalog",
			}}
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, nil, discoverer)
			catalog, err := svc.Models(context.Background(), "claude-code", "", true)
			if err != nil {
				t.Fatal(err)
			}
			if catalog.WarningCode != tc.want {
				t.Fatalf("warningCode = %q, want %q (warning %q)", catalog.WarningCode, tc.want, catalog.Warning)
			}
			if catalog.Warning == "" {
				t.Fatal("the provider's own explanation must stay available as the warning detail")
			}
		})
	}
}

func TestSignInRequiredCatalogCarriesAuthCode(t *testing.T) {
	discoverer := &fakeModelDiscoverer{version: "v1", err: fmt.Errorf("kiro model discovery: %w", ports.ErrAgentModelDiscoverySignInRequired)}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("kiro", "Kiro", nil)}, &fakeModelCache{}, nil, discoverer)
	catalog, err := svc.Models(context.Background(), "kiro", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.WarningCode != ports.ModelCatalogWarningAuthRequired {
		t.Fatalf("warningCode = %q, want auth_required", catalog.WarningCode)
	}
}

// After a login, the cached rejection must disappear immediately instead of
// lingering until the background rediscovery finishes.
func TestInvalidateModelCatalogsClearsAuthWarningAndRediscovers(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := &fakeModelDiscoverer{version: "v1", err: wrapAuthError(ports.ErrAgentModelDiscoveryCredentialRejected, "Anthropic rejected the credential"), catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}},
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, nil, discoverer)
	if catalog, err := svc.Models(context.Background(), "claude-code", "", true); err != nil || catalog.WarningCode == "" {
		t.Fatalf("setup catalog = %+v, err = %v", catalog, err)
	}

	discoverer.mu.Lock()
	discoverer.err = nil
	discoverer.catalog = ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"}},
		Source:        "provider",
	}
	discoverer.mu.Unlock()
	svc.InvalidateModelCatalogs("claude-code")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		record, ok, _ := cache.GetAgentModelCatalog(context.Background(), "claude-code", "")
		var catalog ports.AgentModelCatalog
		if ok && json.Unmarshal([]byte(record.CatalogJSON), &catalog) == nil &&
			catalog.Warning == "" && catalog.WarningCode == "" && len(catalog.Models) == 1 && catalog.Models[0].ID == "claude-opus-5-5" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("invalidation did not clear the auth warning and rediscover the catalog")
}

// When discovery falls back to Claude Code's bare aliases, the versions a
// previous discovery resolved (in any scope) label them.
func TestFallbackAliasesCarryVersionsFromAPreviousDiscovery(t *testing.T) {
	previous := ports.AgentModelCatalog{
		AgentID: "claude-code", SelectionMode: ports.ModelSelectionCatalog, Source: "provider",
		Models: []ports.AgentModelInfo{
			{ID: "claude-opus-5-5-20260901", Label: "Claude Opus 5.5"},
			{ID: "claude-opus-5-1-20260301", Label: "Claude Opus 5.1"},
			{ID: "claude-sonnet-5-5-20260901", Label: "Claude Sonnet 5.5"},
		},
	}
	data, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"claude-code\x00other-project": {AgentID: "claude-code", ProjectID: "other-project", CatalogJSON: string(data), BinaryVersion: "old", LastSuccessAt: time.Now()},
	}}
	discoverer := &aliasLabelingDiscoverer{fakeModelDiscoverer: &fakeModelDiscoverer{
		version: "v1",
		err:     wrapAuthError(ports.ErrAgentModelDiscoveryCredentialExpired, "login token expired"),
		catalog: ports.AgentModelCatalog{SelectionMode: ports.ModelSelectionCatalog, Source: "catalog", Models: []ports.AgentModelInfo{
			{ID: "sonnet", Label: "Sonnet"},
			{ID: "fable", Label: "Fable 5.1"},
			{ID: "opus", Label: "Opus"},
			{ID: "haiku", Label: "Haiku"},
			{ID: "opus[1m]", Label: "Opus (1M context)"},
		}},
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, nil, discoverer)
	catalog, err := svc.Models(context.Background(), "claude-code", "", true)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, item := range catalog.Models {
		labels[item.ID] = item.Label
	}
	want := map[string]string{
		"sonnet": "Sonnet 5.5", "fable": "Fable 5.1", "opus": "Opus 5.5",
		"haiku": "Haiku", "opus[1m]": "Opus 5.5 (1M context)",
	}
	for id, label := range want {
		if labels[id] != label {
			t.Fatalf("label for %s = %q, want %q (all: %v)", id, labels[id], label, labels)
		}
	}
	if catalog.WarningCode != ports.ModelCatalogWarningAuthExpired {
		t.Fatalf("warningCode = %q, want auth_expired", catalog.WarningCode)
	}
}

type aliasLabelingDiscoverer struct{ *fakeModelDiscoverer }

func (d *aliasLabelingDiscoverer) LabelAliases(agentID string, models, reference []ports.AgentModelInfo) []ports.AgentModelInfo {
	return modelcatalog.Discoverer{}.LabelAliases(agentID, models, reference)
}

type testAuthError struct {
	kind    error
	message string
}

func (e testAuthError) Error() string { return e.message }
func (e testAuthError) Unwrap() error { return e.kind }

func wrapAuthError(kind error, message string) error {
	return testAuthError{kind: kind, message: message}
}
